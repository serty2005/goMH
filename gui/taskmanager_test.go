package gui

import (
	"errors"
	"goMH/core"
	"sync"
	"testing"
	"time"
)

func TestTaskManagerFIFOAndSingleRunning(t *testing.T) {
	tm := NewTaskManager()
	tm.Start()

	var mu sync.Mutex
	startedOrder := make([]string, 0, 3)
	currentlyRunning := 0
	maxRunning := 0

	tm.OnTaskStarted(func(s TaskSnapshot) {
		mu.Lock()
		defer mu.Unlock()
		startedOrder = append(startedOrder, s.ID)
		currentlyRunning++
		if currentlyRunning > maxRunning {
			maxRunning = currentlyRunning
		}
	})
	tm.OnTaskFinished(func(TaskSnapshot) {
		mu.Lock()
		currentlyRunning--
		mu.Unlock()
	})

	for i := 1; i <= 3; i++ {
		id := i
		_, err := tm.Enqueue(TaskSpec{
			ID:        "t" + string(rune('0'+id)),
			ModuleID:  "iiko",
			Title:     "test",
			Signature: "fifo-" + string(rune('0'+id)),
			Run: func(_ core.TaskContext) error {
				time.Sleep(40 * time.Millisecond)
				return nil
			},
		})
		if err != nil {
			t.Fatalf("enqueue failed: %v", err)
		}
	}

	waitFor(t, 2*time.Second, func() bool {
		snaps := tm.Snapshots()
		if len(snaps) != 3 {
			return false
		}
		for _, s := range snaps {
			if s.State != TaskSuccess {
				return false
			}
		}
		return true
	})

	mu.Lock()
	defer mu.Unlock()
	if maxRunning != 1 {
		t.Fatalf("expected max 1 running task, got %d", maxRunning)
	}
	if len(startedOrder) != 3 || startedOrder[0] != "t1" || startedOrder[1] != "t2" || startedOrder[2] != "t3" {
		t.Fatalf("unexpected FIFO order: %v", startedOrder)
	}
}

func TestTaskManagerTransitionsAndFailure(t *testing.T) {
	tm := NewTaskManager()
	tm.Start()

	var started, finished bool
	tm.OnTaskStarted(func(TaskSnapshot) { started = true })
	tm.OnTaskFinished(func(s TaskSnapshot) {
		finished = true
		if s.State != TaskFailed {
			t.Fatalf("expected failed state, got %s", s.State)
		}
	})

	_, err := tm.Enqueue(TaskSpec{
		ID:        "fail-1",
		ModuleID:  "iiko",
		Title:     "will-fail",
		Signature: "unique-fail",
		Run: func(_ core.TaskContext) error {
			return errors.New("boom")
		},
	})
	if err != nil {
		t.Fatalf("enqueue failed: %v", err)
	}

	waitFor(t, 1*time.Second, func() bool {
		return started && finished
	})
}

func TestTaskManagerRejectsDuplicate(t *testing.T) {
	tm := NewTaskManager()
	tm.Start()

	block := make(chan struct{})
	_, err := tm.Enqueue(TaskSpec{
		ID:        "dup-1",
		ModuleID:  "iiko",
		Title:     "dup",
		Signature: "same-signature",
		Run: func(_ core.TaskContext) error {
			<-block
			return nil
		},
	})
	if err != nil {
		t.Fatalf("first enqueue failed: %v", err)
	}

	_, err = tm.Enqueue(TaskSpec{
		ID:        "dup-2",
		ModuleID:  "iiko",
		Title:     "dup",
		Signature: "same-signature",
		Run: func(_ core.TaskContext) error {
			return nil
		},
	})
	if !errors.Is(err, ErrDuplicateTask) {
		t.Fatalf("expected ErrDuplicateTask, got %v", err)
	}

	close(block)
}

func waitFor(t *testing.T, timeout time.Duration, fn func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if fn() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timeout waiting for condition")
}
