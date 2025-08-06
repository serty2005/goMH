package serviceutils

import (
	"archive/zip"
	"bufio"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"goMH/config"
	"goMH/core"
	"goMH/tui"
	"io"
	"io/fs"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/mholt/archives"
)

// fileToArchive хранит путь к файлу на диске и желаемый путь внутри архива.
type fileToArchive struct {
	SourcePath       string // Физический путь на диске (может быть временным)
	OriginalPath     string // Оригинальный путь к файлу/архиву (для имени папки)
	OriginalBaseName string // Имя файла, которое будет в итоговом архиве
}

type Module struct{}

func (m *Module) ID() string {
	return "ServiceUtils"
}

func (m *Module) MenuText() string {
	return "Утилиты обслуживания"
}

// Run управляет подменю утилит
func (m *Module) Run(am core.AssetManager, wu core.WinUtils) error {
	reader := bufio.NewReader(os.Stdin)

	for {
		tui.Title("\n--- Меню утилит обслуживания ---")
		fmt.Println(" 1. Очистка временных файлов")
		fmt.Println(" 2. Сборщик логов в архив")
		fmt.Println(" 3. Просмотр лога в реальном времени (tail -f)")
		fmt.Println("\n 0. Назад в главное меню")
		fmt.Print("Выберите пункт: ")

		choiceStr, _ := reader.ReadString('\n')
		choiceStr = strings.TrimSpace(choiceStr)

		var err error
		switch choiceStr {
		case "1":
			err = m.cleanTempFiles(am)
		case "2":
			err = m.collectLogs(am)
		case "3":
			err = m.viewLog(am)
		case "0":
			tui.Info("Возврат в главное меню.")
			return nil
		default:
			tui.Error("Неверный выбор. Попробуйте снова.")
			time.Sleep(2 * time.Second)
			continue
		}

		if err != nil {
			tui.Error(fmt.Sprintf("\n--- ОПЕРАЦИЯ ЗАВЕРШИЛАСЬ С ОШИБКОЙ ---\n%v\n---------------------------------------\n", err))
		} else {
			tui.Success("\n--- Операция завершена успешно. ---")
		}

		fmt.Println("\nНажмите Enter, чтобы вернуться в меню утилит...")
		reader.ReadString('\n')
	}
}

// --- Пункт 1: Очистка временных файлов ---
func (m *Module) cleanTempFiles(am core.AssetManager) error {
	pathsToCleanRaw := am.Cfg().MaintenanceConfig.TempPaths
	if len(pathsToCleanRaw) == 0 {
		return errors.New("список путей для очистки 'TempPaths' в конфигурации пуст")
	}

	tui.Info("Начинается анализ и очистка временных файлов...")
	var totalFreed int64

	var finalPathsToProcess []string
	for _, rawPath := range pathsToCleanRaw {
		expandedPath := os.ExpandEnv(rawPath)
		if strings.Contains(expandedPath, "*") {
			matches, err := filepath.Glob(expandedPath)
			if err == nil {
				finalPathsToProcess = append(finalPathsToProcess, matches...)
			} else {
				tui.Warn(fmt.Sprintf("Ошибка при поиске по шаблону '%s': %v", expandedPath, err))
			}
		} else {
			finalPathsToProcess = append(finalPathsToProcess, expandedPath)
		}
	}

	for _, path := range finalPathsToProcess {
		tui.InfoF("Обработка: %s", path)

		fi, err := os.Stat(path)
		if os.IsNotExist(err) {
			tui.Info("  Путь не существует. Пропускаем.")
			continue
		}
		if err != nil {
			tui.Warn(fmt.Sprintf("  Не удалось получить информацию о пути: %v. Пропускаем.", err))
			continue
		}

		if !fi.IsDir() {
			size := fi.Size()
			if err := os.Remove(path); err != nil {
				tui.Warn(fmt.Sprintf("  Не удалось удалить файл %s: %v", path, err))
			} else {
				totalFreed += size
			}
			continue
		}

		dirEntries, err := os.ReadDir(path)
		if err != nil {
			tui.Warn(fmt.Sprintf("  Не удалось прочитать директорию %s: %v. Пропускаем.", path, err))
			continue
		}

		if len(dirEntries) == 0 {
			tui.Info("  Директория пуста.")
			continue
		}

		var currentPathSize int64
		for _, entry := range dirEntries {
			fullPath := filepath.Join(path, entry.Name())
			size, err := getPathSize(fullPath)
			if err != nil {
				tui.Warn(fmt.Sprintf("  Не удалось посчитать размер %s: %v", entry.Name(), err))
				continue
			}
			currentPathSize += size
		}

		tui.InfoF("  Найдено для удаления: %.2f MB. Начинаем удаление...", float64(currentPathSize)/1024/1024)
		for _, entry := range dirEntries {
			fullPath := filepath.Join(path, entry.Name())
			if err := os.RemoveAll(fullPath); err != nil {
				tui.Warn(fmt.Sprintf("  Не удалось удалить %s: %v", fullPath, err))
			}
		}
		totalFreed += currentPathSize
	}

	tui.SuccessF("\nОчистка завершена. Всего освобождено: %.2f MB", float64(totalFreed)/1024/1024)
	return nil
}

// getPathSize рекурсивно вычисляет размер файла или содержимого директории.
func getPathSize(path string) (int64, error) {
	var size int64
	err := filepath.Walk(path, func(_ string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			size += info.Size()
		}
		return err
	})
	return size, err
}

// --- Пункт 2: Сборщик логов ---
func (m *Module) collectLogs(am core.AssetManager) error {
	reader := bufio.NewReader(os.Stdin)

	fmt.Print("За какое количество дней нужно собрать логи? (например, 7): ")
	daysStr, _ := reader.ReadString('\n')
	days, err := strconv.Atoi(strings.TrimSpace(daysStr))
	if err != nil || days <= 0 {
		return errors.New("некорректное количество дней, должно быть положительное число")
	}

	availableDirs := m.findLogDirectories(am.Cfg())
	if len(availableDirs) == 0 {
		return errors.New("не найдено ни одной доступной директории с логами на основе конфигурации")
	}

	tui.Title("\n--- Доступные директории для сбора логов ---")
	for i, dir := range availableDirs {
		fmt.Printf(" %d. %s\n", i+1, dir)
	}
	fmt.Print("Укажите номера путей, откуда собрать логи (через запятую, например: 1,3): ")
	choiceStr, _ := reader.ReadString('\n')
	choiceStr = strings.TrimSpace(choiceStr)

	var selectedDirs []string
	parts := strings.Split(choiceStr, ",")
	for _, part := range parts {
		idx, err := strconv.Atoi(strings.TrimSpace(part))
		if err != nil || idx < 1 || idx > len(availableDirs) {
			tui.Warn(fmt.Sprintf("Некорректный номер '%s', пропускаем.", part))
			continue
		}
		selectedDirs = append(selectedDirs, availableDirs[idx-1])
	}

	if len(selectedDirs) == 0 {
		return errors.New("не выбрано ни одной корректной директории для сбора логов")
	}

	cutoffDate := time.Now().AddDate(0, 0, -days)
	tui.InfoF("Поиск файлов (.log, .txt, .zip, .gz), измененных после %s", cutoffDate.Format("2006-01-02"))

	var filesToArchive []fileToArchive
	tempExtractDir, err := os.MkdirTemp("", "log_collector_extract_*")
	if err != nil {
		return fmt.Errorf("не удалось создать временную директорию: %w", err)
	}
	defer os.RemoveAll(tempExtractDir)

	for _, dir := range selectedDirs {
		tui.InfoF("Сканирование: %s", dir)
		filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return nil
			}
			info, err := d.Info()
			if err != nil {
				return nil
			}
			if !info.ModTime().After(cutoffDate) {
				return nil
			}

			lowerName := strings.ToLower(d.Name())
			if strings.HasSuffix(lowerName, ".log") || strings.HasSuffix(lowerName, ".txt") {
				filesToArchive = append(filesToArchive, fileToArchive{
					SourcePath:       path,
					OriginalPath:     path,
					OriginalBaseName: d.Name(),
				})
			} else if strings.HasSuffix(lowerName, ".zip") || strings.HasSuffix(lowerName, ".gz") || strings.HasSuffix(lowerName, ".7z") {
				tui.InfoF("  Найден архив, обработка: %s", d.Name())
				extractedLogs, err := m.handleArchive(path, tempExtractDir)
				if err != nil {
					tui.Warn(fmt.Sprintf("    Не удалось обработать архив %s: %v", d.Name(), err))
				} else {
					filesToArchive = append(filesToArchive, extractedLogs...)
				}
			}
			return nil
		})
	}

	if len(filesToArchive) == 0 {
		return errors.New("не найдено ни одного подходящего лог-файла за указанный период в выбранных папках")
	}

	return m.createLogArchive(filesToArchive, am.Cfg().RootPath, days)
}

// handleArchive - гибридная функция, которая пытается распаковать архив сначала как GZIP, а потом как универсальный архив.
func (m *Module) handleArchive(archivePath, tempDir string) ([]fileToArchive, error) {
	extractedGzip, err := m.extractLogFromGzip(archivePath, tempDir)
	if err == nil {
		return []fileToArchive{extractedGzip}, nil
	}

	tui.InfoF("    ...не является GZIP, пробую как стандартный архив (ZIP, 7z...)")
	extractedUniversal, errUniversal := m.extractLogsFromUniversalArchive(archivePath, tempDir)
	if errUniversal == nil {
		return extractedUniversal, nil
	}

	return nil, err
}

// extractLogFromGzip распаковывает ОДИН GZIP-файл.
func (m *Module) extractLogFromGzip(gzipPath, tempDir string) (fileToArchive, error) {
	gzipFile, err := os.Open(gzipPath)
	if err != nil {
		return fileToArchive{}, err
	}
	defer gzipFile.Close()

	gzipReader, err := gzip.NewReader(gzipFile)
	if err != nil {
		return fileToArchive{}, err
	}
	defer gzipReader.Close()

	baseName := gzipReader.Name
	if baseName == "" {
		baseName = strings.TrimSuffix(filepath.Base(gzipPath), filepath.Ext(gzipPath))
	}
	destPath := filepath.Join(tempDir, baseName)

	destFile, err := os.Create(destPath)
	if err != nil {
		return fileToArchive{}, err
	}
	defer destFile.Close()

	if _, err := io.Copy(destFile, gzipReader); err != nil {
		return fileToArchive{}, err
	}

	return fileToArchive{
		SourcePath:       destPath,
		OriginalPath:     gzipPath,
		OriginalBaseName: baseName,
	}, nil
}

// extractLogsFromUniversalArchive использует archives.FileSystem для распаковки ZIP, 7z и т.д.
func (m *Module) extractLogsFromUniversalArchive(archivePath, tempDir string) ([]fileToArchive, error) {
	fsys, err := archives.FileSystem(context.Background(), archivePath, nil)
	if err != nil {
		return nil, fmt.Errorf("не удалось открыть как файловую систему: %w", err)
	}

	var extractedLogs []fileToArchive
	err = fs.WalkDir(fsys, ".", func(pathInArchive string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		lowerName := strings.ToLower(pathInArchive)
		if !(strings.HasSuffix(lowerName, ".log") || strings.HasSuffix(lowerName, ".txt")) {
			return nil
		}
		srcFile, err := fsys.Open(pathInArchive)
		if err != nil {
			return nil
		}
		defer srcFile.Close()

		uniqueDestName := fmt.Sprintf("%s_%s",
			strings.TrimSuffix(filepath.Base(archivePath), filepath.Ext(archivePath)),
			strings.ReplaceAll(pathInArchive, "/", "_"))
		destPath := filepath.Join(tempDir, uniqueDestName)

		destFile, err := os.Create(destPath)
		if err != nil {
			return nil
		}
		defer destFile.Close()

		if _, err := io.Copy(destFile, srcFile); err != nil {
			return nil
		}

		extractedLogs = append(extractedLogs, fileToArchive{
			SourcePath:       destPath,
			OriginalPath:     archivePath,
			OriginalBaseName: d.Name(),
		})
		return nil
	})
	return extractedLogs, err
}

// findLogDirectories "интеллектуально" находит папки с логами с поддержкой wildcards.
func (m *Module) findLogDirectories(cfg *config.Config) []string {
	dirMap := make(map[string]bool)

	potentialPaths := []string{}
	potentialPaths = append(potentialPaths, cfg.MaintenanceConfig.LogCollectorPaths...)
	if cfg.FrpcConfig.InstallPath != "" {
		potentialPaths = append(potentialPaths, os.ExpandEnv(cfg.FrpcConfig.InstallPath))
	}
	potentialPaths = append(potentialPaths, filepath.Join(os.ExpandEnv(cfg.RootPath), "logs"))

	for _, path := range potentialPaths {
		expandedPath := os.ExpandEnv(path)
		if strings.Contains(expandedPath, "*") {
			matches, err := filepath.Glob(expandedPath)
			if err == nil {
				for _, match := range matches {
					if fi, err := os.Stat(match); err == nil && fi.IsDir() {
						dirMap[match] = true
					}
				}
			}
		} else {
			if _, err := os.Stat(expandedPath); err == nil {
				dirMap[expandedPath] = true
			}
		}
	}

	var result []string
	for dir := range dirMap {
		result = append(result, dir)
	}
	return result
}

// createLogArchive создает архив с логами.
func (m *Module) createLogArchive(files []fileToArchive, rootPath string, days int) error {
	archiveDir := filepath.Join(rootPath, "log_collector")
	if err := os.MkdirAll(archiveDir, 0755); err != nil {
		return fmt.Errorf("не удалось создать директорию для архивов %s: %w", archiveDir, err)
	}

	datetimeStr := time.Now().Format("2006-01-02_1504")
	archiveName := fmt.Sprintf("logs_%s_%ddelta.zip", datetimeStr, days)
	archivePath := filepath.Join(archiveDir, archiveName)

	tui.InfoF("Создание архива: %s", archivePath)
	archiveFile, err := os.Create(archivePath)
	if err != nil {
		return fmt.Errorf("не удалось создать файл архива: %w", err)
	}
	defer archiveFile.Close()

	zipWriter := zip.NewWriter(archiveFile)
	defer zipWriter.Close()

	re := regexp.MustCompile(`[\\/:]`)

	for _, file := range files {
		tui.InfoF("  Добавление: %s", file.OriginalPath)

		f, err := os.Open(file.SourcePath)
		if err != nil {
			tui.Warn(fmt.Sprintf("    Не удалось открыть файл: %v", err))
			continue
		}
		defer f.Close()

		dir := filepath.Dir(file.OriginalPath)
		sanitizedDir := re.ReplaceAllString(dir, "_")
		sanitizedDir = strings.TrimPrefix(sanitizedDir, "_")

		baseName := file.OriginalBaseName

		internalPath := filepath.Join(sanitizedDir, baseName)

		w, err := zipWriter.Create(internalPath)
		if err != nil {
			tui.Warn(fmt.Sprintf("    Не удалось создать запись в архиве: %v", err))
			continue
		}
		if _, err := io.Copy(w, f); err != nil {
			tui.Warn(fmt.Sprintf("    Не удалось скопировать данные в архив: %v", err))
		}
	}
	tui.SuccessF("%d файлов добавлено в архив.", len(files))
	return nil
}

// --- Пункт 3: Просмотр лога в реальном времени ---
func (m *Module) viewLog(am core.AssetManager) error {
	allLogDirs := m.findLogDirectories(am.Cfg())
	if len(allLogDirs) == 0 {
		return errors.New("не найдено ни одной директории с логами")
	}

	today := time.Now()
	year, month, day := today.Date()
	startOfDay := time.Date(year, month, day, 0, 0, 0, 0, today.Location())

	dirsWithTodayLogs := make(map[string][]string)
	var dirList []string

	for _, dir := range allLogDirs {
		var todayFiles []string
		filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return nil
			}
			lowerName := strings.ToLower(d.Name())
			if !(strings.HasSuffix(lowerName, ".log") || strings.HasSuffix(lowerName, ".txt")) {
				return nil
			}
			info, err := d.Info()
			if err != nil {
				return nil
			}
			if info.ModTime().After(startOfDay) {
				todayFiles = append(todayFiles, path)
			}
			return nil
		})

		if len(todayFiles) > 0 {
			sort.Strings(todayFiles)
			dirsWithTodayLogs[dir] = todayFiles
			dirList = append(dirList, dir)
		}
	}

	if len(dirList) == 0 {
		return errors.New("не найдено ни одного лог-файла за сегодня")
	}
	sort.Strings(dirList)

	reader := bufio.NewReader(os.Stdin)
	tui.Title("\n--- Найдены сегодняшние логи в следующих папках: ---")
	for i, dir := range dirList {
		fmt.Printf(" %d. %s (%d шт.)\n", i+1, dir, len(dirsWithTodayLogs[dir]))
	}
	fmt.Print("Выберите номер папки: ")
	choiceStr, _ := reader.ReadString('\n')
	choice, err := strconv.Atoi(strings.TrimSpace(choiceStr))
	if err != nil || choice < 1 || choice > len(dirList) {
		return errors.New("неверный выбор папки")
	}
	selectedDirKey := dirList[choice-1]
	filesInSelectedDir := dirsWithTodayLogs[selectedDirKey]

	tui.Title(fmt.Sprintf("\n--- Актуальные логи в папке: %s ---", selectedDirKey))
	for i, file := range filesInSelectedDir {
		fmt.Printf(" %d. %s\n", i+1, filepath.Base(file))
	}
	fmt.Print("Выберите номер файла для просмотра: ")
	choiceStr, _ = reader.ReadString('\n')
	choice, err = strconv.Atoi(strings.TrimSpace(choiceStr))
	if err != nil || choice < 1 || choice > len(filesInSelectedDir) {
		return errors.New("неверный выбор файла")
	}
	selectedLog := filesInSelectedDir[choice-1]

	return m.tailFile(selectedLog, 50) // <-- ВЫЗОВ С КОЛИЧЕСТВОМ СТРОК
}

// tailFile выводит последние N строк файла и продолжает следить за ним.
func (m *Module) tailFile(filePath string, lineCount int) error {
	tui.Title(fmt.Sprintf("\n--- Просмотр файла: %s ---", filePath))
	tui.Info("--- Управление: [Ctrl+C] - выход | [Ctrl+S] - пауза | [Ctrl+Q] - возобновить ---")
	time.Sleep(1 * time.Second)

	file, err := os.Open(filePath)
	if err != nil {
		return err
	}
	defer file.Close()

	startPos, err := findStartOfLastNLines(file, lineCount)
	if err != nil {
		return fmt.Errorf("не удалось определить начальную позицию: %w", err)
	}
	// Перемещаем указатель на найденную позицию
	if _, err := file.Seek(startPos, io.SeekStart); err != nil {
		return fmt.Errorf("не удалось переместить указатель в файле: %w", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// Выводим "хвост" и продолжаем следить
	if _, err := io.Copy(os.Stdout, file); err != nil {
		return err
	}

	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			fmt.Println("\n--- Просмотр завершен. ---")
			return nil
		case <-ticker.C:
			if _, err := io.Copy(os.Stdout, file); err != nil {
				tui.Warn(fmt.Sprintf("\nОшибка чтения файла (возможно, он был удален): %v", err))
				return nil
			}
		}
	}
}

// findStartOfLastNLines ищет позицию в файле, с которой начинаются последние N строк.
func findStartOfLastNLines(file *os.File, n int) (int64, error) {
	stat, err := file.Stat()
	if err != nil {
		return 0, err
	}
	fileSize := stat.Size()
	if fileSize == 0 {
		return 0, nil
	}

	const bufferSize = 4096 // Читаем блоками по 4KB
	buffer := make([]byte, bufferSize)
	lineCount := 0
	var readPos int64 = fileSize

	// Цикл чтения файла с конца
	for {
		var readSize int64 = bufferSize
		if readPos < bufferSize {
			readSize = readPos // Если осталось меньше, чем размер буфера
		}
		readPos -= readSize

		_, err := file.Seek(readPos, io.SeekStart)
		if err != nil {
			return 0, err
		}

		bytesRead, err := file.Read(buffer[:readSize])
		if err != nil {
			return 0, err
		}

		// Сканируем прочитанный блок с конца в начало
		for i := bytesRead - 1; i >= 0; i-- {
			// Ищем символ переноса строки
			if buffer[i] == '\n' {
				lineCount++
				// Если нашли N-ю строку с конца
				if lineCount >= n {
					// Возвращаем позицию следующего за \n символа
					return readPos + int64(i) + 1, nil
				}
			}
		}

		// Если дошли до начала файла, выходим из цикла
		if readPos == 0 {
			break
		}
	}

	// Если во всем файле меньше N строк, начинаем с самого начала
	return 0, nil
}
