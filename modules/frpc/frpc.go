package frpc

import (
	"archive/zip"
	"bufio"
	"encoding/json"
	"fmt"
	"goMH/config"
	"goMH/core"
	"goMH/tui"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort" // Добавлен для сортировки портов
	"strconv"
	"strings"
	"time"
)

type Module struct {
	Cfg *config.FrpcConfig
}

type FrpsProxy struct {
	Name   string    `json:"name"`
	Conf   *FrpsConf `json:"conf"`
	Status string    `json:"status"`
}
type FrpsConf struct {
	RemotePort int `json:"remotePort"`
}
type FrpsProxyInfo struct {
	Proxies []FrpsProxy `json:"proxies"`
}

// ActionType определяет тип действия
type ActionType int

const (
	ActionInstall ActionType = iota
	ActionAddPort
	ActionReinstall
	ActionUninstall
)

// FrpcInstallConfig хранит параметры для выполнения задачи
type FrpcInstallConfig struct {
	Action     ActionType
	LocalPort  string
	Alias      string
	RemotePort int // Вычисленный или введенный пользователем порт
}

func (m *Module) ID() string { return "FRPC" }
func (m *Module) MenuText() string {
	return "Fast Reverse Proxy Client (проброс портов)"
}

// Run - точка входа (UI)
func (m *Module) Run(am core.AssetManager, wu core.WinUtils) error {
	slog.Info("Запуск модуля FRPC")
	m.Cfg = &am.Cfg().FrpcConfig
	ctx := tui.NewConsoleContext()

	// 1. Конфигурация
	cfg, err := m.Configure(ctx, am, wu)
	if err != nil {
		if cfg == nil {
			slog.Info("Настройка FRPC отменена пользователем")
			return nil // Отмена
		}
		slog.Error("Ошибка конфигурации FRPC", "error", err)
		return err
	}

	// 2. Выполнение
	if cfg != nil {
		slog.Info("Запуск выполнения задачи FRPC", "action", cfg.Action)
		return m.Execute(ctx, am, wu, cfg)
	}
	return nil
}

// Configure - опрос пользователя и подготовка данных
func (m *Module) Configure(ctx core.TaskContext, am core.AssetManager, wu core.WinUtils) (*FrpcInstallConfig, error) {
	frpcExePath := filepath.Join(m.Cfg.InstallPath, "frpc.exe")
	isInstalled := false
	if _, err := os.Stat(frpcExePath); err == nil {
		isInstalled = true
		slog.Info("Обнаружена существующая установка FRPC", "path", frpcExePath)
	} else {
		slog.Info("Существующая установка FRPC не найдена")
	}

	var action ActionType

	if isInstalled {
		tui.Info("Обнаружена существующая установка FRPC.")
		fmt.Print("Введите 'R' для добавления порта, 'C' для полной переустановки или 'U' для удаления (R/C/U): ")
		reader := bufio.NewReader(os.Stdin)
		choice, _ := reader.ReadString('\n')
		choice = strings.TrimSpace(strings.ToUpper(choice))

		slog.Info("Выбор действия в меню FRPC", "choice", choice)

		switch choice {
		case "R":
			action = ActionAddPort
		case "C":
			action = ActionReinstall
		case "U":
			fmt.Print("ВНИМАНИЕ: Это полностью удалит FRPC. Вы уверены? (y/n): ")
			confirm, _ := reader.ReadString('\n')
			if strings.TrimSpace(strings.ToLower(confirm)) != "y" {
				slog.Info("Удаление отменено пользователем")
				return nil, nil
			}
			action = ActionUninstall
		default:
			slog.Info("Неверный выбор действия, отмена")
			return nil, nil
		}
	} else {
		action = ActionInstall
	}

	// Если нужно добавить порт или установить с нуля -> спрашиваем параметры
	if action == ActionInstall || action == ActionAddPort || action == ActionReinstall {
		reader := bufio.NewReader(os.Stdin)

		fmt.Print("Введите локальный порт для туннеля (например, 5985 для WinRM): ")
		localPort, _ := reader.ReadString('\n')
		localPort = strings.TrimSpace(localPort)
		if localPort == "" {
			localPort = "5985"
		}

		fmt.Print("Введите имя этого узла (например, SRV-BACKOFFICE-01): ")
		alias, _ := reader.ReadString('\n')
		alias = strings.TrimSpace(alias)
		if alias == "" {
			return nil, fmt.Errorf("имя узла не может быть пустым")
		}

		slog.Info("Введены параметры туннеля", "localPort", localPort, "alias", alias)

		// Поиск свободного порта
		ctx.Info("Поиск свободного порта на сервере...")
		remotePort, err := m.findFreePort()
		if err != nil {
			slog.Error("Не удалось найти свободный порт", "error", err)
			return nil, err
		}
		ctx.Success(fmt.Sprintf("Выбран удаленный порт: %d", remotePort))
		slog.Info("Автоматически выбран удаленный порт", "remotePort", remotePort)

		return &FrpcInstallConfig{
			Action:     action,
			LocalPort:  localPort,
			Alias:      alias,
			RemotePort: remotePort,
		}, nil
	}

	// Для удаления параметры порта не нужны
	if action == ActionUninstall {
		return &FrpcInstallConfig{Action: ActionUninstall}, nil
	}

	return nil, nil
}

// Execute - выполнение задач
func (m *Module) Execute(ctx core.TaskContext, am core.AssetManager, wu core.WinUtils, cfg *FrpcInstallConfig) error {
	ctx.SetStatus(fmt.Sprintf("Выполнение действия FRPC: %d", cfg.Action))
	slog.Info("Начало выполнения Execute", "action", cfg.Action)

	switch cfg.Action {
	case ActionUninstall:
		return m.uninstall(ctx, wu)

	case ActionReinstall:
		slog.Info("Запущен процесс переустановки, сначала удаление...")
		if err := m.uninstall(ctx, wu); err != nil {
			ctx.Warn(fmt.Sprintf("Ошибка при удалении старой версии: %v", err))
			slog.Warn("Ошибка при предварительном удалении FRPC", "error", err)
		}
		fallthrough // Переходим к установке

	case ActionInstall:
		slog.Info("Начало установки FRPC")
		if err := os.MkdirAll(m.Cfg.InstallPath, 0755); err != nil {
			slog.Error("Не удалось создать директорию установки", "path", m.Cfg.InstallPath, "error", err)
			return err
		}

		slog.Debug("Добавление исключения Defender", "path", am.Cfg().RootPath)
		_ = wu.AddDefenderExclusion(am.Cfg().RootPath)

		if err := m.downloadAndExtractComponents(ctx, am, wu); err != nil {
			return err
		}
		// Настраиваем первый порт и службу
		if err := m.setupPortAndService(ctx, wu, cfg, true); err != nil {
			return err
		}

	case ActionAddPort:
		slog.Info("Добавление нового порта к существующей конфигурации")
		// Просто добавляем порт в конфиг и рестартим службу
		if err := m.setupPortAndService(ctx, wu, cfg, false); err != nil {
			return err
		}
	}

	ctx.Success("Операция FRPC успешно завершена.")
	slog.Info("Операция FRPC успешно завершена")
	return nil
}

// --- Вспомогательные функции ---

func (m *Module) setupPortAndService(ctx core.TaskContext, wu core.WinUtils, cfg *FrpcInstallConfig, isNewInstall bool) error {
	ctx.Info("Обновление конфигурации frpc.ini...")
	if err := m.updateFrpcIni(cfg.Alias, cfg.LocalPort, strconv.Itoa(cfg.RemotePort)); err != nil {
		slog.Error("Ошибка обновления frpc.ini", "error", err)
		return err
	}

	if isNewInstall {
		ctx.Info("Настройка службы NSSM...")
		if err := m.setupNssmService(wu); err != nil {
			slog.Error("Ошибка настройки службы NSSM", "error", err)
			return err
		}
	}

	ctx.Info("Перезапуск службы FRPC...")
	slog.Info("Перезапуск службы", "service", m.Cfg.ServiceName)
	_, _ = wu.RunCommand("sc.exe", "stop", m.Cfg.ServiceName)
	time.Sleep(2 * time.Second)
	_, err := wu.RunCommand("sc.exe", "start", m.Cfg.ServiceName)
	if err != nil {
		slog.Error("Не удалось запустить службу", "error", err)
		return fmt.Errorf("не удалось запустить службу: %w", err)
	}
	return nil
}

func (m *Module) uninstall(ctx core.TaskContext, wu core.WinUtils) error {
	slog.Info("Начало удаления FRPC")
	ctx.Info("Остановка и удаление службы FRPC...")
	_, _ = wu.RunCommand("sc.exe", "stop", m.Cfg.ServiceName)
	time.Sleep(1 * time.Second)
	_, _ = wu.RunCommand("sc.exe", "delete", m.Cfg.ServiceName)

	ctx.Info("Удаление файлов...")
	slog.Debug("Удаление директории", "path", m.Cfg.InstallPath)
	os.RemoveAll(m.Cfg.InstallPath)
	return nil
}

func (m *Module) downloadAndExtractComponents(ctx core.TaskContext, am core.AssetManager, wu core.WinUtils) error {
	ctx.Info("Скачивание компонентов...")
	slog.Info("Начало загрузки компонентов FRPC и NSSM")

	// FRPC
	frpcZipPath := filepath.Join(am.Cfg().AssetsCachePath, "frpc.zip")
	slog.Debug("Скачивание FRPC", "url", m.Cfg.FrpcDownloadURL)
	if _, err := am.DownloadHTTPWithProgress(m.Cfg.FrpcDownloadURL, frpcZipPath); err != nil {
		slog.Error("Не удалось скачать FRPC", "error", err)
		return fmt.Errorf("не удалось скачать FRPC: %w", err)
	}
	frpcPathInZip, err := findPathInZip(frpcZipPath, "frpc.exe")
	if err != nil {
		return err
	}
	slog.Debug("Извлечение frpc.exe", "source", frpcPathInZip)
	if err := am.ExtractFile(frpcZipPath, frpcPathInZip, filepath.Join(m.Cfg.InstallPath, "frpc.exe")); err != nil {
		slog.Error("Ошибка извлечения frpc.exe", "error", err)
		return err
	}

	// NSSM
	nssmZipPath := filepath.Join(am.Cfg().AssetsCachePath, "nssm.zip")
	slog.Debug("Скачивание NSSM", "url", m.Cfg.NssmDownloadURL)
	if _, err := am.DownloadHTTPWithProgress(m.Cfg.NssmDownloadURL, nssmZipPath); err != nil {
		slog.Error("Не удалось скачать NSSM", "error", err)
		return fmt.Errorf("не удалось скачать NSSM: %w", err)
	}
	archDir := "win32"
	if wu.Is64BitOS() {
		archDir = "win64"
	}
	slog.Debug("Выбор архитектуры NSSM", "arch", archDir)

	nssmPathInZip, err := findPathInZip(nssmZipPath, filepath.Join(archDir, "nssm.exe"))
	if err != nil {
		return err
	}
	if err := am.ExtractFile(nssmZipPath, nssmPathInZip, filepath.Join(m.Cfg.InstallPath, "nssm.exe")); err != nil {
		slog.Error("Ошибка извлечения nssm.exe", "error", err)
		return err
	}

	return nil
}

func (m *Module) findFreePort() (int, error) {
	apiURL := fmt.Sprintf("https://%s/api/proxy/tcp", m.Cfg.ServerConfig.Host)
	slog.Debug("Запрос к API FRPS", "url", apiURL, "user", m.Cfg.ServerConfig.User)

	req, _ := http.NewRequest("GET", apiURL, nil)
	req.SetBasicAuth(m.Cfg.ServerConfig.User, m.Cfg.ServerConfig.Pass)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		slog.Error("Ошибка HTTP запроса к FRPS", "error", err)
		return 0, err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	var proxyInfo FrpsProxyInfo
	if err := json.Unmarshal(body, &proxyInfo); err != nil {
		slog.Error("Ошибка парсинга JSON от FRPS", "error", err)
		return 0, err
	}

	// 1. Классификация прокси
	type proxyItem struct {
		Port int
		Name string
	}
	var onlineProxies []proxyItem
	var offlineProxies []string

	for _, p := range proxyInfo.Proxies {
		if p.Status == "online" && p.Conf != nil {
			onlineProxies = append(onlineProxies, proxyItem{Port: p.Conf.RemotePort, Name: p.Name})
		} else {
			offlineProxies = append(offlineProxies, p.Name)
		}
	}

	// 2. Если есть оффлайн прокси - выводим полную картину
	if len(offlineProxies) > 0 {
		slog.Warn("Обнаружены оффлайн-прокси", "count", len(offlineProxies))
		tui.Warn("\nВНИМАНИЕ: Обнаружены оффлайн-прокси. Сервер не сообщает их порты, поэтому автоматический выбор небезопасен.")

		// Сортируем онлайн по портам для удобства
		sort.Slice(onlineProxies, func(i, j int) bool {
			return onlineProxies[i].Port < onlineProxies[j].Port
		})

		tui.Info("--- Текущее состояние сети FRPC ---")

		// Вывод занятых портов
		if len(onlineProxies) > 0 {
			fmt.Println(" [Занятые порты (Online)]")
			for _, p := range onlineProxies {
				fmt.Printf("  %d - %s\n", p.Port, p.Name)
			}
		}

		// Вывод оффлайн
		fmt.Println(" [Статус неизвестен (Offline)]")
		for _, name := range offlineProxies {
			fmt.Printf("  OFFLINE - %s\n", name)
		}
		fmt.Println("-----------------------------------")

		reader := bufio.NewReader(os.Stdin)
		for {
			fmt.Print("\nПожалуйста, введите желаемый удаленный порт вручную: ")
			portStr, _ := reader.ReadString('\n')
			port, err := strconv.Atoi(strings.TrimSpace(portStr))
			if err == nil {
				return port, nil
			}
			fmt.Println("Некорректный ввод. Введите число.")
		}
	}

	// Автопоиск (если все онлайн)
	usedPorts := make(map[int]bool)
	for _, p := range proxyInfo.Proxies {
		if p.Conf != nil {
			usedPorts[p.Conf.RemotePort] = true
		}
	}
	// + локальные
	localUsed := getLocalUsedPorts(filepath.Join(m.Cfg.InstallPath, "frpc.ini"))
	for _, p := range localUsed {
		usedPorts[p] = true
	}

	parts := strings.Split(m.Cfg.PortRange, "-")
	start, _ := strconv.Atoi(parts[0])
	end, _ := strconv.Atoi(parts[1])

	slog.Debug("Поиск свободного порта", "start", start, "end", end)

	for port := start; port <= end; port++ {
		if !usedPorts[port] {
			return port, nil
		}
	}
	return 0, fmt.Errorf("нет свободных портов в диапазоне %s", m.Cfg.PortRange)
}

func (m *Module) updateFrpcIni(alias, localPort, remotePort string) error {
	iniPath := filepath.Join(m.Cfg.InstallPath, "frpc.ini")
	slog.Debug("Обновление конфига", "path", iniPath)

	content, err := os.ReadFile(iniPath)
	var lines []string
	if err != nil {
		slog.Info("Создание нового frpc.ini")
		lines = []string{
			"[common]",
			"server_addr = " + m.Cfg.ServerConfig.Host,
			"server_port = " + strconv.Itoa(m.Cfg.ServerConfig.TunnelPort),
			"log_file = " + filepath.Join(m.Cfg.InstallPath, "frpc.log"),
			"log_level = info",
		}
	} else {
		lines = strings.Split(string(content), "\n")
	}

	newSectionName := fmt.Sprintf("[%s-MH]", alias)
	for _, line := range lines {
		if strings.TrimSpace(line) == newSectionName {
			slog.Warn("Секция уже существует, пропускаем запись", "section", newSectionName)
			fmt.Printf("Секция '%s' уже существует. Пропускаем.\n", newSectionName)
			return nil
		}
	}

	slog.Info("Добавление секции", "section", newSectionName, "remote", remotePort)
	lines = append(lines, "", newSectionName, "type = tcp", "local_ip = 127.0.0.1", "local_port = "+localPort, "remote_port = "+remotePort)
	return os.WriteFile(iniPath, []byte(strings.Join(lines, "\r\n")), 0644)
}

func (m *Module) setupNssmService(wu core.WinUtils) error {
	nssmExe := filepath.Join(m.Cfg.InstallPath, "nssm.exe")
	frpcExe := filepath.Join(m.Cfg.InstallPath, "frpc.exe")
	frpcIni := filepath.Join(m.Cfg.InstallPath, "frpc.ini")

	slog.Info("Настройка службы NSSM", "service", m.Cfg.ServiceName)

	commands := [][]string{
		{"install", m.Cfg.ServiceName, frpcExe, "-c", frpcIni},
		{"set", m.Cfg.ServiceName, "Start", "SERVICE_AUTO_START"},
		{"set", m.Cfg.ServiceName, "AppDirectory", m.Cfg.InstallPath},
	}

	for _, args := range commands {
		slog.Debug("Выполнение команды NSSM", "args", args)
		if _, err := wu.RunCommand(nssmExe, args...); err != nil {
			return fmt.Errorf("ошибка nssm %s: %w", args[0], err)
		}
	}
	return nil
}

func getLocalUsedPorts(iniPath string) []int {
	content, err := os.ReadFile(iniPath)
	if err != nil {
		return nil
	}
	re := regexp.MustCompile(`^\s*remote_port\s*=\s*(\d+)`)
	var ports []int
	for _, line := range strings.Split(string(content), "\n") {
		matches := re.FindStringSubmatch(line)
		if len(matches) > 1 {
			port, _ := strconv.Atoi(matches[1])
			ports = append(ports, port)
		}
	}
	slog.Debug("Найдены локально используемые порты", "ports", ports)
	return ports
}

func findPathInZip(zipPath, targetSuffix string) (string, error) {
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return "", err
	}
	defer r.Close()
	targetSuffix = filepath.ToSlash(targetSuffix)
	for _, f := range r.File {
		if f.FileInfo().IsDir() {
			continue
		}
		if strings.HasSuffix(filepath.ToSlash(f.Name), targetSuffix) {
			return f.Name, nil
		}
	}
	return "", fmt.Errorf("файл '%s' не найден в архиве", targetSuffix)
}
