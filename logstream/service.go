package logstream

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type TailRequest struct {
	FilePath     string
	StartLines   int
	PollInterval time.Duration
	IdleTimeout  time.Duration
	Sink         Sink
}

type Handle struct {
	ID       string
	FilePath string

	cancel context.CancelFunc
	done   chan struct{}

	mu  sync.Mutex
	err error
}

func (h *Handle) Cancel() {
	if h == nil || h.cancel == nil {
		return
	}
	h.cancel()
}

func (h *Handle) Done() <-chan struct{} {
	if h == nil {
		return nil
	}
	return h.done
}

func (h *Handle) Wait() error {
	if h == nil {
		return nil
	}
	<-h.done
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.err
}

func (h *Handle) finish(err error) {
	h.mu.Lock()
	h.err = err
	h.mu.Unlock()
	close(h.done)
}

type Service struct {
	mu      sync.Mutex
	seq     uint64
	streams map[string]*Handle
}

func NewService() *Service {
	return &Service{
		streams: make(map[string]*Handle),
	}
}

func (s *Service) StartTail(parent context.Context, req TailRequest) (*Handle, error) {
	if req.FilePath == "" {
		return nil, errors.New("не указан путь к логу")
	}
	if req.Sink == nil {
		return nil, errors.New("не указан sink для лог-стрима")
	}

	file, err := os.Open(req.FilePath)
	if err != nil {
		return nil, err
	}

	if parent == nil {
		parent = context.Background()
	}
	if req.StartLines <= 0 {
		req.StartLines = 50
	}
	if req.PollInterval <= 0 {
		req.PollInterval = 500 * time.Millisecond
	}

	ctx, cancel := context.WithCancel(parent)
	handle := &Handle{
		ID:       fmt.Sprintf("logstream-%06d", atomic.AddUint64(&s.seq, 1)),
		FilePath: req.FilePath,
		cancel:   cancel,
		done:     make(chan struct{}),
	}

	s.mu.Lock()
	s.streams[handle.ID] = handle
	s.mu.Unlock()

	go s.runTail(ctx, handle, file, req)
	return handle, nil
}

func (s *Service) runTail(ctx context.Context, handle *Handle, file *os.File, req TailRequest) {
	err := s.tailFile(ctx, file, req)
	_ = file.Close()
	closeErr := req.Sink.Close()
	if err == nil {
		err = closeErr
	}

	s.mu.Lock()
	delete(s.streams, handle.ID)
	s.mu.Unlock()
	handle.finish(err)
}

func (s *Service) tailFile(ctx context.Context, file *os.File, req TailRequest) error {
	if stat, err := file.Stat(); err == nil && stat.Size() > 0 {
		startPos, err := findStartOfLastNLines(file, req.StartLines)
		if err != nil {
			return err
		}
		if _, err := file.Seek(startPos, io.SeekStart); err != nil {
			return err
		}
	}

	reader := &tailReader{
		file: file,
		buf:  make([]byte, 4096),
	}
	if _, err := reader.DrainTo(req.Sink); err != nil {
		return err
	}

	ticker := time.NewTicker(req.PollInterval)
	defer ticker.Stop()

	var idleTimer *time.Timer
	if req.IdleTimeout > 0 {
		idleTimer = time.NewTimer(req.IdleTimeout)
		defer idleTimer.Stop()
	}

	for {
		select {
		case <-ctx.Done():
			reader.Flush(req.Sink)
			return ctx.Err()
		case <-ticker.C:
			wrote, err := reader.DrainTo(req.Sink)
			if err != nil {
				return err
			}
			if wrote && idleTimer != nil {
				resetTimer(idleTimer, req.IdleTimeout)
			}
		case <-timerChan(idleTimer):
			reader.Flush(req.Sink)
			return nil
		}
	}
}

type tailReader struct {
	file    *os.File
	buf     []byte
	pending string
}

func (r *tailReader) DrainTo(sink Sink) (bool, error) {
	wrote := false
	for {
		n, err := r.file.Read(r.buf)
		if n > 0 {
			wrote = true
			r.pending += string(r.buf[:n])
			if err := r.emitCompleteLines(sink); err != nil {
				return wrote, err
			}
		}
		if err == io.EOF {
			return wrote, nil
		}
		if err != nil {
			return wrote, err
		}
	}
}

func (r *tailReader) Flush(sink Sink) error {
	line := strings.TrimRight(r.pending, "\r\n")
	r.pending = ""
	if strings.TrimSpace(line) == "" {
		return nil
	}
	return sink.WriteLine(line)
}

func (r *tailReader) emitCompleteLines(sink Sink) error {
	for {
		index := strings.IndexByte(r.pending, '\n')
		if index < 0 {
			return nil
		}
		line := strings.TrimRight(r.pending[:index], "\r")
		r.pending = r.pending[index+1:]
		if strings.TrimSpace(line) == "" {
			continue
		}
		if err := sink.WriteLine(line); err != nil {
			return err
		}
	}
}

func findStartOfLastNLines(file *os.File, n int) (int64, error) {
	if n <= 0 {
		return 0, nil
	}

	stat, err := file.Stat()
	if err != nil {
		return 0, err
	}
	fileSize := stat.Size()
	if fileSize == 0 {
		return 0, nil
	}

	readPos := fileSize
	lastByte := make([]byte, 1)
	if _, err := file.ReadAt(lastByte, fileSize-1); err == nil && lastByte[0] == '\n' {
		readPos--
	}
	count := 0
	buf := make([]byte, 4096)

	for readPos > 0 && count < n {
		readSize := int64(len(buf))
		if readPos < readSize {
			readSize = readPos
		}
		readPos -= readSize
		if _, err := file.Seek(readPos, io.SeekStart); err != nil {
			return 0, err
		}
		nn, err := file.Read(buf[:readSize])
		if err != nil && err != io.EOF {
			return 0, err
		}
		for i := nn - 1; i >= 0; i-- {
			if buf[i] == '\n' {
				count++
				if count >= n {
					return readPos + int64(i) + 1, nil
				}
			}
		}
	}
	return 0, nil
}

func timerChan(timer *time.Timer) <-chan time.Time {
	if timer == nil {
		return nil
	}
	return timer.C
}

func resetTimer(timer *time.Timer, timeout time.Duration) {
	if timer == nil {
		return
	}
	if !timer.Stop() {
		select {
		case <-timer.C:
		default:
		}
	}
	timer.Reset(timeout)
}
