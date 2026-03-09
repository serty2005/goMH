package gui

import (
	"errors"
	"goMH/core"
	"goMH/taskqueue"
	"sync"
	"testing"
	"time"
)

func TestTaskManagerUsesSharedQueueAndEmitsCallbacks(t *testing.T) {
	queue := taskqueue.New()
	tm := NewTaskManager(queue)
	tm.Start()

	if tm.Queue() != queue {
		t.Fatal("task manager должен использовать переданный taskqueue")
	}

	var mu sync.Mutex
	var enqueued bool
	var started bool
	var finished bool
	progressSeen := false
	logsSeen := 0

	tm.OnTaskEnqueued(func(TaskSnapshot) {
		mu.Lock()
		enqueued = true
		mu.Unlock()
	})
	tm.OnTaskStarted(func(TaskSnapshot) {
		mu.Lock()
		started = true
		mu.Unlock()
	})
	tm.OnTaskProgress(func(s TaskSnapshot) {
		mu.Lock()
		if s.StageText == "Шаг 1" && s.Progress == 42 {
			progressSeen = true
		}
		mu.Unlock()
	})
	tm.OnTaskLog(func(_ TaskSnapshot, line string) {
		mu.Lock()
		if line != "" {
			logsSeen++
		}
		mu.Unlock()
	})
	tm.OnTaskFinished(func(s TaskSnapshot) {
		mu.Lock()
		finished = s.State == TaskSuccess
		mu.Unlock()
	})

	snapshot, err := tm.Enqueue(TaskSpec{
		ID:        "gui-shared",
		ModuleID:  "iiko",
		Title:     "shared",
		Signature: "gui-shared",
		Run: func(ctx core.TaskContext) error {
			ctx.Info("Старт")
			ctx.SetStatus("Шаг 1")
			ctx.SetProgress(42)
			return nil
		},
	})
	if err != nil {
		t.Fatalf("enqueue failed: %v", err)
	}

	waitFor(t, 2*time.Second, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return enqueued && started && finished && progressSeen && logsSeen >= 2
	})

	snapshots := queue.Snapshots()
	if len(snapshots) != 1 {
		t.Fatalf("expected 1 snapshot in shared queue, got %d", len(snapshots))
	}
	if snapshots[0].ID != snapshot.ID {
		t.Fatalf("unexpected snapshot id: %s", snapshots[0].ID)
	}
	if snapshots[0].State != taskqueue.TaskSuccess {
		t.Fatalf("expected success state, got %s", snapshots[0].State)
	}
	if len(queue.Logs(snapshot.ID)) == 0 {
		t.Fatal("expected shared queue logs to be filled")
	}
}

func TestTaskManagerPreservesExclusiveSemantics(t *testing.T) {
	queue := taskqueue.New()
	tm := NewTaskManager(queue)

	exclusiveStarted := make(chan string, 2)
	exclusiveRelease := make(chan struct{})
	parallelDone := make(chan string, 2)

	mustEnqueue := func(spec TaskSpec) {
		t.Helper()
		if _, err := tm.Enqueue(spec); err != nil {
			t.Fatalf("enqueue failed: %v", err)
		}
	}

	mustEnqueue(TaskSpec{
		ID:        "exclusive-1",
		ModuleID:  "test",
		Title:     "exclusive-1",
		Signature: "exclusive-1",
		Exclusive: true,
		Run: func(core.TaskContext) error {
			exclusiveStarted <- "exclusive-1"
			<-exclusiveRelease
			return nil
		},
	})
	mustEnqueue(TaskSpec{
		ID:        "parallel-1",
		ModuleID:  "test",
		Title:     "parallel-1",
		Signature: "parallel-1",
		Run: func(core.TaskContext) error {
			parallelDone <- "parallel-1"
			return nil
		},
	})
	mustEnqueue(TaskSpec{
		ID:        "parallel-2",
		ModuleID:  "test",
		Title:     "parallel-2",
		Signature: "parallel-2",
		Run: func(core.TaskContext) error {
			parallelDone <- "parallel-2"
			return nil
		},
	})
	mustEnqueue(TaskSpec{
		ID:        "exclusive-2",
		ModuleID:  "test",
		Title:     "exclusive-2",
		Signature: "exclusive-2",
		Exclusive: true,
		Run: func(core.TaskContext) error {
			exclusiveStarted <- "exclusive-2"
			return nil
		},
	})

	tm.Start()

	waitFor(t, 2*time.Second, func() bool {
		return len(exclusiveStarted) >= 1
	})
	first := <-exclusiveStarted
	if first != "exclusive-1" {
		t.Fatalf("unexpected first exclusive task: %s", first)
	}

	waitFor(t, 2*time.Second, func() bool {
		return len(parallelDone) == 2
	})

	select {
	case second := <-exclusiveStarted:
		t.Fatalf("вторая exclusive-задача стартовала слишком рано: %s", second)
	default:
	}

	close(exclusiveRelease)

	waitFor(t, 2*time.Second, func() bool {
		return len(exclusiveStarted) == 1
	})
	second := <-exclusiveStarted
	if second != "exclusive-2" {
		t.Fatalf("unexpected second exclusive task: %s", second)
	}
}

func TestTaskManagerRejectsDuplicateThroughSharedQueue(t *testing.T) {
	queue := taskqueue.New()
	tm := NewTaskManager(queue)
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
