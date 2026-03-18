package iikoplugins

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"goMH/core"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func pluginAssetWorkDir(am core.AssetManager, plugin *Plugin) string {
	return filepath.Join(am.Cfg().AssetsCachePath, "plugins", sanitizePluginAssetKey(pluginAssetCacheKey(plugin)))
}

func pluginAssetCacheKey(plugin *Plugin) string {
	if plugin == nil {
		return "unknown"
	}

	parts := []string{
		strings.TrimSpace(plugin.Name),
		strings.TrimSpace(plugin.PluginVersion),
		strings.TrimSpace(plugin.ApiVersion),
		strings.TrimSpace(plugin.FrontVersion),
		string(plugin.Source),
	}
	return strings.Join(parts, "_")
}

func sanitizePluginAssetKey(value string) string {
	replacer := strings.NewReplacer("\\", "_", "/", "_", ":", "_", "*", "_", "?", "_", "\"", "_", "<", "_", ">", "_", "|", "_", " ", "_")
	value = replacer.Replace(strings.TrimSpace(value))
	value = strings.Trim(value, "._")
	if value == "" {
		return "plugin"
	}
	return value
}

func resolvePluginInstallDir(extractDir, zipPath string, plugin *Plugin) (string, string, error) {
	entries, err := os.ReadDir(extractDir)
	if err != nil {
		return "", "", fmt.Errorf("не удалось прочитать директорию распаковки: %w", err)
	}

	zipBaseName := strings.TrimSuffix(filepath.Base(zipPath), filepath.Ext(zipPath))
	candidateDir := extractDir
	rawName := zipBaseName
	if len(entries) == 1 && entries[0].IsDir() {
		rawName = entries[0].Name()
		candidateDir = filepath.Join(extractDir, rawName)
	}

	if dirContainsPluginPayload(candidateDir, plugin) {
		return candidateDir, rawName, nil
	}

	matches, err := findNestedPluginDirs(candidateDir, plugin)
	if err != nil {
		return "", "", err
	}
	if len(matches) == 0 && candidateDir != extractDir {
		matches, err = findNestedPluginDirs(extractDir, plugin)
		if err != nil {
			return "", "", err
		}
	}
	if len(matches) > 0 {
		return matches[0], filepath.Base(matches[0]), nil
	}

	slog.Warn("Не удалось найти вложенную папку плагина по manifest/dll, используем исходную директорию", "path", candidateDir, "plugin", pluginNameForLog(plugin))
	return candidateDir, rawName, nil
}

func findNestedPluginDirs(root string, plugin *Plugin) ([]string, error) {
	var matches []string
	err := filepath.WalkDir(root, func(current string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() || current == root {
			return nil
		}
		if dirContainsPluginPayload(current, plugin) {
			matches = append(matches, current)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	sort.Slice(matches, func(i, j int) bool {
		di := pathDepth(matches[i])
		dj := pathDepth(matches[j])
		if di != dj {
			return di < dj
		}
		return strings.ToLower(matches[i]) < strings.ToLower(matches[j])
	})
	return matches, nil
}

func pathDepth(value string) int {
	clean := filepath.Clean(value)
	if clean == "." || clean == "" {
		return 0
	}
	return len(strings.Split(clean, string(filepath.Separator)))
}

func dirContainsPluginPayload(dir string, plugin *Plugin) bool {
	dllName, err := detectPluginDLL(dir, plugin)
	if err != nil {
		slog.Debug("Не удалось определить plugin payload в директории", "path", dir, "error", err)
		return false
	}
	return dllName != ""
}

func detectPluginDLL(dir string, plugin *Plugin) (string, error) {
	manifest, manifestFound, err := readLocalPluginManifest(dir)
	if err != nil {
		return "", err
	}
	if manifestFound {
		fileName := strings.TrimSpace(manifest.FileName)
		if fileName != "" && strings.HasSuffix(strings.ToLower(fileName), ".dll") {
			fullPath := filepath.Join(dir, fileName)
			if info, err := os.Stat(fullPath); err == nil && !info.IsDir() {
				return fileName, nil
			}
		}
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", err
	}

	targetTokens := pluginMatchTokens(plugin)
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(strings.ToLower(entry.Name()), ".dll") {
			continue
		}

		baseName := strings.TrimSuffix(entry.Name(), filepath.Ext(entry.Name()))
		fileToken := normalizePluginToken(normalizePluginName(baseName))
		for _, token := range targetTokens {
			if token == "" || fileToken == "" {
				continue
			}
			if fileToken == token || strings.Contains(fileToken, token) || strings.Contains(token, fileToken) {
				return entry.Name(), nil
			}
		}
	}

	return "", nil
}

func readLocalPluginManifest(dir string) (ftpPluginManifest, bool, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ftpPluginManifest{}, false, err
	}

	for _, entry := range entries {
		if entry.IsDir() || !strings.EqualFold(entry.Name(), "manifest.xml") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			return ftpPluginManifest{}, false, err
		}

		var manifest ftpPluginManifest
		if err := xml.Unmarshal(bytes.TrimSpace(data), &manifest); err != nil {
			return ftpPluginManifest{}, false, err
		}
		return manifest, true, nil
	}

	return ftpPluginManifest{}, false, nil
}

func pluginMatchTokens(plugin *Plugin) []string {
	if plugin == nil {
		return nil
	}

	values := []string{
		plugin.Name,
		plugin.InstallDir,
		filepath.Base(plugin.RemotePath),
	}
	tokens := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSuffix(value, filepath.Ext(value))
		token := normalizePluginToken(normalizePluginName(value))
		if token == "" {
			continue
		}
		if _, exists := seen[token]; exists {
			continue
		}
		seen[token] = struct{}{}
		tokens = append(tokens, token)
	}
	return tokens
}

func normalizePluginToken(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var b strings.Builder
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func pluginNameForLog(plugin *Plugin) string {
	if plugin == nil {
		return ""
	}
	return plugin.Name
}
