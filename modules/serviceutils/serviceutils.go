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
	"log/slog"
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
	SourcePath       string
	OriginalPath     string
	OriginalBaseName string
}

// ActionType определяет тип утилиты
type ActionType int

const (
	ActionCleanTemp ActionType = iota
	ActionCollectLogs
	ActionViewLog
	ActionOrderCheck
	ActionFrontTools
)

// ServiceUtilsConfig хранит параметры для выполнения
type ServiceUtilsConfig struct {
	Action ActionType
	// Параметры для CollectLogs
	LogDays int
	LogDirs []string
	// Параметры для ViewLog
	LogFileToView string
	// Параметры для OrderCheck / FrontTools
	TargetDatabasePath string
	DatabaseType       string // "db" or "sdf"
}

type Module struct{}

func (m *Module) ID() string       { return "ServiceUtils" }
func (m *Module) MenuText() string { return "Утилиты обслуживания" }

// Run - точка входа (UI)
func (m *Module) Run(am core.AssetManager, wu core.WinUtils) error {
	slog.Info("Запуск модуля ServiceUtils")
	ctx := tui.NewConsoleContext()

	for {
		// 1. Конфигурация
		cfg, err := m.Configure(ctx, am, wu)
		if err != nil {
			slog.Error("Ошибка конфигурации ServiceUtils", "error", err)
			ctx.Error(fmt.Sprintf("Ошибка: %v", err))
			tui.WaitForAnyKey()
			continue
		}
		if cfg == nil {
			slog.Info("Выход из ServiceUtils в главное меню")
			return nil // Выход
		}

		// 2. Выполнение
		slog.Info("Запуск выполнения задачи ServiceUtils", "action", cfg.Action)
		if err := m.Execute(ctx, am, wu, cfg); err != nil {
			slog.Error("Ошибка выполнения задачи", "action", cfg.Action, "error", err)
			ctx.Error(fmt.Sprintf("Ошибка: %v", err))
		} else {
			slog.Info("Задача выполнена успешно", "action", cfg.Action)
		}

		tui.WaitForAnyKey()
	}
}

// Configure - Сбор данных (UI слой)
func (m *Module) Configure(ctx core.TaskContext, am core.AssetManager, wu core.WinUtils) (*ServiceUtilsConfig, error) {
	tui.ClearScreen()
	tui.Title("\n--- Меню утилит обслуживания ---")
	fmt.Println(" 1. Очистка временных файлов")
	fmt.Println(" 2. Сборщик логов в архив")
	fmt.Println(" 3. Просмотр лога в реальном времени (tail -f)")
	fmt.Println(" 4. OrderCheck")
	fmt.Println(" 5. FrontTools")
	fmt.Println("\n 0. Назад")
	fmt.Print("Выберите пункт: ")

	key, err := tui.ReadKey()
	if err != nil {
		return nil, err
	}

	slog.Info("Пользователь выбрал пункт меню ServiceUtils", "key", key)

	switch key {
	case "1":
		return &ServiceUtilsConfig{Action: ActionCleanTemp}, nil

	case "2": // Collect Logs
		return m.configureCollectLogs(am)

	case "3": // Tail Log
		return m.configureViewLog(am)

	case "4": // OrderCheck
		return m.configureOrderCheck(wu)

	case "5": // FrontTools
		return m.configureFrontTools()

	case "0":
		return nil, nil
	default:
		return m.Configure(ctx, am, wu)
	}
}

// Execute - Выполнение логики (Worker слой)
func (m *Module) Execute(ctx core.TaskContext, am core.AssetManager, wu core.WinUtils, cfg *ServiceUtilsConfig) error {
	switch cfg.Action {
	case ActionCleanTemp:
		return m.cleanTempFiles(ctx, am)
	case ActionCollectLogs:
		return m.collectLogs(ctx, am, cfg)
	case ActionViewLog:
		return m.tailFile(ctx, cfg.LogFileToView, 50)
	case ActionOrderCheck:
		return m.runOrderCheckFlow(ctx, am, wu, cfg)
	case ActionFrontTools:
		return m.runFrontToolsFlow(ctx, am, wu, cfg)
	}
	return nil
}

// --- CONFIGURE HELPERS ---

func (m *Module) configureCollectLogs(am core.AssetManager) (*ServiceUtilsConfig, error) {
	fmt.Print("\nЗа какое количество дней нужно собрать логи? (например, 7): ")
	reader := bufio.NewReader(os.Stdin)
	daysStr, _ := reader.ReadString('\n')
	days, err := strconv.Atoi(strings.TrimSpace(daysStr))
	if err != nil || days <= 0 {
		return nil, errors.New("некорректное число")
	}

	slog.Debug("Поиск доступных директорий логов")
	availableDirs := m.findLogDirectories(am.Cfg())
	if len(availableDirs) == 0 {
		return nil, errors.New("директории логов не найдены")
	}

	tui.Title("\n--- Доступные директории ---")
	for i, dir := range availableDirs {
		fmt.Printf(" %d. %s\n", i+1, dir)
	}
	fmt.Print("Укажите номера через запятую (например: 1,3): ")
	choiceStr, _ := reader.ReadString('\n')

	var selectedDirs []string
	parts := strings.Split(strings.TrimSpace(choiceStr), ",")
	for _, part := range parts {
		idx, err := strconv.Atoi(strings.TrimSpace(part))
		if err == nil && idx >= 1 && idx <= len(availableDirs) {
			selectedDirs = append(selectedDirs, availableDirs[idx-1])
		}
	}
	if len(selectedDirs) == 0 {
		return nil, errors.New("ничего не выбрано")
	}

	slog.Info("Сконфигурирован сбор логов", "days", days, "dirs_count", len(selectedDirs))
	return &ServiceUtilsConfig{
		Action:  ActionCollectLogs,
		LogDays: days,
		LogDirs: selectedDirs,
	}, nil
}

func (m *Module) configureViewLog(am core.AssetManager) (*ServiceUtilsConfig, error) {
	slog.Debug("Поиск директорий для просмотра логов")
	allLogDirs := m.findLogDirectories(am.Cfg())
	if len(allLogDirs) == 0 {
		return nil, errors.New("нет директорий с логами")
	}

	// Поиск файлов за сегодня
	today := time.Now()
	startOfDay := time.Date(today.Year(), today.Month(), today.Day(), 0, 0, 0, 0, today.Location())

	dirsWithTodayLogs := make(map[string][]string)
	var dirList []string

	tui.Info("Поиск сегодняшних логов...")
	slog.Info("Сканирование директорий на наличие свежих логов")

	for _, dir := range allLogDirs {
		var todayFiles []string
		filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return nil
			}
			if !strings.HasSuffix(strings.ToLower(d.Name()), ".log") && !strings.HasSuffix(strings.ToLower(d.Name()), ".txt") {
				return nil
			}
			if info, err := d.Info(); err == nil && info.ModTime().After(startOfDay) {
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
		slog.Warn("Свежие логи не найдены")
		return nil, errors.New("логов за сегодня не найдено")
	}
	sort.Strings(dirList)

	// Выбор папки
	tui.Title("\n--- Папки с логами за сегодня ---")
	for i, dir := range dirList {
		fmt.Printf(" %d. %s (%d шт.)\n", i+1, dir, len(dirsWithTodayLogs[dir]))
	}
	fmt.Print("Выберите папку: ")
	reader := bufio.NewReader(os.Stdin)
	choiceStr, _ := reader.ReadString('\n')
	choice, err := strconv.Atoi(strings.TrimSpace(choiceStr))
	if err != nil || choice < 1 || choice > len(dirList) {
		return nil, errors.New("неверный выбор")
	}

	selectedDir := dirList[choice-1]
	files := dirsWithTodayLogs[selectedDir]

	// Выбор файла
	tui.Title(fmt.Sprintf("\n--- Файлы в %s ---", selectedDir))
	for i, file := range files {
		fmt.Printf(" %d. %s\n", i+1, filepath.Base(file))
	}
	fmt.Print("Выберите файл: ")
	choiceStr, _ = reader.ReadString('\n')
	choice, err = strconv.Atoi(strings.TrimSpace(choiceStr))
	if err != nil || choice < 1 || choice > len(files) {
		return nil, errors.New("неверный выбор")
	}

	selectedFile := files[choice-1]
	slog.Info("Выбран файл для просмотра", "path", selectedFile)

	return &ServiceUtilsConfig{
		Action:        ActionViewLog,
		LogFileToView: selectedFile,
	}, nil
}

func (m *Module) configureOrderCheck(wu core.WinUtils) (*ServiceUtilsConfig, error) {
	slog.Info("Поиск баз данных для OrderCheck")
	// Поиск БД
	alcoholDb, errAlc := m.findAlcoholMarkingPluginDb(wu)
	dbType, errType := m.detectIikoFrontDbType()

	var iikoFrontDb string
	if errType == nil {
		appData := os.ExpandEnv("$APPDATA")
		iikoFrontDb = filepath.Join(appData, "iiko", "CashServer", "EntitiesStorage", "Entities", "entities."+dbType)
	} else {
		slog.Warn("Не удалось определить тип БД iikoFront", "error", errType)
	}

	if errAlc != nil && errType != nil {
		slog.Error("Ни одна база данных не найдена")
		return nil, errors.New("базы данных не найдены")
	}

	targetDb := iikoFrontDb
	if errAlc == nil {
		slog.Info("Найдена БД Алкоплагина", "path", alcoholDb)
		fmt.Println("\nВыберите базу данных:")
		fmt.Printf(" 1. Алкоплагин: %s\n", alcoholDb)
		if iikoFrontDb != "" {
			fmt.Printf(" 2. iikoFront: %s\n", iikoFrontDb)
		}
		fmt.Print("Выбор: ")
		key, _ := tui.ReadKey()
		if key == "1" {
			targetDb = alcoholDb
		}
	} else {
		slog.Info("БД Алкоплагина не найдена, используем iikoFront", "path", iikoFrontDb)
	}

	slog.Info("Выбрана БД для OrderCheck", "path", targetDb)
	return &ServiceUtilsConfig{
		Action:             ActionOrderCheck,
		TargetDatabasePath: targetDb,
	}, nil
}

func (m *Module) configureFrontTools() (*ServiceUtilsConfig, error) {
	slog.Info("Определение типа БД для FrontTools")
	dbType, err := m.detectIikoFrontDbType()
	if err != nil {
		slog.Warn("Не удалось определить тип БД, fallback to sdf", "error", err)
		tui.Warn("Не удалось определить тип БД автоматически. Используем .sdf по умолчанию.")
		dbType = "sdf"
	} else {
		slog.Info("Определен тип БД", "type", dbType)
	}
	return &ServiceUtilsConfig{
		Action:       ActionFrontTools,
		DatabaseType: dbType,
	}, nil
}

// --- EXECUTE IMPLEMENTATION ---

func (m *Module) cleanTempFiles(ctx core.TaskContext, am core.AssetManager) error {
	ctx.SetStatus("Очистка временных файлов")
	slog.Info("Начало очистки временных файлов")
	pathsToCleanRaw := am.Cfg().MaintenanceConfig.TempPaths
	var totalFreed int64

	for _, rawPath := range pathsToCleanRaw {
		expandedPath := os.ExpandEnv(rawPath)
		var paths []string
		if strings.Contains(expandedPath, "*") {
			matches, _ := filepath.Glob(expandedPath)
			paths = append(paths, matches...)
		} else {
			paths = append(paths, expandedPath)
		}

		for _, path := range paths {
			ctx.Info(fmt.Sprintf("Сканирование: %s", path))
			slog.Debug("Обработка пути", "path", path)

			entries, err := os.ReadDir(path)
			if err == nil {
				for _, e := range entries {
					fullPath := filepath.Join(path, e.Name())
					size, _ := getPathSize(fullPath)
					if err := os.RemoveAll(fullPath); err == nil {
						totalFreed += size
					} else {
						slog.Warn("Не удалось удалить", "path", fullPath, "error", err)
					}
				}
			} else if !os.IsNotExist(err) {
				// Если это файл
				fi, err := os.Stat(path)
				if err == nil && !fi.IsDir() {
					size := fi.Size()
					if err := os.Remove(path); err == nil {
						totalFreed += size
					} else {
						slog.Warn("Не удалось удалить файл", "path", path, "error", err)
					}
				}
			}
		}
	}
	msg := fmt.Sprintf("Освобождено: %.2f MB", float64(totalFreed)/1024/1024)
	ctx.Success(msg)
	slog.Info("Очистка завершена", "freed_bytes", totalFreed)
	return nil
}

func (m *Module) collectLogs(ctx core.TaskContext, am core.AssetManager, cfg *ServiceUtilsConfig) error {
	ctx.SetStatus("Сбор логов")
	slog.Info("Начало сбора логов", "days", cfg.LogDays)

	cutoffDate := time.Now().AddDate(0, 0, -cfg.LogDays)
	ctx.Info(fmt.Sprintf("Поиск файлов новее %s", cutoffDate.Format("2006-01-02")))

	var filesToArchive []fileToArchive
	tempExtractDir := filepath.Join(am.Cfg().RootPath, "temp", "log_collector_extract")
	// Создаем временную папку для распакованных логов
	if err := os.MkdirAll(tempExtractDir, 0755); err != nil {
		slog.Error("Не удалось создать temp dir", "path", tempExtractDir, "error", err)
		return err
	}
	defer os.RemoveAll(tempExtractDir)

	for _, dir := range cfg.LogDirs {
		ctx.Info(fmt.Sprintf("Сканирование %s...", dir))
		slog.Debug("Сканирование директории", "path", dir)

		filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return nil
			}
			info, err := d.Info()
			if err != nil {
				return nil
			}
			// Проверяем дату изменения файла (или архива)
			if !info.ModTime().After(cutoffDate) {
				return nil
			}

			lower := strings.ToLower(d.Name())
			if strings.HasSuffix(lower, ".log") || strings.HasSuffix(lower, ".txt") {
				// Обычный файл лога
				filesToArchive = append(filesToArchive, fileToArchive{
					SourcePath:       path,
					OriginalPath:     path,
					OriginalBaseName: d.Name(),
				})
			} else if strings.HasSuffix(lower, ".zip") || strings.HasSuffix(lower, ".gz") || strings.HasSuffix(lower, ".7z") {
				// Архив. Нужно распаковать и добавить содержимое.
				// Используем ту же логику, что была в старом файле.
				ctx.Info(fmt.Sprintf("  Обработка архива: %s", d.Name()))
				slog.Debug("Найден архив, попытка извлечения логов", "archive", path)

				if extracted, err := m.handleArchive(path, tempExtractDir); err == nil {
					filesToArchive = append(filesToArchive, extracted...)
					slog.Debug("Из архива извлечено файлов", "count", len(extracted))
				} else {
					ctx.Warn(fmt.Sprintf("    Не удалось обработать архив %s", d.Name()))
					slog.Warn("Ошибка обработки архива", "archive", path, "error", err)
				}
			}
			return nil
		})
	}

	if len(filesToArchive) == 0 {
		slog.Warn("Файлы для архивации не найдены")
		return errors.New("файлы не найдены")
	}

	archiveDir := filepath.Join(am.Cfg().RootPath, "log_collector")
	_ = os.MkdirAll(archiveDir, 0755)
	archiveName := fmt.Sprintf("logs_%s_%ddelta.zip", time.Now().Format("2006-01-02_1504"), cfg.LogDays)
	archivePath := filepath.Join(archiveDir, archiveName)

	ctx.Info("Создание архива...")
	slog.Info("Создание zip-архива", "path", archivePath, "files_count", len(filesToArchive))

	if err := m.createLogArchive(filesToArchive, archivePath); err != nil {
		slog.Error("Ошибка создания архива", "error", err)
		return err
	}
	ctx.Success(fmt.Sprintf("Архив создан: %s", archivePath))
	return nil
}

func (m *Module) tailFile(ctx core.TaskContext, filePath string, lines int) error {
	ctx.SetStatus("Просмотр лога")
	slog.Info("Запуск просмотра лога (tail)", "file", filePath)

	tui.InfoF("Файл: %s", filePath)
	tui.Info("--- [Ctrl+C] выход ---")

	file, err := os.Open(filePath)
	if err != nil {
		slog.Error("Не удалось открыть файл", "error", err)
		return err
	}
	defer file.Close()

	stat, _ := file.Stat()
	if stat.Size() > 0 {
		startPos, _ := findStartOfLastNLines(file, lines)
		file.Seek(startPos, io.SeekStart)
	}

	cancelCtx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	_, _ = io.Copy(os.Stdout, file) // Вывод хвоста

	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-cancelCtx.Done():
			fmt.Println("\n--- Выход ---")
			slog.Info("Просмотр лога завершен пользователем")
			return nil
		case <-ticker.C:
			_, _ = io.Copy(os.Stdout, file)
		}
	}
}

func (m *Module) runOrderCheckFlow(ctx core.TaskContext, am core.AssetManager, wu core.WinUtils, cfg *ServiceUtilsConfig) error {
	ctx.SetStatus("Запуск OrderCheck")
	slog.Info("Начало процесса запуска OrderCheck")

	// Проверка наличия ассета
	if _, ok := am.Cfg().AssetCatalog["ordercheck"]; !ok {
		return errors.New("ассет ordercheck не найден в конфиге")
	}

	ctx.Info("Скачивание OrderCheck...")
	slog.Debug("Скачивание ассета ordercheck")
	finalPath, err := am.Get("ordercheck")
	if err != nil {
		return err
	}

	exePath := filepath.Join(finalPath, "OrderCheck.exe")
	if _, err := os.Stat(exePath); os.IsNotExist(err) {
		slog.Info("OrderCheck.exe не в корне, поиск рекурсивно")
		if found, err := wu.FindFileRecursive(finalPath, "OrderCheck.exe"); err == nil {
			exePath = found
		} else {
			return errors.New("OrderCheck.exe не найден")
		}
	}

	ctx.Info(fmt.Sprintf("Запуск с БД: %s", cfg.TargetDatabasePath))
	slog.Info("Выполнение команды", "exe", exePath, "arg", cfg.TargetDatabasePath)

	_, err = wu.RunCommand(exePath, cfg.TargetDatabasePath)
	if err == nil {
		ctx.Success("OrderCheck завершен.")
	} else {
		slog.Error("Ошибка выполнения OrderCheck", "error", err)
	}
	return err
}

func (m *Module) runFrontToolsFlow(ctx core.TaskContext, am core.AssetManager, wu core.WinUtils, cfg *ServiceUtilsConfig) error {
	ctx.SetStatus("Запуск FrontTools")
	slog.Info("Начало процесса запуска FrontTools", "db_type", cfg.DatabaseType)

	assetName := "fronttools_db"
	if cfg.DatabaseType == "sdf" {
		assetName = "fronttools_sdf"
	}

	if _, ok := am.Cfg().AssetCatalog[assetName]; !ok {
		return fmt.Errorf("ассет %s не найден", assetName)
	}

	ctx.Info("Скачивание FrontTools...")
	finalPath, err := am.Get(assetName)
	if err != nil {
		return err
	}

	exePath := filepath.Join(finalPath, "FrontTools.exe")
	if _, err := os.Stat(exePath); os.IsNotExist(err) {
		if found, err := wu.FindFileRecursive(finalPath, "FrontTools.exe"); err == nil {
			exePath = found
		} else {
			return errors.New("FrontTools.exe не найден")
		}
	}

	// Запуск
	if wu.IsAdmin() {
		ctx.Info("Запуск процесса...")
		slog.Info("Запуск FrontTools (Admin)", "path", exePath)
		_, err := wu.RunCommand(exePath)
		return err
	} else {
		ctx.Warn("Попытка запуска через Планировщик (требуются права)...")
		slog.Info("Запуск FrontTools через TaskScheduler", "path", exePath)
		taskName := "goMH_FrontTools_Run"
		if err := wu.CreateScheduledTask(taskName, exePath, filepath.Dir(exePath)); err != nil {
			return err
		}
		_, err := wu.RunCommand("schtasks", "/Run", "/TN", taskName)
		time.Sleep(2 * time.Second)
		_ = wu.DeleteScheduledTaskByName(taskName)
		return err
	}
}

// --- UTILS (Private) ---

func (m *Module) findLogDirectories(cfg *config.Config) []string {
	dirMap := make(map[string]bool)
	potentialPaths := cfg.MaintenanceConfig.LogCollectorPaths
	if cfg.FrpcConfig.InstallPath != "" {
		potentialPaths = append(potentialPaths, os.ExpandEnv(cfg.FrpcConfig.InstallPath))
	}
	potentialPaths = append(potentialPaths, filepath.Join(os.ExpandEnv(cfg.RootPath), "logs"))

	for _, path := range potentialPaths {
		expanded := os.ExpandEnv(path)
		if strings.Contains(expanded, "*") {
			matches, _ := filepath.Glob(expanded)
			for _, match := range matches {
				if fi, err := os.Stat(match); err == nil && fi.IsDir() {
					dirMap[match] = true
				}
			}
		} else {
			if fi, err := os.Stat(expanded); err == nil && fi.IsDir() {
				dirMap[expanded] = true
			}
		}
	}
	var res []string
	for k := range dirMap {
		res = append(res, k)
	}
	return res
}

func (m *Module) detectIikoFrontDbType() (string, error) {
	appData := os.ExpandEnv("$APPDATA")
	base := filepath.Join(appData, "iiko", "CashServer", "EntitiesStorage", "Entities", "entities")
	if _, err := os.Stat(base + ".db"); err == nil {
		return "db", nil
	}
	if _, err := os.Stat(base + ".sdf"); err == nil {
		return "sdf", nil
	}
	return "", errors.New("not found")
}

func (m *Module) findAlcoholMarkingPluginDb(wu core.WinUtils) (string, error) {
	appData := os.ExpandEnv("$APPDATA")
	path := filepath.Join(appData, "iiko", "CashServer", "EntitiesStorage", "Plugins")
	return wu.FindNewestFileByPattern(path, "AlcoholMarkingPluginStorage.sdf")
}

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

func (m *Module) createLogArchive(files []fileToArchive, archivePath string) error {
	f, err := os.Create(archivePath)
	if err != nil {
		return err
	}
	defer f.Close()
	zw := zip.NewWriter(f)
	defer zw.Close()

	re := regexp.MustCompile(`[\\/:]`)
	for _, file := range files {
		src, err := os.Open(file.SourcePath)
		if err != nil {
			slog.Warn("Не удалось открыть файл для архивации", "path", file.SourcePath, "error", err)
			continue
		}
		defer src.Close()

		sanitizedDir := re.ReplaceAllString(filepath.Dir(file.OriginalPath), "_")
		sanitizedDir = strings.TrimPrefix(sanitizedDir, "_")
		w, err := zw.Create(filepath.Join(sanitizedDir, file.OriginalBaseName))
		if err == nil {
			io.Copy(w, src)
		}
	}
	return nil
}

// handleArchive - гибридная функция, которая пытается распаковать архив сначала как GZIP, а потом как универсальный архив.
func (m *Module) handleArchive(archivePath, tempDir string) ([]fileToArchive, error) {
	extractedGzip, err := m.extractLogFromGzip(archivePath, tempDir)
	if err == nil {
		return []fileToArchive{extractedGzip}, nil
	}

	// Если не GZIP, пробуем как универсальный архив
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

func findStartOfLastNLines(file *os.File, n int) (int64, error) {
	stat, _ := file.Stat()
	fileSize := stat.Size()
	var readPos int64 = fileSize
	count := 0
	buf := make([]byte, 4096)

	for readPos > 0 && count < n {
		readSize := int64(4096)
		if readPos < 4096 {
			readSize = readPos
		}
		readPos -= readSize
		file.Seek(readPos, io.SeekStart)
		file.Read(buf[:readSize])
		for i := readSize - 1; i >= 0; i-- {
			if buf[i] == '\n' {
				count++
				if count >= n {
					return readPos + i + 1, nil
				}
			}
		}
	}
	return 0, nil
}
