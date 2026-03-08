package modruntime

import (
	"goMH/assetmgr"
	"goMH/core"
	"io"
	"strings"
	"sync"
)

type consoleOutputWinUtils interface {
	WithConsoleOutput(stdout io.Writer, stderr io.Writer) core.WinUtils
}

func ExecuteWithTaskRuntime(taskCtx core.TaskContext, services core.ModuleServices, fn func(taskServices core.ModuleServices) error) error {
	writer := newTaskLogWriter(taskCtx)
	defer writer.Flush()

	taskServices := core.ModuleServices{
		AssetManager: withTaskAssetManager(services.AssetManager, writer, taskCtx),
		WinUtils:     withTaskWinUtils(services.WinUtils, writer),
	}

	return fn(taskServices)
}

func withTaskAssetManager(am core.AssetManager, writer io.Writer, taskCtx core.TaskContext) core.AssetManager {
	manager, ok := am.(*assetmgr.Manager)
	if !ok {
		return am
	}

	return manager.WithTaskRuntime(
		writer,
		io.Discard,
		func(text string) {
			taskCtx.SetStatus(text)
		},
		func(description string, percent int) {
			taskCtx.SetStatus("Скачивание " + description)
			taskCtx.SetProgress(percent)
		},
		taskCtx.SetCancelable,
		taskCtx.Context(),
	)
}

func withTaskWinUtils(wu core.WinUtils, writer io.Writer) core.WinUtils {
	real, ok := wu.(consoleOutputWinUtils)
	if !ok {
		return wu
	}
	return real.WithConsoleOutput(writer, writer)
}

type taskLogWriter struct {
	mu     sync.Mutex
	ctx    core.TaskContext
	buffer strings.Builder
}

func newTaskLogWriter(ctx core.TaskContext) *taskLogWriter {
	return &taskLogWriter{ctx: ctx}
}

func (w *taskLogWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	w.buffer.Write(p)
	w.flushLocked(false)
	return len(p), nil
}

func (w *taskLogWriter) Flush() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.flushLocked(true)
}

func (w *taskLogWriter) flushLocked(force bool) {
	for {
		text := w.buffer.String()
		index := strings.IndexByte(text, '\n')
		if index == -1 {
			if force && text != "" {
				w.ctx.Info(strings.TrimSpace(text))
				w.buffer.Reset()
			}
			return
		}

		line := strings.TrimSpace(text[:index])
		if line != "" {
			w.ctx.Info(line)
		}

		rest := text[index+1:]
		w.buffer.Reset()
		w.buffer.WriteString(rest)
	}
}
