package distro

import (
	"fmt"
	"goMH/config"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"goMH/tui"
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
			return config.DistroComponent{}, err
		}
		return config.DistroComponent{}, err
	}

	// Находим выбранный компонент по тексту отображения
	for _, item := range displayItems {
		if item.DisplayText == selectedText {
			return item.Component, nil
		}
	}

	return config.DistroComponent{}, fmt.Errorf("не удалось найти выбранный компонент")
}

func (h *iikoHandler) getAvailableVersions() ([]string, error) {
	return discoverIikoOfficialVersions()
}

func (h *iikoHandler) getAvailablePortableVersions(component config.DistroComponent) ([]string, error) {
	tui.Info("Поиск доступных портативных версий iiko...")
	cfg := h.dm.AM.Cfg().DistroConfig.IikoPortable
	archiveNameTemplate := cfg.FtpSource.ArchiveNames[component.PortableArchiveKey]
	if archiveNameTemplate == "" {
		return nil, fmt.Errorf("не найден шаблон имени архива для ключа %s", component.PortableArchiveKey)
	}

	// 1. Получаем список файлов с нашего FTP.
	ftpCfg := h.dm.AM.Cfg().FTP[0]
	entries, err := h.dm.AM.ListFTP(ftpCfg, cfg.FtpSource.Directory)
	if err != nil {
		return nil, fmt.Errorf("не удалось получить список файлов с FTP %s: %w", ftpCfg.Host, err)
	}

	// 2. Извлекаем версии из имен файлов.
	// Пример шаблона: RMSOffice{{version}}.zip
	// Нам нужно извлечь {{version}} и то, что вокруг.
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
	}

	sort.Slice(foundVersions, func(i, j int) bool {
		return compareSemanticVersions(foundVersions[i], foundVersions[j]) > 0
	})

	return foundVersions, nil
}

func (h *iikoHandler) installComponent(component config.DistroComponent, version string) error {
	var downloadURL string

	if component.URLTemplate != "" {
		downloadURL = strings.Replace(component.URLTemplate, "{{VERSION}}", version, 1)
	}

	// Создаем путь для версии дистрибутива
	versionDir := filepath.Join(h.dm.AM.Cfg().RootPath, fmt.Sprintf("iiko_%s", version))
	installerPath := filepath.Join(versionDir, filepath.Base(downloadURL))

	if strings.HasPrefix(downloadURL, "http") {
		if _, err := h.dm.AM.DownloadHTTPWithProgress(downloadURL, installerPath); err != nil {
			return err
		}
		return h.dm.runInstaller(installerPath, component.InstallArgs)
	} else {
		ftpCfg := h.dm.AM.Cfg().FTP[0]
		if _, err := h.dm.AM.DownloadFTPWithProgress(ftpCfg, downloadURL, installerPath); err != nil {
			return err
		}
		return h.dm.runInstaller(installerPath, component.InstallArgs)
	}
}

func (h *iikoHandler) installPortable(component config.DistroComponent, version string) error {
	return h.dm.installPortable(h, "iiko", component, version)
}
