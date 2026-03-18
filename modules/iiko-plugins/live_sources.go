package iikoplugins

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"goMH/config"
	"goMH/core"
	"io"
	"log/slog"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/jlaffaye/ftp"
)

const ftpDirectoryScheme = "ftpdir"

const livePluginsCacheSource = "cache"

type livePluginsCache struct {
	FrontVersion string    `json:"front_version"`
	SavedAt      time.Time `json:"saved_at"`
	Plugins      []Plugin  `json:"plugins"`
}

func LoadLivePlugins(frontVersion string) ([]Plugin, error) {
	return LoadLivePluginsWithProgress(context.Background(), frontVersion, nil)
}

func LoadCachedLivePluginsWithProgress(ctx context.Context, rootPath, frontVersion string, report func(RapidScanProgress)) ([]Plugin, error) {
	cachePath := livePluginsCachePath(rootPath, frontVersion)
	if plugins, err := readLivePluginsCache(cachePath, frontVersion); err == nil {
		slog.Info("Используем локальный кэш live-плагинов", "path", cachePath, "front_version", frontVersion, "count", len(plugins))
		reportPluginScanProgress(report, livePluginsCacheSource, 1, 1, cachePath)
		return plugins, nil
	} else if err != nil && !os.IsNotExist(err) {
		slog.Warn("Не удалось прочитать локальный кэш live-плагинов, выполняем сетевой парсинг", "path", cachePath, "error", err)
	}

	plugins, err := LoadLivePluginsWithProgress(ctx, frontVersion, report)
	if err != nil {
		return nil, err
	}
	if err := writeLivePluginsCache(cachePath, frontVersion, plugins); err != nil {
		slog.Warn("Не удалось сохранить локальный кэш live-плагинов", "path", cachePath, "error", err)
	}
	return plugins, nil
}

func LoadLivePluginsWithProgress(ctx context.Context, frontVersion string, report func(RapidScanProgress)) ([]Plugin, error) {
	rapidPlugins, rapidErr := loadRapidPluginsWithProgress(ctx, reportRapidSource(report))
	ftpPlugins, ftpErr := loadFTPPluginsWithProgress(ctx, frontVersion, report)

	switch {
	case rapidErr == nil && ftpErr == nil:
	case rapidErr == nil:
		slog.Warn("FTP-источник плагинов недоступен, продолжаем с Rapid", "error", ftpErr)
	case ftpErr == nil:
		slog.Warn("Rapid-источник плагинов недоступен, продолжаем с FTP", "error", rapidErr)
	default:
		return nil, fmt.Errorf("не удалось получить список плагинов: rapid: %w; ftp: %w", rapidErr, ftpErr)
	}

	plugins := mergePluginsPreferPrimary(rapidPlugins, ftpPlugins)
	if len(plugins) == 0 {
		return nil, fmt.Errorf("не удалось получить список плагинов из доступных источников")
	}
	return plugins, nil
}

func GetIikoFrontVersions(wu core.WinUtils) (string, string, error) {
	fullVersion, err := GetIikoFrontFullVersion(wu)
	if err != nil {
		return "", "", err
	}
	shortVersion := fullVersion
	parts := strings.Split(fullVersion, ".")
	if len(parts) >= 3 {
		shortVersion = strings.Join(parts[:3], ".")
	}
	return shortVersion, fullVersion, nil
}

func GetIikoFrontFullVersion(wu core.WinUtils) (string, error) {
	exePath := `C:\Program Files\iiko\iikoRMS\Front.Net\iikoFront.Net.exe`
	return wu.GetFileVersion(exePath)
}

func liveScanPercent(state RapidScanProgress) int {
	percent := rapidScanPercent(state.ProcessedPages, state.TotalPages)
	switch state.Source {
	case livePluginsCacheSource:
		return 100
	case string(PluginSourceRapid):
		return percent / 2
	case string(PluginSourceFTP):
		return 50 + percent/2
	default:
		return percent
	}
}

func liveScanStatus(state RapidScanProgress) string {
	switch state.Source {
	case livePluginsCacheSource:
		return "Чтение локального кэша плагинов"
	case string(PluginSourceFTP):
		return fmt.Sprintf("Парсинг FTP-плагинов: %d/%d", state.ProcessedPages, state.TotalPages)
	case string(PluginSourceRapid):
		return fmt.Sprintf("Парсинг Rapid-плагинов: %d/%d", state.ProcessedPages, state.TotalPages)
	default:
		return fmt.Sprintf("Парсинг плагинов: %d/%d", state.ProcessedPages, state.TotalPages)
	}
}

func filterCompatiblePlugins(plugins []Plugin, compatibleAPIVersions []string, frontVersion string) []Plugin {
	var filtered []Plugin
	for _, plugin := range plugins {
		if isPluginCompatible(plugin, compatibleAPIVersions, frontVersion) {
			filtered = append(filtered, plugin)
		}
	}
	return filtered
}

func isPluginCompatible(plugin Plugin, compatibleAPIVersions []string, frontVersion string) bool {
	if plugin.Source == PluginSourceFTP {
		return strings.EqualFold(plugin.FrontVersion, frontVersion)
	}
	return contains(compatibleAPIVersions, plugin.ApiVersion)
}

func FilterAutoUpdatePluginsForFront(manifest *Manifest, installed, excluded, autoUpdate, apiVersions []string, frontVersion string) []string {
	resMap := make(map[string]bool)
	for _, p := range manifest.Plugins {
		if !isPluginCompatible(p, apiVersions, frontVersion) || isExcluded(p.Name, excluded) {
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
	for name := range resMap {
		res = append(res, name)
	}
	return res
}

func mergePluginsPreferPrimary(primary, fallback []Plugin) []Plugin {
	merged := append([]Plugin(nil), primary...)
	seen := make(map[string]struct{}, len(primary))
	for _, plugin := range primary {
		seen[pluginIdentityKey(plugin)] = struct{}{}
	}
	for _, plugin := range fallback {
		if _, exists := seen[pluginIdentityKey(plugin)]; exists {
			continue
		}
		seen[pluginIdentityKey(plugin)] = struct{}{}
		merged = append(merged, plugin)
	}
	return merged
}

func pluginIdentityKey(plugin Plugin) string {
	return strings.ToLower(strings.Join([]string{
		strings.TrimSpace(plugin.Name),
		strings.TrimSpace(plugin.ApiVersion),
		strings.TrimSpace(plugin.PluginVersion),
	}, "|"))
}

func reportRapidSource(report func(RapidScanProgress)) func(RapidScanProgress) {
	if report == nil {
		return nil
	}
	return func(state RapidScanProgress) {
		state.Source = string(PluginSourceRapid)
		report(state)
	}
}

func loadFTPPluginsWithProgress(ctx context.Context, frontVersion string, report func(RapidScanProgress)) ([]Plugin, error) {
	frontVersion = strings.TrimSpace(frontVersion)
	if frontVersion == "" {
		return nil, fmt.Errorf("full iikoFront version is required for FTP plugin source")
	}

	conn, err := dialOfficialIikoFTP()
	if err != nil {
		return nil, err
	}
	defer conn.Quit()

	frontPath := officialIikoPluginsFrontPath(frontVersion)
	entries, err := conn.List(frontPath)
	if err != nil {
		return nil, err
	}

	sort.Slice(entries, func(i, j int) bool {
		return strings.ToLower(entries[i].Name) < strings.ToLower(entries[j].Name)
	})

	plugins := make([]Plugin, 0, len(entries))
	seen := make(map[string]struct{}, len(entries))
	total := len(entries)
	for idx, entry := range entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}

		currentPath := path.Join(frontPath, entry.Name)
		plugin, ok, err := parsePluginFromFTPEntry(conn, frontVersion, currentPath, entry)
		reportPluginScanProgress(report, string(PluginSourceFTP), idx+1, total, currentPath)
		if err != nil {
			slog.Warn("Не удалось распарсить FTP-плагин", "path", currentPath, "error", err)
			continue
		}
		if !ok {
			continue
		}

		key := pluginIdentityKey(plugin)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		plugins = append(plugins, plugin)
	}

	if len(plugins) == 0 {
		return nil, fmt.Errorf("не удалось получить список FTP-плагинов из %s", frontPath)
	}
	return plugins, nil
}

func parsePluginFromFTPEntry(conn *ftp.ServerConn, frontVersion, remotePath string, entry *ftp.Entry) (Plugin, bool, error) {
	switch entry.Type {
	case ftp.EntryTypeFile:
		return parsePluginFromFTPArchive(frontVersion, remotePath), true, nil
	case ftp.EntryTypeFolder:
		return parsePluginFromFTPDirectory(conn, frontVersion, remotePath, entry.Name)
	default:
		return Plugin{}, false, nil
	}
}

func parsePluginFromFTPArchive(frontVersion, remotePath string) Plugin {
	filename := path.Base(remotePath)
	baseName := strings.TrimSuffix(filename, path.Ext(filename))
	name := normalizePluginName(baseName)
	apiVersion := ""
	pluginVersion := ""

	if match := apiVersionRe.FindStringIndex(baseName); match != nil {
		apiVersion = baseName[match[0]:match[1]]
		name = normalizePluginName(strings.TrimRight(baseName[:match[0]], ".-"))
		pluginVersion = parseArchiveVersion(strings.TrimLeft(baseName[match[1]:], ".-"))
	}
	if pluginVersion == "" {
		if match := ftpArchiveNameRe.FindStringSubmatch(baseName); len(match) == 3 {
			name = normalizePluginName(match[1])
			pluginVersion = match[2]
		}
	}

	return Plugin{
		Name:          name,
		ApiVersion:    apiVersion,
		PluginVersion: pluginVersion,
		DownloadUrl:   officialFTPFileURL(remotePath),
		Source:        PluginSourceFTP,
		RemotePath:    remotePath,
		FrontVersion:  frontVersion,
	}
}

func parsePluginFromFTPDirectory(conn *ftp.ServerConn, frontVersion, remotePath, dirName string) (Plugin, bool, error) {
	entries, err := conn.List(remotePath)
	if err != nil {
		return Plugin{}, false, err
	}

	manifestPath := ""
	for _, entry := range entries {
		if entry.Type != ftp.EntryTypeFile {
			continue
		}
		if strings.EqualFold(entry.Name, "manifest.xml") {
			manifestPath = path.Join(remotePath, entry.Name)
			break
		}
	}
	if manifestPath == "" {
		return Plugin{}, false, nil
	}

	manifest, err := readFTPPluginManifest(conn, manifestPath)
	if err != nil {
		return Plugin{}, false, err
	}

	baseName := dirName
	if strings.TrimSpace(manifest.FileName) != "" {
		baseName = strings.TrimSuffix(manifest.FileName, filepath.Ext(manifest.FileName))
	}

	return Plugin{
		Name:          normalizePluginName(baseName),
		ApiVersion:    strings.TrimSpace(manifest.ApiVersion),
		PluginVersion: "",
		DownloadUrl:   officialFTPDirectoryURL(remotePath),
		Source:        PluginSourceFTP,
		RemotePath:    remotePath,
		FrontVersion:  frontVersion,
		InstallDir:    dirName,
		IsDirectory:   true,
	}, true, nil
}

func readFTPPluginManifest(conn *ftp.ServerConn, remotePath string) (ftpPluginManifest, error) {
	data, err := readFTPFile(conn, remotePath)
	if err != nil {
		return ftpPluginManifest{}, err
	}

	var manifest ftpPluginManifest
	if err := xml.Unmarshal(bytes.TrimSpace(data), &manifest); err != nil {
		return ftpPluginManifest{}, err
	}
	return manifest, nil
}

func readFTPFile(conn *ftp.ServerConn, remotePath string) ([]byte, error) {
	reader, err := conn.Retr(remotePath)
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	return io.ReadAll(reader)
}

func normalizePluginName(name string) string {
	name = strings.TrimSpace(name)
	for _, prefix := range []string{
		"Resto.Front.Api.",
		"Plugin.Front.Api.",
		"Plugin.Front.",
		"Resto.Front.",
	} {
		name = strings.TrimPrefix(name, prefix)
	}
	return strings.Trim(name, ".- ")
}

func parseArchiveVersion(value string) string {
	value = strings.TrimSpace(value)
	if match := ftpArchiveVersionRe.FindStringSubmatch(value); len(match) == 2 {
		return match[1]
	}
	return ""
}

func reportPluginScanProgress(report func(RapidScanProgress), source string, processed, total int, current string) {
	if report == nil {
		return
	}
	report(RapidScanProgress{
		Source:         source,
		ProcessedPages: processed,
		TotalPages:     total,
		URL:            current,
	})
}

func officialIikoFTPConfig() config.FTPConfig {
	return config.FTPConfig{
		Host: OfficialIikoFTPHost,
		Port: 21,
		User: officialIikoFTPUser,
		Pass: officialIikoFTPPass,
	}
}

func officialIikoPluginsFrontPath(frontVersion string) string {
	return path.Join(officialIikoFTPPath, frontVersion, "Plugins", "Front")
}

func officialFTPFileURL(remotePath string) string {
	return (&url.URL{
		Scheme: "ftp",
		Host:   OfficialIikoFTPHost,
		Path:   remotePath,
	}).String()
}

func officialFTPDirectoryURL(remotePath string) string {
	return (&url.URL{
		Scheme: ftpDirectoryScheme,
		Host:   OfficialIikoFTPHost,
		Path:   remotePath,
	}).String()
}

func dialOfficialIikoFTP() (*ftp.ServerConn, error) {
	conn, err := ftp.Dial(netJoinHostPort(OfficialIikoFTPHost, 21), ftp.DialWithTimeout(15*time.Second))
	if err != nil {
		return nil, err
	}
	if err := conn.Login(officialIikoFTPUser, officialIikoFTPPass); err != nil {
		conn.Quit()
		return nil, err
	}
	return conn, nil
}

func netJoinHostPort(host string, port int) string {
	if strings.Contains(host, ":") {
		return host
	}
	return fmt.Sprintf("%s:%d", host, port)
}

func downloadPluginAsset(am core.AssetManager, parsedURL *url.URL, rawURL, dir string) (string, bool, error) {
	switch strings.ToLower(parsedURL.Scheme) {
	case ftpDirectoryScheme:
		filename := path.Base(parsedURL.Path) + ".zip"
		localPath := filepath.Join(dir, filename)
		if err := downloadOfficialFTPDirectoryAsZip(parsedURL.Path, localPath); err != nil {
			return "", true, err
		}
		return localPath, true, nil
	case "ftp":
		filename := path.Base(parsedURL.Path)
		localPath := filepath.Join(dir, filename)
		_, err := am.DownloadFTPWithProgress(officialIikoFTPConfig(), parsedURL.Path, localPath)
		return localPath, true, err
	default:
		return "", false, nil
	}
}

func downloadOfficialFTPDirectoryAsZip(remoteDir, localZipPath string) error {
	conn, err := dialOfficialIikoFTP()
	if err != nil {
		return err
	}
	defer conn.Quit()

	tempRoot, err := os.MkdirTemp("", "gomh-plugin-ftpdir-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tempRoot)

	localDir := filepath.Join(tempRoot, path.Base(remoteDir))
	if err := downloadFTPDirectory(conn, remoteDir, localDir); err != nil {
		return err
	}
	return zipDirectory(localDir, localZipPath)
}

func downloadFTPDirectory(conn *ftp.ServerConn, remoteDir, localDir string) error {
	if err := os.MkdirAll(localDir, 0o755); err != nil {
		return err
	}

	entries, err := conn.List(remoteDir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		remotePath := path.Join(remoteDir, entry.Name)
		localPath := filepath.Join(localDir, entry.Name)
		switch entry.Type {
		case ftp.EntryTypeFolder:
			if err := downloadFTPDirectory(conn, remotePath, localPath); err != nil {
				return err
			}
		case ftp.EntryTypeFile:
			if err := downloadFTPFile(conn, remotePath, localPath); err != nil {
				return err
			}
		}
	}
	return nil
}

func downloadFTPFile(conn *ftp.ServerConn, remotePath, localPath string) error {
	reader, err := conn.Retr(remotePath)
	if err != nil {
		return err
	}
	defer reader.Close()

	if err := os.MkdirAll(filepath.Dir(localPath), 0o755); err != nil {
		return err
	}
	file, err := os.Create(localPath)
	if err != nil {
		return err
	}
	defer file.Close()

	_, err = io.Copy(file, reader)
	return err
}

func zipDirectory(srcDir, destZipPath string) error {
	if err := os.MkdirAll(filepath.Dir(destZipPath), 0o755); err != nil {
		return err
	}

	outFile, err := os.Create(destZipPath)
	if err != nil {
		return err
	}
	defer outFile.Close()

	zw := zip.NewWriter(outFile)
	defer zw.Close()

	rootParent := filepath.Dir(srcDir)
	return filepath.Walk(srcDir, func(current string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if current == srcDir {
			return nil
		}

		relPath, err := filepath.Rel(rootParent, current)
		if err != nil {
			return err
		}
		zipPath := filepath.ToSlash(relPath)

		header, err := zip.FileInfoHeader(info)
		if err != nil {
			return err
		}
		header.Name = zipPath
		if info.IsDir() {
			header.Name += "/"
			_, err = zw.CreateHeader(header)
			return err
		}
		header.Method = zip.Deflate
		writer, err := zw.CreateHeader(header)
		if err != nil {
			return err
		}

		file, err := os.Open(current)
		if err != nil {
			return err
		}
		defer file.Close()

		_, err = io.Copy(writer, file)
		return err
	})
}

func pluginVersionLabel(plugin Plugin) string {
	if strings.TrimSpace(plugin.PluginVersion) != "" {
		return "v" + plugin.PluginVersion
	}
	if plugin.Source == PluginSourceFTP && plugin.FrontVersion != "" {
		return "iikoFront " + plugin.FrontVersion
	}
	return "без версии"
}

func pluginVersionDescription(plugin Plugin) string {
	var parts []string
	if plugin.ApiVersion != "" {
		parts = append(parts, "API "+plugin.ApiVersion)
	}
	if plugin.Source == PluginSourceFTP && plugin.FrontVersion != "" {
		parts = append(parts, "FTP "+plugin.FrontVersion)
	}
	if len(parts) == 0 {
		return "версия источника не указана"
	}
	return strings.Join(parts, " | ")
}

func livePluginsCachePath(rootPath, frontVersion string) string {
	safeVersion := strings.NewReplacer("\\", "_", "/", "_", ":", "_", ".", "_").Replace(strings.TrimSpace(frontVersion))
	if safeVersion == "" {
		safeVersion = "unknown"
	}
	return filepath.Join(rootPath, "temp", "iiko-plugins-cache-"+safeVersion+".json")
}

func readLivePluginsCache(cachePath, frontVersion string) ([]Plugin, error) {
	data, err := os.ReadFile(cachePath)
	if err != nil {
		return nil, err
	}

	var cache livePluginsCache
	if err := json.Unmarshal(data, &cache); err != nil {
		return nil, err
	}
	if cache.FrontVersion != frontVersion {
		return nil, fmt.Errorf("cache front version mismatch: %q != %q", cache.FrontVersion, frontVersion)
	}
	if len(cache.Plugins) == 0 {
		return nil, fmt.Errorf("cache is empty")
	}
	return cache.Plugins, nil
}

func writeLivePluginsCache(cachePath, frontVersion string, plugins []Plugin) error {
	if err := os.MkdirAll(filepath.Dir(cachePath), 0o755); err != nil {
		return err
	}

	cache := livePluginsCache{
		FrontVersion: frontVersion,
		SavedAt:      time.Now(),
		Plugins:      plugins,
	}
	data, err := json.Marshal(cache)
	if err != nil {
		return err
	}
	return os.WriteFile(cachePath, data, 0o644)
}
