package fiscaldrivers

import (
	"bufio"
	"errors"
	"fmt"
	"goMH/config"
	"goMH/core"
	"goMH/tui"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type Module struct{}

func (m *Module) ID() string {
	return "FiscalDrivers" // Новый ID
}

func (m *Module) MenuText() string {
	return "Установка драйверов фискальных регистраторов"
}

// Run теперь управляет подменю выбора драйвера
func (m *Module) Run(am core.AssetManager, wu core.WinUtils) error {
	drivers := am.Cfg().FiscalDriversConfig
	if len(drivers) == 0 {
		return errors.New("в конфигурации не определено ни одного драйвера (fiscal_drivers_config)")
	}

	reader := bufio.NewReader(os.Stdin)

	for {
		tui.Title("\n--- Выберите драйвер для установки ---")
		for i, driver := range drivers {
			fmt.Printf(" %d. %s\n", i+1, driver.MenuText)
		}
		fmt.Println("\n 0. Назад в главное меню")
		fmt.Print("Выберите пункт: ")

		choiceStr, _ := reader.ReadString('\n')
		choiceStr = strings.TrimSpace(choiceStr)

		if choiceStr == "0" {
			return nil
		}

		choice, err := strconv.Atoi(choiceStr)
		if err != nil || choice < 1 || choice > len(drivers) {
			tui.Error("Неверный выбор. Попробуйте снова.")
			continue
		}

		selectedDriver := drivers[choice-1]
		var installErr error

		// Маршрутизация на основе ID из конфига
		switch selectedDriver.ID {
		case "atol":
			installErr = m.installAtol(am, wu, selectedDriver)
		case "poscenter", "kktlab":
			installErr = m.installUniversalDriver(am, wu, selectedDriver)
		default:
			installErr = fmt.Errorf("неизвестный ID драйвера в конфигурации: %s", selectedDriver.ID)
		}

		if installErr != nil {
			tui.Error(fmt.Sprintf("\n--- ОШИБКА УСТАНОВКИ ---\n%v\n--------------------------\n", installErr))
		} else {
			tui.Success("\n--- Установка успешно завершена. ---")
		}

		// Возвращаемся в главное меню после операции
		return nil
	}
}

// parseInstallerArgs разбирает строку с аргументами командной строки, корректно обрабатывая кавычки.
// Функция разбивает строку на аргументы, учитывая пробелы как разделители только вне кавычек.
// Кавычки удаляются из итоговых элементов среза, но сохраняется содержимое внутри них.
// Поддерживает экранированные кавычки и корректно обрабатывает пустые аргументы.
func parseInstallerArgs(command string) []string {
	if command == "" {
		return nil
	}

	var args []string
	var current strings.Builder
	inQuotes := false

	// Проходим по каждому символу в командной строке
	for _, r := range command {
		switch {
		case r == '"' && !inQuotes:
			// Начало кавычек - устанавливаем флаг, но не добавляем кавычку в результат
			inQuotes = true

		case r == '"' && inQuotes:
			// Конец кавычек - завершаем кавычки
			inQuotes = false

		case r == ' ' && !inQuotes && current.Len() > 0:
			// Пробел вне кавычек - завершаем текущий аргумент
			args = append(args, current.String())
			current.Reset()

		case r != ' ' || inQuotes:
			// Все остальные символы (включая пробелы внутри кавычек) добавляем к текущему аргументу
			current.WriteRune(r)
		}
	}

	// Добавляем последний аргумент, если он не пустой
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
	installerPath, err := am.DownloadToCache(driver.AssetID)
	if err != nil {
		return fmt.Errorf("не удалось получить ассет '%s': %w", driver.AssetID, err)
	}

	tui.Info("Запуск установки в тихом режиме...")
	tui.InfoF("Аргументы: %s", driver.InstallArgs)
	args := parseInstallerArgs(driver.InstallArgs)

	_, err = wu.RunCommand(installerPath, args...)
	if err != nil {
		return fmt.Errorf("ошибка при установке ДТО: %w", err)
	}
	return nil
}

// installUniversalDriver - новая логика для Poscenter и KKTlab
func (m *Module) installUniversalDriver(am core.AssetManager, wu core.WinUtils, driver config.FiscalDriver) error {
	tui.Title(fmt.Sprintf("\n--- Начало установки: %s ---", driver.MenuText))

	// Этап 0: Обнаружение и удаление старых версий
	if err := m.uninstallExistingDrivers(wu); err != nil {
		// Не прерываем установку, если удаление не удалось, просто предупреждаем
		tui.Warn(fmt.Sprintf("Произошла ошибка при удалении предыдущих версий: %v", err))
		tui.Warn("Установка будет продолжена, но могут возникнуть конфликты.")
	}

	// Этап 1: Скачивание и установка нового драйвера
	if driver.AssetID == "" {
		return fmt.Errorf("для драйвера '%s' не указан asset_id", driver.ID)
	}
	tui.Info("Получение нового установщика...")
	installerPath, err := am.DownloadToCache(driver.AssetID)
	if err != nil {
		return fmt.Errorf("не удалось получить ассет '%s': %w", driver.AssetID, err)
	}

	tui.Info("Запуск установки нового драйвера...")
	tui.InfoF("Аргументы: %s", driver.InstallArgs)
	args := parseInstallerArgs(driver.InstallArgs)

	_, err = wu.RunCommand(installerPath, args...)
	if err != nil {
		return fmt.Errorf("ошибка при установке драйвера '%s': %w", driver.ID, err)
	}
	return nil
}

// uninstallExistingDrivers ищет и удаляет драйверы Штрих/Ритейл по стандартным путям установки.
func (m *Module) uninstallExistingDrivers(wu core.WinUtils) error {
	tui.Info("-> Поиск установленных драйверов Штрих/Ритейл по стандартным путям...")
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

	if len(dirsToScan) == 0 {
		tui.Success("-> Директорий с драйверами Штрих/Ритейл не найдено. Пропускаем удаление.")
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
		return nil
	}

	// 4. Запускаем найденные деинсталляторы и очищаем папки.
	tui.Warn(fmt.Sprintf("-> Найдено %d установщиков для удаления. Начинаем процесс...", len(uninstallers)))
	for _, uninstaller := range uninstallers {
		tui.InfoF("Удаление: %s", uninstaller)
		_, err := wu.RunCommand(uninstaller, "/VERYSILENT")
		if err != nil {
			// Не прерываем процесс, просто предупреждаем
			tui.Warn(fmt.Sprintf("Ошибка при запуске деинсталлятора '%s': %v", uninstaller, err))
		}

		// Пауза, чтобы дать деинсталлятору время отработать перед удалением папки
		time.Sleep(2 * time.Second)

		// Проверяем и зачищаем родительскую директорию после удаления
		parentDir := filepath.Dir(uninstaller)
		if _, err := os.Stat(parentDir); err == nil {
			tui.InfoF("Зачистка оставшейся директории: %s", parentDir)
			if err := os.RemoveAll(parentDir); err != nil {
				tui.Warn(fmt.Sprintf("Не удалось полностью удалить директорию '%s': %v", parentDir, err))
			}
		}
	}

	tui.Success("-> Процесс удаления завершен.")
	return nil
}
