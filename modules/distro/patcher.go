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

	slog.Debug("Найдено патчей", "count", len(patches))

	// Запускаем меню выбора
	selectedPatch, err := selectPatchMenu(patches)

	// Обработка особых случаев выбора
	if err != nil {
		// Если пользователь нажал Ctrl+C или выбрал выход в главное меню
		// Мы интерпретируем это как "Пропустить установку патча" и продолжаем основной процесс
		tui.Info("Выбор патча отменен пользователем. Установка продолжится без патча.")
		slog.Info("Пользователь отказался от выбора патча (Interrupt/Cancel)", "error", err)
		return core.PatchInfo{}, false, nil
	}

	// Проверка на фиктивный патч "SKIP"
	if selectedPatch.ShortName == "SKIP" {
		tui.Info("Выбрано: Не устанавливать патч.")
		slog.Info("Пользователь выбрал пропустить патч")
		return core.PatchInfo{}, false, nil
	}

	tui.SuccessF("Выбран патч: %s.", selectedPatch.ShortName)
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
			Description: decodedFileName, // Хранит оригинальное имя файла
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
	// Создаем фиктивный пункт для пропуска
	skipOption := core.PatchInfo{
		ShortName:   "SKIP",
		Description: "Не применять патч",
		BuildNumber: 0,
	}

	// Формируем список: Сначала "Пропустить", потом патчи
	var displayPatches []core.PatchInfo
	displayPatches = append(displayPatches, skipOption)
	displayPatches = append(displayPatches, patches...)

	selectedPatch, err := tui.SelectPatch(displayPatches, "\n--- Выберите патч для установки ---")
	if err != nil {
		// Пробрасываем ошибку наверх, там решим, что это отмена
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

	// 1. Скачивание с правильным расширением
	ext := filepath.Ext(patch.Description) // .7z или .zip
	if ext == "" {
		ext = ".7z" // Fallback
	}
	fileName := patch.ShortName + ext
	patchCachePath := filepath.Join(am.Cfg().AssetsCachePath, fileName)

	slog.Debug("Скачивание патча", "url", patch.FullURL, "path", patchCachePath)
	if _, err := am.DownloadHTTPWithProgress(patch.FullURL, patchCachePath); err != nil {
		slog.Error("Ошибка скачивания патча", "error", err)
		return err
	}

	// 2. Подготовка временной директории
	tempRootPath := filepath.Join(am.Cfg().RootPath, "temp")
	if err := os.MkdirAll(tempRootPath, 0755); err != nil {
		return err
	}
	// Уникальная папка для распаковки этого патча
	extractDir, err := os.MkdirTemp(tempRootPath, "patch-extract-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(extractDir)

	// 3. Первичная распаковка
	slog.Debug("Извлечение патча для анализа", "tempDir", extractDir)
	if err := sevenZip.Extract(patchCachePath, extractDir, false); err != nil { // false = e (flat) для поиска вложенных архивов
		slog.Error("Ошибка извлечения патча", "error", err)
		return fmt.Errorf("не удалось извлечь архив для анализа: %w", err)
	}

	// 4. Поиск вложенного архива (aggregated-patch...zip внутри)
	var sourceDir string

	nestedArchive, err := findNestedFrontArchive(extractDir)
	if err == nil {
		tui.InfoF("Найден вложенный архив: %s", filepath.Base(nestedArchive))
		slog.Info("Найден вложенный архив в патче", "nested", nestedArchive)

		// Очищаем extractDir для распаковки вложенного архива
		finalContentDir := filepath.Join(extractDir, "final_content")
		_ = os.MkdirAll(finalContentDir, 0755)

		slog.Debug("Распаковка вложенного архива", "archive", nestedArchive, "dest", finalContentDir)
		// Тут используем true (full paths), так как внутри уже структура папок
		if err := sevenZip.Extract(nestedArchive, finalContentDir, true); err != nil {
			return fmt.Errorf("ошибка распаковки вложенного архива: %w", err)
		}
		sourceDir = finalContentDir
	} else {
		// Если вложенного архива нет, возможно мы распаковали 'flat'.
		// Попробуем распаковать исходный архив с сохранением путей в чистую папку
		finalContentDir := filepath.Join(extractDir, "final_content_direct")
		_ = os.MkdirAll(finalContentDir, 0755)
		if err := sevenZip.Extract(patchCachePath, finalContentDir, true); err != nil {
			return err
		}
		sourceDir = finalContentDir
	}

	// 5. Нормализация структуры (спуск в корень)
	// Патчи часто лежат в папке "aggregated-patch-x.x.x". Нам нужно содержимое ЭТОЙ папки.
	realContentDir, err := findContentRoot(sourceDir)
	if err != nil {
		return err
	}
	slog.Info("Определен корневой каталог патча", "path", realContentDir)

	// 6. Создание бэкапа (Сравниваем содержимое патча с installDir)
	slog.Info("Создание бэкапа заменяемых файлов")
	if err := createBackupFromDir(wu, realContentDir, installDir, backupBaseDir, patch.BuildNumber); err != nil {
		slog.Error("Ошибка создания бэкапа", "error", err)
		return fmt.Errorf("не удалось создать бэкап: %w", err)
	}

	// 7. Установка (Копирование файлов)
	slog.Info("Применение патча (копирование файлов)", "src", realContentDir, "dest", installDir)
	if err := wu.CopyDir(realContentDir, installDir); err != nil {
		slog.Error("Ошибка копирования файлов патча", "error", err)
		return fmt.Errorf("ошибка при копировании файлов: %w", err)
	}

	tui.Success("Патч успешно применен.")
	slog.Info("Патч успешно применен")
	return nil
}

// findContentRoot спускается вниз по директориям, пока не найдет папку с более чем 1 элементом или файлами.
func findContentRoot(startDir string) (string, error) {
	currentDir := startDir
	for {
		entries, err := os.ReadDir(currentDir)
		if err != nil {
			return "", err
		}
		if len(entries) == 0 {
			return "", fmt.Errorf("пустая директория патча: %s", currentDir)
		}

		// Если в папке только одна директория - спускаемся в неё
		if len(entries) == 1 && entries[0].IsDir() {
			newDir := filepath.Join(currentDir, entries[0].Name())
			slog.Debug("Спуск в подпапку", "dir", newDir)
			currentDir = newDir
			continue
		}

		// Если мы здесь, значит нашли файлы или несколько папок - это и есть корень
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

// createBackupFromDir сканирует исходную папку патча (patchDir) и бэкапит соответствующие файлы из installDir
func createBackupFromDir(wu core.WinUtils, patchDir, installDir, backupBaseDir string, buildNumber int) error {
	backupDir := filepath.Join(backupBaseDir, fmt.Sprintf("backup_%d", buildNumber))
	_ = os.MkdirAll(backupDir, 0755)

	var movedCount int
	err := filepath.Walk(patchDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}

		// Получаем путь файла относительно корня патча
		relPath, err := filepath.Rel(patchDir, path)
		if err != nil {
			return err
		}

		// Определяем, где этот файл находится в установленной версии
		existingFilePath := filepath.Join(installDir, relPath)
		backupFilePath := filepath.Join(backupDir, relPath)

		// Если файл существует в установке - бэкапим его
		if _, err := os.Stat(existingFilePath); err == nil {
			// Создаем папку в бэкапе
			_ = os.MkdirAll(filepath.Dir(backupFilePath), 0755)

			if err := wu.CopyFile(existingFilePath, backupFilePath); err != nil {
				slog.Warn("Не удалось скопировать файл в бэкап", "file", existingFilePath, "error", err)
			} else {
				movedCount++
			}
		}
		return nil
	})

	if err != nil {
		return err
	}

	tui.InfoF("Скопировано в бэкап %d файлов.", movedCount)
	slog.Debug("Бэкап завершен", "moved_count", movedCount)
	return nil
}

func getFullVersionString(shortVersion string) (string, error) {
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
