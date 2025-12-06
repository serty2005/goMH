package distro

import (
	"fmt"
	"goMH/core"
	"goMH/dependencies"
	"goMH/tui"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// FindPatches ищет патчи на сервере
func FindPatches(baseURL, version string) ([]core.PatchInfo, error) {
	fullVersion, err := getFullVersionString(version)
	if err != nil {
		if regexp.MustCompile(`^\d+\.\d+\.\d+\.\d+$`).MatchString(version) {
			fullVersion = version
		} else {
			return nil, err
		}
	}

	targetURL := getPatchesPath(baseURL, fullVersion)
	slog.Debug("Поиск патчей", "url", targetURL)

	resp, err := http.Get(targetURL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %s", resp.Status)
	}

	body, _ := io.ReadAll(resp.Body)
	linkRegex := regexp.MustCompile(`<a href=\"([^\"]+)\"`)
	buildRegex := regexp.MustCompile(`build(?:[\s_]|%20)(\d+)\)`)

	var foundPatches []core.PatchInfo
	for _, match := range linkRegex.FindAllStringSubmatch(string(body), -1) {
		encodedFileName := match[1]
		lowerName := strings.ToLower(encodedFileName)
		if !(strings.HasSuffix(lowerName, ".7z") || strings.HasSuffix(lowerName, ".zip")) {
			continue
		}
		buildMatches := buildRegex.FindStringSubmatch(encodedFileName)
		if len(buildMatches) < 2 {
			continue
		}
		buildNumber, _ := strconv.Atoi(buildMatches[1])
		firstWord := strings.Split(encodedFileName, "-")[0]
		shortName := fmt.Sprintf("%s-%d", firstWord, buildNumber)
		decodedFileName, _ := url.QueryUnescape(encodedFileName)
		fullURL, _ := url.JoinPath(targetURL, encodedFileName)

		foundPatches = append(foundPatches, core.PatchInfo{
			ShortName:   shortName,
			Description: decodedFileName,
			FullURL:     fullURL,
			BuildNumber: buildNumber,
		})
	}

	sort.Slice(foundPatches, func(i, j int) bool { return foundPatches[i].BuildNumber > foundPatches[j].BuildNumber })

	// Возвращаем топ-4
	if len(foundPatches) > 4 {
		return foundPatches[:4], nil
	}
	return foundPatches, nil
}

// SelectPatchMenu показывает меню выбора (UI)
func SelectPatchMenu(patches []core.PatchInfo) (core.PatchInfo, error) {
	skipOption := core.PatchInfo{
		ShortName:   "SKIP",
		Description: "Не применять патч",
	}
	displayPatches := append([]core.PatchInfo{skipOption}, patches...)
	return tui.SelectPatch(displayPatches, "\n--- Выберите патч для установки ---")
}

// ApplyPatch применяет выбранный патч
func ApplyPatch(ctx core.TaskContext, am core.AssetManager, wu core.WinUtils, patch core.PatchInfo, installDir, backupBaseDir string) error {
	ctx.Info("Начало применения патча...")

	sevenZip, err := dependencies.NewClient(am, wu)
	if err != nil {
		return err
	}

	// 1. Восстановление бэкапа (если был)
	if err := restoreFromBackup(wu, installDir, backupBaseDir); err != nil {
		slog.Warn("Не удалось восстановить бэкап", "error", err)
	}

	// 2. Скачивание
	ext := filepath.Ext(patch.Description)
	if ext == "" {
		ext = ".7z"
	}
	fileName := patch.ShortName + ext
	patchCachePath := filepath.Join(am.Cfg().AssetsCachePath, fileName)

	if _, err := am.DownloadHTTPWithProgress(patch.FullURL, patchCachePath); err != nil {
		return err
	}

	// 3. Распаковка во временную
	tempRoot := filepath.Join(am.Cfg().RootPath, "temp")
	_ = os.MkdirAll(tempRoot, 0755)
	extractDir, _ := os.MkdirTemp(tempRoot, "patch-*")
	defer os.RemoveAll(extractDir)

	if err := sevenZip.Extract(patchCachePath, extractDir, false); err != nil {
		return fmt.Errorf("ошибка извлечения архива: %w", err)
	}

	// 4. Поиск контента
	var sourceDir string
	if nested, err := findNestedFrontArchive(extractDir); err == nil {
		finalDir := filepath.Join(extractDir, "final")
		_ = os.MkdirAll(finalDir, 0755)
		if err := sevenZip.Extract(nested, finalDir, true); err != nil {
			return err
		}
		sourceDir = finalDir
	} else {
		finalDir := filepath.Join(extractDir, "final_direct")
		_ = os.MkdirAll(finalDir, 0755)
		if err := sevenZip.Extract(patchCachePath, finalDir, true); err != nil {
			return err
		}
		sourceDir = finalDir
	}

	realContentDir, err := findContentRoot(sourceDir)
	if err != nil {
		return err
	}

	// 5. Бэкап
	ctx.Info("Бэкапирование файлов...")
	if err := createBackupFromDir(wu, realContentDir, installDir, backupBaseDir, patch.BuildNumber); err != nil {
		return fmt.Errorf("ошибка бэкапа: %w", err)
	}

	// 6. Копирование
	ctx.Info("Копирование файлов патча...")
	if err := wu.CopyDir(realContentDir, installDir); err != nil {
		return err
	}

	return nil
}

// Вспомогательные функции (оставляем без изменений логику)

func findContentRoot(startDir string) (string, error) {
	currentDir := startDir
	for {
		entries, err := os.ReadDir(currentDir)
		if err != nil {
			return "", err
		}
		if len(entries) == 0 {
			return "", fmt.Errorf("пустая папка патча")
		}
		if len(entries) == 1 && entries[0].IsDir() {
			currentDir = filepath.Join(currentDir, entries[0].Name())
			continue
		}
		return currentDir, nil
	}
}

func restoreFromBackup(wu core.WinUtils, installDir, backupBaseDir string) error {
	backups, err := filepath.Glob(filepath.Join(backupBaseDir, "backup_*"))
	if err != nil || len(backups) == 0 {
		return nil
	}
	sort.Strings(backups)
	backupDir := backups[len(backups)-1]

	slog.Info("Восстановление из бэкапа", "path", backupDir)
	return filepath.Walk(backupDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(backupDir, path)
		dest := filepath.Join(installDir, rel)
		_ = os.MkdirAll(filepath.Dir(dest), 0755)
		return wu.CopyFile(path, dest)
	})
}

func createBackupFromDir(wu core.WinUtils, patchDir, installDir, backupBaseDir string, buildNumber int) error {
	backupDir := filepath.Join(backupBaseDir, fmt.Sprintf("backup_%d", buildNumber))
	_ = os.MkdirAll(backupDir, 0755)

	return filepath.Walk(patchDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(patchDir, path)
		existing := filepath.Join(installDir, rel)
		backup := filepath.Join(backupDir, rel)

		if _, err := os.Stat(existing); err == nil {
			_ = os.MkdirAll(filepath.Dir(backup), 0755)
			_ = wu.CopyFile(existing, backup)
		}
		return nil
	})
}

func getFullVersionString(short string) (string, error) {
	versions := map[string]string{
		"927": "9.2.7014.0", "926": "9.2.6029.0", "928": "9.2.8035.0",
		"929": "9.2.9037.0", "936": "9.3.6065.0", "937": "9.3.7067.0", "946": "9.4.6046.0",
	}
	if v, ok := versions[short]; ok {
		return v, nil
	}
	return "", fmt.Errorf("unknown short version")
}

func getPatchesPath(baseURL, version string) string {
	if version == "9.2.8035.0" || version == "9.4.6046.0" {
		return fmt.Sprintf("%s/%s", baseURL, version)
	}
	return fmt.Sprintf("%s/%s/Patches", baseURL, version)
}

func findNestedFrontArchive(dir string) (string, error) {
	var found string
	filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if found != "" || err != nil {
			return nil
		}
		if !d.IsDir() {
			l := strings.ToLower(d.Name())
			if strings.Contains(l, "front") && (strings.HasSuffix(l, ".zip") || strings.HasSuffix(l, ".7z")) {
				found = path
			}
		}
		return nil
	})
	if found != "" {
		return found, nil
	}
	return "", os.ErrNotExist
}
