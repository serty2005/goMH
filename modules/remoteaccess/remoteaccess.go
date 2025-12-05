package remoteaccess

import (
	"bytes"
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
	return "Установить средства удаленного доступа (TV, LM, Getad)"
}

// ToolType определяет тип инструмента
type ToolType int

const (
	ToolTeamViewer ToolType = iota
	ToolLiteManager
	ToolGetad
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
	getadInstalled, _ := wu.ServiceExists("MH_Getad")

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
	fmt.Printf(" 3. Getad Agent %s (возможна переустановка)\n", printStatus(getadInstalled))
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
		// Getad можно переустанавливать
		return &RemoteAccessConfig{Tool: ToolGetad}, nil
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
	case ToolGetad:
		ctx.SetStatus("Установка Getad Agent")
		return m.installGetad(ctx, am, wu)
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

func (m *Module) installGetad(ctx core.TaskContext, am core.AssetManager, wu core.WinUtils) error {
	slog.Info("Начало установки Getad")
	const assetName = "Getad_Agent"
	const serviceName = "MH_Getad"
	installDir := filepath.Join(am.Cfg().RootPath, "getad")

	// 1. Очистка старого
	ctx.Info("Поиск и удаление старых версий...")
	oldExePath, err := wu.FindFileRecursive(am.Cfg().RootPath, "getad*.exe")
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
			if matches, _ := filepath.Glob(filepath.Join(dir, "getad*.lnk")); len(matches) > 0 {
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

	serviceExe := filepath.Join(installDir, "getadsc.exe")
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
		ctx.Success("Служба Getad успешно запущена.")
	} else {
		ctx.Warn(fmt.Sprintf("Служба установлена, но статус: %s", status))
	}

	return nil
}
