package taskqueue

import (
	"context"
	"errors"
	"goMH/core"
	"testing"
	"time"
)

func TestQueueRunsPendingInFIFOOrder(t *testing.T) {
	queue := New()
	started := make([]string, 0, 3)

	for i := 1; i <= 3; i++ {
		id := i
		_, err := queue.Enqueue(TaskSpec{
			ID:        "t" + string(rune('0'+id)),
			ModuleID:  "test",
			Title:     "task",
			Signature: "fifo-" + string(rune('0'+id)),
			Run: func(ctx core.TaskContext) error {
				ctx.SetStatus("Выполняется")
				started = append(started, "t"+string(rune('0'+id)))
				return nil
			},
		})
		if err != nil {
			t.Fatalf("enqueue failed: %v", err)
		}
	}

	summary := queue.RunPending(func(s TaskSnapshot) core.TaskContext {
		return NewTaskContext(queue, s.ID)
	})
	if summary.Started != 3 || summary.Success != 3 || summary.Failed != 0 {
		t.Fatalf("unexpected summary: %+v", summary)
	}

	if len(started) != 3 || started[0] != "t1" || started[1] != "t2" || started[2] != "t3" {
		t.Fatalf("unexpected order: %v", started)
	}

	snapshots := queue.Snapshots()
	for _, snapshot := range snapshots {
		if snapshot.State != TaskSuccess {
			t.Fatalf("expected success state, got %s", snapshot.State)
		}
	}
}

func TestQueueRejectsDuplicateActiveTask(t *testing.T) {
	queue := New()

	_, err := queue.Enqueue(TaskSpec{
		ID:        "dup-1",
		ModuleID:  "test",
		Title:     "dup",
		Signature: "same",
		Run: func(core.TaskContext) error {
			time.Sleep(10 * time.Millisecond)
			return nil
		},
	})
	if err != nil {
		t.Fatalf("first enqueue failed: %v", err)
	}

	_, err = queue.Enqueue(TaskSpec{
		ID:        "dup-2",
		ModuleID:  "test",
		Title:     "dup",
		Signature: "same",
		Run: func(core.TaskContext) error {
			return nil
		},
	})
	if !errors.Is(err, ErrDuplicateTask) {
		t.Fatalf("expected ErrDuplicateTask, got %v", err)
	}
}

func TestQueueTracksFailureAndLogs(t *testing.T) {
	queue := New()

	snapshot, err := queue.Enqueue(TaskSpec{
		ID:        "fail-1",
		ModuleID:  "test",
		Title:     "will-fail",
		Signature: "fail",
		Run: func(ctx core.TaskContext) error {
			ctx.Info("Начало")
			return errors.New("boom")
		},
	})
	if err != nil {
		t.Fatalf("enqueue failed: %v", err)
	}

	summary := queue.RunPending(func(s TaskSnapshot) core.TaskContext {
		return NewTaskContext(queue, s.ID)
	})
	if summary.Failed != 1 {
		t.Fatalf("expected 1 failed task, got %+v", summary)
	}

	logs := queue.Logs(snapshot.ID)
	if len(logs) == 0 {
		t.Fatal("expected logs to be stored")
	}

	snapshots := queue.Snapshots()
	if len(snapshots) != 1 {
		t.Fatalf("expected 1 snapshot, got %d", len(snapshots))
	}
	if snapshots[0].State != TaskFailed {
		t.Fatalf("expected failed state, got %s", snapshots[0].State)
	}
	if snapshots[0].LastError != "boom" {
		t.Fatalf("unexpected last error: %s", snapshots[0].LastError)
	}
}

func TestQueueBackgroundModeProcessesNewTasksAfterStart(t *testing.T) {
	queue := New()

	done := make(chan string, 3)
	for _, id := range []string{"t1", "t2"} {
		taskID := id
		_, err := queue.Enqueue(TaskSpec{
			ID:        taskID,
			ModuleID:  "test",
			Title:     taskID,
			Signature: taskID,
			Run: func(ctx core.TaskContext) error {
				ctx.SetStatus("Выполняется")
				done <- taskID
				return nil
			},
		})
		if err != nil {
			t.Fatalf("enqueue failed: %v", err)
		}
	}

	started := queue.StartBackground(func(snapshot TaskSnapshot) core.TaskContext {
		return NewTaskContext(queue, snapshot.ID)
	})
	if !started {
		t.Fatal("background mode was not started")
	}

	waitFor(t, 2*time.Second, func() bool {
		return len(queue.Snapshots()) == 2 && queue.Snapshots()[0].State == TaskSuccess && queue.Snapshots()[1].State == TaskSuccess
	})

	_, err := queue.Enqueue(TaskSpec{
		ID:        "t3",
		ModuleID:  "test",
		Title:     "t3",
		Signature: "t3",
		Run: func(ctx core.TaskContext) error {
			ctx.SetStatus("Выполняется")
			done <- "t3"
			return nil
		},
	})
	if err != nil {
		t.Fatalf("enqueue after start failed: %v", err)
	}

	waitFor(t, 2*time.Second, func() bool {
		snapshots := queue.Snapshots()
		return len(snapshots) == 3 && snapshots[2].State == TaskSuccess
	})
}

func TestQueueRunsParallelTasksWithoutBlockingExclusive(t *testing.T) {
	queue := New()

	exclusiveStarted := make(chan string, 2)
	exclusiveRelease := make(chan struct{})
	parallelDone := make(chan string, 2)

	mustEnqueue := func(spec TaskSpec) {
		t.Helper()
		if _, err := queue.Enqueue(spec); err != nil {
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

	started := queue.StartBackground(func(snapshot TaskSnapshot) core.TaskContext {
		return NewTaskContext(queue, snapshot.ID)
	})
	if !started {
		t.Fatal("background mode was not started")
	}

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

func TestQueueCancelsRunningTaskWhenCancelable(t *testing.T) {
	queue := New()

	taskCtxDone := make(chan struct{})
	_, err := queue.Enqueue(TaskSpec{
		ID:        "cancelable",
		ModuleID:  "test",
		Title:     "cancelable",
		Signature: "cancelable",
		Run: func(ctx core.TaskContext) error {
			control, ok := ctx.(interface {
				SetCancelable(func())
				ClearCancelable()
			})
			if !ok {
				return errors.New("контекст не поддерживает отмену")
			}

			downloadCtx, cancel := context.WithCancel(context.Background())
			control.SetCancelable(cancel)
			defer control.ClearCancelable()
			close(taskCtxDone)
			<-downloadCtx.Done()
			return downloadCtx.Err()
		},
	})
	if err != nil {
		t.Fatalf("enqueue failed: %v", err)
	}

	started := queue.StartBackground(func(snapshot TaskSnapshot) core.TaskContext {
		return NewTaskContext(queue, snapshot.ID)
	})
	if !started {
		t.Fatal("background mode was not started")
	}

	waitFor(t, 2*time.Second, func() bool {
		select {
		case <-taskCtxDone:
			return true
		default:
			return false
		}
	})

	if !queue.Cancel("cancelable") {
		t.Fatal("expected cancel request to be accepted")
	}

	waitFor(t, 2*time.Second, func() bool {
		snapshots := queue.Snapshots()
		return len(snapshots) == 1 && snapshots[0].State == TaskFailed
	})

	snapshot := queue.Snapshots()[0]
	if snapshot.LastError != "Операция отменена пользователем" {
		t.Fatalf("unexpected last error: %s", snapshot.LastError)
	}
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
	t.Fatal("timeout waiting for condition")
}
