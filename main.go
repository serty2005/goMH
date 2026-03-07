package main

import (
	"flag"
	"fmt"
	"goMH/assetmgr"
	"goMH/config"
	"goMH/core"
	"goMH/gui"
	"goMH/logging"
	"goMH/modules/distro"
	fiscaldrivers "goMH/modules/fiscal-drivers"
	"goMH/modules/frpc"
	"goMH/modules/regime"
	"goMH/modules/remoteaccess"
	"goMH/modules/selfupdate"
	"goMH/modules/serviceutils"
	"goMH/modules/utm"
	"goMH/modules/vcomcaster"
	"goMH/taskqueue"
	"goMH/tui"
	"goMH/winutils"
	"io"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"golang.org/x/sys/windows/registry"
)

var realWinUtilsConsoleMu sync.Mutex

type RealWinUtils struct {
	stdout io.Writer
	stderr io.Writer
}

func (rw *RealWinUtils) WithConsoleOutput(stdout io.Writer, stderr io.Writer) *RealWinUtils {
	cloned := *rw
	cloned.stdout = stdout
	cloned.stderr = stderr
	return &cloned
}

func (rw *RealWinUtils) withConsoleCapture(fn func() error) error {
	if rw == nil || (rw.stdout == nil && rw.stderr == nil) {
		return fn()
	}

	realWinUtilsConsoleMu.Lock()
	restore := winutils.SetConsoleOutput(rw.stdout, rw.stderr)
	defer realWinUtilsConsoleMu.Unlock()
	defer restore()
	return fn()
}

func (rw *RealWinUtils) RunCommand(name string, args ...string) (string, error) {
	return winutils.RunCommand(name, args...)
}
func (rw *RealWinUtils) ServiceExists(serviceName string) (bool, error) {
	return winutils.ServiceExists(serviceName)
}
func (rw *RealWinUtils) AddDefenderExclusion(path string) error {
	return rw.withConsoleCapture(func() error {
		return winutils.AddDefenderExclusion(path)
	})
}
func (rw *RealWinUtils) SetServiceTriggers(serviceName string, triggers []string) error {
	return rw.withConsoleCapture(func() error {
		return winutils.SetServiceTriggers(serviceName, triggers)
	})
}
func (rw *RealWinUtils) Is64BitOS() bool {
	return winutils.Is64BitOS()
}
func (rw *RealWinUtils) GetComPorts() ([]string, error) {
	return winutils.GetComPorts()
}
func (rw *RealWinUtils) GetScanners() ([]core.ScannerInfo, error) {
	internalScanners, err := winutils.GetScanners()
	if err != nil {
		return nil, err
	}
	publicScanners := make([]core.ScannerInfo, 0, len(internalScanners))
	for _, scanner := range internalScanners {
		publicScanners = append(publicScanners, core.ScannerInfo{
			Port:        scanner.Port,
			Caption:     scanner.Caption,
			PNPDeviceID: scanner.PNPDeviceID,
		})
	}
	return publicScanners, nil
}
func (rw *RealWinUtils) IsProcessRunning(processName string) (bool, error) {
	return winutils.IsProcessRunning(processName)
}
func (rw *RealWinUtils) GracefulShutdownProcess(processName string) error {
	return winutils.GracefulShutdownProcess(processName)
}
func (rw *RealWinUtils) CreateScheduledTask(taskName, executablePath, arguments, workingDir string) error {
	return rw.withConsoleCapture(func() error {
		return winutils.CreateScheduledTask(taskName, executablePath, arguments, workingDir)
	})
}
func (rw *RealWinUtils) RunCommandWithEnv(env map[string]string, name string, args ...string) (string, error) {
	return winutils.RunCommandWithEnv(env, name, args...)
}
func (rw *RealWinUtils) GetFileVersion(filePath string) (string, error) {
	return winutils.GetFileVersion(filePath)
}
func (rw *RealWinUtils) ListArchiveContents(archivePath string) ([]string, error) {
	return winutils.ListArchiveContents(archivePath)
}
func (rw *RealWinUtils) FindFileRecursive(root, pattern string) (string, error) {
	return winutils.FindFileRecursive(root, pattern)
}
func (rw *RealWinUtils) FindNewestFileByPattern(root, pattern string) (string, error) {
	return winutils.FindNewestFileByPattern(root, pattern)
}
func (rw *RealWinUtils) GetStartupFolders() (string, string, error) {
	return winutils.GetStartupFolders()
}
func (rw *RealWinUtils) DeleteFile(path string) error {
	return winutils.DeleteFile(path)
}
func (rw *RealWinUtils) CleanDirectory(path string) error {
	return winutils.CleanDirectory(path)
}
func (rw *RealWinUtils) FindScheduledTaskByPath(exePath string) (string, error) {
	return winutils.FindScheduledTaskByPath(exePath)
}
func (rw *RealWinUtils) DeleteScheduledTaskByName(taskName string) error {
	return winutils.DeleteScheduledTaskByName(taskName)
}
func (rw *RealWinUtils) GetServiceStatus(serviceName string) (string, error) {
	return winutils.GetServiceStatus(serviceName)
}
func (rw *RealWinUtils) IsAdmin() bool {
	return winutils.IsAdmin()
}
func (rw *RealWinUtils) CopyFile(src, dst string) error {
	return winutils.CopyFile(src, dst)
}
func (rw *RealWinUtils) CopyDir(src, dst string) error {
	return winutils.CopyDir(src, dst)
}
func (rw *RealWinUtils) MoveFile(src, dst string) error {
	return winutils.MoveFile(src, dst)
}
func (rw *RealWinUtils) MoveDir(src, dst string) error {
	return winutils.MoveDir(src, dst)
}
func (rw *RealWinUtils) ReadRegistryKey(rootKey registry.Key, path, valueName string) (string, error) {
	return winutils.ReadRegistryKey(rootKey, path, valueName)
}
func (rw *RealWinUtils) UninstallSystemApp(partialName string) error {
	return rw.withConsoleCapture(func() error {
		return winutils.UninstallSystemApp(partialName)
	})
}
func (rw *RealWinUtils) ExtractArchive(archivePath, destDir string, fullPaths bool) error {
	return winutils.ExtractArchive(archivePath, destDir, fullPaths)
}
func (rw *RealWinUtils) GetDesktopDir() (string, error) {
	return winutils.GetDesktopDir()
}
func (rw *RealWinUtils) CreateShortcut(targetPath, shortcutPath, arguments string) error {
	return rw.withConsoleCapture(func() error {
		return winutils.CreateShortcut(targetPath, shortcutPath, arguments)
	})
}
func (rw *RealWinUtils) Reboot() error {
	return winutils.Reboot()
}

// getConfigPath определяет, какой путь к конфигурации использовать:
// из флага, локальный или удаленный.
func getConfigPath(configFlag *string) (string, error) {
	const defaultConfigName = "config.json"
	const remoteConfigURL = "http://f.serty.top/distr/installer/config.json" // Используем http, если https недоступен

	// Проверяем, был ли флаг изменен пользователем
	flagWasSet := false
	flag.Visit(func(f *flag.Flag) {
		if f.Name == "config" {
			flagWasSet = true
		}
	})

	// Если флаг был явно задан (даже если он равен "config.json"), используем его значение
	if flagWasSet {
		tui.InfoF("Используется конфигурация, указанная в аргументе: %s", *configFlag)
		return *configFlag, nil
	}

	// Флаг не был задан, проверяем наличие config.json рядом с exe
	if _, err := os.Stat(defaultConfigName); err == nil {
		tui.InfoF("Найден локальный файл конфигурации: %s", defaultConfigName)
		return defaultConfigName, nil
	}

	// Локального файла нет, скачиваем с удаленного ресурса
	tui.Warn(fmt.Sprintf("Локальный %s не найден. Попытка загрузить конфигурацию с %s", defaultConfigName, remoteConfigURL))

	resp, err := http.Get(remoteConfigURL)
	if err != nil {
		return "", fmt.Errorf("не удалось выполнить запрос на скачивание конфигурации: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("сервер вернул ошибку при скачивании конфигурации: %s", resp.Status)
	}

	// Создаем временный файл для хранения конфигурации внутри текущей директории
	tempDir := filepath.Join(".", "temp")
	if err := os.MkdirAll(tempDir, 0755); err != nil {
		return "", fmt.Errorf("не удалось создать временную директорию: %w", err)
	}
	tempFile, err := os.CreateTemp(tempDir, "config-*.json")
	if err != nil {
		return "", fmt.Errorf("не удалось создать временный файл для конфигурации: %w", err)
	}
	defer tempFile.Close()

	_, err = io.Copy(tempFile, resp.Body)
	if err != nil {
		return "", fmt.Errorf("не удалось записать конфигурацию во временный файл: %w", err)
	}

	tui.Success("Конфигурация успешно загружена с удаленного сервера.")
	return tempFile.Name(), nil
}

// cleanupTempDir теперь ищет и удаляет папку temp как в CWD, так и в корне диска C:\MH (если задан)
// Для упрощения мы жестко привязываемся к соглашению, что весь мусор лежит в ./temp относительно exe
// или в C:\MH\temp, если конфиг указывает туда.
func cleanupTempDir(rootPath string) {
	// 1. Очистка temp рядом с exe (для конфигов и логов установщиков)
	cwdTemp := filepath.Join(".", "temp")
	if _, err := os.Stat(cwdTemp); err == nil {
		slog.Info("Очистка временной директории", "path", cwdTemp)
		if err := os.RemoveAll(cwdTemp); err != nil {
			slog.Warn("Не удалось удалить временную директорию", "path", cwdTemp, "error", err)
		} else {
			fmt.Println("Временные файлы приложения очищены.")
		}
	}

	// 2. Очистка temp в корне установки (C:\MH\temp), если он отличается
	if rootPath != "" {
		rootTemp := filepath.Join(rootPath, "temp")
		absCwd, _ := filepath.Abs(".")
		absRoot, _ := filepath.Abs(rootPath)

		// Проверяем, не одно и то же ли это
		if absCwd != absRoot {
			if _, err := os.Stat(rootTemp); err == nil {
				slog.Info("Очистка корневой временной директории", "path", rootTemp)
				if err := os.RemoveAll(rootTemp); err != nil {
					slog.Warn("Не удалось удалить корневую временную директорию", "path", rootTemp, "error", err)
				}
			}
		}
	}
}

// cleanupOldExecutable проверяет наличие файла .old и удаляет его.
func cleanupOldExecutable() {
	exePath, err := os.Executable()
	if err != nil {
		return
	}
	oldExePath := exePath + ".old"

	if _, err := os.Stat(oldExePath); err == nil {
		slog.Info("Обнаружен старый исполняемый файл, удаление...", "path", oldExePath)
		// Даем системе немного времени, чтобы отпустить файл, если перезапуск произошел очень быстро
		for i := 0; i < 3; i++ {
			err := os.Remove(oldExePath)
			if err == nil {
				slog.Info("Старый файл успешно удален.")
				return
			}
			time.Sleep(500 * time.Millisecond)
		}
		slog.Warn("Не удалось удалить старый файл (возможно, он заблокирован)", "path", oldExePath, "error", err)
	}
}

// setupSignalHandler принимает функцию очистки с параметрами
func setupSignalHandler(rootPath string) {
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM, syscall.SIGQUIT)

	go func() {
		sig := <-sigChan
		fmt.Printf("\nПолучен сигнал %v. Выполняется очистка временных файлов...\n", sig)
		cleanupTempDir(rootPath)
		os.Exit(0)
	}()
}

func main() {
	// 0. Очистка старых версий при запуске
	cleanupOldExecutable()

	// Предварительный парсинг флагов
	configPathFlag := flag.String("config", "config.json", "Путь к файлу конфигурации (локальный или URL)")
	guiFlag := flag.Bool("gui", false, "Запустить в графическом режиме")
	// Новые флаги для режима возобновления
	moduleFlag := flag.String("module", "", "Прямой запуск модуля (Regime)")
	resumeFlag := flag.String("resume", "", "Путь к файлу конфигурации возобновления")
	flag.Parse()

	// Проверка прав администратора
	if !winutils.IsAdmin() {
		tui.Error("Ошибка: Для выполнения требуются права администратора.")
		tui.Error("Пожалуйста, запустите эту программу от имени Администратора.")
		fmt.Println("\nНажмите Enter для выхода...")
		fmt.Scanln()
		os.Exit(1)
	}
	tui.Success("Приложение запущено с правами администратора.")

	// Загрузка конфига
	finalConfigPath, err := getConfigPath(configPathFlag)
	if err != nil {
		log.Fatalf("Критическая ошибка: не удалось определить источник конфигурации: %v", err)
	}

	cfg, err := config.LoadConfig(finalConfigPath)
	if err != nil {
		log.Fatalf("Критическая ошибка: не удалось загрузить конфигурацию: %v", err)
	}

	// Инициализация логгера
	logging.Init(cfg.LogLevel)
	slog.Info("Логгер инициализирован", "log_level", cfg.LogLevel)

	// Настройка очистки
	setupSignalHandler(cfg.RootPath)
	defer cleanupTempDir(cfg.RootPath)

	// Инициализация утилит
	RealWinUtils := &RealWinUtils{}

	// --- САМООБНОВЛЕНИЕ ---\
	// Пропускаем при режиме возобновления
	if *resumeFlag == "" && cfg.SelfUpdateConfig.Enabled {
		slog.Info("Запуск проверки обновлений...")
		updater := selfupdate.New(cfg.SelfUpdateConfig, RealWinUtils)
		updated, err := updater.CheckAndPerformUpdate()
		if err != nil {
			slog.Error("Ошибка при самообновлении", "error", err)
			tui.Warn(fmt.Sprintf("Ошибка проверки обновлений: %v", err))
		} else if updated {
			slog.Info("Приложение было обновлено, завершение работы старой версии")
			return
		}
	}

	// Инициализация менеджера ресурсов
	assetManager, err := assetmgr.New(cfg)
	if err != nil {
		slog.Error("Критическая ошибка assetmgr", "error", err)
		log.Fatalf("Критическая ошибка: не удалось инициализировать менеджер ресурсов: %v", err)
	}

	// --- РЕЖИМ ВОЗОБНОВЛЕНИЯ / ПРЯМОГО ЗАПУСКА ---
	if *moduleFlag == "Regime" && *resumeFlag != "" {
		slog.Info("Запуск в режиме возобновления Regime", "config", *resumeFlag)
		mod := &regime.Module{}
		// В режиме возобновления мы передаем путь к временному конфигу через Run,
		// так как интерфейс Run не поддерживает произвольные аргументы.
		// Модуль Regime сам распознает, что это resume, если ему передать управление.
		// Но лучше вызвать Resume явно.
		// Однако интерфейс Installer имеет только Run.
		// Для простоты, мы добавим логику в Regime.Resume(), который мы вызовем напрямую,
		// так как мы знаем конкретный тип модуля здесь.

		if err := mod.Resume(assetManager, RealWinUtils, *resumeFlag); err != nil {
			slog.Error("Ошибка возобновления Regime", "error", err)
			tui.Error(fmt.Sprintf("Ошибка возобновления установки: %v", err))
			tui.WaitForAnyKey()
		} else {
			tui.Success("\n--- Операция завершена успешно. ---")
			// Даем пользователю прочитать сообщение перед закрытием
			time.Sleep(5 * time.Second)
		}
		return
	}

	// --- ЗАПУСК GUI ---
	if *guiFlag {
		slog.Info("Запуск в режиме GUI")
		gui.Run(cfg, assetManager, RealWinUtils)
		return // Завершаем main после закрытия окна
	}

	// --- КОНСОЛЬНЫЙ РЕЖИМ ---
	// Устанавливаем комфортный размер окна (поуже и повыше стандартного)
	if err := winutils.SetConsoleSize(120, 20); err != nil {
		slog.Warn("Не удалось изменить размер консоли", "error", err)
	}

	// Регистрация модулей
	registeredModules := map[string]core.Installer{
		"VComCaster":    &vcomcaster.Module{},
		"iiko":          &distro.Module{},
		"FRPC":          &frpc.Module{},
		"Regime":        &regime.Module{},
		"RemoteAccess":  &remoteaccess.Module{},
		"ServiceUtils":  &serviceutils.Module{},
		"FiscalDrivers": &fiscaldrivers.Module{},
		"UTM":           &utm.Module{},
	}

	consoleModules := buildConsoleModules(cfg.Modules, registeredModules)
	if len(consoleModules) == 0 {
		log.Fatal("В конфигурации не определено ни одного доступного модуля.")
	}

	queue := taskqueue.New()
	if err := runConsoleDashboard(consoleModules, queue, assetManager, RealWinUtils); err != nil {
		slog.Error("Ошибка консольного интерфейса", "error", err)
		tui.Error(fmt.Sprintf("Критическая ошибка интерфейса: %v", err))
		tui.WaitForAnyKey()
		os.Exit(1)
	}

	slog.Info("Выход из программы")
	tui.Info("Выход из программы.")
}
