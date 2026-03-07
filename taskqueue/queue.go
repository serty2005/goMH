package taskqueue

import (
	"context"
	"errors"
	"fmt"
	"goMH/core"
	"sync"
	"sync/atomic"
	"time"
)

var ErrDuplicateTask = errors.New("задача с такой сигнатурой уже находится в очереди или выполняется")

type TaskState string

const (
	TaskQueued  TaskState = "queued"
	TaskRunning TaskState = "running"
	TaskSuccess TaskState = "success"
	TaskFailed  TaskState = "failed"
)

type TaskSpec struct {
	ID         string
	ModuleID   string
	Title      string
	Signature  string
	Exclusive  bool
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
	Cancelable bool
	EnqueuedAt time.Time
	StartedAt  time.Time
	FinishedAt time.Time
	UpdatedAt  time.Time
}

type RunSummary struct {
	Started  int
	Success  int
	Failed   int
	Skipped  int
	Finished bool
}

type taskRecord struct {
	spec     TaskSpec
	snapshot TaskSnapshot
	logs     []string
	cancel   func()
}

type Queue struct {
	mu                sync.Mutex
	cond              *sync.Cond
	tasks             []*taskRecord
	seq               uint64
	logLimit          int
	started           bool
	runningExclusive  bool
	backgroundFactory func(TaskSnapshot) core.TaskContext
}

func New() *Queue {
	queue := &Queue{logLimit: 200}
	queue.cond = sync.NewCond(&queue.mu)
	return queue
}

func (q *Queue) Enqueue(spec TaskSpec) (TaskSnapshot, error) {
	if spec.Run == nil {
		return TaskSnapshot{}, errors.New("для задачи не задан обработчик выполнения")
	}

	now := time.Now()
	if spec.EnqueuedAt.IsZero() {
		spec.EnqueuedAt = now
	}
	if spec.ID == "" {
		spec.ID = fmt.Sprintf("task-%06d", atomic.AddUint64(&q.seq, 1))
	}
	if spec.Signature == "" {
		spec.Signature = spec.ModuleID + "|" + spec.Title
	}

	q.mu.Lock()
	defer q.mu.Unlock()

	for _, rec := range q.tasks {
		if rec.snapshot.Signature != spec.Signature {
			continue
		}
		if rec.snapshot.State == TaskQueued || rec.snapshot.State == TaskRunning {
			return TaskSnapshot{}, ErrDuplicateTask
		}
	}

	snapshot := TaskSnapshot{
		ID:         spec.ID,
		ModuleID:   spec.ModuleID,
		Title:      spec.Title,
		Signature:  spec.Signature,
		State:      TaskQueued,
		StageText:  "Ожидание запуска",
		Progress:   0,
		Cancelable: false,
		EnqueuedAt: spec.EnqueuedAt,
		UpdatedAt:  now,
	}

	rec := &taskRecord{
		spec:     spec,
		snapshot: snapshot,
		logs:     []string{fmt.Sprintf("[%s] [QUEUE] Задача добавлена в очередь", now.Format("15:04:05"))},
	}
	q.tasks = append(q.tasks, rec)
	if q.started {
		q.cond.Broadcast()
	}
	return rec.snapshot, nil
}

func (q *Queue) RemoveQueued(taskID string) bool {
	q.mu.Lock()
	defer q.mu.Unlock()

	for i, rec := range q.tasks {
		if rec.snapshot.ID != taskID || rec.snapshot.State != TaskQueued {
			continue
		}
		q.tasks = append(q.tasks[:i], q.tasks[i+1:]...)
		return true
	}
	return false
}

func (q *Queue) Cancel(taskID string) bool {
	var cancel func()

	q.mu.Lock()
	rec := q.findLocked(taskID)
	if rec == nil || rec.snapshot.State != TaskRunning || !rec.snapshot.Cancelable || rec.cancel == nil {
		q.mu.Unlock()
		return false
	}

	now := time.Now()
	cancel = rec.cancel
	rec.cancel = nil
	rec.snapshot.Cancelable = false
	rec.snapshot.StageText = "Отмена скачивания..."
	rec.snapshot.UpdatedAt = now
	q.appendLogLocked(rec, fmt.Sprintf("[%s] [WARN] Запрошена отмена скачивания", now.Format("15:04:05")))
	q.mu.Unlock()

	cancel()
	return true
}

func (q *Queue) ClearFinished() int {
	q.mu.Lock()
	defer q.mu.Unlock()

	filtered := q.tasks[:0]
	removed := 0
	for _, rec := range q.tasks {
		if rec.snapshot.State == TaskSuccess || rec.snapshot.State == TaskFailed {
			removed++
			continue
		}
		filtered = append(filtered, rec)
	}
	q.tasks = filtered
	return removed
}

func (q *Queue) Snapshots() []TaskSnapshot {
	q.mu.Lock()
	defer q.mu.Unlock()

	result := make([]TaskSnapshot, 0, len(q.tasks))
	for _, rec := range q.tasks {
		result = append(result, rec.snapshot)
	}
	return result
}

func (q *Queue) Logs(taskID string) []string {
	q.mu.Lock()
	defer q.mu.Unlock()

	for _, rec := range q.tasks {
		if rec.snapshot.ID == taskID {
			out := make([]string, len(rec.logs))
			copy(out, rec.logs)
			return out
		}
	}
	return nil
}

func (q *Queue) PendingCount() int {
	q.mu.Lock()
	defer q.mu.Unlock()

	count := 0
	for _, rec := range q.tasks {
		if rec.snapshot.State == TaskQueued {
			count++
		}
	}
	return count
}

func (q *Queue) HasPending() bool {
	return q.PendingCount() > 0
}

func (q *Queue) Started() bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.started
}

func (q *Queue) StartBackground(contextFactory func(TaskSnapshot) core.TaskContext) bool {
	q.mu.Lock()
	defer q.mu.Unlock()

	if q.started {
		return false
	}

	q.started = true
	q.backgroundFactory = contextFactory
	go q.dispatcher()
	q.cond.Broadcast()
	return true
}

func (q *Queue) RunPending(contextFactory func(TaskSnapshot) core.TaskContext) RunSummary {
	pending := q.pendingRecords()
	summary := RunSummary{
		Started:  len(pending),
		Skipped:  0,
		Success:  0,
		Failed:   0,
		Finished: true,
	}
	if len(pending) == 0 {
		summary.Finished = false
		return summary
	}

	for _, rec := range pending {
		snapshot := q.markRunning(rec)
		ctx := contextFactory(snapshot)
		if ctx == nil {
			ctx = newSilentContext()
		}

		err := rec.spec.Run(ctx)
		if err != nil {
			summary.Failed++
		} else {
			summary.Success++
		}
		q.markFinished(rec, err)
	}

	return summary
}

func (q *Queue) pendingRecords() []*taskRecord {
	q.mu.Lock()
	defer q.mu.Unlock()

	result := make([]*taskRecord, 0)
	for _, rec := range q.tasks {
		if rec.snapshot.State == TaskQueued {
			result = append(result, rec)
		}
	}
	return result
}

func (q *Queue) dispatcher() {
	for {
		rec, snapshot, contextFactory := q.waitNextTask()
		go q.runTask(rec, snapshot, contextFactory)
	}
}

func (q *Queue) runTask(rec *taskRecord, snapshot TaskSnapshot, contextFactory func(TaskSnapshot) core.TaskContext) {
	ctx := contextFactory(snapshot)
	if ctx == nil {
		ctx = newSilentContext()
	}

	err := rec.spec.Run(ctx)
	q.markFinished(rec, err)
}

func (q *Queue) waitNextTask() (*taskRecord, TaskSnapshot, func(TaskSnapshot) core.TaskContext) {
	q.mu.Lock()
	defer q.mu.Unlock()

	for {
		for _, rec := range q.tasks {
			if rec.snapshot.State != TaskQueued {
				continue
			}
			if rec.spec.Exclusive && q.runningExclusive {
				continue
			}

			snapshot := q.markRunningLocked(rec)
			if rec.spec.Exclusive {
				q.runningExclusive = true
			}
			return rec, snapshot, q.backgroundFactory
		}

		q.cond.Wait()
	}
}

func (q *Queue) markRunning(rec *taskRecord) TaskSnapshot {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.markRunningLocked(rec)
}

func (q *Queue) markRunningLocked(rec *taskRecord) TaskSnapshot {
	now := time.Now()
	rec.snapshot.State = TaskRunning
	rec.snapshot.StartedAt = now
	rec.snapshot.UpdatedAt = now
	rec.snapshot.StageText = "Выполняется"
	rec.snapshot.LastError = ""
	rec.snapshot.Progress = 0
	rec.snapshot.Cancelable = false
	rec.cancel = nil
	q.appendLogLocked(rec, fmt.Sprintf("[%s] [STAGE] Задача запущена", now.Format("15:04:05")))
	return rec.snapshot
}

func (q *Queue) markFinished(rec *taskRecord, err error) {
	q.mu.Lock()
	defer q.mu.Unlock()

	now := time.Now()
	rec.snapshot.FinishedAt = now
	rec.snapshot.UpdatedAt = now
	rec.snapshot.Cancelable = false
	rec.cancel = nil
	if rec.spec.Exclusive {
		q.runningExclusive = false
	}

	if err != nil {
		rec.snapshot.State = TaskFailed
		if errors.Is(err, context.Canceled) {
			rec.snapshot.LastError = "Операция отменена пользователем"
			rec.snapshot.StageText = "Отменено"
			q.appendLogLocked(rec, fmt.Sprintf("[%s] [WARN] Операция отменена пользователем", now.Format("15:04:05")))
		} else {
			rec.snapshot.LastError = err.Error()
			rec.snapshot.StageText = "Завершено с ошибкой"
			q.appendLogLocked(rec, fmt.Sprintf("[%s] [ERROR] %s", now.Format("15:04:05"), err.Error()))
		}
		q.cond.Broadcast()
		return
	}

	rec.snapshot.State = TaskSuccess
	rec.snapshot.LastError = ""
	rec.snapshot.StageText = "Успешно завершено"
	rec.snapshot.Progress = 100
	q.appendLogLocked(rec, fmt.Sprintf("[%s] [SUCCESS] Задача завершена успешно", now.Format("15:04:05")))
	q.cond.Broadcast()
}

func (q *Queue) updateStatus(taskID, status string) {
	q.mu.Lock()
	defer q.mu.Unlock()

	if rec := q.findLocked(taskID); rec != nil {
		rec.snapshot.StageText = status
		rec.snapshot.UpdatedAt = time.Now()
		q.appendLogLocked(rec, fmt.Sprintf("[%s] [STAGE] %s", time.Now().Format("15:04:05"), status))
	}
}

func (q *Queue) updateProgress(taskID string, progress int) {
	q.mu.Lock()
	defer q.mu.Unlock()

	if rec := q.findLocked(taskID); rec != nil {
		if progress < 0 {
			progress = 0
		}
		if progress > 100 {
			progress = 100
		}
		rec.snapshot.Progress = progress
		rec.snapshot.UpdatedAt = time.Now()
	}
}

func (q *Queue) setCancelable(taskID string, cancel func()) {
	q.mu.Lock()
	defer q.mu.Unlock()

	if rec := q.findLocked(taskID); rec != nil {
		rec.cancel = cancel
		rec.snapshot.Cancelable = cancel != nil
		rec.snapshot.UpdatedAt = time.Now()
	}
}

func (q *Queue) appendLog(taskID, level, line string) {
	q.mu.Lock()
	defer q.mu.Unlock()

	if rec := q.findLocked(taskID); rec != nil {
		rec.snapshot.UpdatedAt = time.Now()
		q.appendLogLocked(rec, fmt.Sprintf("[%s] [%s] %s", time.Now().Format("15:04:05"), level, line))
	}
}

func (q *Queue) appendRawLog(taskID, line string) {
	q.mu.Lock()
	defer q.mu.Unlock()

	if rec := q.findLocked(taskID); rec != nil {
		rec.snapshot.UpdatedAt = time.Now()
		q.appendLogLocked(rec, line)
	}
}

func (q *Queue) findLocked(taskID string) *taskRecord {
	for _, rec := range q.tasks {
		if rec.snapshot.ID == taskID {
			return rec
		}
	}
	return nil
}

func (q *Queue) appendLogLocked(rec *taskRecord, line string) {
	rec.logs = append(rec.logs, line)
	if len(rec.logs) > q.logLimit {
		rec.logs = rec.logs[len(rec.logs)-q.logLimit:]
	}
}

type taskContext struct {
	queue  *Queue
	taskID string
}

func NewTaskContext(queue *Queue, taskID string) core.TaskContext {
	return &taskContext{
		queue:  queue,
		taskID: taskID,
	}
}

func (c *taskContext) Info(msg string) {
	c.queue.appendLog(c.taskID, "INFO", msg)
}

func (c *taskContext) Warn(msg string) {
	c.queue.appendLog(c.taskID, "WARN", msg)
}

func (c *taskContext) Error(msg string) {
	c.queue.appendLog(c.taskID, "ERROR", msg)
}

func (c *taskContext) Success(msg string) {
	c.queue.appendLog(c.taskID, "SUCCESS", msg)
}

func (c *taskContext) SetStatus(text string) {
	c.queue.updateStatus(c.taskID, text)
}

func (c *taskContext) SetProgress(percent int) {
	c.queue.updateProgress(c.taskID, percent)
}

func (c *taskContext) LogLine(line string) {
	c.queue.appendRawLog(c.taskID, line)
}

func (c *taskContext) SetCancelable(cancel func()) {
	c.queue.setCancelable(c.taskID, cancel)
}

func (c *taskContext) ClearCancelable() {
	c.queue.setCancelable(c.taskID, nil)
}

type silentContext struct{}

func newSilentContext() core.TaskContext {
	return &silentContext{}
}

func (c *silentContext) Info(string)      {}
func (c *silentContext) Warn(string)      {}
func (c *silentContext) Error(string)     {}
func (c *silentContext) Success(string)   {}
func (c *silentContext) SetStatus(string) {}
func (c *silentContext) SetProgress(int)  {}
func (c *silentContext) LogLine(string)   {}
func (c *silentContext) SetCancelable(func()) {
}
func (c *silentContext) ClearCancelable() {}
