package gui

import (
	"context"
	"goMH/core"
	"goMH/taskqueue"
	"sync"
)

var ErrDuplicateTask = taskqueue.ErrDuplicateTask

type TaskState = taskqueue.TaskState

const (
	TaskQueued  = taskqueue.TaskQueued
	TaskRunning = taskqueue.TaskRunning
	TaskSuccess = taskqueue.TaskSuccess
	TaskFailed  = taskqueue.TaskFailed
)

type TaskSpec = taskqueue.TaskSpec
type TaskSnapshot = taskqueue.TaskSnapshot

type TaskManager struct {
	queue *taskqueue.Queue

	mu sync.RWMutex

	onTaskEnqueued func(TaskSnapshot)
	onTaskStarted  func(TaskSnapshot)
	onTaskProgress func(TaskSnapshot)
	onTaskLog      func(TaskSnapshot, string)
	onTaskFinished func(TaskSnapshot)
}

func NewTaskManager(queue *taskqueue.Queue) *TaskManager {
	if queue == nil {
		queue = taskqueue.New()
	}

	tm := &TaskManager{queue: queue}
	queue.AddListener(taskqueue.Listener{
		OnTaskEnqueued: tm.emitTaskEnqueued,
		OnTaskStarted:  tm.emitTaskStarted,
		OnTaskUpdated:  tm.emitTaskProgress,
		OnTaskLog:      tm.emitTaskLog,
		OnTaskFinished: tm.emitTaskFinished,
	})
	return tm
}

func (tm *TaskManager) Queue() *taskqueue.Queue {
	return tm.queue
}

func (tm *TaskManager) Start() {
	tm.queue.StartBackground(func(snapshot taskqueue.TaskSnapshot, runtimeCtx context.Context) core.TaskContext {
		return NewTaskGuiContext(tm.queue, snapshot, runtimeCtx)
	})
}

func (tm *TaskManager) OnTaskEnqueued(cb func(TaskSnapshot)) {
	tm.mu.Lock()
	tm.onTaskEnqueued = cb
	tm.mu.Unlock()
}

func (tm *TaskManager) OnTaskStarted(cb func(TaskSnapshot)) {
	tm.mu.Lock()
	tm.onTaskStarted = cb
	tm.mu.Unlock()
}

func (tm *TaskManager) OnTaskProgress(cb func(TaskSnapshot)) {
	tm.mu.Lock()
	tm.onTaskProgress = cb
	tm.mu.Unlock()
}

func (tm *TaskManager) OnTaskLog(cb func(TaskSnapshot, string)) {
	tm.mu.Lock()
	tm.onTaskLog = cb
	tm.mu.Unlock()
}

func (tm *TaskManager) OnTaskFinished(cb func(TaskSnapshot)) {
	tm.mu.Lock()
	tm.onTaskFinished = cb
	tm.mu.Unlock()
}

func (tm *TaskManager) Enqueue(spec TaskSpec) (TaskSnapshot, error) {
	return tm.queue.Enqueue(spec)
}

func (tm *TaskManager) AddTask(t func() error) {
	_, _ = tm.Enqueue(TaskSpec{
		ModuleID: "legacy",
		Title:    "Legacy Task",
		Run: func(_ core.TaskContext) error {
			return t()
		},
	})
}

func (tm *TaskManager) Snapshots() []TaskSnapshot {
	return tm.queue.Snapshots()
}

func (tm *TaskManager) Logs(taskID string) []string {
	return tm.queue.Logs(taskID)
}

func (tm *TaskManager) Stats() (running int, queued int) {
	for _, snapshot := range tm.queue.Snapshots() {
		switch snapshot.State {
		case TaskRunning:
			running++
		case TaskQueued:
			queued++
		}
	}
	return
}

func (tm *TaskManager) emitTaskEnqueued(snapshot TaskSnapshot) {
	cb := tm.snapshotCallback()
	if cb.onTaskEnqueued != nil {
		cb.onTaskEnqueued(snapshot)
	}
}

func (tm *TaskManager) emitTaskStarted(snapshot TaskSnapshot) {
	cb := tm.snapshotCallback()
	if cb.onTaskStarted != nil {
		cb.onTaskStarted(snapshot)
	}
}

func (tm *TaskManager) emitTaskProgress(snapshot TaskSnapshot) {
	cb := tm.snapshotCallback()
	if cb.onTaskProgress != nil {
		cb.onTaskProgress(snapshot)
	}
}

func (tm *TaskManager) emitTaskLog(snapshot TaskSnapshot, line string) {
	cb := tm.snapshotCallback()
	if cb.onTaskLog != nil {
		cb.onTaskLog(snapshot, line)
	}
}

func (tm *TaskManager) emitTaskFinished(snapshot TaskSnapshot) {
	cb := tm.snapshotCallback()
	if cb.onTaskFinished != nil {
		cb.onTaskFinished(snapshot)
	}
}

type taskManagerCallbacks struct {
	onTaskEnqueued func(TaskSnapshot)
	onTaskStarted  func(TaskSnapshot)
	onTaskProgress func(TaskSnapshot)
	onTaskLog      func(TaskSnapshot, string)
	onTaskFinished func(TaskSnapshot)
}

func (tm *TaskManager) snapshotCallback() taskManagerCallbacks {
	tm.mu.RLock()
	defer tm.mu.RUnlock()

	return taskManagerCallbacks{
		onTaskEnqueued: tm.onTaskEnqueued,
		onTaskStarted:  tm.onTaskStarted,
		onTaskProgress: tm.onTaskProgress,
		onTaskLog:      tm.onTaskLog,
		onTaskFinished: tm.onTaskFinished,
	}
}
