package iiko

import (
	"bufio"
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
		firstWord := strings.Split(encodedFileName, "-")[0]
		shortName := fmt.Sprintf("%s-%d", firstWord, buildNumber)
		decodedFileName, _ := url.QueryUnescape(encodedFileName)
		fullURL, _ := url.JoinPath(targetURL, encodedFileName)
		patch := IikoPatch{
			ShortName:   shortName,
			Description: decodedFileName,
			FullURL:     fullURL,
			BuildNumber: buildNumber,
		}
		foundPatches = append(foundPatches, patch)
	}

	sort.Slice(foundPatches, func(i, j int) bool {
		return foundPatches[i].BuildNumber > foundPatches[j].BuildNumber
	})

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

// applyPatch выполняет полный цикл применения патча с использованием 7z.exe.
func applyPatch(am core.AssetManager, wu core.WinUtils, patch IikoPatch, installDir, backupBaseDir string) error {
	tui.Title(fmt.Sprintf("\n--- Применение патча: %s ---", patch.ShortName))

	// 0. Проверяем наличие 7z.exe
	sevenZipPath, err := wu.FindAndInstall7z(am, wu)
	if err != nil {
		return err
	}
	tui.InfoF("Используется 7-Zip: %s", sevenZipPath)

	// 1. Восстанавливаем файлы из предыдущего бэкапа, если он есть
	if err := restoreFromBackup(wu, installDir, backupBaseDir); err != nil {
		tui.Warn(fmt.Sprintf("Не удалось восстановить бэкап (возможно, его не было): %v", err))
	}

	// 2. Скачиваем архив с патчем в кэш
	tui.Info("Скачивание архива с патчем...")
	patchCachePath := filepath.Join(am.Cfg().AssetsCachePath, patch.ShortName)
	if _, err := am.DownloadHTTPWithProgress(patch.FullURL, patchCachePath); err != nil {
		return fmt.Errorf("не удалось скачать патч: %w", err)
	}

	// 3. Анализируем архив, чтобы найти исходник для распаковки (основной или вложенный)
	tui.Info("Анализ структуры архива...")
	tempRootPath := filepath.Join(am.Cfg().RootPath, "temp")
	if err := os.MkdirAll(tempRootPath, 0755); err != nil {
		return fmt.Errorf("не удалось создать корневую временную директорию: %w", err)
	}
	tempAnalysisDir, err := os.MkdirTemp(tempRootPath, "patch-analysis-*")
	if err != nil {
		return fmt.Errorf("не удалось создать временную папку для анализа: %w", err)
	}
	defer os.RemoveAll(tempAnalysisDir)

	// Распаковываем архив во временную папку для поиска вложенного архива
	_, err = wu.RunCommand(sevenZipPath, "e", patchCachePath, fmt.Sprintf("-o%s", tempAnalysisDir), "-y")
	if err != nil {
		return fmt.Errorf("не удалось выполнить первичную распаковку для анализа: %w", err)
	}

	sourceArchive := patchCachePath // По умолчанию используем основной скачанный архив
	nestedArchive, err := findNestedFrontArchive(tempAnalysisDir)
	if err == nil {
		tui.InfoF("Обнаружен вложенный архив '%s', он будет использован для установки.", filepath.Base(nestedArchive))
		sourceArchive = nestedArchive
	} else {
		tui.Info("Вложенный архив не найден, для установки будет использован основной архив.")
	}

	// 4. Собираем список файлов для бэкапа
	tui.Info("Получение списка файлов из исходного архива...")
	filesOutput, err := wu.RunCommand(sevenZipPath, "l", "-slt", sourceArchive)
	if err != nil {
		return fmt.Errorf("не удалось получить список файлов из архива '%s': %w", sourceArchive, err)
	}
	filesToBackup := parse7zFileList(filesOutput)
	if len(filesToBackup) == 0 {
		return errors.New("не удалось найти файлы для установки внутри архива")
	}
	tui.SuccessF("Найдено %d файлов для установки.", len(filesToBackup))

	// 5. Создаем новый бэкап
	if err := createBackup(wu, installDir, filesToBackup, backupBaseDir, patch.BuildNumber); err != nil {
		return fmt.Errorf("критическая ошибка: не удалось создать бэкап перед установкой патча: %w", err)
	}

	// 6. Извлекаем файлы из архива напрямую в папку установки iiko
	tui.InfoF("Извлечение файлов из '%s' напрямую в '%s'...", filepath.Base(sourceArchive), installDir)
	_, err = wu.RunCommand(sevenZipPath, "x", sourceArchive, fmt.Sprintf("-o%s", installDir), "-y")
	if err != nil {
		return fmt.Errorf("7-Zip завершился с ошибкой при извлечении файлов: %w", err)
	}

	tui.Success("Патч успешно применен.")
	return nil
}

// restoreFromBackup использует связку "копирование + удаление".
func restoreFromBackup(wu core.WinUtils, installDir, backupBaseDir string) error {
	backups, err := filepath.Glob(filepath.Join(backupBaseDir, "backup_*"))
	if err != nil {
		return err
	}
	if len(backups) == 0 {
		tui.Info("Предыдущие бэкапы не найдены. Пропускаем восстановление.")
		return nil
	}

	sort.Strings(backups)
	backupDir := backups[len(backups)-1]
	tui.Warn(fmt.Sprintf("Обнаружен предыдущий бэкап: %s. Восстановление файлов...", filepath.Base(backupDir)))

	err = filepath.Walk(backupDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		relativePath, _ := filepath.Rel(backupDir, path)
		destPath := filepath.Join(installDir, relativePath)

		if err := os.MkdirAll(filepath.Dir(destPath), 0755); err != nil {
			return err
		}

		// Используем интерфейс WinUtils для копирования
		return wu.CopyFile(path, destPath)
	})

	if err != nil {
		return fmt.Errorf("ошибка при восстановлении файлов из бэкапа: %w", err)
	}

	tui.Info("Удаление старой папки бэкапа...")
	return os.RemoveAll(backupDir)
}

// createBackup использует связку "копирование + удаление".
func createBackup(wu core.WinUtils, installDir string, filesToReplace []string, backupBaseDir string, buildNumber int) error {
	backupDir := filepath.Join(backupBaseDir, fmt.Sprintf("backup_%d", buildNumber))

	tui.InfoF("Создание нового бэкапа в: %s", backupDir)
	if err := os.MkdirAll(backupDir, 0755); err != nil {
		return err
	}

	var movedCount int
	for _, fileRelPath := range filesToReplace {
		sourcePath := filepath.Join(installDir, fileRelPath)
		destPath := filepath.Join(backupDir, fileRelPath)

		if _, err := os.Stat(sourcePath); err == nil {
			if err := os.MkdirAll(filepath.Dir(destPath), 0755); err != nil {
				tui.Warn(fmt.Sprintf("Не удалось создать директорию для бэкапа '%s': %v", destPath, err))
				continue
			}

			// Копируем файл в бэкап
			if err := wu.CopyFile(sourcePath, destPath); err != nil {
				tui.Warn(fmt.Sprintf("Не удалось скопировать файл '%s' в бэкап: %v", sourcePath, err))
				continue
			}

			// Удаляем исходный файл
			if err := wu.DeleteFile(sourcePath); err != nil {
				tui.Warn(fmt.Sprintf("Не удалось удалить исходный файл '%s' после бэкапа: %v", sourcePath, err))
				continue
			}
			movedCount++
		}
	}
	tui.InfoF("Перемещено в бэкап %d файлов.", movedCount)
	return nil
}

// getFullVersionString преобразует "927" в "9.2.7014.0"
func getFullVersionString(shortVersion string) (string, error) {
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
	return "", fmt.Errorf("не удалось найти полное имя версии для %s", shortVersion)
}

// getPatchesPath определяет путь к патчам в зависимости от версии.
func getPatchesPath(baseURL, fullVersion string) string {
	if fullVersion == "9.2.8035.0" {
		return fmt.Sprintf("%s/%s", baseURL, fullVersion)
	}
	return fmt.Sprintf("%s/%s/Patches", baseURL, fullVersion)
}

// FindAndSelectPatch выполняет поиск и предлагает пользователю выбрать патч.
func FindAndSelectPatch(am core.AssetManager, version string) (IikoPatch, bool, error) {
	tui.Title("\n--- Поиск доступных патчей для iikoFront ---")

	baseURL := am.Cfg().IikoConfig.PatchesBaseURL
	patches, err := findLatestPatches(baseURL, version)
	if err != nil {
		tui.Warn(fmt.Sprintf("Не удалось найти патчи: %v", err))
		return IikoPatch{}, false, nil
	}
	if len(patches) == 0 {
		tui.Info(fmt.Sprintf("Актуальные патчи для версии %s не найдены. Установка продолжится без них.", version))
		return IikoPatch{}, false, nil
	}

	selectedPatch, err := selectPatchMenu(patches) // <-- ИСПРАВЛЕНО: было selectMenu
	if err != nil {
		tui.Info("Установка патча пропущена по выбору пользователя.")
		return IikoPatch{}, false, nil
	}

	tui.SuccessF("Выбран патч: %s. Он будет установлен после основного дистрибутива.", selectedPatch.ShortName)
	return selectedPatch, true, nil
}

// findNestedFrontArchive рекурсивно ищет архив, содержащий "front" в имени.
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

// parse7zFileList парсит вывод команды `7z l -slt` и возвращает список путей файлов.
func parse7zFileList(output string) []string {
	var files []string
	var currentPath string
	lines := strings.Split(output, "\n")

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "Path = ") {
			currentPath = strings.TrimPrefix(line, "Path = ")
		} else if strings.HasPrefix(line, "Size = ") && currentPath != "" {
			// Это запись о файле, а не о папке
			files = append(files, currentPath)
			currentPath = "" // Сбрасываем, чтобы не добавить папку
		} else if line == "" {
			currentPath = "" // Сбрасываем на пустой строке
		}
	}
	return files
}
