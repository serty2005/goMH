// logging/logging.go
package logging

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

// Init инициализирует глобальный логгер slog.
func Init(logLevel string) {
	// Определяем путь к лог-файлу рядом с исполняемым файлом.
	exePath, err := os.Executable()
	if err != nil {
		// В случае ошибки используем текущую директорию.
		exePath = "."
	}
	logDir := filepath.Dir(exePath)
	logPath := filepath.Join(logDir, "goMH.log")

	// Открываем или создаем файл лога.
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)
	if err != nil {
		// Если не удалось создать файл, логируем в stderr и выходим.
		slog.Error("Не удалось открыть файл лога", "path", logPath, "error", err)
		os.Exit(1)
	}

	// Определяем уровень логирования из конфигурации.
	var level slog.Level
	switch strings.ToUpper(logLevel) {
	case "DEBUG":
		level = slog.LevelDebug
	case "INFO":
		level = slog.LevelInfo
	case "WARN":
		level = slog.LevelWarn
	case "ERROR":
		level = slog.LevelError
	default:
		// По умолчанию используем INFO.
		level = slog.LevelInfo
	}

	// Создаем обработчик TextHandler, который будет писать в файл.
	handler := slog.NewTextHandler(logFile, &slog.HandlerOptions{
		Level: level,
		// AddSource: true, // Раскомментируйте для добавления пути к файлу и номера строки в лог (полезно для DEBUG).
	})

	// Создаем логгер и устанавливаем его как логгер по умолчанию для всего приложения.
	logger := slog.New(handler)
	slog.SetDefault(logger)

	fmt.Printf("Логирование настроено. Уровень: %s. Файл: %s\n", level.String(), logPath)
	slog.Info("--- Запуск приложения goMH ---")
	slog.Info("Логгер успешно инициализирован", "level", level.String(), "path", logPath)
}
