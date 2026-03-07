package distro

import (
	"fmt"
	"goMH/config"
	"goMH/core"
	"goMH/dependencies"
	iikoplugins "goMH/modules/iiko-plugins"
	"goMH/tui"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// DistroAction определяет тип действия
type DistroAction int

const (
	ActionInstallComponent DistroAction = iota
	ActionInstallPortable
	ActionManualPatch
	ActionPlugins
)

// DistroInstallConfig хранит все решения пользователя
type DistroInstallConfig struct {
	Action               DistroAction
	Brand                string // "iiko" or "syrve"
	Component            config.DistroComponent
	Version              string
	Patch                *core.PatchInfo // Может быть nil
	PluginSelection      *iikoplugins.InstallSelection
	RunAutoUpdatePlugins bool
	UninstallOldVersion  bool
	OldVersionString     string // Версия для удаления (если UninstallOldVersion=true)
	PortableSourceType   string // "http" или "ftp" (для portable)
	PortableArchiveName  string // Имя архива (для portable)
	PortableDownloadURL  string // URL (для portable http)
	PortableFTPConfig    config.FTPConfig
	PortableFTPPath      string
}

// Module реализует интерфейс core.Installer.
type Module struct{}

func (m *Module) ID() string { return "iiko" }
func (m *Module) MenuText() string {
	return "iiko / Syrve (Дистрибутивы и плагины)"
}

// brandHandler определяет интерфейс для специфичной логики бренда при конфигурации.
type brandHandler interface {
	// ConfigureBrand проводит опрос пользователя и возвращает конфиг
	ConfigureBrand(ctx core.TaskContext, am core.AssetManager, wu core.WinUtils) (*DistroInstallConfig, error)
}

// Run - основная точка входа (UI слой).
func (m *Module) Run(am core.AssetManager, wu core.WinUtils) error {
	slog.Info("Запуск модуля Distro")
	ctx := tui.NewConsoleContext()

	// 1. Конфигурация
	cfg, err := m.Configure(ctx, am, wu)
	if err != nil {
		if err == tui.ErrExitToMainMenu {
			return nil
		}
		slog.Error("Ошибка конфигурации Distro", "error", err)
		return err
	}
	if cfg == nil {
		return nil
	}

	// 2. Выполнение
	slog.Info("Запуск выполнения задачи Distro", "action", cfg.Action, "brand", cfg.Brand)
	if err := m.Execute(ctx, am, wu, cfg); err != nil {
		slog.Error("Ошибка выполнения задачи Distro", "error", err)
		return err
	}
	return nil
}

// Configure управляет выбором бренда и передает управление хендлеру бренда.
func (m *Module) Configure(ctx core.TaskContext, am core.AssetManager, wu core.WinUtils) (*DistroInstallConfig, error) {
	for {
		choice, err := tui.SelectItem([]tui.ChoiceItem{
			{Title: "iiko", Description: "Дистрибутивы, плагины и патчи"},
			{Title: "Syrve", Description: "Дистрибутивы и portable-сборки"},
		}, tui.SelectionConfig{
			Title:    "Выберите продукт",
			Subtitle: "Esc для возврата в главное меню",
		})
		if err != nil {
			return nil, err
		}

		var handler brandHandler
		var brandName string

		switch choice {
		case 0:
			brandName = "iiko"
			handler = &iikoHandler{}
		case 1:
			brandName = "syrve"
			handler = &syrveHandler{}
		default:
			return nil, nil
		}

		slog.Info("Выбран бренд", "brand", brandName)
		cfg, err := handler.ConfigureBrand(ctx, am, wu)
		if err != nil {
			if err == tui.ErrExitToMainMenu {
				return nil, nil // Возврат в главное меню
			}
			// Если ошибка не критичная, можем попробовать снова
			tui.Error(fmt.Sprintf("Ошибка при выборе параметров: %v", err))
			tui.WaitForAnyKey()
			continue
		}
		if cfg != nil {
			cfg.Brand = brandName
			return cfg, nil
		}
		// Если cfg == nil, значит пользователь нажал "Назад" внутри меню бренда
	}
}

// Execute выполняет задачу на основе конфига.
func (m *Module) Execute(ctx core.TaskContext, am core.AssetManager, wu core.WinUtils, cfg *DistroInstallConfig) error {
	switch cfg.Action {
	case ActionPlugins:
		pluginMod := &iikoplugins.Module{}
		return pluginMod.ExecuteInstall(am, wu, cfg.PluginSelection)

	case ActionManualPatch:
		return m.executeManualPatch(ctx, am, wu, cfg)

	case ActionInstallComponent:
		return m.executeInstallComponent(ctx, am, wu, cfg)

	case ActionInstallPortable:
		return m.executeInstallPortable(ctx, am, wu, cfg)
	}
	return nil
}

// --- EXECUTE IMPLEMENTATIONS ---

func (m *Module) executeInstallComponent(ctx core.TaskContext, am core.AssetManager, wu core.WinUtils, cfg *DistroInstallConfig) error {
	statusMsg := fmt.Sprintf("Установка %s", cfg.Component.MenuText)
	if cfg.Version != "" {
		statusMsg += fmt.Sprintf(" %s", cfg.Version)
	}
	ctx.SetStatus(statusMsg)

	// 1. Удаление старой версии при необходимости
	if cfg.UninstallOldVersion && cfg.OldVersionString != "" {
		ctx.Info(fmt.Sprintf("Удаление старой версии %s...", cfg.OldVersionString))
		if err := m.uninstallVersion(ctx, am, wu, cfg.Component, cfg.Brand, cfg.OldVersionString); err != nil {
			ctx.Warn(fmt.Sprintf("Не удалось удалить старую версию: %v. Попытка продолжить...", err))
		}
	}

	// 2. Скачивание инсталлятора
	downloadURL := cfg.Component.URLTemplate
	if cfg.Version != "" {
		downloadURL = strings.Replace(downloadURL, "{{VERSION}}", cfg.Version, 1)
	}

	// Если версия пустая, формируем имя папки из ID компонента (например, iiko_card)
	folderName := fmt.Sprintf("%s_%s", cfg.Brand, cfg.Version)
	if cfg.Version == "" {
		folderName = cfg.Component.ID
	}

	versionDir := filepath.Join(am.Cfg().RootPath, folderName)
	installerPath := filepath.Join(versionDir, filepath.Base(downloadURL))

	ctx.Info("Скачивание дистрибутива...")
	slog.Debug("Скачивание", "url", downloadURL, "dest", installerPath)

	if strings.HasPrefix(downloadURL, "http") {
		if _, err := am.DownloadHTTPWithProgress(downloadURL, installerPath); err != nil {
			return err
		}
	} else {
		// FTP
		ftpCfg := am.Cfg().FTP[0]
		if _, err := am.DownloadFTPWithProgress(ftpCfg, downloadURL, installerPath); err != nil {
			return err
		}
	}

	// 3. Установка
	ctx.Info("Запуск установщика...")
	if err := m.runInstaller(ctx, am, installerPath, cfg.Component.InstallArgs); err != nil {
		return err
	}

	// 4. Патчинг (если выбран)
	if cfg.Patch != nil {
		installDir := filepath.Dir(cfg.Component.RunAfter)
		// Если RunAfter пуст (например, iikoCard), патчинг невозможен
		if installDir == "." || installDir == "" {
			ctx.Warn("Не удалось определить папку установки для патчинга.")
		} else {
			ctx.Info(fmt.Sprintf("Применение патча %s...", cfg.Patch.ShortName))
			backupDir := versionDir // Бэкап кладем рядом с дистрибутивом
			if err := ApplyPatch(ctx, am, wu, *cfg.Patch, installDir, backupDir); err != nil {
				ctx.Error(fmt.Sprintf("Ошибка применения патча: %v", err))
				return err
			}
		}
	}

	// 5. Автообновление плагинов
	if cfg.RunAutoUpdatePlugins {
		ctx.Info("Запуск автообновления плагинов...")
		pluginsMod := &iikoplugins.Module{}
		if err := pluginsMod.AutoUpdatePlugins(am, wu); err != nil {
			ctx.Warn(fmt.Sprintf("Ошибка автообновления плагинов: %v", err))
		} else {
			ctx.Success("Плагины обновлены.")
		}
	}

	ctx.Success("Установка завершена успешно.")
	return nil
}

func (m *Module) executeInstallPortable(ctx core.TaskContext, am core.AssetManager, wu core.WinUtils, cfg *DistroInstallConfig) error {
	ctx.SetStatus(fmt.Sprintf("Установка Portable %s %s", cfg.Component.MenuText, cfg.Version))

	// Временная директория для загрузки
	tempDir := filepath.Join(am.Cfg().RootPath, "temp", fmt.Sprintf("portable_dl_%d", time.Now().Unix()))
	_ = os.MkdirAll(tempDir, 0755)
	defer os.RemoveAll(tempDir)

	cachePath := filepath.Join(tempDir, filepath.Base(cfg.PortableArchiveName))

	// 1. Скачивание
	ctx.Info("Скачивание архива...")
	if cfg.PortableSourceType == "http" {
		if _, err := am.DownloadHTTPWithProgress(cfg.PortableDownloadURL, cachePath); err != nil {
			return err
		}
	} else {
		if _, err := am.DownloadFTPWithProgress(cfg.PortableFTPConfig, cfg.PortableFTPPath, cachePath); err != nil {
			return err
		}
	}

	// 2. Распаковка
	redaction := strings.ToUpper(strings.Split(strings.TrimPrefix(cfg.Component.ID, cfg.Brand+"_"), "_")[0])
	destDir := filepath.Join(am.Cfg().RootPath, fmt.Sprintf("p_%s%s_%s", cfg.Brand, redaction, cfg.Version))
	ctx.Info(fmt.Sprintf("Распаковка в %s...", destDir))

	sevenZip, err := dependencies.NewClient(am, wu)
	if err != nil {
		return err
	}

	// Распаковка во временную для анализа структуры
	extractTempDir := filepath.Join(tempDir, "extracted")
	if err := sevenZip.Extract(cachePath, extractTempDir, true); err != nil {
		return fmt.Errorf("ошибка распаковки: %w", err)
	}

	// Анализ вложенности
	sourceDir := extractTempDir
	entries, _ := os.ReadDir(extractTempDir)
	if len(entries) == 1 && entries[0].IsDir() {
		sourceDir = filepath.Join(extractTempDir, entries[0].Name())
	}

	// Копирование в целевую
	_ = os.MkdirAll(destDir, 0755)
	if err := wu.CopyDir(sourceDir, destDir); err != nil {
		return fmt.Errorf("ошибка копирования файлов: %w", err)
	}

	// 3. Создание ярлыка
	ctx.Info("Создание ярлыка...")
	exeName := filepath.Base(cfg.Component.RunAfter)
	if exeName == "." || exeName == "" {
		if strings.Contains(strings.ToLower(cfg.Component.ID), "front") {
			exeName = "iikoFront.Net.exe"
		} else {
			exeName = "BackOffice.exe"
		}
	}

	targetExePath := filepath.Join(destDir, exeName)
	if _, err := os.Stat(targetExePath); os.IsNotExist(err) {
		// Рекурсивный поиск
		if found, err := wu.FindFileRecursive(destDir, exeName); err == nil {
			targetExePath = found
		} else {
			ctx.Warn(fmt.Sprintf("Файл %s не найден, ярлык может не работать.", exeName))
		}
	}

	desktopDir, err := wu.GetDesktopDir()
	if err == nil {
		shortcutName := fmt.Sprintf("%s %s (Portable).lnk", cfg.Component.MenuText, cfg.Version)
		shortcutName = strings.Map(func(r rune) rune {
			if strings.ContainsRune(`<>:"/\|?*`, r) {
				return -1
			}
			return r
		}, shortcutName)
		shortcutPath := filepath.Join(desktopDir, shortcutName)
		args := fmt.Sprintf(`/AdditionalTmpFolder="%s_%s"`, cfg.Brand, cfg.Version)

		if err := wu.CreateShortcut(targetExePath, shortcutPath, args); err != nil {
			ctx.Warn(fmt.Sprintf("Не удалось создать ярлык: %v", err))
		} else {
			ctx.Success(fmt.Sprintf("Ярлык создан: %s", shortcutName))
		}
	}

	ctx.Success("Portable версия установлена.")
	return nil
}

func (m *Module) executeManualPatch(ctx core.TaskContext, am core.AssetManager, wu core.WinUtils, cfg *DistroInstallConfig) error {
	ctx.SetStatus("Установка патча вручную")
	const iikoFrontDir = `C:\Program Files\iiko\iikoRMS\Front.Net`

	// Версия уже была определена в Configure, но проверим путь
	if _, err := os.Stat(iikoFrontDir); os.IsNotExist(err) {
		return fmt.Errorf("директория iikoFront не найдена: %s", iikoFrontDir)
	}

	backupDir := filepath.Join(am.Cfg().RootPath, fmt.Sprintf("iiko_%s", cfg.Version))
	ctx.Info(fmt.Sprintf("Применение патча %s...", cfg.Patch.ShortName))

	if err := ApplyPatch(ctx, am, wu, *cfg.Patch, iikoFrontDir, backupDir); err != nil {
		return err
	}
	ctx.Success("Патч успешно установлен.")
	return nil
}

// uninstallVersion удаляет старую версию перед установкой новой
func (m *Module) uninstallVersion(ctx core.TaskContext, am core.AssetManager, wu core.WinUtils, component config.DistroComponent, brand, version string) error {
	// 1. Попытка системного удаления
	var systemAppName string
	switch component.ID {
	case "iiko_front":
		systemAppName = "iikoRMS Front"
	case "iiko_rms_back":
		systemAppName = "iikoRMS BackOffice"
	case "syrve_front":
		systemAppName = "SyrveRMS Front"
		// ... добавить другие по необходимости
	}

	if systemAppName != "" {
		ctx.Info(fmt.Sprintf("Попытка системного удаления '%s'...", systemAppName))
		if err := wu.UninstallSystemApp(systemAppName); err == nil {
			return nil
		}
	}

	// 2. Использование установщика
	downloadURL := strings.Replace(component.URLTemplate, "{{VERSION}}", version, 1)
	fileName := filepath.Base(downloadURL)
	versionDir := filepath.Join(am.Cfg().RootPath, fmt.Sprintf("%s_%s", brand, version))
	localInstallerPath := filepath.Join(versionDir, fileName)

	if _, err := os.Stat(localInstallerPath); err != nil {
		ctx.Info("Скачивание деинсталлятора...")
		_ = os.MkdirAll(versionDir, 0755)
		if strings.HasPrefix(downloadURL, "http") {
			if _, err := am.DownloadHTTPWithProgress(downloadURL, localInstallerPath); err != nil {
				return err
			}
		} else {
			ftpCfg := am.Cfg().FTP[0]
			if _, err := am.DownloadFTPWithProgress(ftpCfg, downloadURL, localInstallerPath); err != nil {
				return err
			}
		}
	}

	uninstallArgs := strings.ReplaceAll(component.InstallArgs, "/install", "/uninstall")
	if !strings.Contains(uninstallArgs, "/uninstall") {
		uninstallArgs = "/uninstall " + uninstallArgs
	}

	ctx.Info("Запуск деинсталлятора...")
	return m.runInstaller(ctx, am, localInstallerPath, uninstallArgs)
}

func (m *Module) runInstaller(ctx core.TaskContext, am core.AssetManager, installerPath, args string) error {
	logFileName := fmt.Sprintf("installer_log_%d.txt", time.Now().Unix())
	tempLogPath := filepath.Join(am.Cfg().RootPath, "temp", logFileName)
	_ = os.MkdirAll(filepath.Dir(tempLogPath), 0755)

	finalArgs := append(strings.Fields(args), "/log", tempLogPath)
	slog.Info("Запуск MSI/EXE", "path", installerPath, "args", finalArgs)

	cmd := exec.Command(installerPath, finalArgs...)
	output, err := cmd.CombinedOutput()

	var exitCode int
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			return fmt.Errorf("ошибка запуска: %w", err)
		}
	}

	if exitCode != 0 {
		finalLogPath := filepath.Join(am.Cfg().RootPath, logFileName)
		_ = os.Rename(tempLogPath, finalLogPath)
		return fmt.Errorf("код возврата %d. Лог: %s. Вывод: %s", exitCode, finalLogPath, string(output))
	}

	_ = os.Remove(tempLogPath)
	return nil
}
