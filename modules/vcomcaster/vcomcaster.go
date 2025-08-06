// file: modules/vcomcaster/vcomcaster.go

package vcomcaster

import (
	"bufio"
	"fmt"
	"goMH/core"
	"goMH/tui"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
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
	vcomcasterBaseDir := filepath.Join(am.Cfg().RootPath, "vcomcaster")

	if _, err := os.Stat(vcomcasterBaseDir); err == nil {
		// Если директория есть, запускаем режим диагностики/удаления
		return m.runDiagnosticsWorkflow(wu, vcomcasterBaseDir)
	}

	// Если директории нет, запускаем режим установки
	return m.runInstallWorkflow(am, wu)
}

// --- РЕЖИМ УСТАНОВКИ ---
func (m *Module) runInstallWorkflow(am core.AssetManager, wu core.WinUtils) error {
	tui.Title("\n--- Запуск установки VComCaster ---")

	// 1. Получаем ресурсы
	tui.Info("-> Этап 1: Загрузка необходимых ресурсов...")
	vcomcasterDestPath, err := am.Get("VComCaster_Package")
	if err != nil {
		return fmt.Errorf("не удалось получить VComCaster_Package: %w", err)
	}
	com0comDestPath, err := am.Get("Com0Com_Installer")
	if err != nil {
		return fmt.Errorf("не удалось получить Com0Com_Installer: %w", err)
	}
	com0comInstallerExe := filepath.Join(com0comDestPath, "com0com_Setup_v3_x64.exe")

	// 2. Установка com0com
	tui.Info("-> Этап 2: Установка com0com...")
	portsBefore, _ := wu.GetComPorts()

	com0comInstallDir := filepath.Join(vcomcasterDestPath, "com0com")
	_ = os.MkdirAll(com0comInstallDir, 0755)

	com0comEnv := map[string]string{
		"CNC_INSTALL_COMX_COMX_PORTS":      "YES",
		"CNC_INSTALL_CNCA0_CNCB0_PORTS":    "NO",
		"CNC_INSTALL_START_MENU_SHORTCUTS": "NO",
	}

	_, err = wu.RunCommandWithEnv(
		com0comEnv,
		com0comInstallerExe,
		"/S",
		fmt.Sprintf("/D=%s", com0comInstallDir),
	)
	if err != nil {
		return fmt.Errorf("ошибка при установке com0com: %w", err)
	}
	tui.Info("Установка com0com завершена, ожидание инициализации портов (5 сек)...")
	time.Sleep(5 * time.Second)

	portsAfter, _ := wu.GetComPorts()
	newPorts := findNewPorts(portsBefore, portsAfter)
	if len(newPorts) < 2 {
		tui.Warn("ПРЕДУПРЕЖДЕНИЕ: Не удалось определить созданные виртуальные COM-порты. Проверьте Диспетчер устройств.")
	} else {
		sort.Strings(newPorts)
		tui.SuccessF("Созданы виртуальные порты: %s и %s", newPorts[0], newPorts[1])
	}

	// 3. Определение сканера
	tui.Info("-> Этап 3: Определение сканера...")
	reader := bufio.NewReader(os.Stdin)
	fmt.Print("Введите НОМЕР физического COM-порта сканера (только цифры): ")
	comPortNum, _ := reader.ReadString('\n')
	scannerComPort := "COM" + strings.TrimSpace(comPortNum)
	tui.InfoF("Порт сканера установлен в %s.", scannerComPort)

	// 4. Создание config.ini
	tui.Info("-> Этап 4: Создание config.ini...")
	outputPort := ""
	if len(newPorts) > 0 {
		outputPort = newPorts[0]
	}
	iniContent := fmt.Sprintf(
		"[app]\r\nautostart_listing = 1\r\nautoreconnect = 1\r\nlogs-autoclear-days = 2\r\n[device]\r\ndevice_id = \r\ninput_port = %s\r\noutput_port = %s\r\nport_baudrate = 115200\r\ncr = 0\r\nlf = 0\r\n[service]\r\namount_rm_char_id = 0\r\ntimeout_clearcash = 1.5\r\ntimeout_autoreconnect = 3\r\ntimeout_reconnect = 3",
		scannerComPort, outputPort,
	)
	configPath := filepath.Join(vcomcasterDestPath, "config.ini")
	if err := os.WriteFile(configPath, []byte(iniContent), 0644); err != nil {
		return fmt.Errorf("не удалось создать config.ini: %w", err)
	}
	tui.Success("Файл config.ini успешно создан.")

	// 5. Финальная настройка
	tui.Info("-> Этап 5: Финальная настройка (Планировщик, запуск)...")
	vcomcasterExePath := filepath.Join(vcomcasterDestPath, "vcomcaster.exe")

	if err := wu.CreateScheduledTask(taskName, vcomcasterExePath, vcomcasterDestPath); err != nil {
		tui.Warn(fmt.Sprintf("ВНИМАНИЕ: Не удалось создать/обновить задачу в планировщике: %v", err))
	} else {
		tui.SuccessF("Задача '%s' в Планировщике Windows успешно создана/обновлена.", taskName)
	}

	if len(newPorts) > 1 {
		iikoPort := newPorts[1]
		if err := m.updateIikoConfig(wu, iikoPort); err != nil {
			tui.Warn(fmt.Sprintf("Не удалось автоматически обновить конфиг iiko: %v", err))
			tui.Warn(fmt.Sprintf("ВАЖНО: Пожалуйста, вручную укажите в настройках iiko порт сканера: %s", iikoPort))
		}
	} else {
		tui.Warn("Не удалось определить порт для iiko. Пропустили обновление конфига.")
	}

	tui.Info("Запуск vcomcaster.exe...")
	// Запуск GUI приложения без ожидания. Этот вызов остается прямым, т.к. не влияет на тестируемую логику.
	startCmd := exec.Command(vcomcasterExePath)
	startCmd.Dir = vcomcasterDestPath
	if err := startCmd.Start(); err != nil {
		return fmt.Errorf("не удалось запустить vcomcaster.exe: %w", err)
	}
	tui.Success("Приложение vcomcaster успешно запущено в фоновом режиме.")

	return nil
}

func (m *Module) updateIikoConfig(wu core.WinUtils, iikoPort string) error {
	const maxRetries = 3
	const retryDelay = 10 * time.Second

	configDir, err := os.UserConfigDir()
	if err != nil {
		return fmt.Errorf("не удалось найти директорию APPDATA: %w", err)
	}
	configPath := filepath.Join(configDir, "iiko", "CashServer", "config.xml")

	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		tui.InfoF("Файл конфигурации iiko не найден по пути: %s. Пропускаем.", configPath)
		tui.InfoF("После первого запуска iiko, пожалуйста, укажите порт сканера вручную: %s", iikoPort)
		return nil
	}
	tui.InfoF("Найден файл конфигурации iiko: %s", configPath)

	for i := 0; i < maxRetries; i++ {
		isRunning, err := wu.IsProcessRunning(iikoProcessName)
		if err != nil {
			return fmt.Errorf("не удалось проверить статус процесса iiko: %w", err)
		}
		if !isRunning {
			break
		}
		tui.Warn(fmt.Sprintf("Обнаружен запущенный процесс '%s'. Это может помешать сохранению файла.", iikoProcessName))
		tui.Warn(fmt.Sprintf("Пожалуйста, закройте iiko Front. Ожидание %v... (попытка %d из %d)", retryDelay, i+1, maxRetries))
		time.Sleep(retryDelay)

		if i == maxRetries-1 {
			return fmt.Errorf("процесс '%s' все еще запущен после %d попыток. Изменение отменено.", iikoProcessName, maxRetries)
		}
	}

	doc := etree.NewDocument()
	if err := doc.ReadFromFile(configPath); err != nil {
		return fmt.Errorf("ошибка чтения XML файла: %w", err)
	}

	root := doc.SelectElement("config")
	if root == nil {
		return fmt.Errorf("корневой элемент <config> не найден в файле %s. Изменение отменено.", configPath)
	}

	portElement := root.SelectElement("comBarcodeScanerPort")
	if portElement == nil {
		tui.Info("Элемент <comBarcodeScanerPort> не найден. Создаем его.")
		portElement = root.CreateElement("comBarcodeScanerPort")
	}

	tui.InfoF("Обновляем порт сканера в конфиге iiko на '%s'...", iikoPort)
	portElement.SetText(iikoPort)

	doc.Indent(2)
	if err := doc.WriteToFile(configPath); err != nil {
		return fmt.Errorf("ошибка сохранения XML файла: %w", err)
	}

	tui.Success("Конфигурация iiko успешно обновлена.")
	return nil
}

// --- РЕЖИМ ДИАГНОСТИКИ И УДАЛЕНИЯ ---
func (m *Module) runDiagnosticsWorkflow(wu core.WinUtils, baseDir string) error {
	tui.Title("\n--- Обнаружена существующая установка. Запуск диагностики... ---")

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
	} else {
		tui.Warn("\n[ДИАГНОСТИКА] Обнаружены следующие проблемы:")
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

	switch choice {
	case "1":
		return m.runUninstallation(wu, baseDir)
	case "2":
		tui.Info("\nНачинается процесс удаления перед переустановкой...")
		err := m.runUninstallation(wu, baseDir)
		if err != nil {
			return fmt.Errorf("ошибка во время удаления перед переустановкой: %w", err)
		}
		tui.Success("\nУдаление завершено. Теперь запустите установку из главного меню еще раз.")
		return nil
	case "3":
		tui.Info("Операция отменена. Возврат в главное меню.")
		return nil
	default:
		tui.Warn("Неверный выбор. Возврат в главное меню.")
		return nil
	}
}

// Функция полного удаления
func (m *Module) runUninstallation(wu core.WinUtils, baseDir string) error {
	tui.Title("\n--- Начало процесса удаления ---")

	tui.Info("-> Остановка процесса 'vcomcaster.exe'...")
	_, _ = wu.RunCommand("taskkill", "/F", "/IM", "vcomcaster.exe")

	tui.InfoF("-> Удаление задачи '%s'...", taskName)
	if _, err := wu.RunCommand("schtasks", "/Delete", "/TN", taskName, "/F"); err != nil {
		tui.Warn("   (Предупреждение: не удалось удалить задачу, возможно, ее и не было)")
	}

	uninstallerPath := filepath.Join(baseDir, "com0com", "uninstall.exe")
	installPath := filepath.Join(baseDir, "com0com")
	if _, err := os.Stat(uninstallerPath); err == nil {
		tui.Info("-> Запуск деинсталлятора com0com...")
		if _, err := wu.RunCommand(uninstallerPath, "/S", fmt.Sprintf("_?=%s", installPath)); err != nil {
			tui.Warn(fmt.Sprintf("   (Предупреждение: деинсталлятор com0com завершился с ошибкой: %v)", err))
		} else {
			tui.Success("   com0com удален.")
		}
	} else {
		tui.Info("-> Деинсталлятор com0com не найден, пропуск.")
	}

	tui.InfoF("-> Удаление папки '%s'...", baseDir)
	if err := os.RemoveAll(baseDir); err != nil {
		return fmt.Errorf("не удалось полностью удалить директорию %s: %w", baseDir, err)
	}

	tui.Success("\nУдаление успешно завершено.")
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
