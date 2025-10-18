package distro

import (
	"encoding/json"
	"errors"
	"fmt"
	"goMH/config"
	"goMH/core"
	"io"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jlaffaye/ftp"
)

// BrandData содержит данные о бренде для установки
type BrandData struct {
	Brand      string
	Components []config.DistroComponent
}

// VersionInfo содержит информацию о версии
type VersionInfo struct {
	Version string
	Brand   string
}

// GetAvailableBrands возвращает список доступных брендов
func GetAvailableBrands(cfg *config.Config) []string {
	brands := []string{"iiko", "syrve"}
	return brands
}

// GetBrandComponents возвращает компоненты для указанного бренда
func GetBrandComponents(brand string, cfg *config.Config) ([]config.DistroComponent, error) {
	switch brand {
	case "iiko":
		return cfg.DistroConfig.Iiko.Components, nil
	case "syrve":
		return cfg.DistroConfig.Syrve.Components, nil
	default:
		return nil, fmt.Errorf("неизвестный бренд: %s", brand)
	}
}

// GetAvailableVersions возвращает доступные версии для бренда
func GetAvailableVersions(brand string) ([]string, error) {
	switch brand {
	case "iiko":
		return discoverIikoOfficialVersions()
	case "syrve":
		return discoverSyrveVersions()
	default:
		return nil, fmt.Errorf("неизвестный бренд: %s", brand)
	}
}

// GetAvailablePortableVersions возвращает доступные портативные версии для компонента
func GetAvailablePortableVersions(brand string, component config.DistroComponent, am core.AssetManager) ([]string, error) {
	switch brand {
	case "iiko":
		return getIikoPortableVersions(component, am)
	case "syrve":
		return getSyrvePortableVersions(component, am)
	default:
		return nil, fmt.Errorf("неизвестный бренд: %s", brand)
	}
}

// InstallComponentData выполняет установку компонента и возвращает результат
func InstallComponentData(brand string, component config.DistroComponent, version string, am core.AssetManager, wu core.WinUtils) error {
	switch brand {
	case "iiko":
		handler := &iikoHandler{dm: &DistroManager{AM: am, WU: wu}}
		return handler.installComponent(component, version)
	case "syrve":
		handler := &syrveHandler{dm: &DistroManager{AM: am, WU: wu}}
		return handler.installComponent(component, version)
	default:
		return fmt.Errorf("неизвестный бренд: %s", brand)
	}
}

// InstallPortableData выполняет установку портативной версии и возвращает результат
func InstallPortableData(brand string, component config.DistroComponent, version string, am core.AssetManager, wu core.WinUtils) error {
	handler := &DistroManager{AM: am, WU: wu}
	switch brand {
	case "iiko":
		iikoHandler := &iikoHandler{dm: handler}
		return iikoHandler.installPortable(component, version)
	case "syrve":
		syrveHandler := &syrveHandler{dm: handler}
		return syrveHandler.installPortable(component, version)
	default:
		return fmt.Errorf("неизвестный бренд: %s", brand)
	}
}

// discoverIikoOfficialVersions получает список официальных версий iiko
func discoverIikoOfficialVersions() ([]string, error) {
	var allErrors []string
	for _, host := range iikoFtpHosts {
		c, err := ftp.Dial(host, ftp.DialWithTimeout(15*time.Second))
		if err != nil {
			allErrors = append(allErrors, fmt.Sprintf("ошибка подключения к %s: %v", host, err))
			continue
		}
		err = c.Login(iikoFtpUser, iikoFtpPass)
		if err != nil {
			_ = c.Quit()
			allErrors = append(allErrors, fmt.Sprintf("ошибка входа на %s: %v", host, err))
			continue
		}
		entries, err := c.List(iikoReleasesPath)
		_ = c.Quit()
		if err != nil {
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
		return versions, nil
	}
	return nil, fmt.Errorf("не удалось получить данные ни с одного из FTP-серверов iiko: %s", strings.Join(allErrors, "; "))
}

// discoverSyrveVersions получает список версий Syrve из манифеста
func discoverSyrveVersions() ([]string, error) {
	const manifestURL = "http://176.98.191.43/Syrve/manifest.json"

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

	sort.Slice(versions, func(i, j int) bool {
		return compareSemanticVersions(versions[i], versions[j]) > 0
	})

	return versions, nil
}

// getIikoPortableVersions получает портативные версии iiko
func getIikoPortableVersions(component config.DistroComponent, am core.AssetManager) ([]string, error) {
	cfg := am.Cfg().DistroConfig.IikoPortable
	archiveNameTemplate := cfg.FtpSource.ArchiveNames[component.PortableArchiveKey]
	if archiveNameTemplate == "" {
		return nil, fmt.Errorf("не найден шаблон имени архива для ключа %s", component.PortableArchiveKey)
	}

	// Получаем список файлов с FTP
	ftpCfg := am.Cfg().FTP[0]
	entries, err := am.ListFTP(ftpCfg, cfg.FtpSource.Directory)
	if err != nil {
		return nil, fmt.Errorf("не удалось получить список файлов с FTP %s: %w", ftpCfg.Host, err)
	}

	// Извлекаем версии из имен файлов
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

	sort.Slice(foundVersions, func(i, j int) bool {
		return compareSemanticVersions(foundVersions[i], foundVersions[j]) > 0
	})

	return foundVersions, nil
}

// getSyrvePortableVersions получает портативные версии Syrve
func getSyrvePortableVersions(component config.DistroComponent, am core.AssetManager) ([]string, error) {
	syrveFTPs := am.Cfg().FTP[1:]
	if len(syrveFTPs) == 0 {
		return nil, errors.New("в конфигурации не определены FTP-серверы для портативных версий Syrve")
	}

	fastestFTP, err := am.GetFastestFTP(syrveFTPs, "/speedtest.txt")
	if err != nil {
		return nil, err
	}

	cfg := am.Cfg().DistroConfig.SyrvePortable
	archiveNameTemplate := cfg.FtpSource.ArchiveNames[component.PortableArchiveKey]
	if archiveNameTemplate == "" {
		return nil, fmt.Errorf("не найден шаблон имени архива для ключа %s", component.PortableArchiveKey)
	}

	entries, err := am.ListFTP(fastestFTP, cfg.FtpSource.Directory)
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

	sort.Slice(foundVersions, func(i, j int) bool {
		return compareSemanticVersions(foundVersions[i], foundVersions[j]) > 0
	})

	return foundVersions, nil
}

// compareSemanticVersions сравнивает семантические версии
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
