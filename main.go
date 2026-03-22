package main

import (
	"flag"
	"fmt"
	"goMH/app/automation"
	"goMH/app/consolequeue"
	"goMH/app/platform"
	"goMH/assetmgr"
	"goMH/config"
	"goMH/core"
	"goMH/gui"
	"goMH/logging"
	"goMH/modules/distro"
	"goMH/modules/regime"
	moduleregistry "goMH/modules/registry"
	"goMH/modules/selfupdate"
	"goMH/tui"
	"goMH/winutils"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"
)

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
func cleanupTempDir(rootPath string, preserveRootTemp bool) {
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
	if rootPath != "" && !preserveRootTemp {
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

func scheduledTaskExists(taskName string) bool {
	_, err := winutils.RunCommand("schtasks", "/Query", "/TN", taskName)
	return err == nil
}

func shouldPreserveRootTemp() bool {
	return scheduledTaskExists(distroResumeTaskName()) || scheduledTaskExists(regimeResumeTaskName())
}

func distroResumeTaskName() string {
	return "goMH_Distro_Resume"
}

func regimeResumeTaskName() string {
	return "goMH_Regime_Resume"
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

type cleanupRunner struct {
	once    sync.Once
	actions []func()
}

func (r *cleanupRunner) Add(action func()) {
	if action == nil {
		return
	}
	r.actions = append(r.actions, action)
}

func (r *cleanupRunner) Run() {
	if r == nil {
		return
	}
	r.once.Do(func() {
		for _, action := range r.actions {
			action()
		}
	})
}

func setupSignalHandler(onSignal func(os.Signal)) func() {
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM, syscall.SIGQUIT)

	go func() {
		sig, ok := <-sigChan
		if !ok {
			return
		}
		if onSignal != nil {
			onSignal(sig)
		}
	}()

	return func() {
		signal.Stop(sigChan)
		close(sigChan)
	}
}

func main() {
	cleanupOldExecutable()

	if len(os.Args) > 1 && os.Args[1] == "automation" {
		os.Exit(automation.ExecuteCLI(
			os.Args[2:],
			os.Stdin,
			os.Stdout,
			os.Stderr,
			automation.CommandDependencies{
				NewWinUtils: func() core.WinUtils {
					return platform.NewRealWinUtils()
				},
			},
		))
	}

	exitCode := 0
	if err := execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Критическая ошибка: %v\n", err)
		exitCode = 1
	}
	os.Exit(exitCode)
}

func execute() error {
	// 0. Очистка старых версий при запуске
	cleanupOldExecutable()

	// Предварительный парсинг флагов
	configPathFlag := flag.String("config", "config.json", "Путь к файлу конфигурации (локальный или URL)")
	guiFlag := flag.Bool("gui", false, "Запустить в графическом режиме")
	// Новые флаги для режима возобновления
	moduleFlag := flag.String("module", "", "Прямой запуск модуля (Regime, iiko)")
	resumeFlag := flag.String("resume", "", "Путь к файлу конфигурации возобновления")
	flag.Parse()

	// Проверка прав администратора
	if !winutils.IsAdmin() {
		tui.Error("Ошибка: Для выполнения требуются права администратора.")
		tui.Error("Пожалуйста, запустите эту программу от имени Администратора.")
		fmt.Println("\nНажмите Enter для выхода...")
		fmt.Scanln()
		return fmt.Errorf("для выполнения требуются права администратора")
	}
	tui.Success("Приложение запущено с правами администратора.")

	// Загрузка конфига
	finalConfigPath, err := getConfigPath(configPathFlag)
	if err != nil {
		return fmt.Errorf("не удалось определить источник конфигурации: %w", err)
	}

	cfg, err := config.LoadConfig(finalConfigPath)
	if err != nil {
		return fmt.Errorf("не удалось загрузить конфигурацию: %w", err)
	}

	// Инициализация логгера
	logSetup, err := logging.Init(cfg.Logging)
	if err != nil {
		return fmt.Errorf("не удалось инициализировать логирование: %w", err)
	}

	cleanup := &cleanupRunner{}
	cleanup.Add(func() {
		if closeErr := logSetup.Close(); closeErr != nil {
			fmt.Fprintf(os.Stderr, "Не удалось закрыть файл лога: %v\n", closeErr)
		}
	})
	cleanup.Add(func() {
		cleanupTempDir(cfg.RootPath, shouldPreserveRootTemp())
	})
	defer cleanup.Run()

	if logSetup.FileEnabled {
		slog.Info("Логирование инициализировано", "log_level", cfg.Logging.Level, "log_path", logSetup.LogPath)
	} else {
		slog.Info("Логирование инициализировано", "log_level", cfg.Logging.Level, "file_enabled", false)
	}

	// Настройка очистки
	stopSignalHandler := setupSignalHandler(func(sig os.Signal) {
		fmt.Printf("\nПолучен сигнал %v. Выполняется завершение приложения...\n", sig)
		cleanup.Run()
		os.Exit(0)
	})
	defer stopSignalHandler()

	// Инициализация утилит
	RealWinUtils := platform.NewRealWinUtils()

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
			return nil
		}
	}

	// Инициализация менеджера ресурсов
	assetManager, err := assetmgr.New(cfg)
	if err != nil {
		slog.Error("Критическая ошибка assetmgr", "error", err)
		return fmt.Errorf("не удалось инициализировать менеджер ресурсов: %w", err)
	}

	// --- РЕЖИМ ВОЗОБНОВЛЕНИЯ / ПРЯМОГО ЗАПУСКА ---
	if *moduleFlag == "Regime" && *resumeFlag != "" {
		slog.Info("Запуск в режиме возобновления Regime", "config", *resumeFlag)
		mod := &regime.Module{}
		logging.LogTaskStarted(mod.ID(), "resume", "Возобновление установки Regime")

		if err := mod.Resume(assetManager, RealWinUtils, *resumeFlag); err != nil {
			logging.LogTaskFinished(mod.ID(), "resume", "Возобновление установки Regime", err)
			slog.Error("Ошибка возобновления Regime", "error", err)
			tui.Error(fmt.Sprintf("Ошибка возобновления установки: %v", err))
			tui.WaitForAnyKey()
			return fmt.Errorf("ошибка возобновления Regime: %w", err)
		} else {
			logging.LogTaskFinished(mod.ID(), "resume", "Возобновление установки Regime", nil)
			tui.Success("\n--- Операция завершена успешно. ---")
			time.Sleep(5 * time.Second)
		}
		return nil
	}

	if *moduleFlag == "iiko" && *resumeFlag != "" {
		slog.Info("Запуск в режиме возобновления iiko/Syrve", "config", *resumeFlag)
		mod := &distro.Module{}
		logging.LogTaskStarted(mod.ID(), "resume", "Возобновление установки iiko/Syrve")

		if err := mod.Resume(assetManager, RealWinUtils, *resumeFlag); err != nil {
			logging.LogTaskFinished(mod.ID(), "resume", "Возобновление установки iiko/Syrve", err)
			slog.Error("Ошибка возобновления iiko/Syrve", "error", err)
			tui.Error(fmt.Sprintf("Ошибка возобновления установки: %v", err))
			tui.WaitForAnyKey()
			return fmt.Errorf("ошибка возобновления iiko/Syrve: %w", err)
		}

		logging.LogTaskFinished(mod.ID(), "resume", "Возобновление установки iiko/Syrve", nil)
		tui.Success("\n--- Операция завершена успешно. ---")
		time.Sleep(5 * time.Second)
		return nil
	}

	// --- ЗАПУСК GUI ---
	if *guiFlag {
		slog.Info("Запуск в режиме GUI")
		registry := moduleregistry.NewDefault()
		if err := gui.Run(cfg, registry, assetManager, RealWinUtils); err != nil {
			return fmt.Errorf("не удалось запустить GUI: %w", err)
		}
		return nil
	}

	// --- КОНСОЛЬНЫЙ РЕЖИМ ---
	// Устанавливаем комфортный размер окна (поуже и повыше стандартного)
	if err := winutils.SetConsoleSize(120, 20); err != nil {
		slog.Warn("Не удалось изменить размер консоли", "error", err)
	}

	registry := moduleregistry.NewDefault()
	if err := consolequeue.Run(cfg.Modules, registry, assetManager, RealWinUtils); err != nil {
		slog.Error("Ошибка консольного интерфейса", "error", err)
		tui.Error(fmt.Sprintf("Критическая ошибка интерфейса: %v", err))
		tui.WaitForAnyKey()
		return fmt.Errorf("ошибка консольного интерфейса: %w", err)
	}

	slog.Info("Выход из программы")
	tui.Info("Выход из программы.")
	return nil
}
