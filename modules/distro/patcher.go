package distro

import (
	"fmt"
	"goMH/core"
	"goMH/dependencies"
	"goMH/tui"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

func FindAndSelectPatch(am core.AssetManager, version string) (core.PatchInfo, bool, error) {
	tui.Title("\n--- Поиск доступных патчей для iikoFront ---")
	baseURL := am.Cfg().DistroConfig.Iiko.PatchesBaseURL
	patches, err := findLatestPatches(baseURL, version)
	if err != nil {
		tui.Warn(fmt.Sprintf("Не удалось найти патчи: %v", err))
		return core.PatchInfo{}, false, nil
	}
	if len(patches) == 0 {
		tui.Info(fmt.Sprintf("Актуальные патчи для версии %s не найдены.", version))
		return core.PatchInfo{}, false, nil
	}
	selectedPatch, err := selectPatchMenu(patches)
	if err != nil {
		tui.Info("Установка патча пропущена по выбору пользователя.")
		return core.PatchInfo{}, false, nil
	}
	tui.SuccessF("Выбран патч: %s. Он будет установлен после основного дистрибутива.", selectedPatch.ShortName)
	return selectedPatch, true, nil
}

func findLatestPatches(baseURL, version string) ([]core.PatchInfo, error) {
	fullVersion, err := getFullVersionString(version)
	if err != nil {
		// Если это уже полная версия, используем как есть
		if regexp.MustCompile(`^\d+\.\d+\.\d+\.\d+$`).MatchString(version) {
			fullVersion = version
		} else {
			return nil, fmt.Errorf("не удалось определить полную версию для '%s': %w", version, err)
		}
	}
	targetURL := getPatchesPath(baseURL, fullVersion)
	tui.InfoF("Поиск патчей по адресу: %s", targetURL)
	resp, err := http.Get(targetURL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("сервер вернул ошибку: %s", resp.Status)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	linkRegex := regexp.MustCompile(`<a href=\"([^\"]+)\"`)
	buildRegex := regexp.MustCompile(`build(?:\s|%20)(\d+)\)`)
	var foundPatches []core.PatchInfo
	for _, match := range linkRegex.FindAllStringSubmatch(string(body), -1) {
		encodedFileName := match[1]
		if !(strings.HasSuffix(strings.ToLower(encodedFileName), ".7z") || strings.HasSuffix(strings.ToLower(encodedFileName), ".zip")) {
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
	if len(foundPatches) > 4 {
		return foundPatches[:4], nil
	}
	return foundPatches, nil
}

func selectPatchMenu(patches []core.PatchInfo) (core.PatchInfo, error) {
	// Конвертируем core.PatchInfo в формат для отображения
	var displayPatches []core.PatchInfo
	for _, patch := range patches {
		displayPatches = append(displayPatches, patch)
	}

	selectedPatch, err := tui.SelectPatch(displayPatches, "\n--- Выберите патч для установки ---")
	if err != nil {
		// Проверяем, является ли ошибка выходом в главное меню
		if err == tui.ErrExitToMainMenu {
			return core.PatchInfo{}, err
		}
		return core.PatchInfo{}, err
	}

	return selectedPatch, nil
}

func ApplyPatch(am core.AssetManager, wu core.WinUtils, patch core.PatchInfo, installDir, backupBaseDir string) error {
	tui.Title(fmt.Sprintf("\n--- Применение патча: %s ---", patch.ShortName))
	sevenZip, err := dependencies.NewClient(am, wu)
	if err != nil {
		return err
	}
	if err := restoreFromBackup(wu, installDir, backupBaseDir); err != nil {
		tui.Warn(fmt.Sprintf("Не удалось восстановить бэкап: %v", err))
	}
	patchCachePath := filepath.Join(am.Cfg().AssetsCachePath, patch.ShortName)
	if _, err := am.DownloadHTTPWithProgress(patch.FullURL, patchCachePath); err != nil {
		return err
	}
	tempAnalysisDir, err := os.MkdirTemp(filepath.Join(am.Cfg().RootPath, "temp"), "patch-analysis-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tempAnalysisDir)
	if err := sevenZip.Extract(patchCachePath, tempAnalysisDir, false); err != nil {
		return err
	}
	sourceArchive := patchCachePath
	if nestedArchive, err := findNestedFrontArchive(tempAnalysisDir); err == nil {
		sourceArchive = nestedArchive
	}
	filesToBackup, err := sevenZip.List(sourceArchive)
	if err != nil {
		return err
	}
	if err := createBackup(wu, installDir, filesToBackup, backupBaseDir, patch.BuildNumber); err != nil {
		return err
	}
	if err := sevenZip.Extract(sourceArchive, installDir, true); err != nil {
		return err
	}
	tui.Success("Патч успешно применен.")
	return nil
}

func restoreFromBackup(wu core.WinUtils, installDir, backupBaseDir string) error {
	backups, err := filepath.Glob(filepath.Join(backupBaseDir, "backup_*"))
	if err != nil || len(backups) == 0 {
		return nil
	}
	sort.Strings(backups)
	backupDir := backups[len(backups)-1]
	tui.Warn(fmt.Sprintf("Обнаружен бэкап: %s. Восстановление...", filepath.Base(backupDir)))
	err = filepath.Walk(backupDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		relativePath, _ := filepath.Rel(backupDir, path)
		destPath := filepath.Join(installDir, relativePath)
		_ = os.MkdirAll(filepath.Dir(destPath), 0755)
		return wu.CopyFile(path, destPath)
	})
	if err != nil {
		return err
	}
	return os.RemoveAll(backupDir)
}

func createBackup(wu core.WinUtils, installDir string, filesToReplace []string, backupBaseDir string, buildNumber int) error {
	backupDir := filepath.Join(backupBaseDir, fmt.Sprintf("backup_%d", buildNumber))
	_ = os.MkdirAll(backupDir, 0755)
	var movedCount int
	for _, fileRelPath := range filesToReplace {
		sourcePath := filepath.Join(installDir, fileRelPath)
		destPath := filepath.Join(backupDir, fileRelPath)
		if _, err := os.Stat(sourcePath); err == nil {
			_ = os.MkdirAll(filepath.Dir(destPath), 0755)
			if err := wu.CopyFile(sourcePath, destPath); err != nil {
				continue
			}
			if err := wu.DeleteFile(sourcePath); err != nil {
				continue
			}
			movedCount++
		}
	}
	tui.InfoF("Перемещено в бэкап %d файлов.", movedCount)
	return nil
}

func getFullVersionString(shortVersion string) (string, error) {
	// Эта функция может быть расширена, если появятся новые версии
	versions := map[string]string{
		"927": "9.2.7014.0", "926": "9.2.6029.0", "928": "9.2.8035.0", "936": "9.3.6065.0",
	}
	if val, ok := versions[shortVersion]; ok {
		return val, nil
	}
	return "", fmt.Errorf("не удалось найти полное имя версии для %s", shortVersion)
}

func getPatchesPath(baseURL, version string) string {
	if version == "9.2.8035.0" {
		return fmt.Sprintf("%s/%s", baseURL, version)
	}
	return fmt.Sprintf("%s/%s/Patches", baseURL, version)
}

func findNestedFrontArchive(dir string) (string, error) {
	var foundPath string
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if foundPath != "" {
			return filepath.SkipDir
		}
		if !d.IsDir() {
			lowerCaseName := strings.ToLower(d.Name())
			if strings.Contains(lowerCaseName, "front") && (strings.HasSuffix(lowerCaseName, ".zip") || strings.HasSuffix(lowerCaseName, ".7z")) {
				foundPath = path
				return filepath.SkipDir
			}
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	if foundPath == "" {
		return "", os.ErrNotExist
	}
	return foundPath, nil
}
