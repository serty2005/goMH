package logstream

import (
	"fmt"
	"goMH/core"
	"goMH/tui"
	"io"
	"os"
	"sync"
)

type Sink interface {
	WriteLine(line string) error
	Close() error
}

type sinkFunc struct {
	write func(string) error
	close func() error
}

func (s sinkFunc) WriteLine(line string) error {
	if s.write == nil {
		return nil
	}
	return s.write(line)
}

func (s sinkFunc) Close() error {
	if s.close == nil {
		return nil
	}
	return s.close()
}

func NewTaskContextSink(ctx core.TaskContext) Sink {
	return sinkFunc{
		write: func(line string) error {
			if ctx != nil {
				ctx.Info(line)
			}
			return nil
		},
	}
}

func NewConsoleSink() Sink {
	return sinkFunc{
		write: func(line string) error {
			tui.Info(line)
			return nil
		},
	}
}

func NewWriterSink(writer io.Writer) Sink {
	sink := &writerSink{writer: writer}
	return sink
}

func NewStdoutSink() Sink {
	return NewWriterSink(os.Stdout)
}

func NewFileSink(path string) (Sink, error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)
	if err != nil {
		return nil, fmt.Errorf("не удалось открыть файл sink %s: %w", path, err)
	}
	return sinkFunc{
		write: func(line string) error {
			_, err := io.WriteString(file, line+"\n")
			return err
		},
		close: file.Close,
	}, nil
}

func NewMultiSink(sinks ...Sink) Sink {
	filtered := make([]Sink, 0, len(sinks))
	for _, sink := range sinks {
		if sink != nil {
			filtered = append(filtered, sink)
		}
	}
	return &multiSink{sinks: filtered}
}

type writerSink struct {
	mu     sync.Mutex
	writer io.Writer
}

func (s *writerSink) WriteLine(line string) error {
	if s.writer == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := io.WriteString(s.writer, line+"\n")
	return err
}

func (s *writerSink) Close() error {
	return nil
}

type multiSink struct {
	sinks []Sink
}

func (s *multiSink) WriteLine(line string) error {
	for _, sink := range s.sinks {
		if err := sink.WriteLine(line); err != nil {
			return err
		}
	}
	return nil
}

func (s *multiSink) Close() error {
	var firstErr error
	for _, sink := range s.sinks {
		if err := sink.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}
