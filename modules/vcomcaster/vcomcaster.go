// file: modules/vcomcaster/vcomcaster.go

package vcomcaster

import (
	"errors"
	"fmt"
	"goMH/core"
	"goMH/tui"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
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

// ActionType определяет тип задачи для VComCaster
type ActionType int

const (
	ActionInstall ActionType = iota
	ActionReinstall
	ActionUninstall
)

// VComCasterConfig хранит все настройки, необходимые для выполнения задачи.
// Эта структура заполняется в UI-потоке (Run/Configure) и передается в Worker (Execute).
type VComCasterConfig struct {
	Action          ActionType
	SelectedScanner core.ScannerInfo // Выбранный физический сканер
	ScannerDeviceID string           // Извлеченный VID/PID
	InstallPath     string           // Куда ставить (C:\MH\vcomcaster)
}

type Module struct{}

func (m *Module) ID() string       { return "VComCaster" }
func (m *Module) MenuText() string { return "VComCaster (для сканера штрих-кодов)" }

func (m *Module) ConfigureTask(ctx core.TaskContext, services core.ModuleServices) (any, error) {
	return m.Configure(ctx, services.AssetManager, services.WinUtils)
}

func (m *Module) BuildTask(config any) (core.ModuleTaskPlan, error) {
	cfg, err := m.taskConfig(config)
	if err != nil {
		return core.ModuleTaskPlan{}, err
	}

	title := "VComCaster"
	signature := "vcomcaster|generic"
	switch cfg.Action {
	case ActionInstall:
		title = fmt.Sprintf("VComCaster: установка для %s", cfg.SelectedScanner.Caption)
		signature = fmt.Sprintf("vcomcaster|install|%s", cfg.ScannerDeviceID)
	case ActionReinstall:
		title = fmt.Sprintf("VComCaster: переустановка для %s", cfg.SelectedScanner.Caption)
		signature = fmt.Sprintf("vcomcaster|reinstall|%s", cfg.ScannerDeviceID)
	case ActionUninstall:
		title = "VComCaster: удаление"
		signature = "vcomcaster|uninstall"
	}

	return core.ModuleTaskPlan{
		Mode: core.ModuleRunModeQueue,
		Task: core.ModuleTaskSpec{
			Title:     title,
			Signature: signature,
			Exclusive: cfg.Action == ActionInstall || cfg.Action == ActionReinstall || cfg.Action == ActionUninstall,
		},
	}, nil
}

func (m *Module) ExecuteTask(ctx core.TaskContext, services core.ModuleServices, config any) error {
	cfg, err := m.taskConfig(config)
	if err != nil {
		return err
	}
	return m.Execute(ctx, services.AssetManager, services.WinUtils, cfg)
}

// Run - точка входа (UI слой). Здесь мы общаемся с пользователем.
func (m *Module) Run(am core.AssetManager, wu core.WinUtils) error {
	// 1. Создаем адаптер для консоли
	ctx := tui.NewConsoleContext()

	// 2. Этап конфигурации (Сбор данных)
	cfg, err := m.Configure(ctx, am, wu)
	if err != nil {
		// Если пользователь отменил выбор (например, вернул nil конфиг без ошибки), выходим
		if cfg == nil {
			return nil
		}
		return err
	}

	// 3. Этап выполнения (Бизнес-логика)
	// В будущем это уйдет в TaskManager.AddTask()
	return m.Execute(ctx, am, wu, cfg)
}

// Configure выполняет диагностику, сканирование оборудования и опрос пользователя.
func (m *Module) Configure(ctx core.TaskContext, am core.AssetManager, wu core.WinUtils) (*VComCasterConfig, error) {
	vcomcasterBaseDir := filepath.Join(am.Cfg().RootPath, "vcomcaster")

	// Проверяем наличие установки
	if _, err := os.Stat(vcomcasterBaseDir); err == nil {
		ctx.Info("Обнаружена существующая установка VComCaster.")
		return m.configureDiagnostics(wu, vcomcasterBaseDir)
	}

	// Если установки нет - конфигурируем новую установку
	return m.configureInstall(wu, vcomcasterBaseDir)
}

// Execute выполняет задачу на основе подготовленного конфига.
func (m *Module) Execute(ctx core.TaskContext, am core.AssetManager, wu core.WinUtils, cfg *VComCasterConfig) error {
	switch cfg.Action {
	case ActionInstall:
		return m.install(ctx, am, wu, cfg)
	case ActionReinstall:
		if err := m.uninstall(ctx, am, wu, cfg.InstallPath); err != nil {
			ctx.Warn(fmt.Sprintf("Ошибка при предварительном удалении: %v", err))
		}
		return m.install(ctx, am, wu, cfg)
	case ActionUninstall:
		return m.uninstall(ctx, am, wu, cfg.InstallPath)
	default:
		return fmt.Errorf("неизвестное действие: %d", cfg.Action)
	}
}

func (m *Module) taskConfig(config any) (*VComCasterConfig, error) {
	cfg, ok := config.(*VComCasterConfig)
	if !ok || cfg == nil {
		return nil, fmt.Errorf("неверный конфиг задачи для модуля %s", m.ID())
	}
	return cfg, nil
}

// --- CONFIGURATION HELPERS ---

func (m *Module) configureInstall(wu core.WinUtils, installPath string) (*VComCasterConfig, error) {
	scanners, err := wu.GetScanners()
	if err != nil {
		return nil, fmt.Errorf("ошибка при поиске сканеров: %w", err)
	}
	if len(scanners) == 0 {
		return nil, errors.New("не найдено ни одного USB-сканера. Проверьте подключение")
	}

	items := make([]tui.ChoiceItem, 0, len(scanners))
	for _, scanner := range scanners {
		portInCaption := fmt.Sprintf("(%s)", scanner.Port)
		title := scanner.Caption
		if strings.Contains(scanner.Caption, portInCaption) {
			title = scanner.Caption
		} else {
			title = fmt.Sprintf("%s (%s)", scanner.Caption, scanner.Port)
		}
		items = append(items, tui.ChoiceItem{Title: title, Meta: scanner.Port})
	}

	choice, err := tui.SelectItem(items, tui.SelectionConfig{
		Title:    "Выберите USB-сканер",
		Subtitle: "Esc для отмены",
	})
	if err != nil {
		return nil, err
	}
	if choice < 0 || choice >= len(scanners) {
		return nil, nil
	}

	selectedScanner := scanners[choice]
	deviceID := extractDeviceID(selectedScanner.PNPDeviceID)

	return &VComCasterConfig{
		Action:          ActionInstall,
		SelectedScanner: selectedScanner,
		ScannerDeviceID: deviceID,
		InstallPath:     installPath,
	}, nil
}

func (m *Module) configureDiagnostics(wu core.WinUtils, baseDir string) (*VComCasterConfig, error) {
	var problems []string
	configPath := filepath.Join(baseDir, "config.ini")
	if _, err := os.Stat(configPath); err != nil {
		problems = append(problems, "x Файл config.ini не найден.")
	}
	if _, err := wu.RunCommand("schtasks", "/Query", "/TN", taskName); err != nil {
		problems = append(problems, fmt.Sprintf("x Задача '%s' не найдена.", taskName))
	}

	subtitle := "Проблем не обнаружено."
	if len(problems) > 0 {
		subtitle = strings.Join(problems, "\n")
	}

	choice, err := tui.SelectItem([]tui.ChoiceItem{
		{Title: "Удалить", Description: "Полностью удалить VComCaster"},
		{Title: "Переустановить", Description: "Удалить и настроить заново"},
	}, tui.SelectionConfig{
		Title:    "VComCaster уже установлен",
		Subtitle: subtitle,
	})
	if err != nil {
		return nil, err
	}
	switch choice {
	case 0:
		return &VComCasterConfig{Action: ActionUninstall, InstallPath: baseDir}, nil
	case 1:
		cfg, err := m.configureInstall(wu, baseDir)
		if err != nil {
			return nil, err
		}
		if cfg != nil {
			cfg.Action = ActionReinstall
		}
		return cfg, nil
	default:
		return nil, nil
	}
}

// --- EXECUTION LOGIC ---

func (m *Module) install(ctx core.TaskContext, am core.AssetManager, wu core.WinUtils, cfg *VComCasterConfig) error {
	ctx.SetStatus("Установка VComCaster...")

	// 1. Получаем ресурсы
	ctx.Info("Загрузка ресурсов...")
	vcomcasterDestPath, err := am.Get("VComCaster_Package")
	if err != nil {
		return fmt.Errorf("ошибка получения пакета: %w", err)
	}

	com0comInstallerExe, err := am.DownloadToCache("Com0Com_Installer")
	if err != nil {
		return fmt.Errorf("ошибка скачивания com0com: %w", err)
	}

	// 2. Установка com0com
	ctx.Info("Установка драйвера виртуальных портов com0com...")
	portsBefore, _ := wu.GetComPorts()

	com0comInstallDir := filepath.Join(vcomcasterDestPath, "com0com")
	_ = os.MkdirAll(com0comInstallDir, 0755)

	com0comEnv := map[string]string{
		"CNC_INSTALL_COMX_COMX_PORTS":      "YES",
		"CNC_INSTALL_CNCA0_CNCB0_PORTS":    "NO",
		"CNC_INSTALL_START_MENU_SHORTCUTS": "NO",
	}

	_, err = wu.RunCommandWithEnv(com0comEnv, com0comInstallerExe, "/S", fmt.Sprintf("/D=%s", com0comInstallDir))
	if err != nil {
		return fmt.Errorf("ошибка установки com0com: %w", err)
	}

	ctx.Info("Ожидание инициализации драйверов (5 сек)...")
	time.Sleep(5 * time.Second)

	portsAfter, _ := wu.GetComPorts()
	newPorts := findNewPorts(portsBefore, portsAfter)

	if len(newPorts) < 2 {
		ctx.Warn("Не удалось определить новые виртуальные порты. Проверьте диспетчер устройств.")
	} else {
		sort.Strings(newPorts)
		ctx.Success(fmt.Sprintf("Виртуальные порты созданы: %s <-> %s", newPorts[0], newPorts[1]))
	}

	// 3. Создание конфига
	ctx.Info("Генерация конфигурации...")
	outputPort := ""
	if len(newPorts) > 0 {
		outputPort = newPorts[0]
	}

	iniContent := fmt.Sprintf(
		"[app]\r\nautostart_listing = 1\r\nautoreconnect = 1\r\nlogs-autoclear-days = 2\r\n[device]\r\ndevice_id = %s\r\ninput_port = %s\r\noutput_port = %s\r\nport_baudrate = 115200\r\ncr = 0\r\nlf = 0\r\n[service]\r\namount_rm_char_id = 0\r\ntimeout_clearcash = 1.5\r\ntimeout_autoreconnect = 3\r\ntimeout_reconnect = 3",
		cfg.ScannerDeviceID, cfg.SelectedScanner.Port, outputPort,
	)

	configPath := filepath.Join(vcomcasterDestPath, "config.ini")
	if err := os.WriteFile(configPath, []byte(iniContent), 0644); err != nil {
		return fmt.Errorf("ошибка записи config.ini: %w", err)
	}

	// 4. Финальная настройка
	ctx.Info("Настройка автозапуска...")
	vcomcasterExePath := filepath.Join(vcomcasterDestPath, "vcomcaster.exe")
	if err := wu.CreateScheduledTask(taskName, vcomcasterExePath, "", vcomcasterDestPath); err != nil {
		ctx.Warn(fmt.Sprintf("Не удалось создать задачу планировщика: %v", err))
	} else {
		ctx.Success("Задача автозапуска создана.")
	}

	// Обновление iiko (если есть второй порт)
	if len(newPorts) > 1 {
		iikoPort := newPorts[1]
		ctx.Info(fmt.Sprintf("Обновление конфигурации iiko (порт %s)...", iikoPort))
		if err := m.updateIikoConfig(ctx, wu, iikoPort); err != nil {
			ctx.Warn(fmt.Sprintf("Не удалось обновить конфиг iiko: %v. Укажите порт %s вручную.", err, iikoPort))
		}
	} else {
		ctx.Warn("Не определен второй порт для iiko. Настройка пропущена.")
	}

	// Запуск
	ctx.Info("Запуск приложения...")
	startCmd := exec.Command(vcomcasterExePath)
	startCmd.Dir = vcomcasterDestPath
	if err := startCmd.Start(); err != nil {
		return fmt.Errorf("не удалось запустить процесс: %w", err)
	}

	ctx.Success("VComCaster успешно установлен и запущен.")
	return nil
}

func (m *Module) uninstall(ctx core.TaskContext, am core.AssetManager, wu core.WinUtils, installPath string) error {
	ctx.SetStatus("Удаление VComCaster...")

	ctx.Info("Остановка процессов...")
	_, _ = wu.RunCommand("taskkill", "/F", "/IM", "vcomcaster.exe")

	ctx.Info("Удаление задачи планировщика...")
	if _, err := wu.RunCommand("schtasks", "/Delete", "/TN", taskName, "/F"); err != nil {
		// Игнорируем ошибку, если задачи нет
	}

	uninstallerPath := filepath.Join(installPath, "com0com", "uninstall.exe")
	if _, err := os.Stat(uninstallerPath); err == nil {
		ctx.Info("Удаление драйвера com0com...")
		if _, err := wu.RunCommand(uninstallerPath, "/S", fmt.Sprintf("_?=%s", filepath.Join(installPath, "com0com"))); err != nil {
			ctx.Warn(fmt.Sprintf("Ошибка деинсталлятора com0com: %v", err))
		}
	}

	ctx.Info("Очистка файлов...")
	// Очистка кэша ассетов для чистоты эксперимента при переустановке
	_ = am.PurgeAsset("VComCaster_Package")
	_ = am.PurgeAsset("Com0Com_Installer")

	ctx.Success("Удаление завершено.")
	return nil
}

func (m *Module) updateIikoConfig(ctx core.TaskContext, wu core.WinUtils, iikoPort string) error {
	const maxRetries = 3
	const retryDelay = 5 * time.Second

	configDir, err := os.UserConfigDir()
	if err != nil {
		return err
	}
	configPath := filepath.Join(configDir, "iiko", "CashServer", "config.xml")

	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		return nil // Конфига нет - не ошибка
	}

	// Логика завершения процесса iiko
	isRunning, _ := wu.IsProcessRunning(iikoProcessName)
	if isRunning {
		ctx.Info("Попытка закрыть iikoFront для обновления конфига...")
		_ = wu.GracefulShutdownProcess(iikoProcessName)
		time.Sleep(5 * time.Second)

		for i := 0; i < maxRetries; i++ {
			if run, _ := wu.IsProcessRunning(iikoProcessName); !run {
				break
			}
			if i == maxRetries-1 {
				return fmt.Errorf("iikoFront не закрылся, обновление конфига отменено")
			}
			ctx.Warn("Ожидание закрытия iikoFront...")
			time.Sleep(retryDelay)
		}
	}

	// Правка XML
	doc := etree.NewDocument()
	if err := doc.ReadFromFile(configPath); err != nil {
		return err
	}

	root := doc.SelectElement("config")
	if root == nil {
		return fmt.Errorf("root element not found")
	}

	portElement := root.SelectElement("comBarcodeScanerPort")
	if portElement == nil {
		portElement = root.CreateElement("comBarcodeScanerPort")
	}
	portElement.SetText(iikoPort)

	doc.Indent(2)
	return doc.WriteToFile(configPath)
}

// extractDeviceID извлекает часть VID_...&PID_... из полного PNPDeviceID.
func extractDeviceID(pnpDeviceID string) string {
	re := regexp.MustCompile(`(?i)USB\\(VID_[^&]+&PID_[^&\\]+)`)
	matches := re.FindStringSubmatch(pnpDeviceID)
	if len(matches) > 1 {
		return matches[1]
	}
	return ""
}

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
