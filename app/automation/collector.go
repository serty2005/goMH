package automation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"goMH/core"
	"goMH/taskqueue"
	"io"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"
)

var queueLogPattern = regexp.MustCompile(`^\[(?P<time>[^\]]+)\] \[(?P<level>[^\]]+)\] (?P<message>.*)$`)

type executionCollector struct {
	mu            sync.Mutex
	requestID     string
	correlationID string
	operationID   string
	stderr        io.Writer
	logs          []LogEntry
	task          *TaskPayload
	lastStatus    string
	lastProgress  int
}

func newExecutionCollector(requestID string, correlationID string, operationID string, stderr io.Writer) *executionCollector {
	if stderr == nil {
		stderr = io.Discard
	}
	return &executionCollector{
		requestID:     requestID,
		correlationID: correlationID,
		operationID:   operationID,
		stderr:        stderr,
		lastProgress:  -1,
	}
}

func (c *executionCollector) AttachQueue(queue *taskqueue.Queue) func() {
	if queue == nil {
		return func() {}
	}

	return queue.AddListener(taskqueue.Listener{
		OnTaskEnqueued: func(snapshot taskqueue.TaskSnapshot) {
			c.updateTask(snapshot)
			c.emitStatus("queued", snapshot.StageText, snapshot.Progress)
		},
		OnTaskStarted: func(snapshot taskqueue.TaskSnapshot) {
			c.updateTask(snapshot)
			c.emitStatus("started", snapshot.StageText, snapshot.Progress)
		},
		OnTaskUpdated: func(snapshot taskqueue.TaskSnapshot) {
			c.updateTask(snapshot)
		},
		OnTaskLog: func(snapshot taskqueue.TaskSnapshot, line string) {
			c.updateTask(snapshot)
			c.appendParsedQueueLog(line)
		},
		OnTaskFinished: func(snapshot taskqueue.TaskSnapshot) {
			c.updateTask(snapshot)
			c.emitStatus("finished", snapshot.StageText, snapshot.Progress)
		},
	})
}

func (c *executionCollector) StartImmediate(moduleID string, title string) {
	c.mu.Lock()
	c.task = &TaskPayload{
		ModuleID:  moduleID,
		Title:     title,
		State:     "running",
		StartedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	c.lastStatus = ""
	c.lastProgress = -1
	c.mu.Unlock()
	c.emitStatus("started", title, 0)
}

func (c *executionCollector) FinishImmediate(err error) {
	c.mu.Lock()
	if c.task == nil {
		c.task = &TaskPayload{}
	}
	c.task.UpdatedAt = time.Now()
	c.task.FinishedAt = c.task.UpdatedAt
	if err != nil {
		c.task.State = "failed"
		c.task.LastError = err.Error()
		if c.task.Status == "" {
			c.task.Status = "Завершено с ошибкой"
		}
	} else {
		c.task.State = "success"
		c.task.Progress = 100
		if c.task.Status == "" {
			c.task.Status = "Успешно завершено"
		}
	}
	task := *c.task
	c.mu.Unlock()
	c.emitStatus("finished", task.Status, task.Progress)
}

func (c *executionCollector) appendLog(level string, message string, source string) {
	entry := LogEntry{
		Timestamp: time.Now(),
		Level:     strings.ToUpper(strings.TrimSpace(level)),
		Message:   message,
		Source:    source,
	}

	c.mu.Lock()
	c.logs = append(c.logs, entry)
	c.mu.Unlock()
}

func (c *executionCollector) appendParsedQueueLog(line string) {
	matches := queueLogPattern.FindStringSubmatch(strings.TrimSpace(line))
	if len(matches) != 4 {
		c.appendLog("INFO", strings.TrimSpace(line), "taskqueue")
		return
	}
	c.appendLog(matches[2], strings.TrimSpace(matches[3]), "taskqueue")
}

func (c *executionCollector) updateTask(snapshot taskqueue.TaskSnapshot) {
	task := queueSnapshotToPayload(snapshot)

	c.mu.Lock()
	c.task = &task
	statusChanged := task.Status != "" && task.Status != c.lastStatus
	progressChanged := task.Progress != c.lastProgress
	if statusChanged {
		c.lastStatus = task.Status
	}
	if progressChanged {
		c.lastProgress = task.Progress
	}
	c.mu.Unlock()

	if statusChanged || progressChanged {
		c.emitStatus("progress", task.Status, task.Progress)
	}
}

func (c *executionCollector) emitStatus(kind string, message string, progress int) {
	payload := map[string]any{
		"type":         "automation_status",
		"timestamp":    time.Now().UTC().Format(time.RFC3339Nano),
		"request_id":   c.requestID,
		"operation_id": c.operationID,
		"status":       kind,
		"message":      message,
	}
	if c.correlationID != "" {
		payload["correlation_id"] = c.correlationID
	}
	if progress >= 0 {
		payload["progress"] = progress
	}

	data, err := json.Marshal(payload)
	if err != nil {
		fmt.Fprintf(os.Stderr, "{\"type\":\"automation_status\",\"request_id\":%q,\"status\":\"marshal_error\"}\n", c.requestID)
		return
	}
	fmt.Fprintln(c.stderr, string(data))
}

func (c *executionCollector) ImmediateContext(ctx context.Context) core.TaskContext {
	return &immediateContext{
		runtime:   ctx,
		collector: c,
	}
}

func (c *executionCollector) Logs(includeEntries bool, path string) (*LogsPayload, []string) {
	c.mu.Lock()
	entries := make([]LogEntry, len(c.logs))
	copy(entries, c.logs)
	c.mu.Unlock()

	var warnings []string
	logs := &LogsPayload{}
	if includeEntries {
		logs.Entries = entries
	}
	if path != "" {
		if err := writeTaskLog(path, entries); err != nil {
			warnings = append(warnings, fmt.Sprintf("не удалось записать task log в %s: %v", path, err))
		} else {
			logs.Path = path
		}
	}
	if !includeEntries && logs.Path == "" {
		return nil, warnings
	}
	return logs, warnings
}

func (c *executionCollector) Task() *TaskPayload {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.task == nil {
		return nil
	}
	task := *c.task
	return &task
}

type immediateContext struct {
	runtime   context.Context
	collector *executionCollector
}

func (c *immediateContext) Context() context.Context {
	if c.runtime == nil {
		return context.Background()
	}
	return c.runtime
}

func (c *immediateContext) Info(msg string) {
	c.collector.appendLog("INFO", msg, "immediate")
}

func (c *immediateContext) Warn(msg string) {
	c.collector.appendLog("WARN", msg, "immediate")
}

func (c *immediateContext) Error(msg string) {
	c.collector.appendLog("ERROR", msg, "immediate")
}

func (c *immediateContext) Success(msg string) {
	c.collector.appendLog("SUCCESS", msg, "immediate")
}

func (c *immediateContext) SetStatus(text string) {
	c.collector.mu.Lock()
	if c.collector.task == nil {
		c.collector.task = &TaskPayload{}
	}
	c.collector.task.Status = text
	c.collector.task.UpdatedAt = time.Now()
	c.collector.mu.Unlock()
	c.collector.emitStatus("progress", text, c.progress())
}

func (c *immediateContext) SetProgress(percent int) {
	c.collector.mu.Lock()
	if c.collector.task == nil {
		c.collector.task = &TaskPayload{}
	}
	c.collector.task.Progress = percent
	c.collector.task.UpdatedAt = time.Now()
	c.collector.mu.Unlock()
	c.collector.emitStatus("progress", c.status(), percent)
}

func (c *immediateContext) SetCancelable(enabled bool) {
	c.collector.mu.Lock()
	if c.collector.task == nil {
		c.collector.task = &TaskPayload{}
	}
	c.collector.task.Cancelable = enabled
	c.collector.task.UpdatedAt = time.Now()
	c.collector.mu.Unlock()
}

func (c *immediateContext) OpenLiveLog(filePath string) error {
	return newRunError(ExitUnsupportedAutomation, "interactive_required", fmt.Sprintf("просмотр live-лога %q не поддерживается в automation mode", filePath), nil)
}

func (c *immediateContext) status() string {
	c.collector.mu.Lock()
	defer c.collector.mu.Unlock()
	if c.collector.task == nil {
		return ""
	}
	return c.collector.task.Status
}

func (c *immediateContext) progress() int {
	c.collector.mu.Lock()
	defer c.collector.mu.Unlock()
	if c.collector.task == nil {
		return 0
	}
	return c.collector.task.Progress
}

func queueSnapshotToPayload(snapshot taskqueue.TaskSnapshot) TaskPayload {
	return TaskPayload{
		ID:         snapshot.ID,
		ModuleID:   snapshot.ModuleID,
		Title:      snapshot.Title,
		Signature:  snapshot.Signature,
		State:      string(snapshot.State),
		Progress:   snapshot.Progress,
		Status:     snapshot.StageText,
		LastError:  snapshot.LastError,
		Cancelable: snapshot.Cancelable,
		EnqueuedAt: snapshot.EnqueuedAt,
		StartedAt:  snapshot.StartedAt,
		FinishedAt: snapshot.FinishedAt,
		UpdatedAt:  snapshot.UpdatedAt,
	}
}

func writeTaskLog(path string, entries []LogEntry) error {
	if strings.TrimSpace(path) == "" {
		return nil
	}
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	defer file.Close()

	encoder := json.NewEncoder(file)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	return encoder.Encode(map[string]any{
		"contract_version": ContractVersion,
		"entries":          entries,
	})
}

func taskError(task *TaskPayload, requestCtx context.Context) error {
	if task == nil {
		if requestCtx != nil && errors.Is(requestCtx.Err(), context.DeadlineExceeded) {
			return newRunError(ExitTimeout, "timeout", "операция превысила timeout_seconds", requestCtx.Err())
		}
		return nil
	}
	if task.State == "success" || task.State == "" {
		return nil
	}
	if requestCtx != nil && errors.Is(requestCtx.Err(), context.DeadlineExceeded) {
		return newRunError(ExitTimeout, "timeout", "операция превысила timeout_seconds", requestCtx.Err())
	}
	return newRunError(ExitExecutionFailed, "task_failed", task.LastError, nil)
}
