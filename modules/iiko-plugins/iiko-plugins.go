package iikoplugins

import (
	"context"
	"encoding/json"
	"fmt"
	"goMH/core"
	"goMH/dependencies"
	"goMH/tui"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/html"
)

// Constants
const (
	DefaultIikoPluginsDir = `C:\Program Files\iiko\iikoRMS\Front.Net\Plugins`
	ManifestURL           = "https://f.serty.top/distr/installer/plugins-manifest.json"
	RapidPluginsBaseURL   = "https://rapid.iiko.ru/plugins/"
	maxScanDepth          = 10
)

var (
	apiVersionRe = regexp.MustCompile(`(V\d+(?:Preview\d+)?)`)
)

// Plugin представляет информацию о плагине
type Plugin struct {
	Name          string `json:"name"`
	ApiVersion    string `json:"api_version"`
	PluginVersion string `json:"plugin_version"`
	DownloadUrl   string `json:"download_url"`
}

// UniquePluginDisplayInfo представляет информацию об уникальном плагине для отображения
type UniquePluginDisplayInfo struct {
	Name        string
	Versions    []Plugin
	DisplayText string
	IsInstalled bool
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

// Module реализует интерфейс core.Installer
type Module struct{}

type InstallSelection struct {
	Plugin            *Plugin
	CurrentFolderName string
	IsUpdate          bool
}

type RapidScanProgress struct {
	ProcessedPages int
	TotalPages     int
	URL            string
}

func (m *Module) ID() string       { return "iiko-plugins" }
func (m *Module) MenuText() string { return "Установка плагинов iiko" }

// Run выполняет интерактивную установку плагинов через меню
func (m *Module) Run(am core.AssetManager, wu core.WinUtils) error {
	slog.Info("Запуск модуля iiko-plugins (интерактивный режим)")
	if !wu.IsAdmin() {
		slog.Warn("Попытка запуска без прав администратора")
		return fmt.Errorf("для установки плагинов требуются права администратора")
	}
	selection, err := m.ConfigureInstall(am, wu)
	if err != nil {
		return err
	}
	if selection == nil {
		return nil
	}
	return m.ExecuteInstall(am, wu, selection)
}

func (m *Module) ConfigureInstall(am core.AssetManager, wu core.WinUtils) (*InstallSelection, error) {
	slog.Info("Подготовка выбора плагина iiko")
	cfg := am.Cfg()

	// 1. Получение версии iikoFront
	version, err := GetIikoFrontVersion(wu)
	if err != nil {
		slog.Error("Ошибка определения версии iikoFront", "error", err)
		return nil, fmt.Errorf("не удалось определить версию iikoFront: %w", err)
	}
	manifest, err := LoadManifest()
	if err != nil {
		slog.Error("Ошибка загрузки манифеста", "error", err)
		return nil, err
	}
	if len(manifest.ApiVersions) == 0 {
		return nil, fmt.Errorf("в манифесте нет таблицы api_compatibility: невозможно определить совместимость API для iikoFront")
	}
	livePlugins, err := tui.RunWithProgress(
		"Установка плагинов iiko",
		"Парсинг источника rapid...",
		func(progress tui.ProgressUpdater) ([]Plugin, error) {
			progress.Update(0, "Парсинг источника rapid...", "")
			return LoadLivePluginsWithProgress(context.Background(), func(state RapidScanProgress) {
				progress.Update(
					rapidScanPercent(state.ProcessedPages, state.TotalPages),
					fmt.Sprintf("Парсинг плагинов: страница %d/%d", state.ProcessedPages, state.TotalPages),
					state.URL,
				)
			})
		},
	)
	if err != nil {
		slog.Error("Ошибка загрузки актуального списка плагинов", "error", err)
		return nil, err
	}
	manifest.Plugins = livePlugins

	// 3. Фильтрация доступных плагинов
	compatibleApiVersions := getCompatibleApiVersions(manifest, version)
	slog.Debug("Определены совместимые версии API", "versions", compatibleApiVersions)

	var availablePlugins []Plugin
	for _, plugin := range manifest.Plugins {
		if contains(compatibleApiVersions, plugin.ApiVersion) {
			availablePlugins = append(availablePlugins, plugin)
		}
	}

	// Фильтрация исключенных
	filteredPlugins := FilterPlugins(availablePlugins, cfg.DistroConfig.ExcludedPlugins)
	slog.Debug("Плагины отфильтрованы", "available_count", len(filteredPlugins))

	// Группировка
	pluginGroups := make(map[string][]Plugin)
	for _, plugin := range filteredPlugins {
		pluginGroups[plugin.Name] = append(pluginGroups[plugin.Name], plugin)
	}

	// Получение установленных
	installed, err := GetInstalledPlugins()
	if err != nil {
		slog.Warn("Не удалось прочитать директорию плагинов", "error", err)
		installed = []string{}
	}

	selectedVersions, err := selectPluginWithSearch(pluginGroups, installed)
	if err != nil {
		if err == tui.ErrExitToMainMenu {
			slog.Info("Пользователь вышел в главное меню")
			return nil, err
		}
		return nil, err
	}
	if len(selectedVersions) == 0 {
		return nil, nil
	}

	selectedPlugin, err := selectPluginVersion(selectedVersions)
	if err != nil || selectedPlugin == nil {
		return nil, err
	}

	slog.Info("Выбран плагин для установки", "name", selectedPlugin.Name, "version", selectedPlugin.PluginVersion)

	// Проверка на наличие установки (для обновления)
	folderName := findInstalledFolderName(installed, selectedPlugin.Name)
	isUpdate := folderName != ""

	return &InstallSelection{
		Plugin:            selectedPlugin,
		CurrentFolderName: folderName,
		IsUpdate:          isUpdate,
	}, nil
}

func (m *Module) ExecuteInstall(am core.AssetManager, wu core.WinUtils, selection *InstallSelection) error {
	if selection == nil || selection.Plugin == nil {
		return fmt.Errorf("не выбрана конфигурация установки плагина")
	}

	opts := deployOptions{
		Plugin:            selection.Plugin,
		RootPath:          am.Cfg().RootPath,
		PluginsDir:        DefaultIikoPluginsDir,
		IsUpdate:          selection.IsUpdate,
		CurrentFolderName: selection.CurrentFolderName,
	}

	if err := deployPlugin(am, wu, opts); err != nil {
		slog.Error("Ошибка при развертывании плагина", "plugin", selection.Plugin.Name, "error", err)
		return err
	}
	return nil
}

// AutoUpdatePlugins выполняет автоматическое обновление плагинов
func (m *Module) AutoUpdatePlugins(ctx core.TaskContext, am core.AssetManager, wu core.WinUtils) error {
	slog.Info("Запуск автоматического обновления плагинов")
	cfg := am.Cfg()

	version, err := GetIikoFrontVersion(wu)
	if err != nil {
		return fmt.Errorf("ошибка версии iikoFront: %w", err)
	}

	manifest, err := LoadManifest()
	if err != nil {
		return err
	}
	if len(manifest.ApiVersions) == 0 {
		return fmt.Errorf("в манифесте нет таблицы api_compatibility: невозможно определить совместимость API для iikoFront")
	}
	if ctx != nil {
		ctx.SetStatus("Парсинг rapid-источника плагинов")
		ctx.SetProgress(0)
	}
	livePlugins, err := LoadLivePluginsWithProgress(taskContextOrBackground(ctx), func(state RapidScanProgress) {
		if ctx == nil {
			return
		}
		ctx.SetStatus(fmt.Sprintf("Парсинг плагинов: страница %d/%d", state.ProcessedPages, state.TotalPages))
		ctx.SetProgress(rapidScanPercent(state.ProcessedPages, state.TotalPages))
	})
	if err != nil {
		return err
	}
	if ctx != nil {
		ctx.SetStatus("Анализ списка плагинов")
	}
	manifest.Plugins = livePlugins

	compatibleApiVersions := getCompatibleApiVersions(manifest, version)
	installed, err := GetInstalledPlugins()
	if err != nil {
		return err
	}

	targets := FilterAutoUpdatePlugins(manifest, installed, cfg.DistroConfig.ExcludedPlugins, cfg.DistroConfig.AutoUpdatePlugins, compatibleApiVersions)
	slog.Info("Цели для автообновления", "count", len(targets), "targets", targets)

	var updatedCount int
	for _, pluginName := range targets {
		// Ищем лучшую версию
		var versions []Plugin
		for _, p := range manifest.Plugins {
			if strings.EqualFold(p.Name, pluginName) {
				versions = append(versions, p)
			}
		}

		// Для автообновления берем "лучшую" версию без вопросов (Stable приоритетнее)
		bestPlugin := selectBestPluginVersion(versions)
		if bestPlugin == nil {
			slog.Warn("Не найдена подходящая версия для автообновления", "plugin", pluginName)
			continue
		}

		folderName := findInstalledFolderName(installed, pluginName)
		if folderName == "" {
			continue
		}

		opts := deployOptions{
			Plugin:            bestPlugin,
			RootPath:          cfg.RootPath,
			PluginsDir:        DefaultIikoPluginsDir,
			IsUpdate:          true,
			CurrentFolderName: folderName,
		}

		slog.Info("Автообновление плагина", "name", pluginName, "version", bestPlugin.PluginVersion)
		if err := deployPlugin(am, wu, opts); err != nil {
			slog.Error("Ошибка автообновления", "plugin", pluginName, "error", err)
			tui.Warn(fmt.Sprintf("Не удалось обновить %s: %v", pluginName, err))
			continue
		}
		updatedCount++
		tui.InfoF("Автообновление: %s -> v%s OK", pluginName, bestPlugin.PluginVersion)
	}

	slog.Info("Автообновление завершено", "updated_count", updatedCount)
	return nil
}

// --- Core Deployment Logic ---

type deployOptions struct {
	Plugin            *Plugin
	RootPath          string // C:\MH
	PluginsDir        string // C:\Program Files\iiko\...\Plugins
	IsUpdate          bool
	CurrentFolderName string // Имя папки существующего плагина (только для update)
}

// deployPlugin - универсальная функция для установки и обновления
func deployPlugin(am core.AssetManager, wu core.WinUtils, opts deployOptions) error {
	slog.Debug("Начало deployPlugin", "plugin", opts.Plugin.Name, "isUpdate", opts.IsUpdate)

	// 1. Подготовка путей
	tempDir := filepath.Join(opts.RootPath, "temp")
	if err := os.MkdirAll(tempDir, 0755); err != nil {
		return fmt.Errorf("ошибка создания temp: %w", err)
	}

	// 2. Скачивание
	zipPath, err := DownloadFile(am, opts.Plugin.DownloadUrl, tempDir)
	if err != nil {
		return fmt.Errorf("скачивание не удалось: %w", err)
	}
	slog.Debug("Файл скачан", "path", zipPath)

	// 3. Распаковка во временную папку
	extractDir := filepath.Join(tempDir, fmt.Sprintf("extract_%s", opts.Plugin.Name))
	// Очистим extractDir на случай мусора
	_ = os.RemoveAll(extractDir)
	if err := os.MkdirAll(extractDir, 0755); err != nil {
		return err
	}

	sevenZip, err := dependencies.NewClient(am, wu)
	if err != nil {
		return err
	}

	slog.Debug("Распаковка архива", "dest", extractDir)
	if err := sevenZip.Extract(zipPath, extractDir, true); err != nil {
		return fmt.Errorf("ошибка распаковки: %w", err)
	}

	// 4. Анализ и нормализация распакованного
	entries, err := os.ReadDir(extractDir)
	if err != nil {
		return fmt.Errorf("не удалось прочитать директорию распаковки: %w", err)
	}

	var pluginContentDir string
	var rawName string
	zipBaseName := strings.TrimSuffix(filepath.Base(zipPath), ".zip")

	// Определение: это архив с одной папкой (стандарт) или "плоский" архив?
	if len(entries) == 1 && entries[0].IsDir() {
		// Стандартный случай: внутри папка с плагином
		rawName = entries[0].Name()
		pluginContentDir = filepath.Join(extractDir, rawName)
		slog.Debug("Архив содержит папку", "rawName", rawName)
	} else {
		// Плоский архив: файлы в корне. Используем имя ZIP-файла как базу.
		rawName = zipBaseName
		pluginContentDir = extractDir
		slog.Debug("Архив плоский, используем имя ZIP-файла", "rawName", rawName)
	}

	// Нормализация имени: берем rawName и отрезаем версию плагина, оставляя API версию
	cleanName := normalizePluginDirName(rawName)
	slog.Debug("Имя папки плагина рассчитано", "original", rawName, "normalized", cleanName)

	// 5. Логика обновления (Бэкап + Конфиг)
	if opts.IsUpdate {
		backupRoot := filepath.Join(opts.RootPath, "plugins_backup")
		currentInstallPath := filepath.Join(opts.PluginsDir, opts.CurrentFolderName)

		// Бэкап
		slog.Debug("Бэкап плагина", "src", currentInstallPath, "dest_root", backupRoot)
		if err := backupPlugin(wu, currentInstallPath, backupRoot, opts.CurrentFolderName); err != nil {
			return err
		}

		// Сохранение конфига: копируем .config из бэкапа в новую распакованную папку
		backupPath := filepath.Join(backupRoot, opts.CurrentFolderName)
		if err := restoreConfig(wu, backupPath, pluginContentDir); err != nil {
			slog.Warn("Не удалось перенести конфиг (возможно его нет)", "error", err)
		}

		// Удаляем старую версию из Plugins
		slog.Debug("Удаление старой версии", "path", currentInstallPath)
		if err := os.RemoveAll(currentInstallPath); err != nil {
			return fmt.Errorf("не удалось удалить старую версию: %w", err)
		}
	}

	// 6. Перемещение новой версии в целевую директорию
	finalDestPath := filepath.Join(opts.PluginsDir, cleanName)

	if !opts.IsUpdate {
		if _, err := os.Stat(finalDestPath); err == nil {
			slog.Info("Целевая папка существует, удаляем перед установкой", "path", finalDestPath)
			_ = os.RemoveAll(finalDestPath)
		}
	}

	slog.Info("Установка файлов плагина", "from", pluginContentDir, "to", finalDestPath)
	if err := wu.CopyDir(pluginContentDir, finalDestPath); err != nil {
		return fmt.Errorf("ошибка копирования в Program Files: %w", err)
	}

	// 7. Очистка
	_ = os.RemoveAll(tempDir)
	return nil
}

// --- Helper Functions ---

func findInstalledFolderName(installed []string, pluginName string) string {
	lowerName := strings.ToLower(pluginName)
	for _, folder := range installed {
		if strings.Contains(strings.ToLower(folder), lowerName) {
			return folder
		}
	}
	return ""
}

func normalizePluginDirName(name string) string {
	// Паттерн ищет версию API: V + цифры + опционально Preview + цифры
	re := regexp.MustCompile(`V\d+(?:Preview\d+)?`)
	loc := re.FindStringIndex(name)

	if loc != nil {
		// loc[1] - это конец совпадения версии API.
		// Мы хотим оставить всё до этого места включительно.
		// Пример: "Name.V9Preview4.1.2.39" -> loc="V9Preview4" -> loc[1] указывает на конец "4".
		// Результат: "Name.V9Preview4"
		return name[:loc[1]]
	}

	if strings.HasPrefix(name, "extract_") {
		return strings.TrimPrefix(name, "extract_")
	}

	return name
}

func backupPlugin(wu core.WinUtils, src, backupRoot, folderName string) error {
	dest := filepath.Join(backupRoot, folderName)
	if err := os.MkdirAll(dest, 0755); err != nil {
		return fmt.Errorf("ошибка создания папки бэкапа: %w", err)
	}
	_ = os.RemoveAll(dest)
	if err := wu.CopyDir(src, dest); err != nil {
		return fmt.Errorf("ошибка копирования в бэкап: %w", err)
	}
	return nil
}

func restoreConfig(wu core.WinUtils, backupSrc, newVersionDir string) error {
	configFile := filepath.Join(backupSrc, ".config")
	if _, err := os.Stat(configFile); os.IsNotExist(err) {
		return nil
	}
	destConfig := filepath.Join(newVersionDir, ".config")
	slog.Debug("Восстановление конфига", "from", configFile, "to", destConfig)
	return wu.CopyFile(configFile, destConfig)
}

func DownloadFile(am core.AssetManager, urlStr, dir string) (string, error) {
	u, err := url.Parse(urlStr)
	if err != nil {
		return "", err
	}
	filename := filepath.Base(u.Path)
	localPath := filepath.Join(dir, filename)
	slog.Debug("Скачивание файла", "url", urlStr, "local", localPath)
	_, err = am.DownloadHTTPWithProgress(urlStr, localPath)
	return localPath, err
}

func GetIikoFrontVersion(wu core.WinUtils) (string, error) {
	exePath := `C:\Program Files\iiko\iikoRMS\Front.Net\iikoFront.Net.exe`
	version, err := wu.GetFileVersion(exePath)
	if err != nil {
		return "", err
	}
	parts := strings.Split(version, ".")
	if len(parts) >= 3 {
		return strings.Join(parts[:3], "."), nil
	}
	return version, nil
}

func LoadManifest() (*Manifest, error) {
	slog.Debug("Загрузка манифеста плагинов", "url", ManifestURL)
	resp, err := http.Get(ManifestURL)
	if err != nil {
		return nil, fmt.Errorf("ошибка сети: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("bad status: %s", resp.Status)
	}

	var manifest Manifest
	if err := json.NewDecoder(resp.Body).Decode(&manifest); err != nil {
		return nil, fmt.Errorf("ошибка JSON: %w", err)
	}
	return &manifest, nil
}

func LoadLivePlugins() ([]Plugin, error) {
	return LoadLivePluginsWithProgress(context.Background(), nil)
}

func LoadLivePluginsWithProgress(ctx context.Context, report func(RapidScanProgress)) ([]Plugin, error) {
	zipURLs, err := scanPluginZipFilesWithProgress(ctx, RapidPluginsBaseURL, maxScanDepth, report)
	if err != nil {
		return nil, err
	}

	plugins := make([]Plugin, 0, len(zipURLs))
	seen := make(map[string]struct{}, len(zipURLs))
	for _, zipURL := range zipURLs {
		plugin, ok := parsePluginFromZipURL(zipURL)
		if !ok {
			continue
		}
		if _, exists := seen[plugin.DownloadUrl]; exists {
			continue
		}
		seen[plugin.DownloadUrl] = struct{}{}
		plugins = append(plugins, plugin)
	}

	if len(plugins) == 0 {
		return nil, fmt.Errorf("не удалось получить список плагинов с %s", RapidPluginsBaseURL)
	}
	return plugins, nil
}

func scanPluginZipFiles(ctx context.Context, startURL string, depthLimit int, report func(RapidScanProgress)) ([]string, error) {
	type queueItem struct {
		url   string
		depth int
	}

	client := &http.Client{Timeout: 15 * time.Second}
	queue := []queueItem{{url: startURL, depth: 0}}
	visited := map[string]struct{}{}
	discovered := map[string]struct{}{startURL: {}}
	zipSet := map[string]struct{}{}
	totalPages := 1
	processedPages := 0

	for len(queue) > 0 {
		if err := ctx.Err(); err != nil {
			return nil, err
		}

		item := queue[0]
		queue = queue[1:]

		if item.depth > depthLimit {
			continue
		}
		if _, ok := visited[item.url]; ok {
			continue
		}
		visited[item.url] = struct{}{}
		processedPages++
		tui.InfoF("Парсинг плагинов: страница %d/%d -> %s", processedPages, totalPages, item.url)

		dirs, zips, err := readDirectoryListing(client, item.url)
		if err != nil {
			slog.Warn("Не удалось прочитать каталог плагинов", "url", item.url, "error", err)
			continue
		}
		for _, zipURL := range zips {
			zipSet[zipURL] = struct{}{}
		}
		for _, dirURL := range dirs {
			if _, ok := discovered[dirURL]; ok {
				continue
			}
			discovered[dirURL] = struct{}{}
			totalPages++
			queue = append(queue, queueItem{url: dirURL, depth: item.depth + 1})
		}
	}

	if len(zipSet) == 0 {
		return nil, fmt.Errorf("не найдено ни одного zip-плагина по адресу %s", startURL)
	}

	zips := make([]string, 0, len(zipSet))
	for zipURL := range zipSet {
		zips = append(zips, zipURL)
	}
	sort.Strings(zips)
	return zips, nil
}

func scanPluginZipFilesWithProgress(ctx context.Context, startURL string, depthLimit int, report func(RapidScanProgress)) ([]string, error) {
	type queueItem struct {
		url   string
		depth int
	}

	client := &http.Client{Timeout: 15 * time.Second}
	queue := []queueItem{{url: startURL, depth: 0}}
	visited := map[string]struct{}{}
	discovered := map[string]struct{}{startURL: {}}
	zipSet := map[string]struct{}{}
	totalPages := 1
	processedPages := 0

	for len(queue) > 0 {
		if err := ctx.Err(); err != nil {
			return nil, err
		}

		item := queue[0]
		queue = queue[1:]

		if item.depth > depthLimit {
			continue
		}
		if _, ok := visited[item.url]; ok {
			continue
		}

		visited[item.url] = struct{}{}
		processedPages++

		dirs, zips, err := readDirectoryListing(client, item.url)
		if err != nil {
			reportRapidScanProgress(report, processedPages, totalPages, item.url)
			slog.Warn("Не удалось прочитать каталог плагинов", "url", item.url, "error", err)
			continue
		}

		for _, zipURL := range zips {
			zipSet[zipURL] = struct{}{}
		}
		for _, dirURL := range dirs {
			if _, ok := discovered[dirURL]; ok {
				continue
			}
			discovered[dirURL] = struct{}{}
			totalPages++
			queue = append(queue, queueItem{url: dirURL, depth: item.depth + 1})
		}

		reportRapidScanProgress(report, processedPages, totalPages, item.url)
	}

	if len(zipSet) == 0 {
		return nil, fmt.Errorf("не найдено ни одного zip-плагина по адресу %s", startURL)
	}

	zips := make([]string, 0, len(zipSet))
	for zipURL := range zipSet {
		zips = append(zips, zipURL)
	}
	sort.Strings(zips)
	return zips, nil
}

func reportRapidScanProgress(report func(RapidScanProgress), processedPages int, totalPages int, pageURL string) {
	if report == nil {
		return
	}
	report(RapidScanProgress{
		ProcessedPages: processedPages,
		TotalPages:     totalPages,
		URL:            pageURL,
	})
}

func rapidScanPercent(processedPages int, totalPages int) int {
	if totalPages <= 0 {
		return 0
	}
	if processedPages >= totalPages {
		return 100
	}
	percent := (processedPages * 100) / totalPages
	if percent < 0 {
		return 0
	}
	if percent > 100 {
		return 100
	}
	return percent
}

func taskContextOrBackground(ctx core.TaskContext) context.Context {
	if ctx == nil || ctx.Context() == nil {
		return context.Background()
	}
	return ctx.Context()
}

func readDirectoryListing(client *http.Client, pageURL string) ([]string, []string, error) {
	req, err := http.NewRequest(http.MethodGet, pageURL, nil)
	if err != nil {
		return nil, nil, err
	}
	req.Header.Set("User-Agent", "goMH/1.0")

	resp, err := client.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, nil, fmt.Errorf("bad status: %s", resp.Status)
	}

	var dirs []string
	var zips []string
	root, err := html.Parse(resp.Body)
	if err != nil {
		return nil, nil, err
	}

	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && strings.EqualFold(n.Data, "a") {
			href := ""
			for _, attr := range n.Attr {
				if strings.EqualFold(attr.Key, "href") {
					href = strings.TrimSpace(attr.Val)
					break
				}
			}

			label := strings.ToLower(strings.TrimSpace(nodeText(n)))
			if href != "" && label != "parent directory" {
				fullURL, err := resolveURL(pageURL, href)
				if err == nil {
					if strings.HasSuffix(href, "/") {
						dirs = append(dirs, fullURL)
					} else if strings.HasSuffix(strings.ToLower(href), ".zip") {
						zips = append(zips, fullURL)
					}
				}
			}
		}

		for child := n.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(root)

	return dirs, zips, nil
}

func nodeText(n *html.Node) string {
	if n == nil {
		return ""
	}

	var b strings.Builder
	var walk func(*html.Node)
	walk = func(node *html.Node) {
		if node.Type == html.TextNode {
			b.WriteString(node.Data)
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(n)
	return b.String()
}

func resolveURL(base, href string) (string, error) {
	baseURL, err := url.Parse(base)
	if err != nil {
		return "", err
	}
	parsedHref, err := url.Parse(href)
	if err != nil {
		return "", err
	}
	return baseURL.ResolveReference(parsedHref).String(), nil
}

func parsePluginFromZipURL(zipURL string) (Plugin, bool) {
	parsed, err := url.Parse(zipURL)
	if err != nil {
		return Plugin{}, false
	}

	filename := filepath.Base(parsed.Path)
	if !strings.HasSuffix(strings.ToLower(filename), ".zip") {
		return Plugin{}, false
	}
	if !strings.HasPrefix(filename, "Resto.Front.Api.") {
		return Plugin{}, false
	}

	core := strings.TrimSuffix(strings.TrimPrefix(filename, "Resto.Front.Api."), ".zip")
	match := apiVersionRe.FindStringSubmatchIndex(core)
	if len(match) < 4 {
		return Plugin{}, false
	}

	apiVersion := core[match[2]:match[3]]
	name := strings.TrimRight(core[:match[0]], ".")
	pluginVersion := strings.TrimLeft(core[match[1]:], ".")
	if name == "" {
		return Plugin{}, false
	}

	return Plugin{
		Name:          name,
		ApiVersion:    apiVersion,
		PluginVersion: pluginVersion,
		DownloadUrl:   zipURL,
	}, true
}

func GetInstalledPlugins() ([]string, error) {
	entries, err := os.ReadDir(DefaultIikoPluginsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return []string{}, nil
		}
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

// --- Logic helpers ---

func getCompatibleApiVersions(manifest *Manifest, frontVersion string) []string {
	var compatible []string
	for _, compat := range manifest.ApiVersions {
		if isVersionInRange(frontVersion, compat.MinFrontVersion, compat.MaxFrontVersion) {
			compatible = append(compatible, compat.ApiVersion)
		}
	}
	return compatible
}

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

func normalizeVersion(version string) string {
	parts := strings.Split(version, ".")
	if len(parts) >= 3 && len(parts[2]) > 1 {
		parts[2] = string(parts[2][0])
	}
	return strings.Join(parts, ".")
}

func compareSemanticVersions(v1, v2 string) int {
	p1 := strings.Split(v1, ".")
	p2 := strings.Split(v2, ".")
	maxLen := len(p1)
	if len(p2) > maxLen {
		maxLen = len(p2)
	}
	for i := 0; i < maxLen; i++ {
		n1, n2 := 0, 0
		if i < len(p1) {
			n1, _ = strconv.Atoi(p1[i])
		}
		if i < len(p2) {
			n2, _ = strconv.Atoi(p2[i])
		}
		if n1 > n2 {
			return 1
		}
		if n1 < n2 {
			return -1
		}
	}
	return 0
}

func FilterPlugins(plugins []Plugin, excluded []string) []Plugin {
	var res []Plugin
	for _, p := range plugins {
		if !isExcluded(p.Name, excluded) {
			res = append(res, p)
		}
	}
	return res
}

func FilterAutoUpdatePlugins(manifest *Manifest, installed, excluded, autoUpdate, apiVersions []string) []string {
	resMap := make(map[string]bool)
	for _, p := range manifest.Plugins {
		if !contains(apiVersions, p.ApiVersion) || isExcluded(p.Name, excluded) {
			continue
		}
		if findInstalledFolderName(installed, p.Name) == "" {
			continue
		}
		for _, pattern := range autoUpdate {
			if strings.Contains(strings.ToLower(p.Name), strings.ToLower(pattern)) {
				resMap[p.Name] = true
				break
			}
		}
	}
	var res []string
	for k := range resMap {
		res = append(res, k)
	}
	return res
}

func isExcluded(name string, excluded []string) bool {
	lower := strings.ToLower(name)
	for _, ex := range excluded {
		if strings.Contains(lower, strings.ToLower(ex)) {
			return true
		}
	}
	return false
}

func contains(slice []string, val string) bool {
	for _, item := range slice {
		if item == val {
			return true
		}
	}
	return false
}

// UI Selection Helpers

// selectPluginWithSearch возвращает СПИСОК версий для выбранного плагина
func selectPluginWithSearch(groups map[string][]Plugin, installed []string) ([]Plugin, error) {
	var displayItems []UniquePluginDisplayInfo
	for name, versions := range groups {
		isInst := findInstalledFolderName(installed, name) != ""
		displayItems = append(displayItems, UniquePluginDisplayInfo{
			Name:        name,
			Versions:    versions,
			IsInstalled: isInst,
			DisplayText: fmt.Sprintf("%s %s", name, formatVersionsInfo(versions, isInst)),
		})
	}
	sort.Slice(displayItems, func(i, j int) bool {
		return strings.ToLower(displayItems[i].Name) < strings.ToLower(displayItems[j].Name)
	})

	var stringsList []string
	for _, d := range displayItems {
		stringsList = append(stringsList, d.DisplayText)
	}

	choice, err := tui.SelectWithSearch(stringsList, "Выберите плагин (поиск по имени):")
	if err != nil {
		return nil, err
	}

	for _, d := range displayItems {
		if d.DisplayText == choice {
			return d.Versions, nil
		}
	}
	return nil, fmt.Errorf("выбор не найден")
}

func formatVersionsInfo(versions []Plugin, installed bool) string {
	status := ""
	if installed {
		status = "[УСТАНОВЛЕН] "
	}
	if len(versions) == 1 {
		return fmt.Sprintf("%s(v%s API:%s)", status, versions[0].PluginVersion, versions[0].ApiVersion)
	}
	return fmt.Sprintf("%s(%d версий)", status, len(versions))
}

// selectPluginVersion запрашивает у пользователя выбор среди всех доступных версий
func selectPluginVersion(versions []Plugin) (*Plugin, error) {
	if len(versions) == 0 {
		return nil, nil
	}

	sorted := append([]Plugin(nil), versions...)
	sort.SliceStable(sorted, func(i, j int) bool {
		if cmp := compareSemanticVersions(sorted[i].PluginVersion, sorted[j].PluginVersion); cmp != 0 {
			return cmp > 0
		}
		return compareApiVersions(sorted[i].ApiVersion, sorted[j].ApiVersion) > 0
	})

	latestVersion := sorted[0].PluginVersion
	items := make([]tui.ChoiceItem, 0, len(sorted))
	for _, plugin := range sorted {
		description := fmt.Sprintf("API %s", plugin.ApiVersion)
		if plugin.PluginVersion == latestVersion {
			description += " | новейшая"
		}
		items = append(items, tui.ChoiceItem{
			Title:       "v" + plugin.PluginVersion,
			Description: description,
			Meta:        plugin.Name,
			FilterValue: strings.ToLower(plugin.Name + " " + plugin.PluginVersion + " " + plugin.ApiVersion),
		})
	}

	index, err := tui.SelectItem(items, tui.SelectionConfig{
		Title:    "Выберите версию плагина",
		Subtitle: sorted[0].Name,
	})
	if err != nil {
		return nil, err
	}
	if index < 0 || index >= len(sorted) {
		return nil, nil
	}
	return &sorted[index], nil
}

// selectBestPluginVersion (для автообновления) - предпочитает Stable, иначе берет Latest
func selectBestPluginVersion(versions []Plugin) *Plugin {
	if len(versions) == 0 {
		return nil
	}

	// Ищем стабильную
	var stable *Plugin
	for i := range versions {
		if !strings.Contains(strings.ToLower(versions[i].ApiVersion), "preview") {
			if stable == nil || compareSemanticVersions(versions[i].PluginVersion, stable.PluginVersion) > 0 {
				v := versions[i]
				stable = &v
			}
		}
	}

	if stable != nil {
		return stable
	}

	// Если стабильных нет, берем самую свежую из всех
	latest := &versions[0]
	for i := range versions {
		if compareSemanticVersions(versions[i].PluginVersion, latest.PluginVersion) > 0 {
			latest = &versions[i]
		}
	}
	return latest
}

func compareApiVersions(v1, v2 string) int {
	// Упрощенное сравнение: V9 > V8
	// Извлекаем цифры после V
	re := regexp.MustCompile(`V(\d+)`)
	m1 := re.FindStringSubmatch(v1)
	m2 := re.FindStringSubmatch(v2)

	n1, n2 := 0, 0
	if len(m1) > 1 {
		n1, _ = strconv.Atoi(m1[1])
	}
	if len(m2) > 1 {
		n2, _ = strconv.Atoi(m2[1])
	}

	if n1 > n2 {
		return 1
	}
	if n1 < n2 {
		return -1
	}
	return 0
}
