package remoteaccess

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"goMH/core"
	"goMH/tui"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

type Module struct {
	selectItem             func(items []tui.ChoiceItem, config tui.SelectionConfig) (int, error)
	promptRustDeskPassword func() (bool, string, error)
}

func (m *Module) ID() string { return "RemoteAccess" }
func (m *Module) MenuText() string {
	return "Установить средства удаленного доступа (TV, LM, RustDesk, POSRelayd)"
}

func (m *Module) ConfigureTask(ctx core.TaskContext, services core.ModuleServices) (any, error) {
	return m.Configure(ctx, services.AssetManager, services.WinUtils)
}

func (m *Module) BuildTask(config any) (core.ModuleTaskPlan, error) {
	cfg, err := m.taskConfig(config)
	if err != nil {
		return core.ModuleTaskPlan{}, err
	}

	title := "Удаленный доступ"
	signature := "remoteaccess|generic"
	switch cfg.Tool {
	case ToolTeamViewer:
		title = "Установка TeamViewer"
		signature = "remoteaccess|teamviewer"
	case ToolLiteManager:
		title = "Установка LiteManager"
		signature = "remoteaccess|litemanager"
	case ToolRustDesk:
		title = "Установка RustDesk"
		signature = "remoteaccess|rustdesk"
	case ToolPOSRelayd:
		title = "Установка POSRelayd Agent"
		signature = "remoteaccess|posrelayd"
	}

	return core.ModuleTaskPlan{
		Mode: core.ModuleRunModeQueue,
		Task: core.ModuleTaskSpec{
			Title:     title,
			Signature: signature,
			Exclusive: isExclusiveRemoteAccess(cfg),
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

// ToolType определяет тип инструмента
type ToolType int

const (
	ToolTeamViewer ToolType = iota
	ToolLiteManager
	ToolRustDesk
	ToolPOSRelayd
)

const (
	rustDeskLatestReleaseURL = "https://github.com/rustdesk/rustdesk/releases/latest"
	rustDeskInstallArg       = "--silent-install"
	rustDeskExePath          = `C:\Program Files\RustDesk\rustdesk.exe`
)

// RemoteAccessConfig хранит выбор пользователя
type RemoteAccessConfig struct {
	Tool        ToolType
	SetPassword bool
	Password    string
}

func (cfg *RemoteAccessConfig) TaskConfirmation() core.TaskConfirmation {
	if cfg == nil {
		return core.TaskConfirmation{}
	}

	details := []string{
		"Инструмент: " + cfg.toolLabel(),
	}
	if cfg.Tool == ToolRustDesk {
		if cfg.SetPassword {
			details = append(details, "Пароль RustDesk: будет установлен")
		} else {
			details = append(details, "Пароль RustDesk: не задавать")
		}
	}

	return core.TaskConfirmation{
		Details:      details,
		ConfirmLabel: "Добавить в очередь",
	}
}

// Run - точка входа (UI)
func (m *Module) Run(am core.AssetManager, wu core.WinUtils) error {
	slog.Info("Запуск модуля RemoteAccess")
	ctx := tui.NewConsoleContext()

	// 1. Конфигурация (меню с обновляемыми статусами)
	// В консольной версии мы хотим возвращаться в меню после установки,
	// поэтому цикл for оставим здесь, в Run.
	// В GUI это будет просто вызов диалога один раз.
	for {
		cfg, err := m.Configure(ctx, am, wu)
		if err != nil {
			slog.Error("Ошибка конфигурации RemoteAccess", "error", err)
			return err
		}
		if cfg == nil {
			return nil // Выход
		}

		// 2. Выполнение
		if err := m.Execute(ctx, am, wu, cfg); err != nil {
			slog.Error("Ошибка выполнения задачи RemoteAccess", "error", err)
			// Ошибку уже показали через ctx.Error внутри (если нужно), или вернем её наверх
			// Но в цикле лучше обработать и продолжить
			ctx.Error(fmt.Sprintf("Ошибка: %v", err))
		}

		tui.WaitForAnyKey()
	}
}

// Configure - показывает меню со статусами и возвращает выбор
func (m *Module) Configure(ctx core.TaskContext, am core.AssetManager, wu core.WinUtils) (*RemoteAccessConfig, error) {
	tvInstalled, _ := wu.ServiceExists("TeamViewer")
	lmInstalled, _ := wu.ServiceExists("ROMService")
	rustDeskInstalled := isRustDeskInstalled()
	posrelaydInstalled, _ := wu.ServiceExists("MH_POSRelayd")

	items := []tui.ChoiceItem{
		{
			Title:       "TeamViewer",
			Description: stateText(tvInstalled, "уже установлен", "будет установлен"),
			Meta:        statusBadge(tvInstalled),
			Disabled:    tvInstalled,
		},
		{
			Title:       "LiteManager",
			Description: stateText(lmInstalled, "уже установлен", "будет установлен"),
			Meta:        statusBadge(lmInstalled),
			Disabled:    lmInstalled,
		},
		{
			Title:       "RustDesk",
			Description: stateText(rustDeskInstalled, "уже установлен", "будет установлен"),
			Meta:        statusBadge(rustDeskInstalled),
			Disabled:    rustDeskInstalled,
		},
		{
			Title:       "POSRelayd Agent",
			Description: stateText(posrelaydInstalled, "доступна переустановка", "будет установлен"),
			Meta:        statusBadge(posrelaydInstalled),
		},
	}

	choice, err := m.selectRemoteAccessItem(items, tui.SelectionConfig{
		Title:    "Средства удаленного доступа",
		Subtitle: "Недоступные варианты уже установлены",
	})
	if err != nil {
		if err == tui.ErrExitToMainMenu {
			return nil, nil
		}
		return nil, err
	}

	return m.configFromChoice(choice)
}

// Execute - выполнение установки
func (m *Module) Execute(ctx core.TaskContext, am core.AssetManager, wu core.WinUtils, cfg *RemoteAccessConfig) error {
	switch cfg.Tool {
	case ToolTeamViewer:
		ctx.SetStatus("Установка TeamViewer")
		return m.installTeamViewer(ctx, am, wu)
	case ToolLiteManager:
		ctx.SetStatus("Установка LiteManager")
		return m.installLiteManager(ctx, am, wu)
	case ToolRustDesk:
		ctx.SetStatus("Установка RustDesk")
		return m.installRustDesk(ctx, am, wu, cfg)
	case ToolPOSRelayd:
		ctx.SetStatus("Установка POSRelayd Agent")
		return m.installPOSRelayd(ctx, am, wu)
	}
	return nil
}

func (m *Module) taskConfig(config any) (*RemoteAccessConfig, error) {
	cfg, ok := config.(*RemoteAccessConfig)
	if !ok || cfg == nil {
		return nil, fmt.Errorf("неверный конфиг задачи для модуля %s", m.ID())
	}
	return cfg, nil
}

func isExclusiveRemoteAccess(cfg *RemoteAccessConfig) bool {
	switch cfg.Tool {
	case ToolTeamViewer, ToolLiteManager, ToolRustDesk:
		return true
	default:
		return false
	}
}

// --- Логика установки ---

func (m *Module) installTeamViewer(ctx core.TaskContext, am core.AssetManager, wu core.WinUtils) error {
	slog.Info("Начало установки TeamViewer")
	cfg := am.Cfg().TeamViewerConfig

	// 1. Получение configId
	ctx.Info(fmt.Sprintf("Запрос страницы: %s", cfg.ShortURL))
	client := &http.Client{}
	req, err := http.NewRequest("GET", cfg.ShortURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64)")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	re := regexp.MustCompile(`var configId\s*=\s*"([^"]+)"`)
	matches := re.FindStringSubmatch(string(body))
	if len(matches) < 2 {
		return fmt.Errorf("не удалось найти configId")
	}
	configID := matches[1]
	slog.Debug("Найден configId", "id", configID)

	// 2. API запрос
	type ApiRequestBody struct {
		ConfigID       string `json:"ConfigId"`
		Version        string `json:"Version"`
		IsCustomModule bool   `json:"IsCustomModule"`
		Subdomain      string `json:"Subdomain"`
		ConnectionID   string `json:"ConnectionId"`
	}
	jsonBody, _ := json.Marshal(ApiRequestBody{
		ConfigID:       configID,
		Version:        "15",
		IsCustomModule: true,
		Subdomain:      "1",
	})

	ctx.Info("Запрос прямой ссылки через API...")
	apiReq, _ := http.NewRequest("POST", cfg.ApiURL, bytes.NewBuffer(jsonBody))
	apiReq.Header.Set("Content-Type", "application/json;charset=UTF-8")
	apiReq.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64)")

	apiResp, err := client.Do(apiReq)
	if err != nil {
		return err
	}
	defer apiResp.Body.Close()

	if apiResp.StatusCode != http.StatusOK {
		return fmt.Errorf("API error: %s", apiResp.Status)
	}

	directURLBody, _ := io.ReadAll(apiResp.Body)
	directURL := strings.Trim(string(directURLBody), `"`)
	slog.Debug("Получена ссылка", "url", directURL)

	// 3. Скачивание
	installerPath := filepath.Join(am.Cfg().AssetsCachePath, "TeamViewer_Setup.exe")
	ctx.Info("Скачивание дистрибутива...")
	if _, err := am.DownloadHTTPWithProgress(directURL, installerPath); err != nil {
		return err
	}

	// 4. Установка
	ctx.Info("Запуск установки...")
	_, err = wu.RunCommand(installerPath, "/S")
	if err == nil {
		ctx.Success("TeamViewer успешно установлен.")
	}
	return err
}

func (m *Module) installLiteManager(ctx core.TaskContext, am core.AssetManager, wu core.WinUtils) error {
	slog.Info("Начало установки LiteManager")
	ctx.Info("Скачивание MSI пакета...")
	msiPath, err := am.DownloadToCache("LiteManager_Installer")
	if err != nil {
		return err
	}

	ctx.Info("Запуск msiexec...")
	_, err = wu.RunCommand("msiexec.exe", "/i", msiPath, "/quiet", "/norestart")
	if err == nil {
		ctx.Success("LiteManager успешно установлен.")
	}
	return err
}

func (m *Module) installRustDesk(ctx core.TaskContext, am core.AssetManager, wu core.WinUtils, cfg *RemoteAccessConfig) error {
	slog.Info("Начало установки RustDesk")
	password := strings.TrimSpace(cfg.Password)
	if cfg.SetPassword && password == "" {
		return fmt.Errorf("не задан пароль RustDesk")
	}

	ctx.Info("Получение актуального релиза RustDesk...")
	installerURL, err := resolveRustDeskInstallerURL()
	if err != nil {
		return err
	}

	installerPath := filepath.Join(am.Cfg().AssetsCachePath, "rustdesk-latest.exe")
	ctx.Info("Скачивание дистрибутива RustDesk...")
	if _, err := am.DownloadHTTPWithProgress(installerURL, installerPath); err != nil {
		return err
	}

	ctx.Info("Тихая установка RustDesk...")
	if err := startRustDeskInstaller(installerPath, wu); err != nil {
		return err
	}
	if err := waitForRustDeskExecutable(2 * time.Minute); err != nil {
		return err
	}

	if cfg.SetPassword {
		if err := waitForRustDeskExecutable(30 * time.Second); err != nil {
			return err
		}
		ctx.Info("Установка пароля RustDesk...")
		if _, err := wu.RunCommand(rustDeskExePath, "--password", password); err != nil {
			return fmt.Errorf("не удалось установить пароль RustDesk: %w", err)
		}
	}

	ctx.Success("RustDesk успешно установлен.")
	return nil
}

func startRustDeskInstaller(installerPath string, wu core.WinUtils) error {
	psScript := fmt.Sprintf(
		"Start-Process -FilePath '%s' -ArgumentList '%s' -WindowStyle Hidden",
		strings.ReplaceAll(installerPath, "'", "''"),
		rustDeskInstallArg,
	)
	if _, err := wu.RunCommand(
		"powershell",
		"-NoProfile",
		"-NonInteractive",
		"-ExecutionPolicy", "Bypass",
		"-Command", psScript,
	); err != nil {
		return fmt.Errorf("не удалось запустить установщик RustDesk: %w", err)
	}
	return nil
}

func (m *Module) installPOSRelayd(ctx core.TaskContext, am core.AssetManager, wu core.WinUtils) error {
	slog.Info("Начало установки POSRelayd")
	const assetName = "POSRelayd_Agent"
	const serviceName = "MH_POSRelayd"
	installDir := filepath.Join(am.Cfg().RootPath, "POSRelayd")

	// cleanup legacy getad artifacts before install
	m.cleanupLegacyGetad(ctx, am, wu)

	// 1. Очистка старого
	ctx.Info("Поиск и удаление старых версий...")
	oldExePath, err := wu.FindFileRecursive(am.Cfg().RootPath, "posrelayd*.exe")
	if err == nil {
		slog.Info("Найдена старая версия", "path", oldExePath)
		if exists, _ := wu.ServiceExists(serviceName); exists {
			ctx.Info("Остановка службы...")
			_, _ = wu.RunCommand(oldExePath, "stop")
			time.Sleep(2 * time.Second)
			_, _ = wu.RunCommand(oldExePath, "remove")
		}

		// Очистка автозагрузки
		userStartup, commonStartup, _ := wu.GetStartupFolders()
		for _, dir := range []string{userStartup, commonStartup} {
			if matches, _ := filepath.Glob(filepath.Join(dir, "posrelayd*.lnk")); len(matches) > 0 {
				for _, match := range matches {
					_ = os.Remove(match)
				}
			}
		}

		// Планировщик
		taskName, _ := wu.FindScheduledTaskByPath(oldExePath)
		if taskName != "" {
			_ = wu.DeleteScheduledTaskByName(taskName)
		}

		// Очистка папки
		oldDir := filepath.Dir(oldExePath)
		if !strings.EqualFold(oldDir, am.Cfg().RootPath) {
			_ = wu.CleanDirectory(oldDir)
		}
	}

	_ = wu.CleanDirectory(installDir)
	_ = os.MkdirAll(installDir, 0755)

	// 2. Установка нового
	ctx.Info("Добавление исключения в Defender...")
	_ = wu.AddDefenderExclusion(installDir)

	ctx.Info("Скачивание и распаковка...")
	cachePath, err := am.DownloadToCache(assetName)
	if err != nil {
		return err
	}
	if err := am.UnpackToFlatDir(assetName, cachePath, installDir); err != nil {
		return err
	}

	serviceExe := filepath.Join(installDir, "posrelaydsc.exe")
	if _, err := os.Stat(serviceExe); os.IsNotExist(err) {
		return fmt.Errorf("файл %s не найден", serviceExe)
	}

	ctx.Info("Регистрация службы...")
	if _, err := wu.RunCommand(serviceExe, "--startup", "auto", "install"); err != nil {
		return err
	}
	if _, err := wu.RunCommand(serviceExe, "start"); err != nil {
		return err
	}

	ctx.Info("Настройка триггеров...")
	_ = wu.SetServiceTriggers(serviceName, []string{"start/machinepolicy", "start/userpolicy"})

	time.Sleep(3 * time.Second)
	status, _ := wu.GetServiceStatus(serviceName)
	if status == "RUNNING" {
		ctx.Success("Служба POSRelayd успешно запущена.")
	} else {
		ctx.Warn(fmt.Sprintf("Служба установлена, но статус: %s", status))
	}

	return nil
}

func isRustDeskInstalled() bool {
	_, err := os.Stat(rustDeskExePath)
	return err == nil
}

func waitForRustDeskExecutable(timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(rustDeskExePath); err == nil {
			return nil
		}
		time.Sleep(2 * time.Second)
	}
	return fmt.Errorf("rustdesk.exe не появился после установки: %s", rustDeskExePath)
}

func askRustDeskPassword() (bool, string, error) {
	confirmed, err := tui.Confirm("Пароль RustDesk", "Добавить пароль после установки?", "Да, задать пароль")
	if err != nil {
		return false, "", err
	}
	if !confirmed {
		return false, "", nil
	}

	for {
		password, err := tui.PromptText(tui.InputConfig{
			Title:       "Пароль RustDesk",
			Subtitle:    "Пароль будет применен после тихой установки",
			Placeholder: "Введите пароль",
			Password:    true,
			Validate: func(value string) error {
				if strings.TrimSpace(value) == "" {
					return fmt.Errorf("пароль не может быть пустым")
				}
				return nil
			},
		})
		if err != nil {
			return false, "", err
		}
		if strings.TrimSpace(password) == "" {
			return false, "", nil
		}
		return true, strings.TrimSpace(password), nil
	}
}

func (m *Module) selectRemoteAccessItem(items []tui.ChoiceItem, config tui.SelectionConfig) (int, error) {
	if m.selectItem != nil {
		return m.selectItem(items, config)
	}
	return tui.SelectItem(items, config)
}

func (m *Module) promptForRustDeskPassword() (bool, string, error) {
	if m.promptRustDeskPassword != nil {
		return m.promptRustDeskPassword()
	}
	return askRustDeskPassword()
}

func (m *Module) configFromChoice(choice int) (*RemoteAccessConfig, error) {
	switch choice {
	case 0:
		return &RemoteAccessConfig{Tool: ToolTeamViewer}, nil
	case 1:
		return &RemoteAccessConfig{Tool: ToolLiteManager}, nil
	case 2:
		return m.configForRustDesk()
	case 3:
		return &RemoteAccessConfig{Tool: ToolPOSRelayd}, nil
	default:
		return nil, nil
	}
}

func (m *Module) configForRustDesk() (*RemoteAccessConfig, error) {
	setPassword, password, err := m.promptForRustDeskPassword()
	if err != nil {
		if err == tui.ErrExitToMainMenu {
			return nil, nil
		}
		return nil, err
	}
	return &RemoteAccessConfig{
		Tool:        ToolRustDesk,
		SetPassword: setPassword,
		Password:    strings.TrimSpace(password),
	}, nil
}

func (cfg *RemoteAccessConfig) toolLabel() string {
	switch cfg.Tool {
	case ToolTeamViewer:
		return "TeamViewer"
	case ToolLiteManager:
		return "LiteManager"
	case ToolRustDesk:
		return "RustDesk"
	case ToolPOSRelayd:
		return "POSRelayd Agent"
	default:
		return "Неизвестно"
	}
}

func statusBadge(installed bool) string {
	if installed {
		return "установлено"
	}
	return "доступно"
}

func stateText(installed bool, present string, absent string) string {
	if installed {
		return present
	}
	return absent
}

func resolveRustDeskInstallerURL() (string, error) {
	client := &http.Client{Timeout: 30 * time.Second}

	req, err := http.NewRequest(http.MethodGet, rustDeskLatestReleaseURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "goMH/1.0")

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("ошибка получения страницы релиза RustDesk: %s", resp.Status)
	}

	finalURL := resp.Request.URL.String()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	re := regexp.MustCompile(`(?i)href=["']([^"']*rustdesk/rustdesk/releases/download/[^"']*rustdesk-[^"']*-x86_64\.exe)["']`)
	matches := re.FindStringSubmatch(string(body))
	if len(matches) < 2 {
		return "", fmt.Errorf("не удалось определить ссылку на rustdesk x86_64 installer с %s", finalURL)
	}

	baseURL, err := url.Parse(finalURL)
	if err != nil {
		return "", err
	}
	hrefURL, err := url.Parse(matches[1])
	if err != nil {
		return "", err
	}
	return baseURL.ResolveReference(hrefURL).String(), nil
}

func (m *Module) cleanupLegacyGetad(ctx core.TaskContext, am core.AssetManager, wu core.WinUtils) {
	rootPath := am.Cfg().RootPath

	// remove old getad folders
	for _, dir := range []string{
		filepath.Join(rootPath, "getad"),
		filepath.Join(rootPath, "Getad"),
	} {
		if _, err := os.Stat(dir); err == nil {
			_ = wu.CleanDirectory(dir)
			_ = os.RemoveAll(dir)
		}
	}

	// remove old getad shortcuts from startup and desktop
	var linkDirs []string
	userStartup, commonStartup, err := wu.GetStartupFolders()
	if err == nil {
		linkDirs = append(linkDirs, userStartup, commonStartup)
	}
	if desktopDir, err := wu.GetDesktopDir(); err == nil {
		linkDirs = append(linkDirs, desktopDir)
	}
	for _, dir := range linkDirs {
		for _, pattern := range []string{"getad*.lnk", "Getad*.lnk"} {
			if matches, _ := filepath.Glob(filepath.Join(dir, pattern)); len(matches) > 0 {
				for _, match := range matches {
					_ = os.Remove(match)
				}
			}
		}
	}

	// remove scheduler tasks related to getad
	taskNames := map[string]struct{}{}
	exeCandidates := []string{
		filepath.Join(rootPath, "getad", "getad.exe"),
		filepath.Join(rootPath, "getad", "getadsc.exe"),
		filepath.Join(rootPath, "Getad", "getad.exe"),
		filepath.Join(rootPath, "Getad", "getadsc.exe"),
	}
	if oldExePath, err := wu.FindFileRecursive(rootPath, "getad*.exe"); err == nil {
		exeCandidates = append(exeCandidates, oldExePath)
	}
	for _, exePath := range exeCandidates {
		if taskName, _ := wu.FindScheduledTaskByPath(exePath); taskName != "" {
			taskNames[taskName] = struct{}{}
		}
	}
	for _, taskName := range m.findScheduledTasksByKeyword(wu, "getad") {
		taskNames[taskName] = struct{}{}
	}
	for taskName := range taskNames {
		_ = wu.DeleteScheduledTaskByName(taskName)
	}

	ctx.Info("Legacy getad cleanup complete.")
}

func (m *Module) findScheduledTasksByKeyword(wu core.WinUtils, keyword string) []string {
	out, err := wu.RunCommand("schtasks", "/Query", "/V", "/FO", "CSV")
	if err != nil {
		return nil
	}

	reader := csv.NewReader(strings.NewReader(out))
	records, err := reader.ReadAll()
	if err != nil {
		return nil
	}

	needle := strings.ToLower(keyword)
	taskNames := map[string]struct{}{}
	for _, record := range records {
		if len(record) <= 8 {
			continue
		}
		taskName := record[0]
		taskToRun := record[8]
		if strings.Contains(strings.ToLower(taskName), needle) || strings.Contains(strings.ToLower(taskToRun), needle) {
			taskNames[taskName] = struct{}{}
		}
	}

	result := make([]string, 0, len(taskNames))
	for taskName := range taskNames {
		result = append(result, taskName)
	}
	return result
}
