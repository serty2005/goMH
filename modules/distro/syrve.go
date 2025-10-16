// modules/distro/syrve.go
package distro

import (
	"encoding/json"
	"errors"
	"fmt"
	"goMH/config"
	"goMH/tui"
	"io"
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
	return h.dm.runWorkflow(h)
}

func (h *syrveHandler) selectComponentMenu() (config.DistroComponent, error) {
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

func (h *syrveHandler) getAvailableVersions() ([]string, error) {
	const manifestURL = "http://176.98.191.43/Syrve/manifest.json"
	tui.InfoF("Загрузка манифеста версий Syrve с %s", manifestURL)
	resp, err := http.Get(manifestURL)
	if err != nil {
		return nil, fmt.Errorf("не удалось скачать манифест версий Syrve: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("сервер манифеста Syrve вернул статус: %s", resp.Status)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	var versionsData []syrveVersionInfo
	if err := json.Unmarshal(body, &versionsData); err != nil {
		return nil, fmt.Errorf("не удалось распарсить JSON манифеста: %w", err)
	}
	var versions []string
	for _, v := range versionsData {
		versions = append(versions, v.FullVersion)
	}
	sort.Slice(versions, func(i, j int) bool { return compareSemanticVersions(versions[i], versions[j]) > 0 })
	return versions, nil
}

func (h *syrveHandler) getAvailablePortableVersions(component config.DistroComponent) ([]string, error) {
	tui.Info("Поиск доступных портативных версий Syrve...")
	syrveFTPs := h.dm.AM.Cfg().FTP[1:]
	if len(syrveFTPs) == 0 {
		return nil, errors.New("в конфигурации не определены FTP-серверы для портативных версий Syrve (требуется >=2 записей в ftp_config)")
	}

	fastestFTP, err := h.dm.AM.GetFastestFTP(syrveFTPs, "/speedtest.txt") // Используем плейсхолдер
	if err != nil {
		return nil, err
	}

	cfg := h.dm.AM.Cfg().DistroConfig.SyrvePortable
	archiveNameTemplate := cfg.FtpSource.ArchiveNames[component.PortableArchiveKey]
	if archiveNameTemplate == "" {
		return nil, fmt.Errorf("не найден шаблон имени архива для ключа %s", component.PortableArchiveKey)
	}

	entries, err := h.dm.AM.ListFTP(fastestFTP, cfg.FtpSource.Directory)
	if err != nil {
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
	}

	sort.Slice(foundVersions, func(i, j int) bool {
		return compareSemanticVersions(foundVersions[i], foundVersions[j]) > 0
	})

	return foundVersions, nil
}

func (h *syrveHandler) installComponent(component config.DistroComponent, version string) error {
	downloadURL := strings.Replace(component.URLTemplate, "{{VERSION}}", version, 1)
	// Создаем путь для версии дистрибутива Syrve
	versionDir := filepath.Join(h.dm.AM.Cfg().RootPath, fmt.Sprintf("syrve_%s", version))
	installerPath := filepath.Join(versionDir, filepath.Base(downloadURL))
	if _, err := h.dm.AM.DownloadHTTPWithProgress(downloadURL, installerPath); err != nil {
		return err
	}
	return h.dm.runInstaller(installerPath, component.InstallArgs)
}

func (h *syrveHandler) installPortable(component config.DistroComponent, version string) error {
	return h.dm.installPortable(h, "syrve", component, version)
}
