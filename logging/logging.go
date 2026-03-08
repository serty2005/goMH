package logging

import (
	"context"
	"fmt"
	"goMH/config"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

type InitResult struct {
	LogPath     string
	FileEnabled bool
	Level       slog.Level
	closeFile   func() error
}

var defaultLogPathFunc = defaultLogPath
var fallbackLogWriterFunc = fallbackLogWriter

func (r *InitResult) Close() error {
	if r == nil || r.closeFile == nil {
		return nil
	}
	return r.closeFile()
}

func Init(cfg config.LoggingConfig) (*InitResult, error) {
	level := parseLevel(cfg.Level)
	writer := fallbackLogWriterFunc()

	result := &InitResult{
		FileEnabled: cfg.FileEnabled,
		Level:       level,
	}

	if cfg.FileEnabled {
		logPath, err := defaultLogPathFunc()
		if err != nil {
			return nil, err
		}

		logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)
		if err != nil {
			return nil, fmt.Errorf("не удалось открыть файл лога %s: %w", logPath, err)
		}

		writer = logFile
		result.LogPath = logPath
		result.closeFile = logFile.Close
	}

	handler := NewPrettyHandler(writer, &slog.HandlerOptions{Level: level})
	slog.SetDefault(slog.New(handler))
	return result, nil
}

func fallbackLogWriter() io.Writer {
	return os.Stderr
}

func LogTaskQueued(moduleID, taskID, title string) {
	slog.Info("Задача добавлена в runtime-очередь",
		"module_id", moduleID,
		"task_id", taskID,
		"title", title,
	)
}

func LogTaskStarted(moduleID, taskID, title string) {
	slog.Info("Задача запущена",
		"module_id", moduleID,
		"task_id", taskID,
		"title", title,
	)
}

func LogTaskFinished(moduleID, taskID, title string, err error) {
	attrs := []any{
		"module_id", moduleID,
		"task_id", taskID,
		"title", title,
	}

	if err != nil {
		slog.Error("Задача завершена с ошибкой", append(attrs, "error", err)...)
		return
	}

	slog.Info("Задача завершена успешно", attrs...)
}

func parseLevel(level string) slog.Level {
	switch strings.ToUpper(strings.TrimSpace(level)) {
	case "DEBUG":
		return slog.LevelDebug
	case "WARN", "WARNING":
		return slog.LevelWarn
	case "ERROR":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

func defaultLogPath() (string, error) {
	exePath, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("не удалось определить путь к исполняемому файлу: %w", err)
	}
	return filepath.Join(filepath.Dir(exePath), "goMH.log"), nil
}

type PrettyHandler struct {
	w     io.Writer
	mu    sync.Mutex
	opts  slog.HandlerOptions
	attrs []slog.Attr
}

func NewPrettyHandler(w io.Writer, opts *slog.HandlerOptions) *PrettyHandler {
	handlerOptions := slog.HandlerOptions{}
	if opts != nil {
		handlerOptions = *opts
	}

	return &PrettyHandler{
		w:    w,
		opts: handlerOptions,
	}
}

func (h *PrettyHandler) Enabled(_ context.Context, level slog.Level) bool {
	return level >= h.opts.Level.Level()
}

func (h *PrettyHandler) Handle(_ context.Context, r slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	timeStr := r.Time.Format("2006-01-02 15:04:05.000")
	levelStr := fmt.Sprintf("[%s]", r.Level.String())

	if _, err := fmt.Fprintf(h.w, "%s %-7s %s\n", timeStr, levelStr, r.Message); err != nil {
		return err
	}

	for _, attr := range h.attrs {
		if err := h.printAttr(attr); err != nil {
			return err
		}
	}

	var handleErr error
	r.Attrs(func(attr slog.Attr) bool {
		handleErr = h.printAttr(attr)
		return handleErr == nil
	})

	return handleErr
}

func (h *PrettyHandler) printAttr(attr slog.Attr) error {
	if attr.Key == "" {
		return nil
	}
	_, err := fmt.Fprintf(h.w, "  %s=%s\n", attr.Key, attr.Value.String())
	return err
}

func (h *PrettyHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	cloned := make([]slog.Attr, 0, len(h.attrs)+len(attrs))
	cloned = append(cloned, h.attrs...)
	cloned = append(cloned, attrs...)
	return &PrettyHandler{
		w:     h.w,
		opts:  h.opts,
		attrs: cloned,
	}
}

func (h *PrettyHandler) WithGroup(string) slog.Handler {
	return &PrettyHandler{
		w:     h.w,
		opts:  h.opts,
		attrs: append([]slog.Attr(nil), h.attrs...),
	}
}
