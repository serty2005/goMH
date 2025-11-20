package distro

import (
	"encoding/json"
	"errors"
	"fmt"
	"goMH/config"
	"goMH/tui"
	"io"
	"log/slog" // Импорт
	"net/http"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

type syrveHandler struct {
	dm *DistroManager
}

type syrveVersionInfo struct {
	FullVersion string `json:"full_version"`
}

func (h *syrveHandler) Run() error {
	// Теперь Run сам инициирует выбор компонента, так как StartInstallFlow ожидает уже выбранный
	component, err := h.selectComponentMenu()
	if err != nil {
		if err == tui.ErrExitToMainMenu {
			return nil
		}
		return err
	}

	// Запускаем поток установки для выбранного компонента
	return h.dm.StartInstallFlow(h, component)
}

func (h *syrveHandler) selectComponentMenu() (config.DistroComponent, error) {
	slog.Debug("Формирование меню компонентов Syrve")
	components := h.dm.AM.Cfg().DistroConfig.Syrve.Components

	// Создаем список для отображения с дополнительной информацией
	type ComponentDisplay struct {
		Component   config.DistroComponent
		DisplayText string
	}

	var displayItems []ComponentDisplay
	for _, comp := range components {
		displayText := comp.MenuText
		displayItems = append(displayItems, ComponentDisplay{
			Component:   comp,
			DisplayText: displayText,
		})
	}

	// Создаем список строк для отображения
	itemStrings := make([]string, len(displayItems))
	for i, item := range displayItems {
		itemStrings[i] = item.DisplayText
	}

	selectedText, err := tui.SelectSimple(itemStrings, "\n--- Выберите дистрибутив Syrve для установки ---")
	if err != nil {
		// Проверяем, является ли ошибка выходом в главное меню
		if err == tui.ErrExitToMainMenu {
			slog.Debug("Пользователь вышел в главное меню из списка компонентов Syrve")
			return config.DistroComponent{}, err
		}
		return config.DistroComponent{}, err
	}

	// Находим выбранный компонент по тексту отображения
	for _, item := range displayItems {
		if item.DisplayText == selectedText {
			slog.Info("Выбран компонент Syrve", "id", item.Component.ID)
			return item.Component, nil
		}
	}

	slog.Error("Не удалось сопоставить выбранный текст с компонентом Syrve", "text", selectedText)
	return config.DistroComponent{}, fmt.Errorf("не удалось найти выбранный компонент")
}

func (h *syrveHandler) getAvailableVersions() ([]string, error) {
	const manifestURL = "http://176.98.191.43/Syrve/manifest.json"
	tui.InfoF("Загрузка манифеста версий Syrve с %s", manifestURL)
	slog.Info("Запрос манифеста версий Syrve", "url", manifestURL)

	resp, err := http.Get(manifestURL)
	if err != nil {
		slog.Error("Ошибка сети при получении манифеста Syrve", "error", err)
		return nil, fmt.Errorf("не удалось скачать манифест версий Syrve: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		slog.Error("Некорректный HTTP статус манифеста Syrve", "status", resp.Status)
		return nil, fmt.Errorf("сервер манифеста Syrve вернул статус: %s", resp.Status)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		slog.Error("Ошибка чтения тела ответа", "error", err)
		return nil, err
	}

	var versionsData []syrveVersionInfo
	if err := json.Unmarshal(body, &versionsData); err != nil {
		// Безопасное получение превью для лога
		previewLen := len(body)
		if previewLen > 100 {
			previewLen = 100
		}
		slog.Error("Ошибка парсинга JSON манифеста", "error", err, "body_preview", string(body)[:previewLen])
		return nil, fmt.Errorf("не удалось распарсить JSON манифеста: %w", err)
	}

	var versions []string
	for _, v := range versionsData {
		versions = append(versions, v.FullVersion)
	}

	sort.Slice(versions, func(i, j int) bool { return compareSemanticVersions(versions[i], versions[j]) > 0 })

	slog.Info("Получен список версий Syrve", "count", len(versions))
	slog.Debug("Список версий Syrve", "versions", versions)
	return versions, nil
}

func (h *syrveHandler) getAvailablePortableVersions(component config.DistroComponent) ([]string, error) {
	tui.Info("Поиск доступных портативных версий Syrve...")
	slog.Info("Поиск портативных версий Syrve", "archive_key", component.PortableArchiveKey)

	syrveFTPs := h.dm.AM.Cfg().FTP[1:]
	if len(syrveFTPs) == 0 {
		slog.Error("В конфигурации отсутствуют FTP серверы для Syrve")
		return nil, errors.New("в конфигурации не определены FTP-серверы для портативных версий Syrve (требуется >=2 записей в ftp_config)")
	}

	slog.Debug("Определение самого быстрого FTP для Syrve")
	fastestFTP, err := h.dm.AM.GetFastestFTP(syrveFTPs, "/speedtest.txt") // Используем плейсхолдер
	if err != nil {
		slog.Error("Ошибка при выборе быстрого FTP", "error", err)
		return nil, err
	}
	slog.Info("Выбран быстрый FTP", "host", fastestFTP.Host)

	cfg := h.dm.AM.Cfg().DistroConfig.SyrvePortable
	archiveNameTemplate := cfg.FtpSource.ArchiveNames[component.PortableArchiveKey]
	if archiveNameTemplate == "" {
		slog.Error("Шаблон имени архива не найден", "key", component.PortableArchiveKey)
		return nil, fmt.Errorf("не найден шаблон имени архива для ключа %s", component.PortableArchiveKey)
	}

	entries, err := h.dm.AM.ListFTP(fastestFTP, cfg.FtpSource.Directory)
	if err != nil {
		slog.Error("Ошибка листинга FTP", "host", fastestFTP.Host, "error", err)
		return nil, fmt.Errorf("не удалось получить список файлов с FTP %s: %w", fastestFTP.Host, err)
	}

	rePattern := strings.Replace(archiveNameTemplate, "{{version}}", `([\d\.]+)`, 1)
	re := regexp.MustCompile(rePattern)

	var foundVersions []string
	for _, entry := range entries {
		if entry.Type == 0 {
			matches := re.FindStringSubmatch(entry.Name)
			if len(matches) > 1 {
				foundVersions = append(foundVersions, matches[1])
			}
		}
	}

	if len(foundVersions) == 0 {
		tui.Warn("На FTP не найдено ни одного файла, соответствующего шаблону портативной версии.")
		slog.Warn("Портативные версии не найдены на FTP")
	}

	sort.Slice(foundVersions, func(i, j int) bool {
		return compareSemanticVersions(foundVersions[i], foundVersions[j]) > 0
	})

	slog.Info("Найдены портативные версии Syrve", "count", len(foundVersions))
	return foundVersions, nil
}

func (h *syrveHandler) installComponent(component config.DistroComponent, version string) error {
	slog.Info("Установка компонента Syrve", "component", component.ID, "version", version)

	downloadURL := strings.Replace(component.URLTemplate, "{{VERSION}}", version, 1)
	slog.Debug("URL загрузки", "url", downloadURL)

	// Создаем путь для версии дистрибутива Syrve
	versionDir := filepath.Join(h.dm.AM.Cfg().RootPath, fmt.Sprintf("syrve_%s", version))
	installerPath := filepath.Join(versionDir, filepath.Base(downloadURL))

	if _, err := h.dm.AM.DownloadHTTPWithProgress(downloadURL, installerPath); err != nil {
		slog.Error("Ошибка загрузки Syrve", "url", downloadURL, "error", err)
		return err
	}

	return h.dm.runInstaller(installerPath, component.InstallArgs)
}

func (h *syrveHandler) installPortable(component config.DistroComponent, version string) error {
	return h.dm.installPortable(h, "syrve", component, version)
}
