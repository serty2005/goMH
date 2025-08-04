package main

import (
	"flag"
	"fmt"
	"goMH/assetmgr"
	"goMH/config"
	"goMH/modules/frpc"
	"goMH/modules/iiko"
	"goMH/modules/regime"
	"goMH/modules/remoteaccess"
	"goMH/modules/vcomcaster"
	"goMH/tui"
	"goMH/winutils"
	"io"
	"net/http"

	"log"
	"os"
)

// Общий интерфейс для всех модулей
type Installer interface {
	ID() string
	MenuText() string
	Run(am *assetmgr.Manager) error
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

	// Создаем временный файл для хранения конфигурации
	tempFile, err := os.CreateTemp("", "config-*.json")
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

func main() {
	// 0. Обработка аргументов командной строки
	configPathFlag := flag.String("config", "config.json", "Путь к файлу конфигурации (локальный или URL)")
	flag.Parse()

	// 1. Проверка прав администратора
	if !winutils.IsAdmin() {
		tui.Error("Ошибка: Для выполнения требуются права администратора.")
		tui.Error("Пожалуйста, запустите эту программу от имени Администратора.")
		fmt.Println("\nНажмите Enter для выхода...")
		fmt.Scanln()
		os.Exit(1)
	}
	tui.Success("Приложение запущено с правами администратора.")

	// 2. Получение пути к конфигурации (новая логика)
	finalConfigPath, err := getConfigPath(configPathFlag)
	if err != nil {
		log.Fatalf("Критическая ошибка: не удалось определить источник конфигурации: %v", err)
	}

	// 3. Загрузка конфигурации
	cfg, err := config.LoadConfig(finalConfigPath)
	if err != nil {
		log.Fatalf("Критическая ошибка: не удалось загрузить конфигурацию: %v", err)
	}

	// 4. Инициализация менеджера ресурсов
	assetManager, err := assetmgr.New(cfg)
	if err != nil {
		log.Fatalf("Критическая ошибка: не удалось инициализировать менеджер ресурсов: %v", err)
	}

	// 5. Регистрация всех доступных модулей
	registeredModules := map[string]Installer{
		"VComCaster":   &vcomcaster.Module{},
		"iiko":         &iiko.Module{},
		"FRPC":         &frpc.Module{},
		"Regime":       &regime.Module{},
		"RemoteAccess": &remoteaccess.Module{},
	}

	// 6. Основной цикл меню
	for {
		var availableModules []tui.Installer
		for _, modDef := range cfg.Modules {
			if module, ok := registeredModules[modDef.ID]; ok {
				availableModules = append(availableModules, module)
			}
		}

		if len(availableModules) == 0 {
			log.Fatal("В конфигурации не определено ни одного доступного модуля.")
		}

		selected, err := tui.ShowMenu(availableModules)
		if err != nil {
			tui.Info("Выход из программы.")
			os.Exit(0)
		}

		selectedModule := selected.(Installer)

		err = selectedModule.Run(assetManager)
		if err != nil {
			tui.Error(fmt.Sprintf("\n--- ОПЕРАЦИЯ ЗАВЕРШИЛАСЬ С ОШИБКОЙ ---\n%v\n---------------------------------------\n", err))
		} else {
			tui.Success("\n--- Операция завершена успешно. ---")
		}

		fmt.Println("\nНажмите Enter, чтобы вернуться в главное меню...")
		fmt.Scanln()
	}
}
