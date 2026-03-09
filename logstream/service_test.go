package logstream

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestServiceTailReadsLastLinesAndAppends(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tail.log")
	if err := os.WriteFile(path, []byte("old-1\nold-2\n"), 0666); err != nil {
		t.Fatalf("не удалось подготовить лог: %v", err)
	}

	sink := &captureSink{}
	service := NewService()
	handle, err := service.StartTail(context.Background(), TailRequest{
		FilePath:     path,
		StartLines:   1,
		PollInterval: 20 * time.Millisecond,
		IdleTimeout:  200 * time.Millisecond,
		Sink:         sink,
	})
	if err != nil {
		t.Fatalf("не удалось запустить tail: %v", err)
	}

	time.Sleep(40 * time.Millisecond)
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0666)
	if err != nil {
		t.Fatalf("не удалось открыть лог для дозаписи: %v", err)
	}
	if _, err := file.WriteString("new-1\n"); err != nil {
		file.Close()
		t.Fatalf("не удалось дописать лог: %v", err)
	}
	_ = file.Close()

	if err := handle.Wait(); err != nil {
		t.Fatalf("tail завершился ошибкой: %v", err)
	}

	lines := sink.Lines()
	if len(lines) != 2 || lines[0] != "old-2" || lines[1] != "new-1" {
		t.Fatalf("неожиданные строки tail: %#v", lines)
	}
}

func TestServiceTailReturnsContextCanceled(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cancel.log")
	if err := os.WriteFile(path, []byte("line-1\n"), 0666); err != nil {
		t.Fatalf("не удалось подготовить лог: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	sink := &captureSink{}
	service := NewService()
	handle, err := service.StartTail(ctx, TailRequest{
		FilePath:     path,
		StartLines:   1,
		PollInterval: 20 * time.Millisecond,
		Sink:         sink,
	})
	if err != nil {
		t.Fatalf("не удалось запустить tail: %v", err)
	}

	time.Sleep(40 * time.Millisecond)
	cancel()

	if err := handle.Wait(); !errors.Is(err, context.Canceled) {
		t.Fatalf("ожидалась context.Canceled, получено: %v", err)
	}
}

func TestServiceTailReadsFromStartWhenStartLinesNegative(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "full.log")
	if err := os.WriteFile(path, []byte("line-1\nline-2\nline-3\n"), 0666); err != nil {
		t.Fatalf("не удалось подготовить лог: %v", err)
	}

	sink := &captureSink{}
	service := NewService()
	handle, err := service.StartTail(context.Background(), TailRequest{
		FilePath:     path,
		StartLines:   -1,
		PollInterval: 20 * time.Millisecond,
		IdleTimeout:  80 * time.Millisecond,
		Sink:         sink,
	})
	if err != nil {
		t.Fatalf("не удалось запустить tail: %v", err)
	}

	if err := handle.Wait(); err != nil {
		t.Fatalf("tail завершился ошибкой: %v", err)
	}

	lines := sink.Lines()
	if len(lines) != 3 || lines[0] != "line-1" || lines[2] != "line-3" {
		t.Fatalf("ожидалось чтение файла с начала, получено: %#v", lines)
	}
}

type captureSink struct {
	mu    sync.Mutex
	lines []string
}

func (s *captureSink) WriteLine(line string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lines = append(s.lines, line)
	return nil
}

func (s *captureSink) Close() error {
	return nil
}

func (s *captureSink) Lines() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, len(s.lines))
	copy(out, s.lines)
	return out
}
