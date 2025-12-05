// file: modules/vcomcaster/vcomcaster.go

package vcomcaster

import (
	"bufio"
	"errors"
	"fmt"
	"goMH/core"
	"goMH/tui"
	"log/slog" // Импорт логгера
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/beevik/etree"
	"gopkg.in/ini.v1"
)

const (
	taskName        = "VComCaster Autostart"
	iikoProcessName = "iikoFront"
)

type Module struct{}

func (m *Module) ID() string       { return "VComCaster" }
func (m *Module) MenuText() string { return "VComCaster (для сканера штрих-кодов)" }

// Run - главная точка входа в модуль.
func (m *Module) Run(am core.AssetManager, wu core.WinUtils) error {
	slog.Info("Запуск модуля VComCaster")
	vcomcasterBaseDir := filepath.Join(am.Cfg().RootPath, "vcomcaster")
	slog.Debug("Проверка базовой директории", "path", vcomcasterBaseDir)

	if _, err := os.Stat(vcomcasterBaseDir); err == nil {
		// Если директория есть, запускаем режим диагностики/удаления
		slog.Info("Обнаружена существующая установка", "path", vcomcasterBaseDir)
		return m.runDiagnosticsWorkflow(wu, am, vcomcasterBaseDir)
	}

	// Если директории нет, запускаем режим установки
	slog.Info("Существующая установка не найдена, запуск режима установки")
	return m.runInstallWorkflow(am, wu)
}

// extractDeviceID извлекает часть VID_...&PID_... из полного PNPDeviceID.
func extractDeviceID(pnpDeviceID string) string {
	slog.Debug("Попытка извлечь DeviceID", "input", pnpDeviceID)
	// Регулярное выражение для поиска "VID_...&PID_..." после "USB\"
	// (?i) - регистронезависимый поиск
	// \\ - экранированный обратный слеш
	// ([^&\\]+) - захватывает все символы до следующего & или \
	re := regexp.MustCompile(`(?i)USB\\(VID_[^&]+&PID_[^&\\]+)`)
	matches := re.FindStringSubmatch(pnpDeviceID)

	if len(matches) > 1 {
		slog.Debug("DeviceID успешно извлечен", "result", matches[1])
		return matches[1] // Возвращаем первую захваченную группу
	}
	// Если не найдено, возвращаем пустую строку, конфиг будет без этого значения
	slog.Debug("DeviceID не найден в строке")
	return ""
}

// --- РЕЖИМ УСТАНОВКИ ---
func (m *Module) runInstallWorkflow(am core.AssetManager, wu core.WinUtils) error {
	tui.Title("\n--- Запуск установки VComCaster ---")
	slog.Info("Начало workflow установки VComCaster")

	// 1. Получаем ресурсы
	tui.Info("-> Этап 1: Загрузка необходимых ресурсов...")
	slog.Debug("Запрос ресурса VComCaster_Package")
	vcomcasterDestPath, err := am.Get("VComCaster_Package")
	if err != nil {
		slog.Error("Ошибка получения VComCaster_Package", "error", err)
		return fmt.Errorf("не удалось получить VComCaster_Package: %w", err)
	}
	slog.Debug("VComCaster_Package получен", "path", vcomcasterDestPath)

	slog.Debug("Загрузка Com0Com_Installer в кэш")
	com0comInstallerExe, err := am.DownloadToCache("Com0Com_Installer")
	if err != nil {
		slog.Error("Ошибка загрузки Com0Com_Installer", "error", err)
		return fmt.Errorf("не удалось скачать Com0Com_Installer в кэш: %w", err)
	}
	tui.SuccessF("Установщик com0com находится в кэше: %s", com0comInstallerExe)

	// 2. Установка com0com
	tui.Info("-> Этап 2: Установка com0com...")
	portsBefore, _ := wu.GetComPorts()
	slog.Debug("Порты до установки com0com", "ports", portsBefore)

	com0comInstallDir := filepath.Join(vcomcasterDestPath, "com0com")
	_ = os.MkdirAll(com0comInstallDir, 0755)

	com0comEnv := map[string]string{
		"CNC_INSTALL_COMX_COMX_PORTS":      "YES",
		"CNC_INSTALL_CNCA0_CNCB0_PORTS":    "NO",
		"CNC_INSTALL_START_MENU_SHORTCUTS": "NO",
	}

	slog.Info("Запуск установки com0com", "exe", com0comInstallerExe, "target", com0comInstallDir, "env", com0comEnv)
	_, err = wu.RunCommandWithEnv(
		com0comEnv,
		com0comInstallerExe,
		"/S",
		fmt.Sprintf("/D=%s", com0comInstallDir),
	)
	if err != nil {
		slog.Error("Ошибка установки com0com", "error", err)
		return fmt.Errorf("ошибка при установке com0com: %w", err)
	}
	tui.Info("Установка com0com завершена, ожидание инициализации портов (5 сек)...")
	time.Sleep(5 * time.Second)

	portsAfter, _ := wu.GetComPorts()
	slog.Debug("Порты после установки com0com", "ports", portsAfter)
	newPorts := findNewPorts(portsBefore, portsAfter)
	if len(newPorts) < 2 {
		tui.Warn("ПРЕДУПРЕЖДЕНИЕ: Не удалось определить созданные виртуальные COM-порты. Проверьте Диспетчер устройств.")
		slog.Warn("Не удалось определить новые порты", "new_ports_count", len(newPorts))
	} else {
		sort.Strings(newPorts)
		tui.SuccessF("Созданы виртуальные порты: %s и %s", newPorts[0], newPorts[1])
		slog.Info("Обнаружены новые COM-порты", "ports", newPorts)
	}

	// 3. Определение сканера (НОВАЯ ЛОГИКА)
	tui.Info("-> Этап 3: Определение сканера...")
	slog.Debug("Поиск подключенных сканеров")
	scanners, err := wu.GetScanners()
	if err != nil {
		slog.Error("Критическая ошибка поиска сканеров", "error", err)
		return fmt.Errorf("критическая ошибка при поиске сканеров: %w", err)
	}
	if len(scanners) == 0 {
		slog.Error("Сканеры не найдены")
		return errors.New("не найдено ни одного USB-сканера, подключенного к COM-порту. Проверьте подключение и драйверы")
	}
	slog.Debug("Найдены сканеры", "count", len(scanners), "details", scanners)

	tui.Title("\n--- Найдены следующие устройства ---")
	for i, scanner := range scanners {
		// Проверяем, содержит ли Caption (название продукта) уже имя порта.
		portInCaption := fmt.Sprintf("(%s)", scanner.Port)
		if strings.Contains(scanner.Caption, portInCaption) {
			// Если да, то просто выводим Caption как есть.
			fmt.Printf(" %d. %s\n", i+1, scanner.Caption)
		} else {
			// Если нет, то добавляем порт в скобках для красоты.
			fmt.Printf(" %d. %s (%s)\n", i+1, scanner.Caption, scanner.Port)
		}
	}
	fmt.Print("Выберите номер вашего сканера: ")

	reader := bufio.NewReader(os.Stdin)
	choiceStr, _ := reader.ReadString('\n')
	choice, err := strconv.Atoi(strings.TrimSpace(choiceStr))
	if err != nil || choice < 1 || choice > len(scanners) {
		slog.Info("Неверный выбор сканера пользователем", "input", choiceStr)
		return errors.New("неверный выбор, установка прервана")
	}

	selectedScanner := scanners[choice-1]
	slog.Info("Пользователь выбрал сканер", "caption", selectedScanner.Caption, "port", selectedScanner.Port)
	scannerComPort := selectedScanner.Port
	scannerDeviceID := extractDeviceID(selectedScanner.PNPDeviceID)

	tui.SuccessF("Выбран сканер: %s на порту %s", selectedScanner.Caption, scannerComPort)
	if scannerDeviceID != "" {
		tui.SuccessF("Определен ID устройства: %s", scannerDeviceID)
		slog.Info("Определен DeviceID сканера", "id", scannerDeviceID)
	} else {
		tui.Warn("Не удалось определить VID/PID устройства. Поле device_id в конфиге будет пустым.")
		slog.Warn("DeviceID не определен", "pnp_id", selectedScanner.PNPDeviceID)
	}

	// 4. Создание config.ini
	tui.Info("-> Этап 4: Создание config.ini...")
	outputPort := ""
	if len(newPorts) > 0 {
		outputPort = newPorts[0]
	}
	slog.Debug("Формирование config.ini", "scannerPort", scannerComPort, "outputPort", outputPort, "deviceID", scannerDeviceID)
	iniContent := fmt.Sprintf(
		"[app]\r\nautostart_listing = 1\r\nautoreconnect = 1\r\nlogs-autoclear-days = 2\r\n[device]\r\ndevice_id = %s\r\ninput_port = %s\r\noutput_port = %s\r\nport_baudrate = 115200\r\ncr = 0\r\nlf = 0\r\n[service]\r\namount_rm_char_id = 0\r\ntimeout_clearcash = 1.5\r\ntimeout_autoreconnect = 3\r\ntimeout_reconnect = 3",
		scannerDeviceID, scannerComPort, outputPort,
	)
	configPath := filepath.Join(vcomcasterDestPath, "config.ini")
	if err := os.WriteFile(configPath, []byte(iniContent), 0644); err != nil {
		slog.Error("Ошибка записи config.ini", "path", configPath, "error", err)
		return fmt.Errorf("не удалось создать config.ini: %w", err)
	}
	tui.Success("Файл config.ini успешно создан.")

	// 5. Финальная настройка
	tui.Info("-> Этап 5: Финальная настройка (Планировщик, запуск)...")
	vcomcasterExePath := filepath.Join(vcomcasterDestPath, "vcomcaster.exe")

	slog.Info("Создание задачи планировщика", "taskName", taskName, "exe", vcomcasterExePath)
	if err := wu.CreateScheduledTask(taskName, vcomcasterExePath, vcomcasterDestPath); err != nil {
		slog.Warn("Ошибка создания задачи планировщика", "error", err)
		tui.Warn(fmt.Sprintf("ВНИМАНИЕ: Не удалось создать/обновить задачу в планировщике: %v", err))
	} else {
		tui.SuccessF("Задача '%s' в Планировщике Windows успешно создана/обновлена.", taskName)
	}

	if len(newPorts) > 1 {
		iikoPort := newPorts[1]
		slog.Info("Попытка обновления конфига iiko", "port", iikoPort)
		if err := m.updateIikoConfig(wu, iikoPort); err != nil {
			slog.Warn("Ошибка обновления конфига iiko", "error", err)
			tui.Warn(fmt.Sprintf("Не удалось автоматически обновить конфиг iiko: %v", err))
			tui.Warn(fmt.Sprintf("ВАЖНО: Пожалуйста, вручную укажите в настройках iiko порт сканера: %s", iikoPort))
		}
	} else {
		slog.Warn("Не хватает свободных портов для iiko, обновление конфига пропущено")
		tui.Warn("Не удалось определить порт для iiko. Пропустили обновление конфига.")
	}

	tui.Info("Запуск vcomcaster.exe...")
	slog.Info("Запуск процесса vcomcaster.exe", "path", vcomcasterExePath)
	// Запуск GUI приложения без ожидания. Этот вызов остается прямым, т.к. не влияет на тестируемую логику.
	startCmd := exec.Command(vcomcasterExePath)
	startCmd.Dir = vcomcasterDestPath
	if err := startCmd.Start(); err != nil {
		slog.Error("Ошибка запуска процесса", "error", err)
		return fmt.Errorf("не удалось запустить vcomcaster.exe: %w", err)
	}
	tui.Success("Приложение vcomcaster успешно запущено в фоновом режиме.")

	return nil
}

func (m *Module) updateIikoConfig(wu core.WinUtils, iikoPort string) error {
	slog.Debug("Вход в функцию updateIikoConfig", "port", iikoPort)
	const maxRetries = 3
	const retryDelay = 10 * time.Second

	configDir, err := os.UserConfigDir()
	if err != nil {
		return fmt.Errorf("не удалось найти директорию APPDATA: %w", err)
	}
	configPath := filepath.Join(configDir, "iiko", "CashServer", "config.xml")
	slog.Debug("Целевой путь конфига iiko", "path", configPath)

	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		tui.InfoF("Файл конфигурации iiko не найден по пути: %s. Пропускаем.", configPath)
		tui.InfoF("После первого запуска iiko, пожалуйста, укажите порт сканера вручную: %s", iikoPort)
		slog.Info("Конфиг iiko не найден, пропускаем обновление")
		return nil
	}
	tui.InfoF("Найден файл конфигурации iiko: %s", configPath)

	// Проверить статус процесса iikoFront
	isRunning, err := wu.IsProcessRunning(iikoProcessName)
	if err != nil {
		slog.Error("Ошибка проверки статуса процесса iiko", "error", err)
		return fmt.Errorf("не удалось проверить статус процесса iiko: %w", err)
	}
	if !isRunning {
		slog.Debug("Процесс iiko не запущен, можно обновлять конфиг")
		goto editConfig
	}

	// Процесс запущен, попытка мягкого завершения
	slog.Info("Попытка мягкого завершения процесса iikoFront для безопасного обновления конфигурации")
	if err := wu.GracefulShutdownProcess(iikoProcessName); err != nil {
		slog.Warn("Не удалось выполнить мягкое завершение процесса iikoFront", "error", err)
	}
	time.Sleep(5 * time.Second)

	// Проверяем статус после попытки мягкого завершения
	isRunning, err = wu.IsProcessRunning(iikoProcessName)
	if err != nil {
		slog.Error("Ошибка проверки статуса процесса iiko после мягкого завершения", "error", err)
		return fmt.Errorf("не удалось проверить статус процесса iiko: %w", err)
	}
	if !isRunning {
		slog.Info("Процесс iikoFront успешно завершен мягко")
		goto editConfig
	} else {
		slog.Warn("Процесс iikoFront все еще запущен после попытки мягкого завершения, продолжаем ожидание")
	}

	// Цикл ожидания с предупреждениями
	for i := 0; i < maxRetries; i++ {
		isRunning, err := wu.IsProcessRunning(iikoProcessName)
		if err != nil {
			slog.Error("Ошибка проверки статуса процесса iiko", "error", err)
			return fmt.Errorf("не удалось проверить статус процесса iiko: %w", err)
		}
		if !isRunning {
			slog.Debug("Процесс iiko не запущен, можно обновлять конфиг")
			break
		}
		slog.Warn("Процесс iiko запущен, ожидание завершения", "attempt", i+1)
		tui.Warn(fmt.Sprintf("Обнаружен запущенный процесс '%s'. Это может помешать сохранению файла.", iikoProcessName))
		tui.Warn(fmt.Sprintf("Пожалуйста, закройте iiko Front. Ожидание %v... (попытка %d из %d)", retryDelay, i+1, maxRetries))
		time.Sleep(retryDelay)

		if i == maxRetries-1 {
			slog.Error("Таймаут ожидания закрытия iiko")
			return fmt.Errorf("процесс '%s' все еще запущен после %d попыток. Изменение отменено", iikoProcessName, maxRetries)
		}
	}

editConfig:

	doc := etree.NewDocument()
	if err := doc.ReadFromFile(configPath); err != nil {
		slog.Error("Ошибка чтения XML", "error", err)
		return fmt.Errorf("ошибка чтения XML файла: %w", err)
	}

	root := doc.SelectElement("config")
	if root == nil {
		slog.Error("Корневой элемент <config> не найден в XML")
		return fmt.Errorf("корневой элемент <config> не найден в файле %s. Изменение отменено", configPath)
	}

	portElement := root.SelectElement("comBarcodeScanerPort")
	if portElement == nil {
		tui.Info("Элемент <comBarcodeScanerPort> не найден. Создаем его.")
		slog.Info("Создание элемента comBarcodeScanerPort")
		portElement = root.CreateElement("comBarcodeScanerPort")
	}

	tui.InfoF("Обновляем порт сканера в конфиге iiko на '%s'...", iikoPort)
	slog.Info("Установка значения порта", "old_value", portElement.Text(), "new_value", iikoPort)
	portElement.SetText(iikoPort)

	doc.Indent(2)
	if err := doc.WriteToFile(configPath); err != nil {
		slog.Error("Ошибка сохранения XML", "error", err)
		return fmt.Errorf("ошибка сохранения XML файла: %w", err)
	}

	tui.Success("Конфигурация iiko успешно обновлена.")
	return nil
}

// --- РЕЖИМ ДИАГНОСТИКИ И УДАЛЕНИЯ ---
func (m *Module) runDiagnosticsWorkflow(wu core.WinUtils, am core.AssetManager, baseDir string) error {
	tui.Title("\n--- Обнаружена существующая установка. Запуск диагностики... ---")
	slog.Info("Запуск диагностики существующей установки", "baseDir", baseDir)

	var problems []string
	configPath := filepath.Join(baseDir, "config.ini")
	com0comUninstallerPath := filepath.Join(baseDir, "com0com", "uninstall.exe")

	if _, err := os.Stat(configPath); err != nil {
		problems = append(problems, "x Файл конфигурации config.ini не найден.")
	}
	if _, err := os.Stat(com0comUninstallerPath); err != nil {
		problems = append(problems, "x Деинсталлятор com0com не найден.")
	}
	if _, err := wu.RunCommand("schtasks", "/Query", "/TN", taskName); err != nil {
		problems = append(problems, fmt.Sprintf("x Задача '%s' в Планировщике не найдена.", taskName))
	}

	if len(problems) == 0 {
		tui.Success("\n[ДИАГНОСТИКА] Проблем не обнаружено. Система выглядит настроенной.")
		slog.Info("Диагностика не выявила проблем")
	} else {
		tui.Warn("\n[ДИАГНОСТИКА] Обнаружены следующие проблемы:")
		slog.Warn("Диагностика выявила проблемы", "problems", problems)
		for _, p := range problems {
			tui.Warn(p)
		}
	}

	fmt.Println("\nВыберите действие:")
	fmt.Println(" 1. Полностью удалить VComCaster")
	fmt.Println(" 2. Выполнить переустановку (сначала удалит, потом нужно запустить снова)")
	fmt.Println(" 3. Вернуться в главное меню")
	fmt.Print("Ваш выбор: ")

	reader := bufio.NewReader(os.Stdin)
	choice, _ := reader.ReadString('\n')
	choice = strings.TrimSpace(choice)

	slog.Info("Выбор пользователя в меню диагностики", "choice", choice)

	switch choice {
	case "1":
		return m.runUninstallation(wu, am) // <-- Передаем am
	case "2":
		return m.runReinstallation(wu, am) // <-- Вызываем новую функцию
	case "3":
		tui.Info("Операция отменена. Возврат в главное меню.")
		return nil
	default:
		tui.Warn("Неверный выбор. Возврат в главное меню.")
		return nil
	}
}

// Новая функция переустановки
func (m *Module) runReinstallation(wu core.WinUtils, am core.AssetManager) error {
	tui.Title("\n--- Начало процесса переустановки VComCaster ---")
	slog.Info("Запуск процесса переустановки")

	tui.Info("-> Остановка процесса 'vcomcaster.exe'...")
	slog.Debug("Остановка процесса taskkill")
	_, _ = wu.RunCommand("taskkill", "/F", "/IM", "vcomcaster.exe")

	tui.InfoF("-> Удаление задачи '%s' из Планировщика...", taskName)
	slog.Debug("Удаление задачи планировщика")
	if _, err := wu.RunCommand("schtasks", "/Delete", "/TN", taskName, "/F"); err != nil {
		tui.Warn("   (Предупреждение: не удалось удалить задачу, возможно, ее и не было)")
		slog.Debug("Ошибка удаления задачи (не критично)", "error", err)
	}

	tui.Info("-> Очистка старых ассетов VComCaster...")
	slog.Info("Очистка кэша ассетов")
	if err := am.PurgeAsset("VComCaster_Package"); err != nil {
		tui.Warn(fmt.Sprintf("Произошла ошибка при очистке ассета VComCaster_Package: %v", err))
		slog.Warn("Ошибка очистки VComCaster_Package", "error", err)
	}
	if err := am.PurgeAsset("Com0Com_Installer"); err != nil {
		tui.Warn(fmt.Sprintf("Произошла ошибка при очистке ассета Com0Com_Installer: %v", err))
		slog.Warn("Ошибка очистки Com0Com_Installer", "error", err)
	}
	tui.Success("Старые ассеты очищены.")

	tui.Info("\n--- Запуск новой установки ---")
	// Просто вызываем основной воркфлоу установки
	return m.runInstallWorkflow(am, wu)
}

// Функция полного удаления
func (m *Module) runUninstallation(wu core.WinUtils, am core.AssetManager) error { // <-- Добавляем am в аргументы
	tui.Title("\n--- Начало процесса полного удаления ---")
	slog.Info("Запуск процесса полного удаления")
	baseDir := filepath.Join(am.Cfg().RootPath, "vcomcaster")

	tui.Info("-> Остановка процесса 'vcomcaster.exe'...")
	slog.Debug("Остановка процесса taskkill")
	_, _ = wu.RunCommand("taskkill", "/F", "/IM", "vcomcaster.exe")

	tui.InfoF("-> Удаление задачи '%s'...", taskName)
	slog.Debug("Удаление задачи планировщика")
	if _, err := wu.RunCommand("schtasks", "/Delete", "/TN", taskName, "/F"); err != nil {
		tui.Warn("   (Предупреждение: не удалось удалить задачу, возможно, ее и не было)")
		slog.Debug("Ошибка удаления задачи (не критично)", "error", err)
	}

	uninstallerPath := filepath.Join(baseDir, "com0com", "uninstall.exe")
	installPath := filepath.Join(baseDir, "com0com")

	slog.Debug("Проверка деинсталлятора com0com", "path", uninstallerPath)
	if _, err := os.Stat(uninstallerPath); err == nil {
		tui.Info("-> Запуск деинсталлятора com0com...")
		slog.Info("Запуск деинсталлятора com0com")
		if _, err := wu.RunCommand(uninstallerPath, "/S", fmt.Sprintf("_?=%s", installPath)); err != nil {
			tui.Warn(fmt.Sprintf("   (Предупреждение: деинсталлятор com0com завершился с ошибкой: %v)", err))
			slog.Warn("Ошибка деинсталлятора com0com", "error", err)
		} else {
			tui.Success("   com0com удален.")
		}
	} else {
		tui.Info("-> Деинсталлятор com0com не найден, пропуск.")
		slog.Warn("Деинсталлятор com0com не найден")
	}

	tui.Info("-> Очистка ассетов и директорий VComCaster...")
	if err := am.PurgeAsset("VComCaster_Package"); err != nil {
		tui.Warn(fmt.Sprintf("Произошла ошибка при очистке ассета VComCaster_Package: %v", err))
		slog.Warn("Ошибка очистки VComCaster_Package", "error", err)
	}
	if err := am.PurgeAsset("Com0Com_Installer"); err != nil {
		tui.Warn(fmt.Sprintf("Произошла ошибка при очистке ассета Com0Com_Installer: %v", err))
		slog.Warn("Ошибка очистки Com0Com_Installer", "error", err)
	}

	tui.Success("\nПолное удаление успешно завершено.")
	slog.Info("Полное удаление завершено")
	return nil
}

// Вспомогательные функции
func findNewPorts(before, after []string) []string {
	beforeMap := make(map[string]bool)
	for _, port := range before {
		beforeMap[port] = true
	}
	var newPorts []string
	for _, port := range after {
		if !beforeMap[port] {
			newPorts = append(newPorts, port)
		}
	}
	return newPorts
}

func readConfig(path string) (*ini.File, error) {
	cfg, err := ini.Load(path)
	if err != nil {
		return nil, err
	}
	return cfg, nil
}
