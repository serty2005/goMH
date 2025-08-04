package frpc

import (
	"archive/zip"
	"bufio"
	"encoding/json"
	"fmt"
	"goMH/assetmgr"
	"goMH/config"
	"goMH/winutils"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type Module struct {
	Cfg *config.FrpcConfig
	Am  *assetmgr.Manager
}

// ... структуры FrpsProxy, FrpsConf, FrpsProxyInfo без изменений ...
type FrpsProxy struct {
	Name   string    `json:"name"`
	Conf   *FrpsConf `json:"conf"`
	Status string    `json:"status"`
}
type FrpsConf struct {
	RemotePort int `json:"remote_port"`
}
type FrpsProxyInfo struct {
	Proxies []FrpsProxy `json:"proxies"`
}

func (m *Module) ID() string { return "FRPC" }
func (m *Module) MenuText() string {
	return "Установить/настроить FRPC (проброс портов)"
}

func (m *Module) Run(am *assetmgr.Manager) error { // ...
	m.Am = am
	m.Cfg = &am.Cfg().FrpcConfig
	frpcExePath := filepath.Join(m.Cfg.InstallPath, "frpc.exe")
	if _, err := os.Stat(frpcExePath); err == nil {
		return m.runDiagnosticsWorkflow()
	}
	return m.runFullInstallWorkflow(false)
}
func (m *Module) runDiagnosticsWorkflow() error { // ...
	fmt.Println("\nОбнаружена существующая установка FRPC.")
	fmt.Print("Введите 'R' для добавления порта, 'C' для полной переустановки или 'U' для удаления (R/C/U): ")
	reader := bufio.NewReader(os.Stdin)
	choice, _ := reader.ReadString('\n')
	choice = strings.TrimSpace(strings.ToUpper(choice))
	switch choice {
	case "R":
		return m.runAddPortWorkflow(true)
	case "C":
		fmt.Println("Выполняем полную переустановку...")
		return m.runFullInstallWorkflow(true)
	case "U":
		fmt.Print("ВНИМАНИЕ: Это полностью удалит FRPC. Вы уверены? (y/n): ")
		confirm, _ := reader.ReadString('\n')
		if strings.TrimSpace(strings.ToLower(confirm)) != "y" {
			fmt.Println("Удаление отменено.")
			return nil
		}
		return m.uninstall()
	default:
		fmt.Println("Отмена операции.")
		return nil
	}
}
func (m *Module) runFullInstallWorkflow(isReinstall bool) error { // ...
	if isReinstall {
		m.uninstall()
	}
	_ = os.MkdirAll(m.Cfg.InstallPath, 0755)
	winutils.AddDefenderExclusion(m.Am.Cfg().RootPath)
	if err := m.downloadAndExtractComponents(); err != nil { // Вызываем исправленную функцию
		return err
	}
	return m.runAddPortWorkflow(false)
}
func (m *Module) runAddPortWorkflow(isAddingToExisting bool) error { // ...
	reader := bufio.NewReader(os.Stdin)
	fmt.Print("Введите локальный порт для туннеля (например, 5985 для WinRM): ")
	localPortStr, _ := reader.ReadString('\n')
	localPortStr = strings.TrimSpace(localPortStr)
	if localPortStr == "" {
		localPortStr = "5985"
	}
	fmt.Print("Введите имя этого узла (например, SRV-BACKOFFICE-01): ")
	alias, _ := reader.ReadString('\n')
	alias = strings.TrimSpace(alias)
	freePort, err := m.findFreePort()
	if err != nil {
		return err
	}
	fmt.Printf("Выбран удаленный порт: %d\n", freePort)
	if err := m.updateFrpcIni(alias, localPortStr, strconv.Itoa(freePort)); err != nil {
		return err
	}
	if !isAddingToExisting {
		if err := m.setupNssmService(); err != nil {
			return err
		}
	}
	fmt.Println("Перезапускаем службу для применения изменений...")
	winutils.ManageService("stop", m.Cfg.ServiceName)
	time.Sleep(2 * time.Second)
	winutils.ManageService("start", m.Cfg.ServiceName)
	fmt.Println("\n--- Настройка FRPC завершена ---")
	return nil
}
func (m *Module) uninstall() error { // ...
	fmt.Println("Остановка и удаление службы FRPC...")
	winutils.ManageService("stop", m.Cfg.ServiceName)
	time.Sleep(2 * time.Second)
	winutils.ManageService("delete", m.Cfg.ServiceName)
	fmt.Println("Удаление директории установки...")
	os.RemoveAll(m.Cfg.InstallPath)
	fmt.Println("Очистка завершена.")
	return nil
}

func (m *Module) downloadAndExtractComponents() error {
	fmt.Println("\n--- Скачивание и распаковка компонентов ---")

	// 1. Скачиваем архив FRPC с помощью assetmgr
	frpcZipPath := filepath.Join(m.Am.Cfg().AssetsCachePath, "frpc.zip")
	if _, err := m.Am.DownloadHTTPWithProgress(m.Cfg.FrpcDownloadURL, frpcZipPath); err != nil {
		return fmt.Errorf("не удалось скачать FRPC: %w", err)
	}

	// 2. Находим путь к frpc.exe внутри архива
	frpcPathInZip, err := findPathInZip(frpcZipPath, "frpc.exe")
	if err != nil {
		return fmt.Errorf("не найден frpc.exe в архиве: %w", err)
	}

	// 3. Извлекаем frpc.exe с помощью assetmgr.ExtractFile
	frpcDestPath := filepath.Join(m.Cfg.InstallPath, "frpc.exe")
	if err := assetmgr.ExtractFile(frpcZipPath, frpcPathInZip, frpcDestPath); err != nil {
		return fmt.Errorf("не удалось извлечь frpc.exe: %w", err)
	}
	fmt.Println("frpc.exe успешно извлечен.")

	// 4. Скачиваем архив NSSM с помощью assetmgr
	nssmZipPath := filepath.Join(m.Am.Cfg().AssetsCachePath, "nssm.zip")
	if _, err := m.Am.DownloadHTTPWithProgress(m.Cfg.NssmDownloadURL, nssmZipPath); err != nil {
		return fmt.Errorf("не удалось скачать NSSM: %w", err)
	}

	// 5. Определяем архитектуру и путь к nssm.exe внутри архива
	archDir := "win32"
	if winutils.Is64BitOS() {
		archDir = "win64"
		fmt.Println("Обнаружена 64-битная система. Ищем nssm.exe в папке win64.")
	} else {
		fmt.Println("Обнаружена 32-битная система. Ищем nssm.exe в папке win32.")
	}
	nssmSubPath := filepath.Join(archDir, "nssm.exe")

	nssmPathInZip, err := findPathInZip(nssmZipPath, nssmSubPath)
	if err != nil {
		return fmt.Errorf("не найден nssm.exe для архитектуры %s: %w", archDir, err)
	}

	// 6. Извлекаем nssm.exe с помощью assetmgr.ExtractFile
	nssmDestPath := filepath.Join(m.Cfg.InstallPath, "nssm.exe")
	if err := assetmgr.ExtractFile(nssmZipPath, nssmPathInZip, nssmDestPath); err != nil {
		return fmt.Errorf("не удалось извлечь nssm.exe: %w", err)
	}
	fmt.Println("nssm.exe успешно извлечен.")

	return nil
}

func findPathInZip(zipPath, targetSuffix string) (string, error) {
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return "", err
	}
	defer r.Close()

	// Нормализуем разделители
	targetSuffix = filepath.ToSlash(targetSuffix)

	for _, f := range r.File {
		if f.FileInfo().IsDir() {
			continue
		}
		if strings.HasSuffix(filepath.ToSlash(f.Name), targetSuffix) {
			return f.Name, nil // Возвращаем полный путь файла в архиве
		}
	}
	return "", fmt.Errorf("файл, заканчивающийся на '%s', не найден в архиве '%s'", targetSuffix, zipPath)
}

func (m *Module) findFreePort() (int, error) { // ...
	fmt.Println("Получение информации о прокси с сервера FRPS...")
	apiURL := fmt.Sprintf("https://%s:%d/api/proxy/tcp", m.Cfg.ServerConfig.Host, m.Cfg.ServerConfig.APIPort)
	req, _ := http.NewRequest("GET", apiURL, nil)
	req.SetBasicAuth(m.Cfg.ServerConfig.User, m.Cfg.ServerConfig.Pass)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	var proxyInfo FrpsProxyInfo
	if err := json.Unmarshal(body, &proxyInfo); err != nil {
		return 0, fmt.Errorf("ошибка парсинга JSON ответа от FRPS: %w", err)
	}
	hasOfflineProxy := false
	for _, p := range proxyInfo.Proxies {
		if p.Status == "offline" {
			hasOfflineProxy = true
			break
		}
	}
	if hasOfflineProxy {
		fmt.Println("\nВНИМАНИЕ: Обнаружены оффлайн-прокси. Автоматический выбор порта рискован.")
		reader := bufio.NewReader(os.Stdin)
		for {
			fmt.Print("Пожалуйста, введите желаемый удаленный порт вручную: ")
			portStr, _ := reader.ReadString('\n')
			port, err := strconv.Atoi(strings.TrimSpace(portStr))
			if err != nil {
				fmt.Println("Неверный ввод. Пожалуйста, введите число.")
				continue
			}
			return port, nil
		}
	}
	fmt.Println("Все прокси онлайн. Выполняем автоматический поиск свободного порта...")
	usedPorts := make(map[int]bool)
	for _, p := range proxyInfo.Proxies {
		if p.Conf != nil {
			usedPorts[p.Conf.RemotePort] = true
		}
	}
	localUsedPorts := getLocalUsedPorts(filepath.Join(m.Cfg.InstallPath, "frpc.ini"))
	for _, p := range localUsedPorts {
		usedPorts[p] = true
	}
	parts := strings.Split(m.Cfg.PortRange, "-")
	startPort, _ := strconv.Atoi(parts[0])
	endPort, _ := strconv.Atoi(parts[1])
	for port := startPort; port <= endPort; port++ {
		if !usedPorts[port] {
			return port, nil
		}
	}
	return 0, fmt.Errorf("свободные порты в диапазоне %s не найдены", m.Cfg.PortRange)
}
func (m *Module) updateFrpcIni(alias, localPort, remotePort string) error { // ...
	iniPath := filepath.Join(m.Cfg.InstallPath, "frpc.ini")
	content, err := os.ReadFile(iniPath)
	var lines []string
	if err != nil {
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
	sectionExists := false
	for _, line := range lines {
		if strings.TrimSpace(line) == newSectionName {
			sectionExists = true
			break
		}
	}
	if !sectionExists {
		lines = append(lines, "", newSectionName, "type = tcp", "local_ip = 127.0.0.1", "local_port = "+localPort, "remote_port = "+remotePort)
		err = os.WriteFile(iniPath, []byte(strings.Join(lines, "\r\n")), 0644)
		if err != nil {
			return fmt.Errorf("не удалось записать в frpc.ini: %w", err)
		}
		fmt.Println("Новая секция добавлена в frpc.ini")
	} else {
		fmt.Printf("Секция '%s' уже существует. Пропускаем.\n", newSectionName)
	}
	return nil
}
func (m *Module) setupNssmService() error { // ...
	nssmExe := filepath.Join(m.Cfg.InstallPath, "nssm.exe")
	frpcExe := filepath.Join(m.Cfg.InstallPath, "frpc.exe")
	frpcIni := filepath.Join(m.Cfg.InstallPath, "frpc.ini")
	commands := [][]string{
		{"install", m.Cfg.ServiceName, frpcExe, "-c", frpcIni},
		{"set", m.Cfg.ServiceName, "Start", "SERVICE_AUTO_START"},
		{"set", m.Cfg.ServiceName, "AppDirectory", m.Cfg.InstallPath},
	}
	fmt.Printf("Создание и настройка службы '%s' с помощью nssm...\n", m.Cfg.ServiceName)
	for _, args := range commands {
		cmd := exec.Command(nssmExe, args...)
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("ошибка при выполнении nssm %s: %w", args[0], err)
		}
	}
	fmt.Println("Служба успешно настроена.")
	return nil
}
func getLocalUsedPorts(iniPath string) []int { // ...
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
	return ports
}
