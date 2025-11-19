package distro

import (
	"fmt"
	"goMH/config"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"goMH/tui"
	"log/slog" // Импорт логгера

	"github.com/jlaffaye/ftp"
)

const (
	iikoMinVersion   = "8.7.6032.0"
	iikoReleasesPath = "/release_iiko"
)

var (
	iikoFtpHosts = []string{"ftp.iiko.ru:21", "ftp2.iiko.ru:21"}
	iikoFtpUser  = "partners"
	iikoFtpPass  = "partners#iiko"
)

type iikoHandler struct {
	dm *DistroManager
}

func (h *iikoHandler) Run() error {
	return h.dm.runWorkflow(h)
}

func (h *iikoHandler) selectComponentMenu() (config.DistroComponent, error) {
	slog.Debug("Формирование меню компонентов iiko")
	components := h.dm.AM.Cfg().DistroConfig.Iiko.Components

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

	selectedText, err := tui.SelectSimple(itemStrings, "\n--- Выберите дистрибутив iiko для установки ---")
	if err != nil {
		// Проверяем, является ли ошибка выходом в главное меню
		if err == tui.ErrExitToMainMenu {
			slog.Debug("Пользователь выбрал выход в главное меню из списка компонентов iiko")
			return config.DistroComponent{}, err
		}
		return config.DistroComponent{}, err
	}

	// Находим выбранный компонент по тексту отображения
	for _, item := range displayItems {
		if item.DisplayText == selectedText {
			slog.Info("Выбран компонент iiko", "id", item.Component.ID, "text", item.DisplayText)
			return item.Component, nil
		}
	}

	slog.Error("Не удалось сопоставить выбранный текст с компонентом", "selectedText", selectedText)
	return config.DistroComponent{}, fmt.Errorf("не удалось найти выбранный компонент")
}

func (h *iikoHandler) getAvailableVersions() ([]string, error) {
	slog.Info("Запуск поиска официальных версий iiko на FTP")
	return discoverIikoOfficialVersions()
}

func (h *iikoHandler) getAvailablePortableVersions(component config.DistroComponent) ([]string, error) {
	tui.Info("Поиск доступных портативных версий iiko...")
	slog.Info("Поиск доступных портативных версий iiko", "archive_key", component.PortableArchiveKey)

	cfg := h.dm.AM.Cfg().DistroConfig.IikoPortable
	archiveNameTemplate := cfg.FtpSource.ArchiveNames[component.PortableArchiveKey]
	if archiveNameTemplate == "" {
		slog.Error("Шаблон архива не найден", "archive_key", component.PortableArchiveKey)
		return nil, fmt.Errorf("не найден шаблон имени архива для ключа %s", component.PortableArchiveKey)
	}

	// 1. Получаем список файлов с нашего FTP.
	ftpCfg := h.dm.AM.Cfg().FTP[0]
	slog.Debug("Подключение к FTP для поиска портативных версий", "host", ftpCfg.Host, "path", cfg.FtpSource.Directory)

	entries, err := h.dm.AM.ListFTP(ftpCfg, cfg.FtpSource.Directory)
	if err != nil {
		slog.Error("Ошибка листинга FTP", "host", ftpCfg.Host, "error", err)
		return nil, fmt.Errorf("не удалось получить список файлов с FTP %s: %w", ftpCfg.Host, err)
	}

	// 2. Извлекаем версии из имен файлов.
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

	slog.Info("Найдены портативные версии", "count", len(foundVersions), "versions", foundVersions)

	if len(foundVersions) == 0 {
		tui.Warn("На FTP не найдено ни одного файла, соответствующего шаблону портативной версии.")
	}

	sort.Slice(foundVersions, func(i, j int) bool {
		return compareSemanticVersions(foundVersions[i], foundVersions[j]) > 0
	})

	return foundVersions, nil
}

func (h *iikoHandler) installComponent(component config.DistroComponent, version string) error {
	slog.Info("Начало установки компонента iiko", "component", component.ID, "version", version)

	var downloadURL string

	if component.URLTemplate != "" {
		downloadURL = strings.Replace(component.URLTemplate, "{{VERSION}}", version, 1)
	}
	slog.Debug("Сформирован URL загрузки", "url", downloadURL)

	// Создаем путь для версии дистрибутива
	versionDir := filepath.Join(h.dm.AM.Cfg().RootPath, fmt.Sprintf("iiko_%s", version))
	installerPath := filepath.Join(versionDir, filepath.Base(downloadURL))

	slog.Debug("Целевой путь для установщика", "path", installerPath)

	if strings.HasPrefix(downloadURL, "http") {
		if _, err := h.dm.AM.DownloadHTTPWithProgress(downloadURL, installerPath); err != nil {
			slog.Error("Ошибка загрузки по HTTP", "url", downloadURL, "error", err)
			return err
		}
		return h.dm.runInstaller(installerPath, component.InstallArgs)
	} else {
		ftpCfg := h.dm.AM.Cfg().FTP[0]
		slog.Debug("Загрузка по FTP (внутренний источник)", "host", ftpCfg.Host)
		if _, err := h.dm.AM.DownloadFTPWithProgress(ftpCfg, downloadURL, installerPath); err != nil {
			slog.Error("Ошибка загрузки по FTP", "url", downloadURL, "error", err)
			return err
		}
		return h.dm.runInstaller(installerPath, component.InstallArgs)
	}
}

func (h *iikoHandler) installPortable(component config.DistroComponent, version string) error {
	return h.dm.installPortable(h, "iiko", component, version)
}

func discoverIikoOfficialVersions() ([]string, error) {
	var allErrors []string
	for _, host := range iikoFtpHosts {
		slog.Debug("Попытка подключения к официальному FTP iiko", "host", host)

		c, err := ftp.Dial(host, ftp.DialWithTimeout(15*time.Second))
		if err != nil {
			slog.Debug("Не удалось подключиться к FTP", "host", host, "error", err)
			allErrors = append(allErrors, fmt.Sprintf("ошибка подключения к %s: %v", host, err))
			continue
		}

		err = c.Login(iikoFtpUser, iikoFtpPass)
		if err != nil {
			slog.Debug("Ошибка авторизации на FTP", "host", host, "error", err)
			_ = c.Quit()
			allErrors = append(allErrors, fmt.Sprintf("ошибка входа на %s: %v", host, err))
			continue
		}

		entries, err := c.List(iikoReleasesPath)
		_ = c.Quit()

		if err != nil {
			slog.Debug("Ошибка получения списка папок", "host", host, "path", iikoReleasesPath, "error", err)
			allErrors = append(allErrors, fmt.Sprintf("ошибка получения списка с %s: %v", host, err))
			continue
		}

		versionRegex := regexp.MustCompile(`^\d+\.\d+\.\d+\.\d+$`)
		var versions []string
		for _, entry := range entries {
			if entry.Type == ftp.EntryTypeFolder && versionRegex.MatchString(entry.Name) {
				if compareSemanticVersions(entry.Name, iikoMinVersion) >= 0 {
					versions = append(versions, entry.Name)
				}
			}
		}

		sort.Slice(versions, func(i, j int) bool {
			return compareSemanticVersions(versions[i], versions[j]) > 0
		})

		slog.Info("Успешно получен список версий с FTP iiko", "host", host, "count", len(versions))
		return versions, nil
	}

	finalErr := fmt.Errorf("не удалось получить данные ни с одного из FTP-серверов iiko: %s", strings.Join(allErrors, "; "))
	slog.Error("Критическая ошибка поиска версий iiko", "error", finalErr)
	return nil, finalErr
}

func compareSemanticVersions(v1, v2 string) int {
	parts1 := strings.Split(v1, ".")
	parts2 := strings.Split(v2, ".")
	maxLen := len(parts1)
	if len(parts2) > maxLen {
		maxLen = len(parts2)
	}
	for i := 0; i < maxLen; i++ {
		var num1, num2 int
		if i < len(parts1) {
			num1, _ = strconv.Atoi(parts1[i])
		}
		if i < len(parts2) {
			num2, _ = strconv.Atoi(parts2[i])
		}
		if num1 > num2 {
			return 1
		}
		if num1 < num2 {
			return -1
		}
	}
	return 0
}
