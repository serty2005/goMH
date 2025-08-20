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
	ID             string
	Name           string
	ServiceName    string
	IsInstalled    bool
	AllowReinstall bool
	InstallFunc    func(am core.AssetManager, wu core.WinUtils) error
}

// Главная функция Run теперь управляет подменю
func (m *Module) Run(am core.AssetManager, wu core.WinUtils) error {
	// Инициализируем компоненты
	components := []*remoteComponent{
		{ID: "1", Name: "TeamViewer", ServiceName: "TeamViewer", InstallFunc: m.installTeamViewer, AllowReinstall: false},
		{ID: "2", Name: "LiteManager", ServiceName: "ROMService", InstallFunc: m.installLiteManager, AllowReinstall: false},
		{ID: "3", Name: "Getad Agent", ServiceName: "MH_Getad", InstallFunc: m.installGetad, AllowReinstall: true},
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
			menuText := fmt.Sprintf("Установить %s", c.Name)
			if c.IsInstalled {
				status = tui.ColorGreen + "[установлено]" + tui.ColorReset
				// Если разрешена переустановка, меняем текст
				if c.AllowReinstall {
					menuText = fmt.Sprintf("Переустановить %s", c.Name)
				}
			}
			fmt.Printf(" %s. %s %s\n", c.ID, menuText, status)
		}
		fmt.Println("\n 0. Назад в главное меню")
		fmt.Print("Выберите пункт: ")

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
			// Новая логика проверки
			// Блокируем только если компонент установлен И у него НЕТ флага AllowReinstall
			if chosenComponent.IsInstalled && !chosenComponent.AllowReinstall {
				tui.Warn(fmt.Sprintf("\n%s уже установлен. Для переустановки сначала удалите его стандартными средствами Windows.", chosenComponent.Name))
				fmt.Println("Нажмите Enter для продолжения...")
				_, _ = reader.ReadString('\n')
				continue
			}

			// Запускаем функцию установки
			err := chosenComponent.InstallFunc(am, wu)
			if err != nil {
				tui.Error(fmt.Sprintf("\n--- ОШИБКА при установке/переустановке %s ---\n%v\n---------------------------------------\n", chosenComponent.Name, err))
			} else {
				tui.Success(fmt.Sprintf("\n--- %s успешно установлен/переустановлен. ---", chosenComponent.Name))
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
	const serviceName = "MH_Getad"
	const installSubDir = "getad" // Папка внутри C:\MH

	installDir := filepath.Join(am.Cfg().RootPath, installSubDir)

	tui.Title("\n--- Начало установки/переустановки Getad Agent ---")

	// --- ЭТАП 1: ПОИСК И ОЧИСТКА СТАРОЙ ВЕРСИИ ---
	tui.Info("-> Этап 1: Поиск и удаление предыдущих версий...")

	// Ищем старый getad*.exe
	oldExePath, err := wu.FindFileRecursive(am.Cfg().RootPath, "getad*.exe")
	if err == nil {
		tui.InfoF("Найдена предыдущая установка: %s", oldExePath)
		oldInstallDir := filepath.Dir(oldExePath)

		// 1.1 Удаление службы
		serviceExists, _ := wu.ServiceExists(serviceName)
		if serviceExists {
			tui.Info("Остановка и удаление службы...")
			_, _ = wu.RunCommand(oldExePath, "stop")
			time.Sleep(2 * time.Second) // Даем время на остановку
			_, err := wu.RunCommand(oldExePath, "remove")
			if err != nil {
				// Пробуем альтернативный вариант, если 'remove' не сработал
				_, _ = wu.RunCommand(oldExePath, "uninstall")
			}
		}

		// 1.2 Удаление из автозагрузки
		userStartup, commonStartup, err := wu.GetStartupFolders()
		if err == nil {
			for _, startupDir := range []string{userStartup, commonStartup} {
				entries, _ := os.ReadDir(startupDir)
				for _, entry := range entries {
					if strings.HasSuffix(strings.ToLower(entry.Name()), ".lnk") {
						// TODO: Нужна библиотека для чтения .lnk, чтобы проверить путь.
						// Пока что удаляем по имени файла getad*.lnk
						if strings.HasPrefix(strings.ToLower(entry.Name()), "getad") {
							shortcutPath := filepath.Join(startupDir, entry.Name())
							tui.InfoF("Удаление ярлыка из автозагрузки: %s", shortcutPath)
							_ = wu.DeleteFile(shortcutPath)
						}
					}
				}
			}
		}

		// 1.3 Удаление из планировщика
		taskName, _ := wu.FindScheduledTaskByPath(oldExePath)
		if taskName != "" {
			tui.InfoF("Удаление задачи '%s' из планировщика...", taskName)
			_ = wu.DeleteScheduledTaskByName(taskName)
		}

		// 1.4 Очистка папки установки (если это не корневая папка C:\MH)
		if strings.EqualFold(oldInstallDir, am.Cfg().RootPath) {
			tui.Warn("Getad установлен в корневую папку, очистка не производится.")
		} else {
			tui.InfoF("Очистка директории: %s", oldInstallDir)
			_ = wu.CleanDirectory(oldInstallDir)
		}

	} else {
		tui.Info("Предыдущих версий не найдено, выполняется чистая установка.")
	}
	// Очищаем целевую папку на всякий случай
	_ = wu.CleanDirectory(installDir)
	_ = os.MkdirAll(installDir, 0755)

	// --- ЭТАП 2: УСТАНОВКА НОВОЙ ВЕРСИИ ---
	tui.Info("\n-> Этап 2: Установка новой версии...")

	// 2.1 Добавляем исключение в антивирус
	tui.InfoF("Добавление пути '%s' в исключения Защитника Windows...", installDir)
	if err := wu.AddDefenderExclusion(installDir); err != nil {
		tui.Warn(fmt.Sprintf("Не удалось добавить исключение: %v", err))
	}

	// 2.2 Скачиваем и распаковываем
	tui.Info("Скачивание архива агента...")
	cachePath, err := am.DownloadToCache(assetName)
	if err != nil {
		return fmt.Errorf("не удалось скачать архив агента: %w", err)
	}

	tui.InfoF("Распаковка архива в '%s'...", installDir)
	// Используем новую "умную" распаковку
	if err := am.UnpackToFlatDir(assetName, cachePath, installDir); err != nil {
		return fmt.Errorf("не удалось распаковать архив агента: %w", err)
	}

	// 2.3 Установка и запуск службы
	serviceExe := filepath.Join(installDir, "getad-service.exe")
	if _, err := os.Stat(serviceExe); os.IsNotExist(err) {
		return fmt.Errorf("не найден исполняемый файл службы: %s", serviceExe)
	}

	tui.Info("Установка службы...")
	if _, err := wu.RunCommand(serviceExe, "--startup", "auto", "install"); err != nil {
		return fmt.Errorf("не удалось установить службу: %w", err)
	}

	tui.Info("Запуск службы...")
	if _, err := wu.RunCommand(serviceExe, "start"); err != nil {
		return fmt.Errorf("не удалось запустить службу: %w", err)
	}

	// 2.4 Настройка триггеров
	tui.Info("Настройка триггеров службы...")
	triggers := []string{"start/machinepolicy", "start/userpolicy"}
	if err := wu.SetServiceTriggers(serviceName, triggers); err != nil {
		tui.Warn(fmt.Sprintf("Не удалось установить триггеры: %v", err))
	}

	// --- ЭТАП 3: ПРОВЕРКА ---
	tui.Info("\n-> Этап 3: Проверка статуса службы...")
	time.Sleep(3 * time.Second) // Даем службе время на запуск
	status, err := wu.GetServiceStatus(serviceName)
	if err != nil {
		return fmt.Errorf("не удалось проверить статус службы: %w", err)
	}

	if status == "RUNNING" {
		tui.SuccessF("Служба '%s' успешно установлена и запущена.", serviceName)
	} else {
		return fmt.Errorf("служба '%s' установлена, но ее статус '%s', а не 'RUNNING'", serviceName, status)
	}

	return nil
}
