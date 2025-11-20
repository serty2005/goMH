package fiscaldrivers

import (
	"errors"
	"fmt"
	"goMH/config"
	"goMH/core"
	"goMH/tui"
	"log/slog" // Импорт
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type Module struct{}

func (m *Module) ID() string {
	return "FiscalDrivers"
}

func (m *Module) MenuText() string {
	return "Установка драйверов фискальных регистраторов"
}

// Run теперь управляет подменю выбора драйвера (мгновенный ввод)
func (m *Module) Run(am core.AssetManager, wu core.WinUtils) error {
	slog.Info("Запуск модуля FiscalDrivers")
	drivers := am.Cfg().FiscalDriversConfig
	if len(drivers) == 0 {
		slog.Error("Конфигурация драйверов пуста")
		return errors.New("в конфигурации не определено ни одного драйвера (fiscal_drivers_config)")
	}

	for {
		tui.ClearScreen()
		tui.Title("\n--- Выберите драйвер для установки ---")
		for i, driver := range drivers {
			fmt.Printf(" %d. %s\n", i+1, driver.MenuText)
		}
		fmt.Println("\n 0. Назад в главное меню")
		fmt.Print("Выберите пункт: ")

		key, err := tui.ReadKey()
		if err != nil {
			return nil
		}

		if key == "0" {
			slog.Info("Пользователь вышел из меню драйверов")
			return nil
		}

		choice, err := strconv.Atoi(key)
		if err != nil || choice < 1 || choice > len(drivers) {
			// Неверный выбор, игнорируем
			continue
		}

		selectedDriver := drivers[choice-1]
		slog.Info("Выбран драйвер", "id", selectedDriver.ID, "name", selectedDriver.MenuText)

		var installErr error

		// Маршрутизация на основе ID из конфига
		switch selectedDriver.ID {
		case "atol":
			installErr = m.installAtol(am, wu, selectedDriver)
		case "poscenter", "kktlab":
			installErr = m.installUniversalDriver(am, wu, selectedDriver)
		default:
			slog.Warn("Неизвестный ID драйвера", "id", selectedDriver.ID)
			installErr = fmt.Errorf("неизвестный ID драйвера в конфигурации: %s", selectedDriver.ID)
		}

		if installErr != nil {
			slog.Error("Ошибка установки драйвера", "driver", selectedDriver.ID, "error", installErr)
			tui.Error(fmt.Sprintf("\n--- ОШИБКА УСТАНОВКИ ---\n%v\n--------------------------\n", installErr))
		} else {
			slog.Info("Драйвер успешно установлен", "driver", selectedDriver.ID)
			tui.Success("\n--- Установка успешно завершена. ---")
		}

		// Возвращаемся в главное меню после операции
		tui.WaitForAnyKey()
		return nil
	}
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

// installAtol - старая логика установки драйвера АТОЛ
func (m *Module) installAtol(am core.AssetManager, wu core.WinUtils, driver config.FiscalDriver) error {
	tui.Title(fmt.Sprintf("\n--- Начало установки: %s ---", driver.MenuText))
	if driver.AssetID == "" {
		return errors.New("для драйвера АТОЛ не указан asset_id")
	}

	tui.Info("Получение установщика через AssetManager...")
	slog.Info("Загрузка ассета", "asset_id", driver.AssetID)

	installerPath, err := am.DownloadToCache(driver.AssetID)
	if err != nil {
		return fmt.Errorf("не удалось получить ассет '%s': %w", driver.AssetID, err)
	}

	tui.Info("Запуск установки в тихом режиме...")
	tui.InfoF("Аргументы: %s", driver.InstallArgs)
	args := parseInstallerArgs(driver.InstallArgs)
	slog.Info("Запуск установщика АТОЛ", "path", installerPath, "args", args)

	_, err = wu.RunCommand(installerPath, args...)
	if err != nil {
		return fmt.Errorf("ошибка при установке ДТО: %w", err)
	}
	return nil
}

// installUniversalDriver - новая логика для Poscenter и KKTlab
func (m *Module) installUniversalDriver(am core.AssetManager, wu core.WinUtils, driver config.FiscalDriver) error {
	tui.Title(fmt.Sprintf("\n--- Начало установки: %s ---", driver.MenuText))
	slog.Info("Начало установки универсального драйвера", "driver", driver.MenuText)

	// Этап 0: Обнаружение и удаление старых версий
	if err := m.uninstallExistingDrivers(wu); err != nil {
		slog.Warn("Ошибка при удалении старых драйверов", "error", err)
		tui.Warn(fmt.Sprintf("Произошла ошибка при удалении предыдущих версий: %v", err))
		tui.Warn("Установка будет продолжена, но могут возникнуть конфликты.")
	}

	// Этап 1: Скачивание и установка нового драйвера
	if driver.AssetID == "" {
		return fmt.Errorf("для драйвера '%s' не указан asset_id", driver.ID)
	}
	tui.Info("Получение нового установщика...")
	slog.Debug("Скачивание установщика", "asset_id", driver.AssetID)

	installerPath, err := am.DownloadToCache(driver.AssetID)
	if err != nil {
		return fmt.Errorf("не удалось получить ассет '%s': %w", driver.AssetID, err)
	}

	tui.Info("Запуск установки нового драйвера...")
	tui.InfoF("Аргументы: %s", driver.InstallArgs)
	args := parseInstallerArgs(driver.InstallArgs)

	slog.Info("Запуск установщика", "path", installerPath, "args", args)
	_, err = wu.RunCommand(installerPath, args...)
	if err != nil {
		return fmt.Errorf("ошибка при установке драйвера '%s': %w", driver.ID, err)
	}
	return nil
}

// uninstallExistingDrivers ищет и удаляет драйверы Штрих/Ритейл по стандартным путям установки.
func (m *Module) uninstallExistingDrivers(wu core.WinUtils) error {
	tui.Info("-> Поиск установленных драйверов Штрих/Ритейл по стандартным путям...")
	slog.Info("Запуск поиска старых драйверов для удаления")
	var uninstallers []string

	// 1. Определяем список директорий для сканирования.
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

	// 2. Особый случай для SHTRIH-M: ищем поддиректории.
	shtrihBaseDir := `C:\Program Files (x86)\SHTRIH-M`
	if entries, err := os.ReadDir(shtrihBaseDir); err == nil {
		for _, entry := range entries {
			if entry.IsDir() {
				fullPath := filepath.Join(shtrihBaseDir, entry.Name())
				dirsToScan = append(dirsToScan, fullPath)
			}
		}
	}

	slog.Debug("Директории для сканирования на наличие деинсталляторов", "dirs", dirsToScan)

	if len(dirsToScan) == 0 {
		tui.Success("-> Директорий с драйверами Штрих/Ритейл не найдено. Пропускаем удаление.")
		slog.Info("Старые драйверы не найдены (директории отсутствуют)")
		return nil
	}

	// 3. Ищем unins*.exe во всех найденных папках.
	tui.Info("Сканирование найденных директорий на наличие деинсталляторов...")
	for _, dir := range dirsToScan {
		uninstallerPath, err := wu.FindFileRecursive(dir, "unins*.exe")
		if err == nil {
			uninstallers = append(uninstallers, uninstallerPath)
		}
	}

	if len(uninstallers) == 0 {
		tui.Success("-> Установленных драйверов Штрих/Ритейл не найдено.")
		slog.Info("Деинсталляторы не найдены")
		return nil
	}

	// 4. Запускаем найденные деинсталляторы и очищаем папки.
	tui.Warn(fmt.Sprintf("-> Найдено %d установщиков для удаления. Начинаем процесс...", len(uninstallers)))
	slog.Info("Найдены деинсталляторы", "count", len(uninstallers), "paths", uninstallers)

	for _, uninstaller := range uninstallers {
		tui.InfoF("Удаление: %s", uninstaller)
		slog.Info("Запуск деинсталлятора", "path", uninstaller)
		_, err := wu.RunCommand(uninstaller, "/VERYSILENT")
		if err != nil {
			// Не прерываем процесс, просто предупреждаем
			tui.Warn(fmt.Sprintf("Ошибка при запуске деинсталлятора '%s': %v", uninstaller, err))
			slog.Warn("Ошибка деинсталлятора", "path", uninstaller, "error", err)
		}

		// Пауза, чтобы дать деинсталлятору время отработать перед удалением папки
		time.Sleep(2 * time.Second)

		// Проверяем и зачищаем родительскую директорию после удаления
		parentDir := filepath.Dir(uninstaller)
		if _, err := os.Stat(parentDir); err == nil {
			tui.InfoF("Зачистка оставшейся директории: %s", parentDir)
			slog.Debug("Удаление остатков директории", "path", parentDir)
			if err := os.RemoveAll(parentDir); err != nil {
				tui.Warn(fmt.Sprintf("Не удалось полностью удалить директорию '%s': %v", parentDir, err))
				slog.Warn("Не удалось удалить директорию", "path", parentDir, "error", err)
			}
		}
	}

	tui.Success("-> Процесс удаления завершен.")
	slog.Info("Процесс удаления завершен")
	return nil
}
