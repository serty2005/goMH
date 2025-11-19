package distro

import (
	"fmt"
	"goMH/core"
	"goMH/dependencies"
	"goMH/tui"
	"io"
	"io/fs"
	"log/slog" // Импорт
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
	slog.Info("Поиск патчей для iikoFront", "version", version)

	baseURL := am.Cfg().DistroConfig.Iiko.PatchesBaseURL
	patches, err := findLatestPatches(baseURL, version)
	if err != nil {
		tui.Warn(fmt.Sprintf("Не удалось найти патчи: %v", err))
		slog.Warn("Ошибка поиска патчей", "error", err)
		return core.PatchInfo{}, false, nil
	}
	if len(patches) == 0 {
		tui.Info(fmt.Sprintf("Актуальные патчи для версии %s не найдены.", version))
		slog.Info("Патчи не найдены")
		return core.PatchInfo{}, false, nil
	}

	slog.Debug("Найдено патчей", "count", len(patches), "top_patches", patches)

	selectedPatch, err := selectPatchMenu(patches)
	if err != nil {
		tui.Info("Установка патча пропущена по выбору пользователя.")
		slog.Info("Пользователь отказался от выбора патча", "error", err)
		return core.PatchInfo{}, false, nil
	}

	tui.SuccessF("Выбран патч: %s. Он будет установлен после основного дистрибутива.", selectedPatch.ShortName)
	slog.Info("Патч выбран", "patch", selectedPatch.ShortName, "url", selectedPatch.FullURL)
	return selectedPatch, true, nil
}

func findLatestPatches(baseURL, version string) ([]core.PatchInfo, error) {
	fullVersion, err := getFullVersionString(version)
	if err != nil {
		// Если это уже полная версия, используем как есть
		if regexp.MustCompile(`^\d+\.\d+\.\d+\.\d+$`).MatchString(version) {
			fullVersion = version
		} else {
			slog.Warn("Не удалось определить полную версию для поиска патчей", "version", version, "error", err)
			return nil, fmt.Errorf("не удалось определить полную версию для '%s': %w", version, err)
		}
	}

	targetURL := getPatchesPath(baseURL, fullVersion)
	tui.InfoF("Поиск патчей по адресу: %s", targetURL)
	slog.Debug("Запрос страницы патчей", "url", targetURL)

	resp, err := http.Get(targetURL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		slog.Debug("Сервер патчей вернул ошибку", "status", resp.Status, "url", targetURL)
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
	displayPatches = append(displayPatches, patches...)

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
	slog.Info("Начало применения патча", "patch", patch.ShortName, "installDir", installDir)

	sevenZip, err := dependencies.NewClient(am, wu)
	if err != nil {
		slog.Error("Не удалось инициализировать 7zip", "error", err)
		return err
	}

	if err := restoreFromBackup(wu, installDir, backupBaseDir); err != nil {
		tui.Warn(fmt.Sprintf("Не удалось восстановить бэкап: %v", err))
		slog.Warn("Ошибка восстановления бэкапа (возможно, его и не было)", "error", err)
	}

	patchCachePath := filepath.Join(am.Cfg().AssetsCachePath, patch.ShortName)
	slog.Debug("Скачивание патча", "url", patch.FullURL, "path", patchCachePath)
	if _, err := am.DownloadHTTPWithProgress(patch.FullURL, patchCachePath); err != nil {
		slog.Error("Ошибка скачивания патча", "error", err)
		return err
	}

	// Создаем временную директорию для анализа архива
	tempRootPath := filepath.Join(am.Cfg().RootPath, "temp")
	if err := os.MkdirAll(tempRootPath, 0755); err != nil {
		return fmt.Errorf("не удалось создать временную директорию %s: %w", tempRootPath, err)
	}

	tempAnalysisDir, err := os.MkdirTemp(tempRootPath, "patch-analysis-*")
	if err != nil {
		return fmt.Errorf("не удалось создать временную директорию для анализа: %w", err)
	}
	defer os.RemoveAll(tempAnalysisDir)

	// Извлекаем архив для анализа структуры
	slog.Debug("Извлечение патча для анализа", "tempDir", tempAnalysisDir)
	if err := sevenZip.Extract(patchCachePath, tempAnalysisDir, false); err != nil {
		slog.Error("Ошибка извлечения патча", "error", err)
		return fmt.Errorf("не удалось извлечь архив для анализа: %w", err)
	}

	// Определяем, какой архив использовать для извлечения
	sourceArchive := patchCachePath
	if nestedArchive, err := findNestedFrontArchive(tempAnalysisDir); err == nil {
		tui.InfoF("Найден вложенный архив: %s", filepath.Base(nestedArchive))
		slog.Info("Найден вложенный архив в патче", "nested", nestedArchive)
		sourceArchive = nestedArchive
	}

	// Получаем список файлов из правильного архива
	slog.Debug("Чтение списка файлов из архива", "archive", sourceArchive)
	filesToBackup, err := sevenZip.List(sourceArchive)
	if err != nil {
		slog.Error("Ошибка чтения списка файлов архива", "error", err)
		return fmt.Errorf("не удалось получить список файлов из архива: %w", err)
	}

	// Создаем бэкап перед извлечением
	slog.Info("Создание бэкапа заменяемых файлов", "count", len(filesToBackup))
	if err := createBackup(wu, installDir, filesToBackup, backupBaseDir, patch.BuildNumber); err != nil {
		slog.Error("Ошибка создания бэкапа", "error", err)
		return fmt.Errorf("не удалось создать бэкап: %w", err)
	}

	// Извлекаем файлы из правильного архива
	slog.Info("Извлечение патча в целевую директорию", "dest", installDir)
	if err := sevenZip.Extract(sourceArchive, installDir, true); err != nil {
		slog.Error("Ошибка применения патча (распаковка)", "error", err)
		return fmt.Errorf("не удалось извлечь архив: %w", err)
	}

	tui.Success("Патч успешно применен.")
	slog.Info("Патч успешно применен")
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
	slog.Info("Восстановление из бэкапа", "backupDir", backupDir)

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
		slog.Error("Ошибка при восстановлении файлов", "error", err)
		return err
	}
	return os.RemoveAll(backupDir)
}

func createBackup(wu core.WinUtils, installDir string, filesToReplace []string, backupBaseDir string, buildNumber int) error {
	slog.Debug("Создание бэкапа", "installDir", installDir, "backupBaseDir", backupBaseDir, "buildNumber", buildNumber)
	backupDir := filepath.Join(backupBaseDir, fmt.Sprintf("backup_%d", buildNumber))
	_ = os.MkdirAll(backupDir, 0755)
	var movedCount int
	for _, fileRelPath := range filesToReplace {
		sourcePath := filepath.Join(installDir, fileRelPath)
		destPath := filepath.Join(backupDir, fileRelPath)
		if _, err := os.Stat(sourcePath); err == nil {
			_ = os.MkdirAll(filepath.Dir(destPath), 0755)
			if err := wu.MoveFile(sourcePath, destPath); err != nil {
				tui.Warn(fmt.Sprintf("Не удалось переместить файл %s в бэкап: %v", sourcePath, err))
				slog.Warn("Не удалось переместить файл в бэкап", "file", sourcePath, "error", err)
				continue
			}
			movedCount++
		}
	}
	tui.InfoF("Перемещено в бэкап %d файлов.", movedCount)
	slog.Debug("Бэкап завершен", "moved_count", movedCount)
	return nil
}

func getFullVersionString(shortVersion string) (string, error) {
	// Эта функция может быть расширена, если появятся новые версии
	versions := map[string]string{
		"927": "9.2.7014.0",
		"926": "9.2.6029.0",
		"928": "9.2.8035.0",
		"929": "9.2.9037.0",
		"936": "9.3.6065.0",
		"937": "9.3.7067.0",
		"946": "9.4.6046.0",
	}
	if val, ok := versions[shortVersion]; ok {
		return val, nil
	}
	return "", fmt.Errorf("не удалось найти полное имя версии для %s", shortVersion)
}

func getPatchesPath(baseURL, version string) string {
	if version == "9.2.8035.0" || version == "9.4.6046.0" {
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
