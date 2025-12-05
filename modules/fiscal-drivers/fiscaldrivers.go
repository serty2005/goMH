package fiscaldrivers

import (
	"errors"
	"fmt"
	"goMH/config"
	"goMH/core"
	"goMH/tui"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type Module struct{}

// DriverInstallConfig хранит выбранный драйвер для установки
type DriverInstallConfig struct {
	Driver config.FiscalDriver
}

func (m *Module) ID() string {
	return "FiscalDrivers"
}

func (m *Module) MenuText() string {
	return "Установка драйверов фискальных регистраторов"
}

// Run - точка входа (UI слой)
func (m *Module) Run(am core.AssetManager, wu core.WinUtils) error {
	slog.Info("Запуск модуля FiscalDrivers")
	ctx := tui.NewConsoleContext()

	// 1. Конфигурация
	cfg, err := m.Configure(ctx, am)
	if err != nil {
		if cfg == nil {
			slog.Info("Выбор драйвера отменен пользователем")
			return nil
		}
		slog.Error("Ошибка конфигурации драйверов", "error", err)
		return err
	}

	// 2. Исполнение
	if cfg != nil {
		slog.Info("Запуск установки драйвера", "driver_id", cfg.Driver.ID)
		return m.Execute(ctx, am, wu, cfg)
	}
	return nil
}

// Configure - показывает меню и возвращает выбранный драйвер
func (m *Module) Configure(ctx core.TaskContext, am core.AssetManager) (*DriverInstallConfig, error) {
	drivers := am.Cfg().FiscalDriversConfig
	if len(drivers) == 0 {
		return nil, errors.New("в конфигурации не определено ни одного драйвера")
	}

	for {
		tui.ClearScreen()
		tui.Title("\n--- Выберите драйвер для установки ---")
		for i, driver := range drivers {
			fmt.Printf(" %d. %s\n", i+1, driver.MenuText)
		}
		fmt.Println("\n 0. Назад")
		fmt.Print("Выберите пункт: ")

		key, err := tui.ReadKey()
		if err != nil {
			return nil, err
		}
		if key == "0" {
			return nil, nil // Назад
		}

		choice, err := strconv.Atoi(key)
		if err != nil || choice < 1 || choice > len(drivers) {
			continue // Игнорируем неверный ввод
		}

		selected := drivers[choice-1]
		slog.Info("Пользователь выбрал драйвер", "name", selected.MenuText, "id", selected.ID)
		return &DriverInstallConfig{Driver: selected}, nil
	}
}

// Execute - скачивание и установка (фоновый процесс)
func (m *Module) Execute(ctx core.TaskContext, am core.AssetManager, wu core.WinUtils, cfg *DriverInstallConfig) error {
	ctx.SetStatus(fmt.Sprintf("Установка драйвера: %s", cfg.Driver.MenuText))

	// Логика предварительного удаления (специфична для Poscenter/KKTlab, но полезна везде)
	// Если драйвер помечен как "универсальный" (по ID), запускаем чистку.
	if cfg.Driver.ID == "poscenter" || cfg.Driver.ID == "kktlab" {
		ctx.Info("Запуск процедуры очистки старых версий...")
		if err := m.uninstallExistingDrivers(ctx, wu); err != nil {
			ctx.Warn(fmt.Sprintf("Предупреждение: ошибка при удалении старых драйверов: %v", err))
			slog.Warn("Ошибка очистки старых драйверов", "error", err)
		}
	}

	// Скачивание
	if cfg.Driver.AssetID == "" {
		return fmt.Errorf("в конфигурации не указан asset_id для драйвера %s", cfg.Driver.ID)
	}
	ctx.Info("Скачивание установщика...")
	slog.Debug("Запрос ассета", "asset_id", cfg.Driver.AssetID)

	installerPath, err := am.DownloadToCache(cfg.Driver.AssetID)
	if err != nil {
		slog.Error("Ошибка скачивания ассета", "error", err)
		return fmt.Errorf("ошибка скачивания установщика: %w", err)
	}

	// Установка
	ctx.Info("Запуск установки...")
	ctx.Info(fmt.Sprintf("Аргументы: %s", cfg.Driver.InstallArgs))

	args := parseInstallerArgs(cfg.Driver.InstallArgs)
	slog.Info("Запуск процесса установки", "path", installerPath, "args", args)

	if _, err := wu.RunCommand(installerPath, args...); err != nil {
		slog.Error("Ошибка выполнения установщика", "error", err)
		return fmt.Errorf("ошибка установки: %w", err)
	}

	ctx.Success("Драйвер успешно установлен.")
	slog.Info("Установка драйвера завершена успешно")
	return nil
}

// uninstallExistingDrivers ищет и удаляет драйверы Штрих/Ритейл по стандартным путям.
func (m *Module) uninstallExistingDrivers(ctx core.TaskContext, wu core.WinUtils) error {
	slog.Info("Начало поиска старых драйверов")

	var dirsToScan []string
	baseDirs := []string{
		`C:\Program Files (x86)\kktlab\DrvKKT`,
		`C:\Program Files (x86)\Poscenter\DrvKKT`,
	}
	for _, dir := range baseDirs {
		if _, err := os.Stat(dir); err == nil {
			dirsToScan = append(dirsToScan, dir)
		}
	}

	// Особый случай для SHTRIH-M (ищем подпапки)
	shtrihBaseDir := `C:\Program Files (x86)\SHTRIH-M`
	if entries, err := os.ReadDir(shtrihBaseDir); err == nil {
		for _, entry := range entries {
			if entry.IsDir() {
				dirsToScan = append(dirsToScan, filepath.Join(shtrihBaseDir, entry.Name()))
			}
		}
	}

	if len(dirsToScan) == 0 {
		slog.Debug("Директории с драйверами не найдены")
		return nil
	}

	var uninstallers []string
	for _, dir := range dirsToScan {
		if path, err := wu.FindFileRecursive(dir, "unins*.exe"); err == nil {
			uninstallers = append(uninstallers, path)
		}
	}

	if len(uninstallers) == 0 {
		slog.Debug("Деинсталляторы не найдены")
		return nil
	}

	ctx.Info(fmt.Sprintf("Найдено %d старых версий. Удаление...", len(uninstallers)))
	slog.Info("Найдены деинсталляторы", "count", len(uninstallers), "paths", uninstallers)

	for _, uninst := range uninstallers {
		ctx.Info(fmt.Sprintf("Удаление: %s", uninst))
		slog.Debug("Запуск деинсталлятора", "path", uninst)

		_, err := wu.RunCommand(uninst, "/VERYSILENT")
		if err != nil {
			slog.Warn("Ошибка запуска деинсталлятора", "path", uninst, "error", err)
		}

		// Даем время на отработку
		time.Sleep(2 * time.Second)

		// Зачистка папки
		parent := filepath.Dir(uninst)
		if err := os.RemoveAll(parent); err != nil {
			slog.Warn("Не удалось удалить папку драйвера", "path", parent, "error", err)
		}
	}

	ctx.Success("Старые версии удалены.")
	return nil
}

func parseInstallerArgs(command string) []string {
	if command == "" {
		return nil
	}

	var args []string
	var current strings.Builder
	inQuotes := false

	for _, r := range command {
		switch {
		case r == '"' && !inQuotes:
			inQuotes = true
		case r == '"' && inQuotes:
			inQuotes = false
		case r == ' ' && !inQuotes && current.Len() > 0:
			args = append(args, current.String())
			current.Reset()
		case r != ' ' || inQuotes:
			current.WriteRune(r)
		}
	}

	if current.Len() > 0 {
		args = append(args, current.String())
	}

	return args
}
