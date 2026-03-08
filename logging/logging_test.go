package logging

import (
	"bytes"
	"goMH/config"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInitDisablesFileLogging(t *testing.T) {
	restorePath := overrideDefaultLogPath(t, filepath.Join(t.TempDir(), "disabled.log"))
	fallbackBuffer := &bytes.Buffer{}
	restoreFallback := overrideFallbackLogWriter(t, fallbackBuffer)
	defer restoreFallback()

	result, err := Init(config.LoggingConfig{Level: "WARN", FileEnabled: false})
	if err != nil {
		restorePath()
		t.Fatalf("Init вернул ошибку: %v", err)
	}
	defer restorePath()

	if result.FileEnabled {
		t.Fatal("ожидалось отключенное файловое логирование")
	}
	if result.LogPath != "" {
		t.Fatalf("путь к логу должен быть пустым, получено %q", result.LogPath)
	}
	slog.Warn("Проверка fallback-логирования")
	if err := result.Close(); err != nil {
		t.Fatalf("Close вернул ошибку: %v", err)
	}
	if content := fallbackBuffer.String(); !strings.Contains(content, "Проверка fallback-логирования") {
		t.Fatalf("ожидалась запись в fallback sink, содержимое:\n%s", content)
	}
}

func TestInitWritesTaskLifecycleToConfiguredLogFile(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "goMH.log")
	restorePath := overrideDefaultLogPath(t, logPath)

	result, err := Init(config.LoggingConfig{Level: "INFO", FileEnabled: true})
	if err != nil {
		restorePath()
		t.Fatalf("Init вернул ошибку: %v", err)
	}
	defer restorePath()

	LogTaskQueued("ServiceUtils", "task-1", "Просмотр лога")
	LogTaskStarted("ServiceUtils", "task-1", "Просмотр лога")
	LogTaskFinished("ServiceUtils", "task-1", "Просмотр лога", nil)
	slog.Info("Проверка записи")

	if err := result.Close(); err != nil {
		t.Fatalf("Close вернул ошибку: %v", err)
	}

	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("не удалось прочитать лог-файл: %v", err)
	}
	content := string(data)
	for _, want := range []string{
		"Задача добавлена в runtime-очередь",
		"Задача запущена",
		"Задача завершена успешно",
		"module_id=ServiceUtils",
		"task_id=task-1",
		"title=Просмотр лога",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("в лог-файле не найден фрагмент %q.\nСодержимое:\n%s", want, content)
		}
	}
}

func overrideDefaultLogPath(t *testing.T, path string) func() {
	t.Helper()
	prev := defaultLogPathFunc
	defaultLogPathFunc = func() (string, error) {
		return path, nil
	}
	return func() {
		defaultLogPathFunc = prev
	}
}

func overrideFallbackLogWriter(t *testing.T, writer *bytes.Buffer) func() {
	t.Helper()
	prev := fallbackLogWriterFunc
	fallbackLogWriterFunc = func() io.Writer {
		return writer
	}
	return func() {
		fallbackLogWriterFunc = prev
	}
}
