package iikoplugins

import (
	"bufio"
	"encoding/json"
	"fmt"
	"goMH/core"
	"goMH/dependencies"
	"goMH/tui"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
)

// Plugin представляет информацию о плагине
type Plugin struct {
	Name          string `json:"name"`
	ApiVersion    string `json:"api_version"`
	PluginVersion string `json:"plugin_version"`
	DownloadUrl   string `json:"download_url"`
}

// ApiCompat представляет совместимость API версии
type ApiCompat struct {
	ApiVersion      string `json:"api_version"`
	MinFrontVersion string `json:"min_front_version"`
	MaxFrontVersion string `json:"max_front_version"`
}

// Manifest представляет манифест плагинов
type Manifest struct {
	ApiVersions []ApiCompat `json:"api_compatibility"`
	Plugins     []Plugin    `json:"plugins"`
}

// Module реализует интерфейс core.Installer для управления плагинами iiko
type Module struct{}

// ID возвращает идентификатор модуля
func (m *Module) ID() string {
	return "iiko-plugins"
}

// MenuText возвращает текст для меню
func (m *Module) MenuText() string {
	return "Установка плагинов iiko"
}

// Run выполняет интерактивную установку плагинов через меню
func (m *Module) Run(am core.AssetManager, wu core.WinUtils) error {
	// Проверяем права администратора для операций с плагинами
	if !wu.IsAdmin() {
		return fmt.Errorf("для установки плагинов требуются права администратора")
	}
	return m.MenuInstallPlugins(am, wu)
}

// MenuInstallPlugins выполняет интерактивную установку плагинов через меню
func (m *Module) MenuInstallPlugins(am core.AssetManager, wu core.WinUtils) error {
	cfg := am.Cfg()

	// Получить версию iikoFront
	version, err := GetIikoFrontVersion(wu)
	if err != nil {
		return fmt.Errorf("не удалось определить версию iikoFront: %w", err)
	}
	tui.Info(fmt.Sprintf("Обнаружена версия iikoFront: %s", version))

	// Загрузить манифест
	manifest, err := LoadManifest()
	if err != nil {
		return fmt.Errorf("не удалось загрузить манифест: %w", err)
	}

	// Получить совместимые API версии
	compatibleApiVersions := getCompatibleApiVersions(manifest, version)

	// Собрать доступные плагины по совместимым API версиям
	var availablePlugins []Plugin
	for _, plugin := range manifest.Plugins {
		for _, apiVer := range compatibleApiVersions {
			if plugin.ApiVersion == apiVer {
				availablePlugins = append(availablePlugins, plugin)
				break
			}
		}
	}

	// Отфильтровать по excludedPlugins
	filteredPlugins := FilterPlugins(availablePlugins, cfg.IikoConfig.ExcludedPlugins)

	// Сгруппировать плагины по имени
	pluginGroups := make(map[string][]Plugin)
	for _, plugin := range filteredPlugins {
		pluginGroups[plugin.Name] = append(pluginGroups[plugin.Name], plugin)
	}

	// Получить установленные плагины для проверки
	installed, err := GetInstalledPlugins()
	if err != nil {
		tui.Warn(fmt.Sprintf("Не удалось получить список установленных плагинов: %v", err))
		installed = []string{} // пустой список, если ошибка
	}

	// Отобразить меню выбора плагинов
	selectedPlugin, err := showPluginSelectionMenu(pluginGroups, installed)
	if err != nil {
		return err
	}
	if selectedPlugin == nil {
		return nil // пользователь отменил
	}

	// Если несколько версий, спросить выбор
	if len(pluginGroups[selectedPlugin.Name]) > 1 {
		selectedPlugin, err = selectPluginVersion(pluginGroups[selectedPlugin.Name])
		if err != nil {
			return err
		}
		if selectedPlugin == nil {
			return nil // отмена
		}
	}

	// Найти folderName если установлен
	var folderName string
	for _, inst := range installed {
		if strings.Contains(strings.ToLower(inst), strings.ToLower(selectedPlugin.Name)) {
			folderName = inst
			break
		}
	}

	// Установить или обновить
	if folderName != "" {
		tui.Info(fmt.Sprintf("Плагин %s уже установлен, выполняем обновление", selectedPlugin.Name))
		if err := updatePlugin(am, wu, selectedPlugin, cfg.RootPath, folderName); err != nil {
			return fmt.Errorf("ошибка обновления плагина %s: %w", selectedPlugin.Name, err)
		}
		tui.Success(fmt.Sprintf("Плагин %s успешно обновлен", selectedPlugin.Name))
	} else {
		tui.Info(fmt.Sprintf("Устанавливаем новый плагин: %s", selectedPlugin.Name))
		if err := installPlugin(am, wu, selectedPlugin, cfg.RootPath); err != nil {
			return fmt.Errorf("ошибка установки плагина %s: %w", selectedPlugin.Name, err)
		}
		tui.Success(fmt.Sprintf("Плагин %s успешно установлен", selectedPlugin.Name))
	}

	return nil
}

// AutoUpdatePlugins выполняет автоматическое обновление плагинов
func (m *Module) AutoUpdatePlugins(am core.AssetManager, wu core.WinUtils) error {
	cfg := am.Cfg()

	// Получить версию iikoFront
	version, err := GetIikoFrontVersion(wu)
	if err != nil {
		return fmt.Errorf("не удалось определить версию iikoFront: %w", err)
	}
	tui.Info(fmt.Sprintf("Обнаружена версия iikoFront: %s", version))

	// Загрузить манифест
	manifest, err := LoadManifest()
	if err != nil {
		return fmt.Errorf("не удалось загрузить манифест: %w", err)
	}

	// Получить совместимые API версии
	compatibleApiVersions := getCompatibleApiVersions(manifest, version)

	// Получить установленные плагины
	installed, err := GetInstalledPlugins()
	if err != nil {
		return fmt.Errorf("не удалось получить список установленных плагинов: %w", err)
	}
	tui.Info(fmt.Sprintf("Установлено плагинов: %d", len(installed)))

	// Фильтровать установленные плагины по excludedPlugins и autoUpdatePlugins
	filteredInstalled := FilterAutoUpdatePlugins(manifest, installed, cfg.IikoConfig.ExcludedPlugins, cfg.IikoConfig.AutoUpdatePlugins, compatibleApiVersions)
	tui.Info(fmt.Sprintf("Плагинов для автообновления после фильтрации: %d", len(filteredInstalled)))

	var updated []string

	for _, pluginName := range filteredInstalled {
		// Найти все версии плагина с этим именем в манифесте
		var pluginVersions []Plugin
		for _, p := range manifest.Plugins {
			if strings.EqualFold(p.Name, pluginName) {
				pluginVersions = append(pluginVersions, p)
			}
		}
		if len(pluginVersions) == 0 {
			tui.Warn(fmt.Sprintf("Плагин %s не найден в манифесте", pluginName))
			continue
		}
		// Выбрать плагин с максимальной plugin_version
		plugin := pluginVersions[0]
		for _, p := range pluginVersions {
			if compareSemanticVersions(p.PluginVersion, plugin.PluginVersion) > 0 {
				plugin = p
			}
		}

		// Найти folderName
		var folderName string
		for _, inst := range installed {
			if strings.Contains(strings.ToLower(inst), strings.ToLower(pluginName)) {
				folderName = inst
				break
			}
		}
		if folderName == "" {
			tui.Warn(fmt.Sprintf("Папка для плагина %s не найдена", pluginName))
			continue
		}

		// Выполнить обновление
		if err := updatePlugin(am, wu, &plugin, cfg.RootPath, folderName); err != nil {
			tui.Warn(fmt.Sprintf("Ошибка обновления плагина %s: %v", pluginName, err))
			continue
		}

		updated = append(updated, pluginName)
		tui.Info(fmt.Sprintf("Плагин %s успешно обновлен", pluginName))
	}

	tui.Info(fmt.Sprintf("Обновлено плагинов: %d", len(updated)))
	return nil
}

// updatePlugin выполняет обновление одного плагина
func updatePlugin(am core.AssetManager, wu core.WinUtils, plugin *Plugin, rootPath, folderName string) error {
	pluginsDir := `C:\Program Files\iiko\iikoRMS\Front.Net\Plugins`
	backupDir := filepath.Join(rootPath, "plugins_backup", folderName)
	tempDir := filepath.Join(rootPath, "temp")

	// Создать директории
	if err := os.MkdirAll(backupDir, 0755); err != nil {
		return fmt.Errorf("не удалось создать директорию бэкапа: %w", err)
	}
	if err := os.MkdirAll(tempDir, 0755); err != nil {
		return fmt.Errorf("не удалось создать временную директорию: %w", err)
	}

	// Бэкап: переместить старую папку в backupDir
	srcDir := filepath.Join(pluginsDir, folderName)
	if err := wu.MoveDir(srcDir, backupDir); err != nil {
		return fmt.Errorf("не удалось переместить старую папку в бэкап: %w", err)
	}

	// Скачать
	zipPath, err := DownloadFile(am, plugin.DownloadUrl, tempDir)
	if err != nil {
		return fmt.Errorf("не удалось скачать плагин: %w", err)
	}

	// Получить путь к 7z.exe
	sevenZip, err := dependencies.NewClient(am, wu)
	if err != nil {
		return fmt.Errorf("не удалось инициализировать клиент 7-Zip: %w", err)
	}

	// Распаковать
	extractDir := filepath.Join(tempDir, strings.TrimSuffix(filepath.Base(zipPath), ".zip"))
	// Создать директорию для распаковки заранее
	if err := os.MkdirAll(extractDir, 0755); err != nil {
		return fmt.Errorf("не удалось создать директорию для распаковки: %w", err)
	}

	// Использовать 7z для распаковки
	if err := sevenZip.Extract(zipPath, extractDir, true); err != nil {
		return fmt.Errorf("не удалось распаковать архив: %w", err)
	}

	// Обработать содержимое распакованной папки
	if err := processExtractedContent(extractDir); err != nil {
		return fmt.Errorf("не удалось обработать содержимое архива: %w", err)
	}

	// Найти папку плагина в extractDir (теперь гарантированно одна)
	entries, err := os.ReadDir(extractDir)
	if err != nil {
		return fmt.Errorf("не удалось прочитать директорию распаковки: %w", err)
	}
	if len(entries) != 1 || !entries[0].IsDir() {
		return fmt.Errorf("неожиданная структура архива после обработки")
	}
	extractedPluginDir := filepath.Join(extractDir, entries[0].Name())

	// Переименовать папку (убрать версию)
	extractedName := entries[0].Name()
	// Найти первое вхождение версии API (V\d+ или V\d+Preview\d+) и обрезать строку после этой версии
	re := regexp.MustCompile(`V\d+(?:Preview\d+)?`)
	loc := re.FindStringIndex(extractedName)
	var newName string
	if loc != nil {
		newName = extractedName[:loc[1]]
	} else {
		newName = extractedName // если не найдено, оставить как есть
	}
	renamedDir := filepath.Join(extractDir, newName)
	// Создать новую папку с правильным именем и скопировать содержимое
	if err := os.MkdirAll(renamedDir, 0755); err != nil {
		return fmt.Errorf("не удалось создать папку для плагина: %w", err)
	}
	if err := wu.CopyDir(extractedPluginDir, renamedDir); err != nil {
		return fmt.Errorf("не удалось скопировать содержимое плагина: %w", err)
	}
	// Удалить старую папку
	if err := os.RemoveAll(extractedPluginDir); err != nil {
		return fmt.Errorf("не удалось удалить старую папку плагина: %w", err)
	}

	// Скопировать .config из бэкапа
	if err := CopyConfigIfExists(wu, backupDir, renamedDir); err != nil {
		return fmt.Errorf("не удалось скопировать .config: %w", err)
	}

	// Переместить в Plugins
	destDir := filepath.Join(pluginsDir, newName)
	// Убедиться, что finalDir не существует
	if _, err := os.Stat(destDir); !os.IsNotExist(err) {
		if err := os.RemoveAll(destDir); err != nil {
			return fmt.Errorf("не удалось удалить старую версию: %w", err)
		}
	}
	if err := wu.MoveDir(renamedDir, destDir); err != nil {
		return fmt.Errorf("не удалось переместить обновленный плагин: %w", err)
	}

	// Очистить temp
	os.RemoveAll(tempDir)

	return nil
}

// installPlugin выполняет установку нового плагина
func installPlugin(am core.AssetManager, wu core.WinUtils, plugin *Plugin, rootPath string) error {
	pluginsDir := `C:\Program Files\iiko\iikoRMS\Front.Net\Plugins`
	tempDir := filepath.Join(rootPath, "temp")

	// Создать временную директорию
	if err := os.MkdirAll(tempDir, 0755); err != nil {
		return fmt.Errorf("не удалось создать временную директорию: %w", err)
	}

	// Скачать
	zipPath, err := DownloadFile(am, plugin.DownloadUrl, tempDir)
	if err != nil {
		return fmt.Errorf("не удалось скачать плагин: %w", err)
	}

	sevenZip, err := dependencies.NewClient(am, wu)
	if err != nil {
		return fmt.Errorf("не удалось инициализировать клиент 7-Zip: %w", err)
	}

	// Распаковать
	extractDir := filepath.Join(tempDir, strings.TrimSuffix(filepath.Base(zipPath), ".zip"))
	// Создать директорию для распаковки заранее
	if err := os.MkdirAll(extractDir, 0755); err != nil {
		return fmt.Errorf("не удалось создать директорию для распаковки: %w", err)
	}

	// Использовать 7z для распаковки
	if err := sevenZip.Extract(zipPath, extractDir, true); err != nil {
		return fmt.Errorf("не удалось распаковать архив: %w", err)
	}

	// Обработать содержимое распакованной папки
	if err := processExtractedContent(extractDir); err != nil {
		return fmt.Errorf("не удалось обработать содержимое архива: %w", err)
	}

	// Найти папку плагина в extractDir (теперь гарантированно одна)
	entries, err := os.ReadDir(extractDir)
	if err != nil {
		return fmt.Errorf("не удалось прочитать директорию распаковки: %w", err)
	}
	if len(entries) != 1 || !entries[0].IsDir() {
		return fmt.Errorf("неожиданная структура архива после обработки")
	}
	extractedPluginDir := filepath.Join(extractDir, entries[0].Name())

	// Переименовать папку (убрать версию)
	extractedName := entries[0].Name()
	// Найти первое вхождение версии API (V\d+ или V\d+Preview\d+) и обрезать строку после этой версии
	re := regexp.MustCompile(`V\d+(?:Preview\d+)?`)
	loc := re.FindStringIndex(extractedName)
	var newName string
	if loc != nil {
		newName = extractedName[:loc[1]]
	} else {
		newName = extractedName // если не найдено, оставить как есть
	}
	renamedDir := filepath.Join(extractDir, newName)
	// Создать новую папку с правильным именем и скопировать содержимое
	if err := os.MkdirAll(renamedDir, 0755); err != nil {
		return fmt.Errorf("не удалось создать папку для плагина: %w", err)
	}
	if err := wu.CopyDir(extractedPluginDir, renamedDir); err != nil {
		return fmt.Errorf("не удалось скопировать содержимое плагина: %w", err)
	}
	// Удалить старую папку
	if err := os.RemoveAll(extractedPluginDir); err != nil {
		return fmt.Errorf("не удалось удалить старую папку плагина: %w", err)
	}

	// Переместить в Plugins
	destDir := filepath.Join(pluginsDir, newName)
	if err := wu.MoveDir(renamedDir, destDir); err != nil {
		return fmt.Errorf("не удалось переместить плагин: %w", err)
	}

	// Очистить temp
	os.RemoveAll(tempDir)

	return nil
}

// showPluginSelectionMenu отображает меню выбора плагина
func showPluginSelectionMenu(pluginGroups map[string][]Plugin, installed []string) (*Plugin, error) {
	reader := bufio.NewReader(os.Stdin)

	for {
		clearScreen()
		fmt.Println(tui.ColorYellow + "==================================================" + tui.ColorReset)
		fmt.Println(tui.ColorYellow + "         ВЫБОР ПЛАГИНА ДЛЯ УСТАНОВКИ              " + tui.ColorReset)
		fmt.Println(tui.ColorYellow + "==================================================" + tui.ColorReset)
		fmt.Println()

		// Собрать список уникальных имен плагинов
		var pluginNames []string
		for name := range pluginGroups {
			pluginNames = append(pluginNames, name)
		}

		// Отсортировать имена плагинов по алфавиту
		sort.Strings(pluginNames)

		for i, name := range pluginNames {
			versions := pluginGroups[name]
			status := ""
			for _, inst := range installed {
				if strings.Contains(strings.ToLower(inst), strings.ToLower(name)) {
					status = " [УСТАНОВЛЕН]"
					break
				}
			}
			if len(versions) > 1 {
				fmt.Printf(tui.ColorYellow+" %d. %s (%d версий)%s"+tui.ColorReset+"\n", i+1, name, len(versions), status)
			} else {
				fmt.Printf(" %d. %s%s\n", i+1, name, status)
			}
		}
		fmt.Println()
		fmt.Println(" Q. Назад")
		fmt.Println()
		fmt.Print("Введите номер пункта и нажмите Enter: ")

		choiceStr, _ := reader.ReadString('\n')
		choiceStr = strings.TrimSpace(choiceStr)

		if strings.EqualFold(choiceStr, "q") {
			return nil, nil // отмена
		}

		choiceInt, err := strconv.Atoi(choiceStr)
		if err != nil || choiceInt < 1 || choiceInt > len(pluginNames) {
			tui.Error("\nНеверный выбор. Нажмите Enter, чтобы попробовать снова.")
			_, _ = reader.ReadString('\n')
			continue
		}

		selectedName := pluginNames[choiceInt-1]
		versions := pluginGroups[selectedName]
		// Возвращаем первый плагин из группы (если одна версия) или nil для дальнейшей обработки
		return &versions[0], nil
	}
}

// selectPluginVersion позволяет выбрать версию плагина
func selectPluginVersion(versions []Plugin) (*Plugin, error) {
	reader := bufio.NewReader(os.Stdin)

	// Найти стабильную версию: среди версий без "Preview" в api_version, сначала найти максимальный api_version, затем среди версий с этим api_version выбрать максимальную plugin_version
	var stable *Plugin
	var stableVersions []Plugin
	for _, v := range versions {
		if !strings.Contains(strings.ToLower(v.ApiVersion), "preview") {
			stableVersions = append(stableVersions, v)
		}
	}
	if len(stableVersions) > 0 {
		// Найти максимальный api_version среди стабильных
		maxApiVer := stableVersions[0].ApiVersion
		for _, v := range stableVersions {
			if compareApiVersions(v.ApiVersion, maxApiVer) > 0 {
				maxApiVer = v.ApiVersion
			}
		}
		// Среди версий с максимальным api_version найти максимальную plugin_version
		var candidates []Plugin
		for _, v := range stableVersions {
			if v.ApiVersion == maxApiVer {
				candidates = append(candidates, v)
			}
		}
		stable = &candidates[0]
		maxPluginVer := candidates[0].PluginVersion
		for _, v := range candidates {
			if compareSemanticVersions(v.PluginVersion, maxPluginVer) > 0 {
				maxPluginVer = v.PluginVersion
				stable = &v
			}
		}
	}

	// Найти последнюю версию: самая большая plugin_version среди всех версий
	latest := versions[0]
	for _, v := range versions {
		if compareSemanticVersions(v.PluginVersion, latest.PluginVersion) > 0 {
			latest = v
		}
	}

	// Если нет стабильной версии, использовать последнюю как стабильную
	if stable == nil {
		stable = &latest
	}

	for {
		clearScreen()
		fmt.Println(tui.ColorYellow + "==================================================" + tui.ColorReset)
		fmt.Println(tui.ColorYellow + "            ВЫБОР ВЕРСИИ ПЛАГИНА                 " + tui.ColorReset)
		fmt.Println(tui.ColorYellow + "==================================================" + tui.ColorReset)
		fmt.Println()
		fmt.Printf("Плагин: %s\n", versions[0].Name)
		fmt.Println()
		fmt.Printf(" 1. Для LTS-версии API: %s\n", stable.PluginVersion)
		fmt.Printf(" 2. Для Preview-версии API: %s\n", latest.PluginVersion)
		fmt.Println()
		fmt.Println(" Q. Назад")
		fmt.Println()
		fmt.Print("Введите номер пункта и нажмите Enter: ")

		choiceStr, _ := reader.ReadString('\n')
		choiceStr = strings.TrimSpace(choiceStr)

		if strings.EqualFold(choiceStr, "q") {
			return nil, nil // отмена
		}

		switch choiceStr {
		case "1":
			return stable, nil
		case "2":
			return &latest, nil
		default:
			tui.Error("\nНеверный выбор. Нажмите Enter, чтобы попробовать снова.")
			_, _ = reader.ReadString('\n')
			continue
		}
	}
}

// compareSemanticVersions сравнивает две семантические версии
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
		} else if num1 < num2 {
			return -1
		}
	}
	return 0
}

// compareApiVersions сравнивает api_version, извлекая число после "V" и сравнивая как int
func compareApiVersions(v1, v2 string) int {
	num1, err1 := strconv.Atoi(strings.TrimPrefix(v1, "V"))
	num2, err2 := strconv.Atoi(strings.TrimPrefix(v2, "V"))
	if err1 != nil {
		num1 = 0
	}
	if err2 != nil {
		num2 = 0
	}
	if num1 > num2 {
		return 1
	} else if num1 < num2 {
		return -1
	}
	return 0
}

// normalizeVersion нормализует версию, убирая из третьего октета все цифры после первой
func normalizeVersion(version string) string {
	parts := strings.Split(version, ".")
	if len(parts) >= 3 && len(parts[2]) > 1 {
		parts[2] = string(parts[2][0])
	}
	return strings.Join(parts, ".")
}

// isVersionInRange проверяет, находится ли версия в диапазоне [min, max]
func isVersionInRange(version, min, max string) bool {
	version = normalizeVersion(version)
	min = normalizeVersion(min)
	if compareSemanticVersions(version, min) < 0 {
		return false
	}
	if max != "" {
		max = normalizeVersion(max)
		if compareSemanticVersions(version, max) > 0 {
			return false
		}
	}
	return true
}

// getCompatibleApiVersions возвращает список api_version, совместимых с frontVersion
func getCompatibleApiVersions(manifest *Manifest, frontVersion string) []string {
	var compatible []string
	for _, compat := range manifest.ApiVersions {
		if isVersionInRange(frontVersion, compat.MinFrontVersion, compat.MaxFrontVersion) {
			compatible = append(compatible, compat.ApiVersion)
		}
	}
	return compatible
}

// contains проверяет, содержит ли слайс строку
func contains(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}

// clearScreen очищает экран
func clearScreen() {
	if runtime.GOOS == "windows" {
		cmd := exec.Command("cmd", "/c", "cls")
		cmd.Stdout = os.Stdout
		_ = cmd.Run()
	} else {
		fmt.Print("\033[H\033[2J")
	}
}

// GetIikoFrontVersion определяет версию iikoFront
func GetIikoFrontVersion(wu core.WinUtils) (string, error) {
	exePath := `C:\Program Files\iiko\iikoRMS\Front.Net\iikoFront.Net.exe`
	version, err := wu.GetFileVersion(exePath)
	if err != nil {
		return "", err
	}
	// Откидываем версию билда (4-й октет)
	parts := strings.Split(version, ".")
	if len(parts) >= 3 {
		return strings.Join(parts[:3], "."), nil
	}
	return version, nil
}

// LoadManifest загружает манифест плагинов из URL
func LoadManifest() (*Manifest, error) {
	url := "https://f.serty.top/distr/installer/plugins-manifest.json"
	resp, err := http.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var manifest Manifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return nil, err
	}

	return &manifest, nil
}

// GetInstalledPlugins собирает список установленных плагинов
func GetInstalledPlugins() ([]string, error) {
	pluginsDir := `C:\Program Files\iiko\iikoRMS\Front.Net\Plugins`
	entries, err := os.ReadDir(pluginsDir)
	if err != nil {
		return nil, err
	}

	var plugins []string
	for _, entry := range entries {
		if entry.IsDir() {
			plugins = append(plugins, entry.Name())
		}
	}
	return plugins, nil
}

// FilterPlugins исключает плагины из списка excluded
func FilterPlugins(plugins []Plugin, excluded []string) []Plugin {
	var filtered []Plugin
	for _, plugin := range plugins {
		exclude := false
		pluginNameLower := strings.ToLower(plugin.Name)
		for _, excl := range excluded {
			if strings.Contains(pluginNameLower, strings.ToLower(excl)) {
				exclude = true
				break
			}
		}
		if !exclude {
			filtered = append(filtered, plugin)
		}
	}
	return filtered
}

// FilterInstalledPlugins возвращает список имен плагинов из манифеста, которые установлены, совместимы и не исключены
func FilterInstalledPlugins(manifest *Manifest, installed []string, excluded []string, compatibleApiVersions []string) []string {
	var filtered []string
	for _, plugin := range manifest.Plugins {
		if !contains(compatibleApiVersions, plugin.ApiVersion) {
			continue
		}
		pluginNameLower := strings.ToLower(plugin.Name)
		exclude := false
		for _, excl := range excluded {
			if strings.Contains(pluginNameLower, strings.ToLower(excl)) {
				exclude = true
				break
			}
		}
		if exclude {
			continue
		}
		// Проверить, установлен ли
		isInstalled := false
		for _, folder := range installed {
			if strings.Contains(strings.ToLower(folder), pluginNameLower) {
				isInstalled = true
				break
			}
		}
		if isInstalled {
			filtered = append(filtered, plugin.Name)
		}
	}
	return filtered
}

// FilterAutoUpdatePlugins возвращает список имен плагинов для автообновления: установлены, совместимы, не исключены и содержат фрагмент из autoUpdatePlugins
func FilterAutoUpdatePlugins(manifest *Manifest, installed []string, excluded []string, autoUpdate []string, compatibleApiVersions []string) []string {
	filteredMap := make(map[string]bool)
	for _, plugin := range manifest.Plugins {
		if !contains(compatibleApiVersions, plugin.ApiVersion) {
			continue
		}
		pluginNameLower := strings.ToLower(plugin.Name)
		exclude := false
		for _, excl := range excluded {
			if strings.Contains(pluginNameLower, strings.ToLower(excl)) {
				exclude = true
				break
			}
		}
		if exclude {
			continue
		}
		// Проверить, установлен ли
		isInstalled := false
		for _, folder := range installed {
			if strings.Contains(strings.ToLower(folder), pluginNameLower) {
				isInstalled = true
				break
			}
		}
		if !isInstalled {
			continue
		}
		// Проверить, содержит ли имя хотя бы одну строку из autoUpdatePlugins
		autoUpdateMatch := false
		for _, au := range autoUpdate {
			if strings.Contains(pluginNameLower, strings.ToLower(au)) {
				autoUpdateMatch = true
				break
			}
		}
		if autoUpdateMatch {
			filteredMap[plugin.Name] = true
		}
	}
	var filtered []string
	for name := range filteredMap {
		filtered = append(filtered, name)
	}
	return filtered
}

// DownloadFile скачивает файл по URL в указанную директорию под оригинальным именем
func DownloadFile(am core.AssetManager, urlStr, dir string) (string, error) {
	u, err := url.Parse(urlStr)
	if err != nil {
		return "", err
	}
	filename := filepath.Base(u.Path)
	localPath := filepath.Join(dir, filename)
	_, err = am.DownloadHTTPWithProgress(urlStr, localPath)
	return localPath, err
}

// processExtractedContent обрабатывает содержимое распакованной папки
func processExtractedContent(extractDir string) error {
	entries, err := os.ReadDir(extractDir)
	if err != nil {
		return fmt.Errorf("не удалось прочитать директорию распаковки: %w", err)
	}
	if len(entries) == 0 {
		return fmt.Errorf("архив пустой")
	}

	extractName := filepath.Base(extractDir) // имя папки, zip_name без .zip

	if len(entries) == 1 && entries[0].IsDir() && entries[0].Name() == extractName {
		// Вложенная папка с тем же именем: переместить содержимое в родительскую
		nestedDir := filepath.Join(extractDir, extractName)
		nestedEntries, err := os.ReadDir(nestedDir)
		if err != nil {
			return fmt.Errorf("не удалось прочитать вложенную директорию: %w", err)
		}
		for _, ne := range nestedEntries {
			src := filepath.Join(nestedDir, ne.Name())
			dest := filepath.Join(extractDir, ne.Name())
			if err := os.Rename(src, dest); err != nil {
				return fmt.Errorf("не удалось переместить %s: %w", ne.Name(), err)
			}
		}
		if err := os.Remove(nestedDir); err != nil {
			return fmt.Errorf("не удалось удалить пустую вложенную директорию: %w", err)
		}
	} else if len(entries) == 1 && !entries[0].IsDir() {
		// Сразу файл: создать папку и переместить
		pluginDir := filepath.Join(extractDir, extractName)
		if err := os.MkdirAll(pluginDir, 0755); err != nil {
			return fmt.Errorf("не удалось создать директорию плагина: %w", err)
		}
		src := filepath.Join(extractDir, entries[0].Name())
		dest := filepath.Join(pluginDir, entries[0].Name())
		if err := os.Rename(src, dest); err != nil {
			return fmt.Errorf("не удалось переместить файл: %w", err)
		}
	} else if len(entries) > 1 {
		// Проверить, есть ли вложенная папка с тем же именем среди нескольких элементов
		var nestedDir string
		found := false
		for _, entry := range entries {
			if entry.IsDir() && entry.Name() == extractName {
				nestedDir = filepath.Join(extractDir, entry.Name())
				found = true
				break
			}
		}
		if found {
			// Переместить содержимое вложенной папки в родительскую
			nestedEntries, err := os.ReadDir(nestedDir)
			if err != nil {
				return fmt.Errorf("не удалось прочитать вложенную директорию: %w", err)
			}
			for _, ne := range nestedEntries {
				src := filepath.Join(nestedDir, ne.Name())
				dest := filepath.Join(extractDir, ne.Name())
				if err := os.Rename(src, dest); err != nil {
					return fmt.Errorf("не удалось переместить %s: %w", ne.Name(), err)
				}
			}
			if err := os.Remove(nestedDir); err != nil {
				return fmt.Errorf("не удалось удалить пустую вложенную директорию: %w", err)
			}
		} else {
			// Архив содержит сразу файлы и папки: создать папку с нужным именем и переместить все содержимое туда
			pluginDir := filepath.Join(extractDir, extractName)
			if err := os.MkdirAll(pluginDir, 0755); err != nil {
				return fmt.Errorf("не удалось создать директорию плагина: %w", err)
			}
			for _, entry := range entries {
				src := filepath.Join(extractDir, entry.Name())
				dest := filepath.Join(pluginDir, entry.Name())
				if err := os.Rename(src, dest); err != nil {
					return fmt.Errorf("не удалось переместить %s: %w", entry.Name(), err)
				}
			}
		}
	}
	// Если len(entries) == 1 и папка с другим именем, оставить как есть
	return nil
}

// CopyConfigIfExists копирует .config файл из srcDir в destDir, если он существует
func CopyConfigIfExists(wu core.WinUtils, srcDir, destDir string) error {
	configFile := filepath.Join(srcDir, ".config")
	if _, err := os.Stat(configFile); os.IsNotExist(err) {
		return nil // файл не существует, ничего не копируем
	}

	destConfig := filepath.Join(destDir, ".config")
	return wu.CopyFile(configFile, destConfig)
}
