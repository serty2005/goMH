package remoteaccess

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"goMH/core"
	"goMH/tui"
	"io"
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

// Структура для хранения информации о компоненте
type remoteComponent struct {
	ID          string
	Name        string
	ServiceName string
	IsInstalled bool
	InstallFunc func(am core.AssetManager, wu core.WinUtils) error
}

// Главная функция Run теперь управляет подменю
func (m *Module) Run(am core.AssetManager, wu core.WinUtils) error {
	// Инициализируем компоненты
	components := []*remoteComponent{
		{ID: "1", Name: "TeamViewer", ServiceName: "TeamViewer", InstallFunc: m.installTeamViewer},
		{ID: "2", Name: "LiteManager", ServiceName: "ROMService", InstallFunc: m.installLiteManager},
		{ID: "3", Name: "Getad Agent", ServiceName: "MH_Getad", InstallFunc: m.installGetad},
	}

	reader := bufio.NewReader(os.Stdin)

	// Основной цикл подменю
	for {
		tui.Title("\n--- Меню установки средств удаленного доступа ---")
		// Перед показом меню обновляем статусы
		m.checkStatuses(wu, components)

		// Отображаем меню
		for _, c := range components {
			status := tui.ColorRed + "[не установлено]" + tui.ColorReset
			if c.IsInstalled {
				status = tui.ColorGreen + "[установлено]" + tui.ColorReset
			}
			fmt.Printf(" %s. Установить %s %s\n", c.ID, c.Name, status)
		}
		fmt.Println("\n 0. Назад в главное меню")
		fmt.Print("Выберите пункт для установки или возврата: ")

		// Читаем выбор пользователя
		choiceStr, _ := reader.ReadString('\n')
		choiceStr = strings.TrimSpace(choiceStr)

		if choiceStr == "0" {
			return nil // Выход из подменю
		}

		// Находим выбранный компонент
		var chosenComponent *remoteComponent
		for _, c := range components {
			if c.ID == choiceStr {
				chosenComponent = c
				break
			}
		}

		// Если выбор корректен, запускаем установку
		if chosenComponent != nil {
			if chosenComponent.IsInstalled {
				tui.Warn(fmt.Sprintf("\n%s уже установлен. Для переустановки сначала удалите его стандартными средствами Windows.", chosenComponent.Name))
				fmt.Println("Нажмите Enter для продолжения...")
				_, _ = reader.ReadString('\n')
				continue
			}

			// Запускаем функцию установки
			err := chosenComponent.InstallFunc(am, wu)
			if err != nil {
				tui.Error(fmt.Sprintf("\n--- ОШИБКА при установке %s ---\n%v\n---------------------------------------\n", chosenComponent.Name, err))
			} else {
				tui.Success(fmt.Sprintf("\n--- %s успешно установлен. ---", chosenComponent.Name))
			}
			fmt.Println("\nНажмите Enter, чтобы вернуться в меню...")
			reader.ReadString('\n')

		} else {
			tui.Error("\nНеверный выбор. Попробуйте снова.")
			time.Sleep(2 * time.Second)
		}
	}
}

// checkStatuses обновляет поле IsInstalled для каждого компонента
func (m *Module) checkStatuses(wu core.WinUtils, components []*remoteComponent) {
	for _, c := range components {
		installed, err := wu.ServiceExists(c.ServiceName)
		if err != nil {
			// Если проверка не удалась, считаем что не установлено, но выводим ошибку
			tui.Warn(fmt.Sprintf("Не удалось проверить статус службы %s: %v", c.ServiceName, err))
			c.IsInstalled = false
		} else {
			c.IsInstalled = installed
		}
	}
}

// --- Функции установки остаются такими же, как и были ---

// --- Установка TeamViewer ---
func (m *Module) installTeamViewer(am core.AssetManager, wu core.WinUtils) error {
	tui.Info("\n-> Начало установки TeamViewer...")
	cfg := am.Cfg().TeamViewerConfig

	// --- Шаг 1: Получение configId ---
	tui.InfoF("Запрос страницы: %s", cfg.ShortURL)
	client := &http.Client{}
	req, err := http.NewRequest("GET", cfg.ShortURL, nil)
	if err != nil {
		return fmt.Errorf("не удалось создать HTTP-запрос: %w", err)
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/91.0.4472.124 Safari/537.36")
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("не удалось выполнить HTTP-запрос: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("не удалось прочитать тело ответа: %w", err)
	}

	re := regexp.MustCompile(`var configId\s*=\s*"([^"]+)"`)
	matches := re.FindStringSubmatch(string(body))
	if len(matches) < 2 {
		return fmt.Errorf("не удалось найти configId на странице")
	}
	configID := matches[1]
	tui.InfoF("Найден configId: %s", configID)

	// --- Шаг 2: Запрос прямой ссылки от API ---
	type ApiRequestBody struct {
		ConfigID       string `json:"ConfigId"`
		Version        string `json:"Version"`
		IsCustomModule bool   `json:"IsCustomModule"`
		Subdomain      string `json:"Subdomain"`
		ConnectionID   string `json:"ConnectionId"`
	}
	reqBody := ApiRequestBody{
		ConfigID:       configID,
		Version:        "15",
		IsCustomModule: true,
		Subdomain:      "1",
		ConnectionID:   "",
	}
	jsonBody, _ := json.Marshal(reqBody)

	tui.InfoF("Запрос прямой ссылки от API: %s", cfg.ApiURL)
	apiReq, err := http.NewRequest("POST", cfg.ApiURL, bytes.NewBuffer(jsonBody))
	if err != nil {
		return fmt.Errorf("не удалось создать API-запрос: %w", err)
	}
	apiReq.Header.Set("Content-Type", "application/json;charset=UTF-8")
	apiReq.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/91.0.4472.124 Safari/537.36")
	apiReq.Header.Set("Accept", "application/json, text/plain, */*")

	apiResp, err := client.Do(apiReq)
	if err != nil {
		return fmt.Errorf("ошибка при выполнении API-запроса: %w", err)
	}
	defer apiResp.Body.Close()

	if apiResp.StatusCode != http.StatusOK {
		errorBody, _ := io.ReadAll(apiResp.Body)
		return fmt.Errorf("API вернуло ошибку: %s. Тело ответа: %s", apiResp.Status, string(errorBody))
	}

	directURLBody, err := io.ReadAll(apiResp.Body)
	if err != nil {
		return fmt.Errorf("не удалось прочитать ответ от API: %w", err)
	}
	directURL := strings.Trim(string(directURLBody), `"`)
	tui.InfoF("Получена прямая ссылка для скачивания")

	// --- Шаг 3: Скачивание файла с помощью assetmgr ---
	installerName := "TeamViewer_Setup.exe"
	installerPath := filepath.Join(am.Cfg().AssetsCachePath, installerName)

	if _, err := am.DownloadHTTPWithProgress(directURL, installerPath); err != nil {
		return fmt.Errorf("не удалось скачать установщик: %w", err)
	}

	// --- Шаг 4: Запуск установщика ---
	tui.Info("Запуск установщика TeamViewer в тихом режиме...")
	_, err = wu.RunCommand(installerPath, "/S")
	return err
}

// --- Установка LiteManager ---
func (m *Module) installLiteManager(am core.AssetManager, wu core.WinUtils) error {
	tui.Info("\n-> Начало установки LiteManager...")
	msiPath, err := am.DownloadToCache("LiteManager_Installer")
	if err != nil {
		return fmt.Errorf("не удалось скачать установщик LiteManager: %w", err)
	}

	tui.Info("Запуск установки LiteManager в тихом режиме...")
	_, err = wu.RunCommand("msiexec.exe", "/i", msiPath, "/quiet", "/norestart")
	return err
}

// --- Установка Getad ---
func (m *Module) installGetad(am core.AssetManager, wu core.WinUtils) error {
	const assetName = "Getad_Agent"
	tui.Info("\n-> Начало установки Getad Agent...")

	// Определяем путь установки ЗАРАНЕЕ из конфигурации
	assetInfo := am.Cfg().AssetCatalog[assetName]
	installDir := filepath.Join(am.Cfg().RootPath, assetInfo.Destination)

	tui.InfoF("Подготовка директории: %s", installDir)
	if err := os.MkdirAll(installDir, 0755); err != nil {
		return fmt.Errorf("не удалось создать директорию установки %s: %w", installDir, err)
	}

	// --- ШАГ 1: Добавляем исключение в антивирус ---
	tui.InfoF("Добавление пути '%s' в исключения Защитника Windows...", installDir)
	if err := wu.AddDefenderExclusion(installDir); err != nil {
		// Это не критичная ошибка, просто выводим предупреждение
		tui.Warn(fmt.Sprintf("Не удалось добавить исключение: %v", err))
	} else {
		tui.Success("Путь успешно добавлен в исключения.")
	}

	// --- ШАГ 2: Получаем архив в кэш ---
	tui.Info("Скачивание архива агента...")
	cachePath, err := am.DownloadToCache(assetName)
	if err != nil {
		return fmt.Errorf("не удалось скачать архив агента: %w", err)
	}
	tui.Success("Архив успешно скачан.")

	// --- ШАГ 3: Разархивируем архив в путь установки ---
	tui.InfoF("Распаковка архива в '%s'...", installDir)
	if err := am.ProcessFromCache(assetName, cachePath); err != nil {
		return fmt.Errorf("не удалось распаковать архив агента: %w", err)
	}
	tui.Success("Агент успешно распакован.")

	// --- ШАГ 4: Запуск с аргументами ---
	serviceExe := filepath.Join(installDir, "getad-service.exe")

	if _, err := os.Stat(serviceExe); os.IsNotExist(err) {
		return fmt.Errorf("не найден исполняемый файл службы: %s", serviceExe)
	}

	// Остановка существующей службы (на случай переустановки)
	tui.Info("Остановка существующей службы (если есть)...")
	_, _ = wu.RunCommand(serviceExe, "stop")
	time.Sleep(1 * time.Second)

	tui.Info("Установка службы...")
	if _, err := wu.RunCommand(serviceExe, "--startup", "auto", "install"); err != nil {
		return fmt.Errorf("не удалось установить службу: %w", err)
	}

	tui.Info("Запуск службы...")
	if _, err := wu.RunCommand(serviceExe, "start"); err != nil {
		return fmt.Errorf("не удалось запустить службу: %w", err)
	}

	tui.Info("Настройка триггеров службы...")
	triggers := []string{"start/machinepolicy", "start/userpolicy"}
	if err := wu.SetServiceTriggers("MH_Getad", triggers); err != nil {
		tui.Warn(fmt.Sprintf("Не удалось установить триггеры: %v", err))
	}

	return nil
}
