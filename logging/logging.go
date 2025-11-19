// logging/logging.go
package logging

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// Init инициализирует глобальный логгер с кастомным форматированием.
func Init(logLevel string) {
	exePath, err := os.Executable()
	if err != nil {
		exePath = "."
	}
	logDir := filepath.Dir(exePath)
	logPath := filepath.Join(logDir, "goMH.log")

	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)
	if err != nil {
		fmt.Printf("Ошибка создания лога: %v\n", err)
		os.Exit(1)
	}

	var level slog.Level
	switch strings.ToUpper(logLevel) {
	case "DEBUG":
		level = slog.LevelDebug
	case "WARN":
		level = slog.LevelWarn
	case "ERROR":
		level = slog.LevelError
	default:
		level = slog.LevelInfo
	}

	// Используем MultiWriter для вывода и в файл, и в консоль (опционально, сейчас только файл как в оригинале)
	// Если нужно только в файл:
	writer := logFile

	handler := NewPrettyHandler(writer, &slog.HandlerOptions{
		Level: level,
	})

	logger := slog.New(handler)
	slog.SetDefault(logger)

	// Первичное сообщение в старом формате fmt для консоли, чтобы видеть старт
	fmt.Printf("Логирование настроено. Уровень: %s. Файл: %s\n", level.String(), logPath)
	slog.Info("--- Запуск приложения goMH ---")
}

// PrettyHandler - кастомный обработчик для красивого вывода.
type PrettyHandler struct {
	w     io.Writer
	mu    sync.Mutex
	opts  slog.HandlerOptions
	attrs []slog.Attr
}

func NewPrettyHandler(w io.Writer, opts *slog.HandlerOptions) *PrettyHandler {
	return &PrettyHandler{
		w:    w,
		opts: *opts,
	}
}

func (h *PrettyHandler) Enabled(_ context.Context, level slog.Level) bool {
	return level >= h.opts.Level.Level()
}

func (h *PrettyHandler) Handle(_ context.Context, r slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	// 1. Форматирование времени: 2025-11-19 05:23:22.343
	timeStr := r.Time.Format("2006-01-02 15:04:05.000")

	// 2. Уровень лога в квадратных скобках
	levelStr := fmt.Sprintf("[%s]", r.Level.String())

	// 3. Запись заголовка: Time [LEVEL] Message
	// Например: 2025-11-19 05:23:22.343 [DEBUG] Перемещение старой версии в бэкап
	_, err := fmt.Fprintf(h.w, "%s %-7s %s\n", timeStr, levelStr, r.Message)
	if err != nil {
		return err
	}

	// 4. Обработка атрибутов
	// Сначала добавляем атрибуты, добавленные через WithAttrs
	for _, attr := range h.attrs {
		h.printAttr(attr)
	}
	// Затем атрибуты текущей записи
	r.Attrs(func(attr slog.Attr) bool {
		h.printAttr(attr)
		return true
	})

	return nil
}

func (h *PrettyHandler) printAttr(attr slog.Attr) {
	// Пропускаем пустые атрибуты
	if attr.Key == "" {
		return
	}

	value := attr.Value.String()
	// Если значение содержит кавычки или пробелы, можно дополнительно обработать,
	// но по запросу нужно просто key=value, где value может быть путем.
	// Для путей Windows с обратными слешами Go может их экранировать, если использовать %q.
	// Используем %v или %s для чистого вывода.

	// Особая обработка для ошибок, чтобы они выглядели понятнее
	if attr.Key == "error" || attr.Key == "err" {
		fmt.Fprintf(h.w, "  %s=%v\n", attr.Key, value)
		return
	}

	fmt.Fprintf(h.w, "  %s=%s\n", attr.Key, value)
}

func (h *PrettyHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &PrettyHandler{
		w:     h.w,
		opts:  h.opts,
		attrs: append(h.attrs, attrs...),
	}
}

func (h *PrettyHandler) WithGroup(name string) slog.Handler {
	// Группировка в этом простом формате не реализована визуально отступами,
	// возвращаем тот же хендлер (или можно реализовать префикс ключей)
	return h
}
