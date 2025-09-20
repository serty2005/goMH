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
	targetURL := fmt.Sprintf("%s/%s/Patches", baseURL, fullVersion)
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

	// 3. Получаем содержимое архива для создания бэкапа
	tui.Info("Анализ содержимого архива...")
	contents, err := wu.ListArchiveContents(patchCachePath)
	if err != nil {
		return fmt.Errorf("не удалось прочитать содержимое архива: %w", err)
	}
	tui.Success("Содержимое архива проанализировано.")

	// 4. Создаем новый бэкап
	if err := createBackup(installDir, contents, backupBaseDir, patch.BuildNumber); err != nil {
		return fmt.Errorf("критическая ошибка: не удалось создать бэкап перед установкой патча: %w", err)
	}

	// 5. Распаковываем патч
	tui.InfoF("Распаковка '%s' в '%s'...", patch.ShortName, installDir)

	fsys, err := archives.FileSystem(context.Background(), patchCachePath, nil)
	if err != nil {
		return fmt.Errorf("не удалось открыть архив для распаковки: %w", err)
	}

	err = fs.WalkDir(fsys, ".", func(pathInArchive string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}

		// Открываем исходный файл из виртуальной ФС
		srcFile, err := fsys.Open(pathInArchive)
		if err != nil {
			return fmt.Errorf("не удалось открыть '%s' в архиве: %w", pathInArchive, err)
		}
		defer srcFile.Close()

		// Извлекаем только имя файла, игнорируя структуру папок архива
		fileName := filepath.Base(pathInArchive)
		destPath := filepath.Join(installDir, fileName)

		// Создаем конечный файл на диске
		destFile, err := os.Create(destPath)
		if err != nil {
			return fmt.Errorf("не удалось создать файл '%s' на диске: %w", destPath, err)
		}
		defer destFile.Close()

		// Копируем содержимое
		if _, err := io.Copy(destFile, srcFile); err != nil {
			return fmt.Errorf("не удалось скопировать данные для '%s': %w", pathInArchive, err)
		}
		return nil
	})

	if err != nil {
		return fmt.Errorf("ошибка при распаковке архива: %w", err)
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
	// Генерируем случайное число для имени, чтобы избежать конфликтов

	backupDir := filepath.Join(backupBaseDir, fmt.Sprintf("backup_%d", buildNumber))

	tui.InfoF("Создание нового бэкапа в: %s", backupDir)
	if err := os.MkdirAll(backupDir, 0755); err != nil {
		return err
	}

	var copiedCount int
	for _, filePathInArchive := range filesToReplace {
		// Извлекаем только имя файла из полного пути в архиве
		fileName := filepath.Base(filePathInArchive)

		// Ищем файл в корне installDir, а не по полному пути из архива
		sourcePath := filepath.Join(installDir, fileName)
		destPath := filepath.Join(backupDir, fileName)

		// Копируем только если исходный файл существует
		if _, err := os.Stat(sourcePath); err == nil {
			sourceFile, err := os.Open(sourcePath)
			if err != nil {
				continue
			}
			defer sourceFile.Close()

			// Создаем подкаталоги в бэкапе, если они есть во вложенных путях
			if err := os.MkdirAll(filepath.Dir(destPath), 0755); err != nil {
				continue
			}

			destFile, err := os.Create(destPath)
			if err != nil {
				continue
			}
			defer destFile.Close()

			io.Copy(destFile, sourceFile)
			copiedCount++
		}
	}
	tui.InfoF("Зарезервировано %d файлов.", copiedCount)
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
	// и т.д.
	return "", fmt.Errorf("не удалось найти полное имя версии для %s", shortVersion)
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
