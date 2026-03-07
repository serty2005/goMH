package distro

import (
	"encoding/json"
	"errors"
	"fmt"
	"goMH/config"
	"goMH/core"
	"goMH/tui"
	"io" // Импорт
	"net/http"
	"regexp"
	"strings"
)

type syrveHandler struct{}

type syrveVersionInfo struct {
	FullVersion string `json:"full_version"`
}

func (h *syrveHandler) ConfigureBrand(ctx core.TaskContext, am core.AssetManager, wu core.WinUtils) (*DistroInstallConfig, error) {
	components := am.Cfg().DistroConfig.Syrve.Components

	// Подготавливаем список строк для меню
	var menuItems []string
	for _, c := range components {
		menuText := c.MenuText
		if c.PortableArchiveKey != "" {
			menuText += " (portable)"
		}
		menuItems = append(menuItems, menuText)
	}

	// Используем классическое текстовое меню
	selectedIdx, err := tui.PrintMenu("\n--- Выберите дистрибутив Syrve ---", menuItems)
	if err != nil {
		return nil, err
	}
	if selectedIdx == -1 {
		return nil, nil // Назад
	}

	comp := components[selectedIdx]

	cfg := &DistroInstallConfig{
		Brand:     "syrve",
		Component: comp,
		Action:    ActionInstallComponent,
	}

	// Проверка на необходимость выбора версии
	if !strings.Contains(comp.URLTemplate, "{{VERSION}}") {
		return cfg, nil
	}

	// Определяем установленную версию
	var installedVer string
	if comp.RunAfter != "" {
		if ver, err := wu.GetFileVersion(comp.RunAfter); err == nil {
			installedVer = ver
		}
	}

	// Выбор режима (Portable) - здесь можно оставить SelectSimple, т.к. всего 2 варианта
	if comp.PortableArchiveKey != "" {
		label := "Выберите режим"
		if installedVer != "" {
			label = fmt.Sprintf("Выберите режим (установлена %s)", installedVer)
		}
		mode, err := tui.SelectSimple([]string{"Стандартная установка", "Портативная версия"}, label)
		if err != nil {
			return nil, err
		}
		if mode == "Портативная версия" {
			return h.configurePortable(ctx, am, comp)
		}
	}

	// Получение версий (JSON manifest)
	versions, err := tui.RunWithSpinner(
		"Версии Syrve",
		"Загрузка списка доступных версий...",
		h.fetchVersions,
	)
	if err != nil {
		return nil, err
	}

	// Выбор версии - ОСТАВЛЯЕМ SelectWithSearch для длинных списков
	promptLabel := "Выберите версию"
	if installedVer != "" {
		promptLabel += fmt.Sprintf(" (Текущая: %s)", installedVer)
	}
	version, err := tui.SelectWithSearch(versions, promptLabel)
	if err != nil {
		return nil, err
	}
	cfg.Version = version

	return cfg, nil
}

func (h *syrveHandler) configurePortable(ctx core.TaskContext, am core.AssetManager, comp config.DistroComponent) (*DistroInstallConfig, error) {
	cfg := &DistroInstallConfig{
		Brand:     "syrve",
		Component: comp,
		Action:    ActionInstallPortable,
	}

	pCfg := am.Cfg().DistroConfig.SyrvePortable
	tpl := ""
	if pCfg.HttpSource.Enabled {
		cfg.PortableSourceType = "http"
		tpl = pCfg.HttpSource.ArchiveNames[comp.PortableArchiveKey]
	} else if pCfg.FtpSource.Enabled {
		cfg.PortableSourceType = "ftp"
		tpl = pCfg.FtpSource.ArchiveNames[comp.PortableArchiveKey]
	} else {
		return nil, errors.New("нет источников portable")
	}

	if tpl == "" {
		return nil, fmt.Errorf("шаблон архива не найден для %s", comp.PortableArchiveKey)
	}

	// Изменение: всегда используем первый FTP сервер из конфигурации
	ftpList := am.Cfg().FTP
	if len(ftpList) == 0 {
		return nil, errors.New("список FTP серверов в конфигурации пуст")
	}

	selectedFTP := ftpList[0]
	cfg.PortableFTPConfig = selectedFTP

	entries, err := tui.RunWithSpinner(
		"Portable Syrve",
		fmt.Sprintf("Чтение каталога FTP %s...", selectedFTP.Host),
		func() ([]core.FTPEntry, error) {
			return am.ListFTP(selectedFTP, pCfg.FtpSource.Directory)
		},
	)
	if err != nil {
		return nil, err
	}

	rePattern := strings.Replace(tpl, "{{version}}", `([\d\.]+)`, 1)
	re := regexp.MustCompile(rePattern)
	var versions []string

	for _, e := range entries {
		matches := re.FindStringSubmatch(e.Name)
		if len(matches) > 1 {
			versions = append(versions, matches[1])
		}
	}
	sortVersionsDesc(versions)

	ver, err := tui.SelectWithSearch(versions, "Выберите версию portable")
	if err != nil {
		return nil, err
	}
	cfg.Version = ver

	archiveName := strings.Replace(tpl, "{{version}}", ver, 1)
	cfg.PortableArchiveName = archiveName

	if cfg.PortableSourceType == "http" {
		cfg.PortableDownloadURL = fmt.Sprintf("%s/%s", strings.TrimSuffix(pCfg.HttpSource.URL, "/"), archiveName)
	} else {
		cfg.PortableFTPPath = fmt.Sprintf("%s/%s", strings.TrimSuffix(pCfg.FtpSource.Directory, "/"), archiveName)
	}

	return cfg, nil
}

func (h *syrveHandler) fetchVersions() ([]string, error) {
	const manifestURL = "http://f.serty.top/distr/installer/syrve-manifest.json"
	resp, err := http.Get(manifestURL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("bad status: %s", resp.Status)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var versionsData []syrveVersionInfo
	if err := json.Unmarshal(body, &versionsData); err != nil {
		return nil, err
	}

	var versions []string
	for _, v := range versionsData {
		versions = append(versions, v.FullVersion)
	}
	sortVersionsDesc(versions)
	return versions, nil
}
