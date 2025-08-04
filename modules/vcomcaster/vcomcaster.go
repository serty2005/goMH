package vcomcaster

import (
	"bufio"
	"fmt"
	"goMH/assetmgr"
	"goMH/winutils"
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
	taskName = "VComCaster Autostart"
)

type Module struct{}

func (m *Module) ID() string { return "VComCaster" }
func (m *Module) MenuText() string {
	return "VComCaster (для сканера штрих-кодов)"
}

// Главная точка входа для модуля
func (m *Module) Run(am *assetmgr.Manager) error {
	vcomcasterBaseDir := filepath.Join(am.Cfg().RootPath, "vcomcaster")

	// Проверяем, существует ли директория установки
	if _, err := os.Stat(vcomcasterBaseDir); err == nil {
		// Если директория есть, запускаем режим диагностики/удаления
		return runDiagnosticsWorkflow(vcomcasterBaseDir)
	}

	// Если директории нет, запускаем режим установки
	return runInstallWorkflow(am)
}

// --- РЕЖИМ УСТАНОВКИ ---
func runInstallWorkflow(am *assetmgr.Manager) error {
	fmt.Println("\n--- Запуск установки VComCaster ---")

	// 1. Получаем ресурсы
	fmt.Println("-> Этап 1: Загрузка необходимых ресурсов...")
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
	fmt.Println("-> Этап 2: Установка com0com...")
	portsBefore, _ := winutils.GetComPorts()

	com0comInstallDir := filepath.Join(vcomcasterDestPath, "com0com")
	_ = os.MkdirAll(com0comInstallDir, 0755)

	cmd := exec.Command(com0comInstallerExe, "/S", fmt.Sprintf("/D=%s", com0comInstallDir))
	cmd.Env = append(os.Environ(),
		"CNC_INSTALL_COMX_COMX_PORTS=YES",
		"CNC_INSTALL_CNCA0_CNCB0_PORTS=NO",
		"CNC_INSTALL_START_MENU_SHORTCUTS=NO",
	)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("ошибка при установке com0com: %w", err)
	}
	fmt.Println("Установка com0com завершена, ожидание инициализации портов (5 сек)...")
	time.Sleep(5 * time.Second)

	portsAfter, _ := winutils.GetComPorts()
	newPorts := findNewPorts(portsBefore, portsAfter)
	if len(newPorts) < 2 {
		fmt.Println("ПРЕДУПРЕЖДЕНИЕ: Не удалось определить созданные виртуальные COM-порты. Проверьте Диспетчер устройств.")
	} else {
		sort.Strings(newPorts)
		fmt.Printf("Созданы виртуальные порты: %s и %s\n", newPorts[0], newPorts[1])
	}

	// 3. Определение сканера
	fmt.Println("-> Этап 3: Определение сканера...")
	reader := bufio.NewReader(os.Stdin)
	fmt.Print("Введите НОМЕР физического COM-порта сканера (только цифры): ")
	comPortNum, _ := reader.ReadString('\n')
	scannerComPort := "COM" + strings.TrimSpace(comPortNum)
	fmt.Printf("Порт сканера установлен в %s.\n", scannerComPort)

	// 4. Создание config.ini
	fmt.Println("-> Этап 4: Создание config.ini...")
	outputPort := ""
	if len(newPorts) > 0 {
		outputPort = newPorts[0]
	}
	iniContent := fmt.Sprintf(
		"[app]\r\nautostart_listing = 1\r\nautoreconnect = 1\r\nlogs-autoclear-days = 2\r\n[device]\r\ndevice_id = \r\ninput_port = %s\r\noutput_port = %s\r\nport_baudrate = 115200\r\ncr = 0\r\nlf = 0\r\n[service]\r\namount_rm_char_id = 6\r\ntimeout_clearcash = 1.5\r\ntimeout_autoreconnect = 5\r\ntimeout_reconnect = 5",
		scannerComPort, outputPort,
	)
	configPath := filepath.Join(vcomcasterDestPath, "config.ini")
	if err := os.WriteFile(configPath, []byte(iniContent), 0644); err != nil {
		return fmt.Errorf("не удалось создать config.ini: %w", err)
	}
	fmt.Println("Файл config.ini успешно создан.")

	// 5. Финальная настройка
	fmt.Println("-> Этап 5: Финальная настройка (Планировщик, запуск)...")
	vcomcasterExePath := filepath.Join(vcomcasterDestPath, "vcomcaster.exe")

	if err := winutils.CreateScheduledTask(taskName, vcomcasterExePath, vcomcasterDestPath); err != nil {
		// Используем fmt.Printf для вывода в консоль, но не прерываем выполнение
		fmt.Printf("ВНИМАНИЕ: Не удалось создать/обновить задачу в планировщике: %v\n", err)
	} else {
		// Сообщение из CreateScheduledTask будет выведено автоматически
	}

	if len(newPorts) > 1 {
		iikoPort := newPorts[1]
		if err := updateIikoConfig(iikoPort); err != nil {
			// Это не критическая ошибка, просто выводим предупреждение
			fmt.Printf("ВНИМАНИЕ: Не удалось автоматически обновить конфиг iiko: %v\n", err)
			fmt.Printf("ВАЖНО: Пожалуйста, вручную укажите в настройках iiko порт сканера: %s\n", iikoPort)
		}
	} else {
		fmt.Println("ПРЕДУПРЕЖДЕНИЕ: Не удалось определить порт для iiko. Пропустили обновление конфига.")
	}

	fmt.Println("Запуск vcomcaster.exe...")
	startCmd := exec.Command(vcomcasterExePath)
	startCmd.Dir = vcomcasterDestPath
	if err := startCmd.Start(); err != nil {
		return fmt.Errorf("не удалось запустить vcomcaster.exe: %w", err)
	}
	fmt.Println("Приложение vcomcaster успешно запущено в фоновом режиме.")

	return nil
}

func updateIikoConfig(iikoPort string) error {
	const iikoProcessName = "iikoFront"
	const maxRetries = 3                // Максимум попыток (например, 3 * 10 секунд)
	const retryDelay = 10 * time.Second // Задержка между попытками

	// 1. Определяем путь к конфигу iiko
	configDir, err := os.UserConfigDir()
	if err != nil {
		return fmt.Errorf("не удалось найти директорию APPDATA: %w", err)
	}
	configPath := filepath.Join(configDir, "iiko", "CashServer", "config.xml")

	// 2. Проверяем, существует ли файл.
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		fmt.Printf("Файл конфигурации iiko не найден по пути: %s. Пропускаем.\n", configPath)
		fmt.Printf("После первого запуска iiko, пожалуйста, укажите порт сканера вручную: %s\n", iikoPort)
		return nil
	}
	fmt.Printf("Найден файл конфигурации iiko: %s\n", configPath)

	// Проверяем, запущен ли процесс iikoFront
	for i := 0; i < maxRetries; i++ {
		isRunning, err := winutils.IsProcessRunning(iikoProcessName)
		if err != nil {
			// Если сама проверка завершилась с ошибкой, сообщаем и выходим.
			return fmt.Errorf("не удалось проверить статус процесса iiko: %w", err)
		}

		if !isRunning {
			// Процесс не запущен, можно продолжать
			break
		}

		// Если процесс запущен, выводим предупреждение и ждем
		fmt.Printf("ПРЕДУПРЕЖДЕНИЕ: Обнаружен запущенный процесс '%s'. Это может помешать сохранению файла.\n", iikoProcessName)
		fmt.Printf("Пожалуйста, закройте iiko Front. Ожидание %v... (попытка %d из %d)\n", retryDelay, i+1, maxRetries)
		time.Sleep(retryDelay)

		// Если это была последняя попытка, выходим с ошибкой
		if i == maxRetries-1 {
			return fmt.Errorf("процесс '%s' все еще запущен после %d попыток. Изменение отменено.", iikoProcessName, maxRetries)
		}
	}

	// 3. Открываем и парсим XML
	doc := etree.NewDocument()
	if err := doc.ReadFromFile(configPath); err != nil {
		return fmt.Errorf("ошибка чтения XML файла: %w", err)
	}

	// 4. Находим корневой элемент <config>. Он ОБЯЗАН существовать.
	root := doc.SelectElement("config")
	if root == nil {
		return fmt.Errorf("корневой элемент <config> не найден в файле %s. Изменение отменено.", configPath)
	}

	// 5. Находим элемент <comBarcodeScanerPort>. Если его нет, создаем внутри <config>.
	portElement := root.SelectElement("comBarcodeScanerPort")
	if portElement == nil {
		fmt.Println("Элемент <comBarcodeScanerPort> не найден. Создаем его.")
		portElement = root.CreateElement("comBarcodeScanerPort")
	}

	// 6. Устанавливаем нужное значение
	fmt.Printf("Обновляем порт сканера в конфиге iiko на '%s'...\n", iikoPort)
	portElement.SetText(iikoPort)

	// 7. Сохраняем измененный XML обратно в файл
	doc.Indent(2)
	if err := doc.WriteToFile(configPath); err != nil {
		return fmt.Errorf("ошибка сохранения XML файла: %w", err)
	}

	fmt.Println("Конфигурация iiko успешно обновлена.")
	return nil
}

// --- РЕЖИМ ДИАГНОСТИКИ И УДАЛЕНИЯ ---
func runDiagnosticsWorkflow(baseDir string) error {
	fmt.Println("\n--- Обнаружена существующая установка. Запуск диагностики... ---")

	var problems []string
	configPath := filepath.Join(baseDir, "config.ini")
	com0comUninstallerPath := filepath.Join(baseDir, "com0com", "uninstall.exe")

	// 1. Проверка файлов
	if _, err := os.Stat(configPath); err != nil {
		problems = append(problems, "x Файл конфигурации config.ini не найден.")
	}
	if _, err := os.Stat(com0comUninstallerPath); err != nil {
		problems = append(problems, "x Деинсталлятор com0com не найден.")
	}

	// 2. Проверка задачи в планировщике
	if _, err := winutils.RunCommand("schtasks", "/Query", "/TN", taskName); err != nil {
		problems = append(problems, fmt.Sprintf("x Задача '%s' в Планировщике не найдена.", taskName))
	}

	// 3. Вывод результатов
	if len(problems) == 0 {
		fmt.Println("\n[ДИАГНОСТИКА] Проблем не обнаружено. Система выглядит настроенной.")
	} else {
		fmt.Println("\n[ДИАГНОСТИКА] Обнаружены следующие проблемы:")
		for _, p := range problems {
			fmt.Println(p)
		}
	}

	// 4. Диалог с пользователем
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
		return runUninstallation(baseDir)
	case "2":
		fmt.Println("\nНачинается процесс удаления перед переустановкой...")
		err := runUninstallation(baseDir)
		if err != nil {
			return fmt.Errorf("ошибка во время удаления перед переустановкой: %w", err)
		}
		fmt.Println("\nУдаление завершено. Теперь запустите установку из главного меню еще раз.")
		return nil // Возвращаемся в меню без ошибки
	case "3":
		fmt.Println("Операция отменена. Возврат в главное меню.")
		return nil
	default:
		fmt.Println("Неверный выбор. Возврат в главное меню.")
		return nil
	}
}

// Функция полного удаления
func runUninstallation(baseDir string) error {
	fmt.Println("\n--- Начало процесса удаления ---")

	// 1. Остановка процесса vcomcaster.exe
	fmt.Println("-> Остановка процесса 'vcomcaster.exe'...")
	// В Windows нет pkill, используем taskkill
	_ = exec.Command("taskkill", "/F", "/IM", "vcomcaster.exe").Run()

	// 2. Удаление задачи из планировщика
	fmt.Printf("-> Удаление задачи '%s'...\n", taskName)
	if _, err := winutils.RunCommand("schtasks", "/Delete", "/TN", taskName, "/F"); err != nil {
		fmt.Println("   (Предупреждение: не удалось удалить задачу, возможно, ее и не было)")
	}

	// 3. Запуск деинсталлятора com0com
	uninstallerPath := filepath.Join(baseDir, "com0com", "uninstall.exe")
	installPath := filepath.Join(baseDir, "com0com")
	if _, err := os.Stat(uninstallerPath); err == nil {
		fmt.Println("-> Запуск деинсталлятора com0com...")
		cmd := exec.Command(uninstallerPath, "/S", fmt.Sprintf("_?=%s", installPath))
		if err := cmd.Run(); err != nil {
			fmt.Printf("   (Предупреждение: деинсталлятор com0com завершился с ошибкой: %v)\n", err)
		} else {
			fmt.Println("   com0com удален.")
		}
	} else {
		fmt.Println("-> Деинсталлятор com0com не найден, пропуск.")
	}

	// 4. Удаление всей директории
	fmt.Printf("-> Удаление папки '%s'...\n", baseDir)
	if err := os.RemoveAll(baseDir); err != nil {
		return fmt.Errorf("не удалось полностью удалить директорию %s: %w", baseDir, err)
	}

	fmt.Println("\nУдаление успешно завершено.")
	return nil
}

// Вспомогательные функции findNewPorts и ini-парсер
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

// Для будущих проверок, если понадобится читать конфиг
func readConfig(path string) (*ini.File, error) {
	cfg, err := ini.Load(path)
	if err != nil {
		return nil, err
	}
	return cfg, nil
}
