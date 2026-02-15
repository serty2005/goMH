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
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

type Module struct{}

func (m *Module) ID() string { return "RemoteAccess" }
func (m *Module) MenuText() string {
	return "Установить средства удаленного доступа (TV, LM, POSRelayd)"
}

// ToolType определяет тип инструмента
type ToolType int

const (
	ToolTeamViewer ToolType = iota
	ToolLiteManager
	ToolPOSRelayd
)

// RemoteAccessConfig хранит выбор пользователя
type RemoteAccessConfig struct {
	Tool ToolType
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
	// Определяем статусы служб
	tvInstalled, _ := wu.ServiceExists("TeamViewer")
	lmInstalled, _ := wu.ServiceExists("ROMService")
	posrelaydInstalled, _ := wu.ServiceExists("MH_POSRelayd")

	tui.ClearScreen()
	tui.Title("\n--- Меню установки средств удаленного доступа ---")

	printStatus := func(installed bool) string {
		if installed {
			return tui.ColorGreen + "[установлено]" + tui.ColorReset
		}
		return tui.ColorRed + "[не установлено]" + tui.ColorReset
	}

	fmt.Printf(" 1. TeamViewer %s\n", printStatus(tvInstalled))
	fmt.Printf(" 2. LiteManager %s\n", printStatus(lmInstalled))
	fmt.Printf(" 3. POSRelayd Agent %s (возможна переустановка)\n", printStatus(posrelaydInstalled))
	fmt.Println("\n 0. Назад в главное меню")
	fmt.Print("Выберите пункт: ")

	key, err := tui.ReadKey()
	if err != nil {
		return nil, err
	}

	switch key {
	case "1":
		if tvInstalled {
			tui.Warn("TeamViewer уже установлен.")
			tui.WaitForAnyKey()
			return m.Configure(ctx, am, wu) // Рекурсия для перерисовки меню (можно просто continue в цикле Run, но это не красиво)
		}
		return &RemoteAccessConfig{Tool: ToolTeamViewer}, nil
	case "2":
		if lmInstalled {
			tui.Warn("LiteManager уже установлен.")
			tui.WaitForAnyKey()
			return m.Configure(ctx, am, wu)
		}
		return &RemoteAccessConfig{Tool: ToolLiteManager}, nil
	case "3":
		// POSRelayd можно переустанавливать
		return &RemoteAccessConfig{Tool: ToolPOSRelayd}, nil
	case "0":
		return nil, nil
	default:
		return m.Configure(ctx, am, wu) // Повтор при неверном вводе
	}
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
	case ToolPOSRelayd:
		ctx.SetStatus("Установка POSRelayd Agent")
		return m.installPOSRelayd(ctx, am, wu)
	}
	return nil
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
