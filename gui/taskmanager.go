package gui

import (
	"context"
	"errors"
	"fmt"
	"goMH/core"
	"goMH/logging"
	"sync"
	"sync/atomic"
	"time"
)

var ErrDuplicateTask = errors.New("задача с такой сигнатурой уже находится в очереди или выполняется")

type TaskState string

const (
	TaskQueued  TaskState = "Queued"
	TaskRunning TaskState = "Running"
	TaskSuccess TaskState = "Success"
	TaskFailed  TaskState = "Failed"
)

type TaskSpec struct {
	ID         string
	ModuleID   string
	Title      string
	Signature  string
	Run        func(taskCtx core.TaskContext) error
	EnqueuedAt time.Time
}

type TaskSnapshot struct {
	ID         string
	ModuleID   string
	Title      string
	Signature  string
	State      TaskState
	Progress   int
	StageText  string
	LastError  string
	EnqueuedAt time.Time
	StartedAt  time.Time
	FinishedAt time.Time
	UpdatedAt  time.Time
}

type taskRecord struct {
	spec     TaskSpec
	snapshot TaskSnapshot
	runtime  context.Context
	cancel   context.CancelFunc
}

type TaskManager struct {
	mu      sync.Mutex
	queueCh chan *taskRecord
	tasks   []*taskRecord
	seq     uint64

	onTaskEnqueued func(TaskSnapshot)
	onTaskStarted  func(TaskSnapshot)
	onTaskProgress func(TaskSnapshot)
	onTaskLog      func(TaskSnapshot, string)
	onTaskFinished func(TaskSnapshot)
}

func NewTaskManager() *TaskManager {
	return &TaskManager{
		queueCh: make(chan *taskRecord, 256),
	}
}

func (tm *TaskManager) Start() {
	go tm.worker()
}

func (tm *TaskManager) OnTaskEnqueued(cb func(TaskSnapshot)) { tm.onTaskEnqueued = cb }
func (tm *TaskManager) OnTaskStarted(cb func(TaskSnapshot))  { tm.onTaskStarted = cb }
func (tm *TaskManager) OnTaskProgress(cb func(TaskSnapshot)) { tm.onTaskProgress = cb }
func (tm *TaskManager) OnTaskLog(cb func(TaskSnapshot, string)) {
	tm.onTaskLog = cb
}
func (tm *TaskManager) OnTaskFinished(cb func(TaskSnapshot)) { tm.onTaskFinished = cb }

func (tm *TaskManager) Enqueue(spec TaskSpec) (TaskSnapshot, error) {
	if spec.Run == nil {
		return TaskSnapshot{}, errors.New("для задачи не задан обработчик выполнения")
	}

	now := time.Now()
	if spec.EnqueuedAt.IsZero() {
		spec.EnqueuedAt = now
	}
	if spec.ID == "" {
		spec.ID = fmt.Sprintf("task-%06d", atomic.AddUint64(&tm.seq, 1))
	}
	if spec.Signature == "" {
		spec.Signature = spec.ModuleID + "|" + spec.Title
	}

	tm.mu.Lock()
	defer tm.mu.Unlock()

	for _, rec := range tm.tasks {
		state := rec.snapshot.State
		if rec.snapshot.Signature == spec.Signature && (state == TaskQueued || state == TaskRunning) {
			return TaskSnapshot{}, ErrDuplicateTask
		}
	}

	snap := TaskSnapshot{
		ID:         spec.ID,
		ModuleID:   spec.ModuleID,
		Title:      spec.Title,
		Signature:  spec.Signature,
		State:      TaskQueued,
		Progress:   0,
		StageText:  "Ожидание очереди",
		EnqueuedAt: spec.EnqueuedAt,
		UpdatedAt:  now,
	}

	rec := &taskRecord{spec: spec, snapshot: snap}
	tm.tasks = append(tm.tasks, rec)
	logging.LogTaskQueued(rec.snapshot.ModuleID, rec.snapshot.ID, rec.snapshot.Title)

	enqueuedCB := tm.onTaskEnqueued
	queueCh := tm.queueCh

	if enqueuedCB != nil {
		enqueuedCB(rec.snapshot)
	}

	queueCh <- rec
	return rec.snapshot, nil
}

// AddTask is kept for backward compatibility with old forms.
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
	tm.mu.Lock()
	defer tm.mu.Unlock()

	res := make([]TaskSnapshot, 0, len(tm.tasks))
	for _, rec := range tm.tasks {
		res = append(res, rec.snapshot)
	}
	return res
}

func (tm *TaskManager) Stats() (running int, queued int) {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	for _, rec := range tm.tasks {
		switch rec.snapshot.State {
		case TaskRunning:
			running++
		case TaskQueued:
			queued++
		}
	}
	return
}

func (tm *TaskManager) worker() {
	for rec := range tm.queueCh {
		tm.setRunning(rec)
		ctx := &TaskGuiContext{tm: tm, taskID: rec.snapshot.ID, module: rec.snapshot.ModuleID, runtime: rec.runtime}
		err := rec.spec.Run(ctx)
		tm.finish(rec, err)
	}
}

func (tm *TaskManager) setRunning(rec *taskRecord) {
	tm.mu.Lock()
	now := time.Now()
	rec.runtime, rec.cancel = context.WithCancel(context.Background())
	rec.snapshot.State = TaskRunning
	rec.snapshot.StartedAt = now
	rec.snapshot.UpdatedAt = now
	rec.snapshot.StageText = "Выполняется"
	snapshot := rec.snapshot
	cb := tm.onTaskStarted
	tm.mu.Unlock()
	logging.LogTaskStarted(snapshot.ModuleID, snapshot.ID, snapshot.Title)

	if cb != nil {
		cb(snapshot)
	}
}

func (tm *TaskManager) finish(rec *taskRecord, err error) {
	tm.mu.Lock()
	now := time.Now()
	if rec.cancel != nil {
		rec.cancel()
		rec.cancel = nil
	}
	rec.runtime = nil
	rec.snapshot.FinishedAt = now
	rec.snapshot.UpdatedAt = now
	if err != nil {
		rec.snapshot.State = TaskFailed
		rec.snapshot.LastError = err.Error()
		rec.snapshot.StageText = "Завершено с ошибкой"
	} else {
		rec.snapshot.State = TaskSuccess
		rec.snapshot.LastError = ""
		rec.snapshot.Progress = 100
		rec.snapshot.StageText = "Успешно завершено"
	}
	snapshot := rec.snapshot
	cb := tm.onTaskFinished
	tm.mu.Unlock()
	logging.LogTaskFinished(snapshot.ModuleID, snapshot.ID, snapshot.Title, err)

	if cb != nil {
		cb(snapshot)
	}
}

func (tm *TaskManager) updateStatus(taskID, status string) {
	tm.mu.Lock()
	var snap TaskSnapshot
	var cb func(TaskSnapshot)
	for _, rec := range tm.tasks {
		if rec.snapshot.ID == taskID {
			rec.snapshot.StageText = status
			rec.snapshot.UpdatedAt = time.Now()
			snap = rec.snapshot
			cb = tm.onTaskProgress
			break
		}
	}
	tm.mu.Unlock()
	if cb != nil {
		cb(snap)
	}
}

func (tm *TaskManager) updateProgress(taskID string, progress int) {
	tm.mu.Lock()
	var snap TaskSnapshot
	var cb func(TaskSnapshot)
	for _, rec := range tm.tasks {
		if rec.snapshot.ID == taskID {
			if progress < 0 {
				progress = 0
			}
			if progress > 100 {
				progress = 100
			}
			rec.snapshot.Progress = progress
			rec.snapshot.UpdatedAt = time.Now()
			snap = rec.snapshot
			cb = tm.onTaskProgress
			break
		}
	}
	tm.mu.Unlock()
	if cb != nil {
		cb(snap)
	}
}

func (tm *TaskManager) appendLog(taskID, line string) {
	tm.mu.Lock()
	var snap TaskSnapshot
	var cb func(TaskSnapshot, string)
	for _, rec := range tm.tasks {
		if rec.snapshot.ID == taskID {
			rec.snapshot.UpdatedAt = time.Now()
			snap = rec.snapshot
			cb = tm.onTaskLog
			break
		}
	}
	tm.mu.Unlock()
	if cb != nil {
		cb(snap, line)
	}
}
