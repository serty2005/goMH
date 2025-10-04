package iiko

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"goMH/core"
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

	"github.com/mholt/archives"
)

// IikoPatch представляет собой найденный и распарсенный патч.
type IikoPatch struct {
	ShortName   string
	Description string
	FullURL     string
	BuildNumber int
}

// RunPatchWorkflow - основная точка входа для запуска процесса поиска и установки патчей.
func RunPatchWorkflow(am core.AssetManager, wu core.WinUtils, version, installDir, backupBaseDir string) error {
	tui.Title("\n--- Поиск и установка патчей для iikoFront ---")

	// 1. Найти патчи
	baseURL := am.Cfg().IikoConfig.PatchesBaseURL
	patches, err := findLatestPatches(baseURL, version)
	if err != nil {
		return err
	}
	if len(patches) == 0 {
		tui.Success(fmt.Sprintf("Актуальные патчи для версии %s не найдены.", version))
		return nil
	}

	// 2. Показать меню и дать выбрать
	selectedPatch, err := selectPatchMenu(patches)
	if err != nil {
		return err // Пользователь отменил
	}

	// 3. Применить выбранный патч
	return applyPatch(am, wu, selectedPatch, installDir, backupBaseDir)
}

// findLatestPatches сканирует веб-страницу и возвращает 4 самых свежих патча.
func findLatestPatches(baseURL, version string) ([]IikoPatch, error) {
	// Формируем URL для конкретной версии
	fullVersion, err := getFullVersionString(version)
	if err != nil {
		return nil, fmt.Errorf("не удалось определить полную версию для '%s': %w", version, err)
	}
	targetURL := getPatchesPath(baseURL, fullVersion)
	tui.InfoF("Поиск патчей по адресу: %s", targetURL)

	resp, err := http.Get(targetURL)
	if err != nil {
		return nil, fmt.Errorf("не удалось выполнить запрос: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("сервер вернул ошибку: %s", resp.Status)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("не удалось прочитать ответ: %w", err)
	}

	// Парсим HTML
	linkRegex := regexp.MustCompile(`<a href="([^"]+)"`)
	buildRegex := regexp.MustCompile(`build(?:\s|%20)(\d+)\)`)

	var foundPatches []IikoPatch
	matches := linkRegex.FindAllStringSubmatch(string(body), -1)

	for _, match := range matches {
		encodedFileName := match[1]
		if !(strings.HasSuffix(strings.ToLower(encodedFileName), ".7z") || strings.HasSuffix(strings.ToLower(encodedFileName), ".zip")) {
			continue
		}

		buildMatches := buildRegex.FindStringSubmatch(encodedFileName)
		if len(buildMatches) < 2 {
			continue
		}

		buildNumber, _ := strconv.Atoi(buildMatches[1])

		// 1. Получаем первое слово из имени файла (например, "aggregated")
		firstWord := strings.Split(encodedFileName, "-")[0]
		// 2. Формируем короткое имя "слово-билд"
		shortName := fmt.Sprintf("%s-%d", firstWord, buildNumber)

		// 3. Декодируем полное имя для красивого отображения в описании
		decodedFileName, _ := url.QueryUnescape(encodedFileName)

		fullURL, _ := url.JoinPath(targetURL, encodedFileName)
		patch := IikoPatch{
			ShortName:   shortName,
			Description: decodedFileName, // Используем декодированное имя
			FullURL:     fullURL,
			BuildNumber: buildNumber,
		}
		foundPatches = append(foundPatches, patch)
	}

	// Сортируем по номеру билда (от большего к меньшему)
	sort.Slice(foundPatches, func(i, j int) bool {
		return foundPatches[i].BuildNumber > foundPatches[j].BuildNumber
	})

	// Возвращаем не более 4-х
	if len(foundPatches) > 4 {
		return foundPatches[:4], nil
	}
	return foundPatches, nil
}

// selectPatchMenu показывает меню выбора и возвращает выбранный патч.
func selectPatchMenu(patches []IikoPatch) (IikoPatch, error) {
	reader := bufio.NewReader(os.Stdin)
	tui.Title("\n--- Найдены следующие актуальные патчи ---")
	for i, p := range patches {
		fmt.Printf(" %d. %s (%s)\n", i+1, p.ShortName, p.Description)
	}
	fmt.Println("\n 0. Пропустить установку патча")
	fmt.Print("Выберите номер патча для установки: ")

	choiceStr, _ := reader.ReadString('\n')
	choice, err := strconv.Atoi(strings.TrimSpace(choiceStr))
	if err != nil || choice < 1 || choice > len(patches) {
		tui.Info("Установка патча пропущена.")
		return IikoPatch{}, errors.New("установка патча отменена пользователем")
	}
	return patches[choice-1], nil
}

// applyPatch выполняет полный цикл применения патча: восстановление, бэкап, установка.
func applyPatch(am core.AssetManager, wu core.WinUtils, patch IikoPatch, installDir, backupBaseDir string) error {
	tui.Title(fmt.Sprintf("\n--- Применение патча: %s ---", patch.ShortName))

	// 1. Восстанавливаем файлы из предыдущего бэкапа, если он есть
	if err := restoreFromBackup(installDir, backupBaseDir); err != nil {
		tui.Warn(fmt.Sprintf("Не удалось восстановить бэкап (возможно, его не было): %v", err))
	}

	// 2. Скачиваем архив с патчем во временную папку
	tui.Info("Скачивание архива с патчем...")
	patchCachePath := filepath.Join(am.Cfg().AssetsCachePath, patch.ShortName)
	if _, err := am.DownloadHTTPWithProgress(patch.FullURL, patchCachePath); err != nil {
		return fmt.Errorf("не удалось скачать патч: %w", err)
	}
	defer os.Remove(patchCachePath)

	// 3. Подготовка исходных файлов патча (распаковка, поиск вложенных архивов и т.д.)
	tui.Info("Подготовка исходных файлов патча...")
	// Определяем корневую директорию приложения для создания временных файлов
	exePath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("не удалось определить путь к исполняемому файлу: %w", err)
	}
	tempRootPath := filepath.Dir(exePath)

	sourceDir, cleanup, err := preparePatchSource(patchCachePath, tempRootPath)
	if err != nil {
		return fmt.Errorf("не удалось подготовить исходные файлы патча: %w", err)
	}
	defer cleanup()

	// 4. Собираем список файлов для бэкапа из исходной директории
	var filesToBackup []string
	err = filepath.WalkDir(sourceDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			// Нам нужно относительное имя файла, как это было в ListArchiveContents
			relPath, err := filepath.Rel(sourceDir, path)
			if err != nil {
				return err
			}
			filesToBackup = append(filesToBackup, relPath)
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("не удалось составить список файлов для бэкапа: %w", err)
	}
	tui.Success("Анализ файлов патча завершен.")

	// 5. Создаем новый бэкап
	if err := createBackup(installDir, filesToBackup, backupBaseDir, patch.BuildNumber); err != nil {
		return fmt.Errorf("критическая ошибка: не удалось создать бэкап перед установкой патча: %w", err)
	}

	// 6. Копируем файлы патча в директорию установки
	tui.InfoF("Копирование файлов патча в '%s'...", installDir)
	for _, fileRelPath := range filesToBackup {
		srcPath := filepath.Join(sourceDir, fileRelPath)
		destPath := filepath.Join(installDir, fileRelPath)

		// Создаем подкаталоги в целевом каталоге
		if err := os.MkdirAll(filepath.Dir(destPath), 0755); err != nil {
			return fmt.Errorf("не удалось создать директорию '%s': %w", filepath.Dir(destPath), err)
		}

		// Копируем файл
		srcFile, err := os.Open(srcPath)
		if err != nil {
			return fmt.Errorf("не удалось открыть исходный файл '%s': %w", srcPath, err)
		}
		defer srcFile.Close()

		destFile, err := os.Create(destPath)
		if err != nil {
			return fmt.Errorf("не удалось создать целевой файл '%s': %w", destPath, err)
		}
		defer destFile.Close()

		if _, err := io.Copy(destFile, srcFile); err != nil {
			return fmt.Errorf("не удалось скопировать файл '%s': %w", fileRelPath, err)
		}
	}

	tui.Success("Патч успешно применен.")
	return nil
}

// restoreFromBackup находит папку бэкапа, восстанавливает из нее файлы и удаляет ее.
func restoreFromBackup(installDir, backupBaseDir string) error {
	backups, err := filepath.Glob(filepath.Join(backupBaseDir, "backup_*"))
	if err != nil {
		return err
	}
	if len(backups) == 0 {
		tui.Info("Предыдущие бэкапы не найдены. Пропускаем восстановление.")
		return nil
	}

	// Берем самый свежий бэкап (хотя должен быть только один)
	sort.Strings(backups)
	backupDir := backups[len(backups)-1]
	tui.Warn(fmt.Sprintf("Обнаружен предыдущий бэкап: %s. Восстановление файлов...", filepath.Base(backupDir)))

	err = filepath.Walk(backupDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		relativePath, _ := filepath.Rel(backupDir, path)
		destPath := filepath.Join(installDir, relativePath)

		// Создаем подкаталоги в целевом каталоге, если они есть во вложенных путях
		if err := os.MkdirAll(filepath.Dir(destPath), 0755); err != nil {
			return err
		}

		// Просто перемещаем файл с заменой
		return os.Rename(path, destPath)
	})

	if err != nil {
		return fmt.Errorf("ошибка при восстановлении файлов из бэкапа: %w", err)
	}

	tui.Info("Удаление старой папки бэкапа...")
	return os.RemoveAll(backupDir)
}

// createBackup создает новую папку бэкапа и копирует в нее файлы, которые будут заменены.
func createBackup(installDir string, filesToReplace []string, backupBaseDir string, buildNumber int) error {
	backupDir := filepath.Join(backupBaseDir, fmt.Sprintf("backup_%d", buildNumber))

	tui.InfoF("Создание нового бэкапа в: %s", backupDir)
	if err := os.MkdirAll(backupDir, 0755); err != nil {
		return err
	}

	var movedCount int
	for _, fileRelPath := range filesToReplace {
		sourcePath := filepath.Join(installDir, fileRelPath)
		destPath := filepath.Join(backupDir, fileRelPath)

		// Перемещаем только если исходный файл существует
		if _, err := os.Stat(sourcePath); err == nil {
			// Создаем родительскую директорию для файла в бэкапе
			if err := os.MkdirAll(filepath.Dir(destPath), 0755); err != nil {
				tui.Warn(fmt.Sprintf("Не удалось создать директорию для бэкапа '%s': %v", destPath, err))
				continue // Пропускаем этот файл
			}

			// Используем Rename для перемещения файла
			if err := os.Rename(sourcePath, destPath); err != nil {
				tui.Warn(fmt.Sprintf("Не удалось переместить файл '%s' в бэкап: %v", sourcePath, err))
				continue // Пропускаем этот файл
			}
			movedCount++
		}
	}
	tui.InfoF("Перемещено в бэкап %d файлов.", movedCount)
	return nil
}

// getFullVersionString преобразует "927" в "9.2.7014.0" (пока заглушка, нужна логика).
// ВАЖНО: Эта функция требует более сложной логики сопоставления.
// Для текущей задачи мы сделаем простое предположение.
func getFullVersionString(shortVersion string) (string, error) {
	// TODO: Реализовать более надежное сопоставление.
	// Пока что ищем на странице версию, которая начинается с нужных цифр.
	// Это очень упрощенный подход!
	if shortVersion == "927" {
		return "9.2.7014.0", nil
	}
	if shortVersion == "926" {
		return "9.2.6029.0", nil
	}
	if shortVersion == "928" {
		return "9.2.8035.0", nil
	}
	if shortVersion == "936" {
		return "9.3.6065.0", nil
	}

	// и т.д.
	return "", fmt.Errorf("не удалось найти полное имя версии для %s", shortVersion)
}

// getPatchesPath определяет путь к патчам в зависимости от версии.
// Для версии "9.2.8035.0" используется корневой путь (без /Patches),
// для всех остальных версий используется путь с подпапкой /Patches.
func getPatchesPath(baseURL, fullVersion string) string {
	if fullVersion == "9.2.8035.0" {
		return fmt.Sprintf("%s/%s", baseURL, fullVersion)
	}
	return fmt.Sprintf("%s/%s/Patches", baseURL, fullVersion)
}

// FindAndSelectPatch выполняет поиск и предлагает пользователю выбрать патч.
// Возвращает выбранный патч и флаг, был ли сделан выбор.
func FindAndSelectPatch(am core.AssetManager, version string) (IikoPatch, bool, error) {
	tui.Title("\n--- Поиск доступных патчей для iikoFront ---")

	baseURL := am.Cfg().IikoConfig.PatchesBaseURL
	patches, err := findLatestPatches(baseURL, version)
	if err != nil {
		// Если сервер недоступен или вернул ошибку - это предупреждение, а не провал установки
		tui.Warn(fmt.Sprintf("Не удалось найти патчи: %v", err))
		return IikoPatch{}, false, nil
	}
	if len(patches) == 0 {
		tui.Info(fmt.Sprintf("Актуальные патчи для версии %s не найдены. Установка продолжится без них.", version))
		return IikoPatch{}, false, nil
	}

	selectedPatch, err := selectPatchMenu(patches)
	if err != nil {
		// Пользователь выбрал "0" для отмены - это штатная ситуация
		tui.Info("Установка патча пропущена по выбору пользователя.")
		return IikoPatch{}, false, nil
	}

	tui.SuccessF("Выбран патч: %s. Он будет установлен после основного дистрибутива.", selectedPatch.ShortName)
	return selectedPatch, true, nil
}

// preparePatchSource распаковывает архив и находит исходную директорию с файлами патча.
// Возвращает путь к исходной директории, функцию для очистки временных файлов и ошибку.
func preparePatchSource(archivePath string, tempRootPath string) (sourceDir string, cleanup func(), err error) {
	// 1. Создаем основную временную директорию для всех операций внутри корневого каталога приложения
	tempBaseDir, err := os.MkdirTemp(tempRootPath, "gomh_patch_*")
	if err != nil {
		return "", func() {}, fmt.Errorf("не удалось создать временную директорию: %w", err)
	}

	cleanup = func() { os.RemoveAll(tempBaseDir) }

	// 2. Распаковываем основной архив
	unpackDir := filepath.Join(tempBaseDir, "unpacked")
	if err := os.Mkdir(unpackDir, 0755); err != nil {
		return "", cleanup, fmt.Errorf("не удалось создать директорию для распаковки: %w", err)
	}

	fsys, err := archives.FileSystem(context.Background(), archivePath, nil)
	if err != nil {
		return "", cleanup, fmt.Errorf("не удалось открыть архив '%s': %w", archivePath, err)
	}

	err = fs.WalkDir(fsys, ".", func(pathInArchive string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if pathInArchive == "." {
			return nil
		}
		destPath := filepath.Join(unpackDir, pathInArchive)
		if d.IsDir() {
			return os.MkdirAll(destPath, 0755)
		}
		srcFile, err := fsys.Open(pathInArchive)
		if err != nil {
			return err
		}
		defer srcFile.Close()
		if err := os.MkdirAll(filepath.Dir(destPath), 0755); err != nil {
			return err
		}
		destFile, err := os.Create(destPath)
		if err != nil {
			return err
		}
		defer destFile.Close()
		_, err = io.Copy(destFile, srcFile)
		return err
	})
	if err != nil {
		return "", cleanup, fmt.Errorf("ошибка при первичной распаковке: %w", err)
	}

	// 3. Гибридный алгоритм поиска исходной директории
	searchBaseDir := unpackDir // По умолчанию ищем в результатах первичной распаковки

	nestedArchive, err := findNestedFrontArchive(unpackDir)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", cleanup, fmt.Errorf("ошибка при поиске вложенного архива: %w", err)
	}

	// Если вложенный архив найден, распаковываем его и меняем базу для поиска
	if err == nil {
		tui.InfoF("Найден вложенный архив: %s. Распаковываем...", filepath.Base(nestedArchive))
		nestedUnpackDir := filepath.Join(tempBaseDir, "nested_unpacked")
		if err := os.Mkdir(nestedUnpackDir, 0755); err != nil {
			return "", cleanup, fmt.Errorf("не удалось создать директорию для вложенной распаковки: %w", err)
		}

		// Распаковываем вложенный архив
		fsysNested, err := archives.FileSystem(context.Background(), nestedArchive, nil)
		if err != nil {
			return "", cleanup, fmt.Errorf("не удалось открыть вложенный архив '%s': %w", nestedArchive, err)
		}
		err = fs.WalkDir(fsysNested, ".", func(pathInArchive string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if pathInArchive == "." {
				return nil
			}
			destPath := filepath.Join(nestedUnpackDir, pathInArchive)
			if d.IsDir() {
				return os.MkdirAll(destPath, 0755)
			}
			srcFile, err := fsysNested.Open(pathInArchive)
			if err != nil {
				return err
			}
			defer srcFile.Close()
			if err := os.MkdirAll(filepath.Dir(destPath), 0755); err != nil {
				return err
			}
			destFile, err := os.Create(destPath)
			if err != nil {
				return err
			}
			defer destFile.Close()
			_, err = io.Copy(destFile, srcFile)
			return err
		})
		if err != nil {
			return "", cleanup, fmt.Errorf("ошибка при распаковке вложенного архива: %w", err)
		}

		searchBaseDir = nestedUnpackDir // Обновляем базу для поиска
	} else {
		tui.Info("Вложенный архив не найден, поиск файлов в основной директории.")
	}

	// 4. Ищем самую глубокую папку в searchBaseDir
	var allDirs []string
	err = filepath.WalkDir(searchBaseDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			allDirs = append(allDirs, path)
		}
		return nil
	})
	if err != nil {
		return "", cleanup, fmt.Errorf("не удалось обойти директорию '%s': %w", searchBaseDir, err)
	}

	if len(allDirs) == 0 {
		return searchBaseDir, cleanup, nil // Если папок нет, возвращаем базовую
	}

	deepestDir := searchBaseDir
	maxDepth := -1
	for _, dir := range allDirs {
		depth := len(strings.Split(dir, string(os.PathSeparator)))
		if depth > maxDepth {
			maxDepth = depth
			deepestDir = dir
		}
	}

	tui.InfoF("Рабочая директория патча определена: %s", deepestDir)
	return deepestDir, cleanup, nil
}

// findNestedFrontArchive рекурсивно ищет архив, содержащий "front" в имени.
func findNestedFrontArchive(dir string) (string, error) {
	var foundPath string
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		// Если уже нашли, дальше не ищем
		if foundPath != "" {
			return filepath.SkipDir
		}
		if !d.IsDir() {
			lowerCaseName := strings.ToLower(d.Name())
			if strings.Contains(lowerCaseName, "front") && (strings.HasSuffix(lowerCaseName, ".zip") || strings.HasSuffix(lowerCaseName, ".7z")) {
				foundPath = path
				return filepath.SkipDir // Прерываем поиск, как только нашли первый
			}
		}
		return nil
	})

	if err != nil {
		return "", err
	}
	if foundPath == "" {
		return "", os.ErrNotExist // Используем стандартную ошибку, чтобы легко ее проверять
	}
	return foundPath, nil
}
