package distro

import (
	"errors"
	"fmt"
	"goMH/config"
	"goMH/core"
	iikoplugins "goMH/modules/iiko-plugins"
	"goMH/tui"
	"log/slog"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

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

type iikoHandler struct{}

// ConfigureBrand реализует логику меню iiko
func (h *iikoHandler) ConfigureBrand(ctx core.TaskContext, am core.AssetManager, wu core.WinUtils) (*DistroInstallConfig, error) {
	// 1. Выбор действия (Компоненты / Плагины / Патчи)
	components := am.Cfg().DistroConfig.Iiko.Components
	var menuItems []string

	// Добавляем компоненты
	for _, c := range components {
		menuText := c.MenuText
		if c.PortableArchiveKey != "" {
			menuText += " (portable)"
		}
		menuItems = append(menuItems, menuText)
	}

	// Добавляем доп. опции
	menuItems = append(menuItems, "Установка плагинов iikoFront")
	menuItems = append(menuItems, "Установка патчей iikoFront (вручную)")

	// Используем PrintMenu вместо SelectSimple
	selectedIdx, err := tui.PrintMenu("\n--- Меню iiko ---", menuItems)
	if err != nil {
		return nil, err
	}
	if selectedIdx == -1 {
		return nil, nil // Назад
	}

	// Обработка выбора
	if selectedIdx < len(components) {
		// Выбран компонент
		return h.configureComponent(ctx, am, wu, components[selectedIdx])
	}

	// Определяем, что выбрано из доп. опций
	// Индекс плагинов = len(components)
	// Индекс патчей = len(components) + 1
	if selectedIdx == len(components) {
		pluginMod := &iikoplugins.Module{}
		selection, err := pluginMod.ConfigureInstall(am, wu)
		if err != nil {
			return nil, err
		}
		if selection == nil {
			return nil, nil
		}
		return &DistroInstallConfig{
			Action:          ActionPlugins,
			Brand:           "iiko",
			PluginSelection: selection,
		}, nil
	} else if selectedIdx == len(components)+1 {
		return h.configureManualPatch(ctx, am, wu)
	}

	return nil, nil
}

func (h *iikoHandler) configureComponent(ctx core.TaskContext, am core.AssetManager, wu core.WinUtils, comp config.DistroComponent) (*DistroInstallConfig, error) {
	cfg := &DistroInstallConfig{
		Brand:     "iiko",
		Component: comp,
		Action:    ActionInstallComponent,
	}

	// Если в URLTemplate нет плейсхолдера версии, значит это компонент с одной версией (например, iikoCard)
	// Сразу возвращаем конфиг для установки
	if !strings.Contains(comp.URLTemplate, "{{VERSION}}") {
		slog.Debug("Компонент без версионирования, пропуск выбора версии", "id", comp.ID)
		return cfg, nil
	}

	// Определяем установленную версию
	var installedVer string
	if comp.RunAfter != "" {
		if ver, err := wu.GetFileVersion(comp.RunAfter); err == nil {
			installedVer = ver
		}
	}

	// Выбор режима (Portable / Install)
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

	// Получение списка версий
	versions, err := tui.RunWithSpinner(
		"Версии iiko",
		"Получение списка релизов с FTP...",
		h.fetchOfficialVersions,
	)
	if err != nil {
		return nil, err
	}
	if len(versions) == 0 {
		return nil, errors.New("список версий пуст")
	}

	// Выбор версии
	promptLabel := "Выберите версию"
	if installedVer != "" {
		promptLabel += fmt.Sprintf(" (Текущая: %s)", installedVer)
	}
	version, err := tui.SelectVersionWithSearch(versions, promptLabel)
	if err != nil {
		return nil, err
	}
	cfg.Version = version

	// Проверка даунгрейда
	if installedVer != "" && compareSemanticVersions(version, installedVer) < 0 {
		confirm, err := tui.Confirm(
			"Понижение версии iiko",
			fmt.Sprintf("Обнаружено понижение с %s до %s. Удалить старую версию автоматически?", installedVer, version),
			"Удалить старую версию",
		)
		if err != nil {
			return nil, err
		}
		if !confirm {
			return nil, nil
		}
		cfg.UninstallOldVersion = true
		cfg.OldVersionString = installedVer
	}

	// Для iikoFront: Выбор патча
	if comp.ID == "iiko_front" {
		patches, err := FindPatches(am.Cfg().DistroConfig.Iiko.PatchesBaseURL, version)
		if err != nil {
			slog.Warn("Ошибка поиска патчей", "error", err)
		} else if len(patches) > 0 {
			patch, err := SelectPatchMenu(patches)
			if err == nil && patch.ShortName != "SKIP" {
				cfg.Patch = &patch
			}
		}
		// Автообновление плагинов
		cfg.RunAutoUpdatePlugins = true
	}

	return cfg, nil
}

func (h *iikoHandler) configurePortable(_ core.TaskContext, am core.AssetManager, comp config.DistroComponent) (*DistroInstallConfig, error) {
	cfg := &DistroInstallConfig{
		Brand:     "iiko",
		Component: comp,
		Action:    ActionInstallPortable,
	}

	pCfg := am.Cfg().DistroConfig.IikoPortable
	tpl := ""
	if pCfg.HttpSource.Enabled {
		cfg.PortableSourceType = "http"
		tpl = pCfg.HttpSource.ArchiveNames[comp.PortableArchiveKey]
	} else if pCfg.FtpSource.Enabled {
		cfg.PortableSourceType = "ftp"
		tpl = pCfg.FtpSource.ArchiveNames[comp.PortableArchiveKey]
		cfg.PortableFTPConfig = am.Cfg().FTP[0]
	} else {
		return nil, errors.New("нет источников portable")
	}

	if tpl == "" {
		return nil, fmt.Errorf("шаблон архива не найден для %s", comp.PortableArchiveKey)
	}

	ftpCfg := am.Cfg().FTP[0]
	dir := pCfg.FtpSource.Directory

	entries, err := tui.RunWithSpinner(
		"Portable iiko",
		"Чтение каталога FTP с portable-версиями...",
		func() ([]core.FTPEntry, error) {
			return am.ListFTP(ftpCfg, dir)
		},
	)
	if err != nil {
		return nil, err
	}

	// Парсинг версий
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

	ver, err := tui.SelectVersionWithSearch(versions, "Выберите версию portable")
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

func (h *iikoHandler) configureManualPatch(_ core.TaskContext, am core.AssetManager, wu core.WinUtils) (*DistroInstallConfig, error) {
	const iikoFrontDir = `C:\Program Files\iiko\iikoRMS\Front.Net`
	exePath := filepath.Join(iikoFrontDir, "iikoFront.Net.exe")

	ver, err := wu.GetFileVersion(exePath)
	if err != nil {
		return nil, fmt.Errorf("iikoFront не найден: %w", err)
	}

	patches, err := FindPatches(am.Cfg().DistroConfig.Iiko.PatchesBaseURL, ver)
	if err != nil || len(patches) == 0 {
		return nil, errors.New("патчи не найдены")
	}

	patch, err := SelectPatchMenu(patches)
	if err != nil || patch.ShortName == "SKIP" {
		return nil, nil
	}

	return &DistroInstallConfig{
		Action:  ActionManualPatch,
		Brand:   "iiko",
		Version: ver,
		Patch:   &patch,
	}, nil
}

func (h *iikoHandler) fetchOfficialVersions() ([]string, error) {
	var allErrors []string
	for _, host := range iikoFtpHosts {
		c, err := ftp.Dial(host, ftp.DialWithTimeout(10*time.Second))
		if err != nil {
			allErrors = append(allErrors, err.Error())
			continue
		}
		if err := c.Login(iikoFtpUser, iikoFtpPass); err != nil {
			c.Quit()
			allErrors = append(allErrors, err.Error())
			continue
		}

		entries, err := c.List(iikoReleasesPath)
		c.Quit()
		if err != nil {
			allErrors = append(allErrors, err.Error())
			continue
		}

		versionRegex := regexp.MustCompile(`^\d+\.\d+\.\d+\.\d+$`)
		var versions []string
		for _, e := range entries {
			if e.Type == ftp.EntryTypeFolder && versionRegex.MatchString(e.Name) {
				if compareSemanticVersions(e.Name, iikoMinVersion) >= 0 {
					versions = append(versions, e.Name)
				}
			}
		}
		sortVersionsDesc(versions)
		return versions, nil
	}
	return nil, fmt.Errorf("ошибка FTP: %s", strings.Join(allErrors, "; "))
}

func sortVersionsDesc(versions []string) {
	sort.Slice(versions, func(i, j int) bool {
		return compareSemanticVersions(versions[i], versions[j]) > 0
	})
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
