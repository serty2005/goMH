package serviceutils

import (
	"archive/zip"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"goMH/assetmgr"
	"goMH/config"
	"goMH/core"
	"goMH/logstream"
	"goMH/tui"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
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

func (cfg *ServiceUtilsConfig) TaskConfirmation() core.TaskConfirmation {
	if cfg == nil {
		return core.TaskConfirmation{}
	}

	details := []string{"Действие: " + cfg.actionLabel()}
	switch cfg.Action {
	case ActionCollectLogs:
		details = append(details,
			fmt.Sprintf("Период: %d дн.", cfg.LogDays),
			"Каталоги: "+strings.Join(cfg.LogDirs, ", "),
		)
	case ActionViewLog:
		details = append(details, "Файл: "+cfg.LogFileToView)
	case ActionOrderCheck, ActionFrontTools:
		if cfg.TargetDatabasePath != "" {
			details = append(details, "База: "+cfg.TargetDatabasePath)
		}
		if cfg.DatabaseType != "" {
			details = append(details, "Тип БД: "+strings.ToUpper(cfg.DatabaseType))
		}
	}

	confirmLabel := "Добавить в очередь"
	if cfg.Action == ActionViewLog || cfg.Action == ActionOrderCheck || cfg.Action == ActionFrontTools {
		confirmLabel = "Запустить"
	}

	return core.TaskConfirmation{
		Details:      details,
		ConfirmLabel: confirmLabel,
	}
}

type Module struct{}

func (m *Module) ID() string       { return "ServiceUtils" }
func (m *Module) MenuText() string { return "Утилиты обслуживания" }

func (m *Module) ConfigureTask(ctx core.TaskContext, services core.ModuleServices) (any, error) {
	return m.Configure(ctx, services.AssetManager, services.WinUtils)
}

func (m *Module) BuildTask(config any) (core.ModuleTaskPlan, error) {
	cfg, err := m.taskConfig(config)
	if err != nil {
		return core.ModuleTaskPlan{}, err
	}

	title := "Утилиты обслуживания"
	signature := "serviceutils|generic"
	switch cfg.Action {
	case ActionCleanTemp:
		title = "Очистка временных файлов"
		signature = "serviceutils|clean_temp"
	case ActionCollectLogs:
		title = fmt.Sprintf("Сбор логов за %d дн.", cfg.LogDays)
		signature = fmt.Sprintf("serviceutils|collect|%d|%s", cfg.LogDays, strings.Join(cfg.LogDirs, ";"))
	case ActionViewLog:
		title = "Просмотр лога: " + filepath.Base(cfg.LogFileToView)
		signature = "serviceutils|view|" + cfg.LogFileToView
	case ActionOrderCheck:
		title = "OrderCheck: " + filepath.Base(cfg.TargetDatabasePath)
		signature = "serviceutils|ordercheck|" + cfg.TargetDatabasePath
	case ActionFrontTools:
		title = "FrontTools: " + strings.ToUpper(cfg.DatabaseType)
		signature = "serviceutils|fronttools|" + cfg.DatabaseType
	}

	plan := core.ModuleTaskPlan{
		Mode: core.ModuleRunModeQueue,
		Task: core.ModuleTaskSpec{
			Title:     title,
			Signature: signature,
		},
	}
	if cfg.Action == ActionViewLog || cfg.Action == ActionOrderCheck || cfg.Action == ActionFrontTools {
		plan.Mode = core.ModuleRunModeImmediate
		switch cfg.Action {
		case ActionViewLog:
			plan.Result.Note = "Поток лога запущен в stdout."
		case ActionOrderCheck:
			plan.Result.Note = "OrderCheck запущен."
			plan.SkipConfirmation = true
		case ActionFrontTools:
			plan.Result.Note = "FrontTools запущен."
			plan.SkipConfirmation = true
		}
	}
	return plan, nil
}

func (m *Module) ExecuteTask(ctx core.TaskContext, services core.ModuleServices, config any) error {
	cfg, err := m.taskConfig(config)
	if err != nil {
		return err
	}
	return m.Execute(ctx, services.AssetManager, services.WinUtils, cfg)
}

func (m *Module) ExecuteImmediate(ctx core.TaskContext, services core.ModuleServices, config any) (core.ModuleActionResult, error) {
	cfg, err := m.taskConfig(config)
	if err != nil {
		return core.ModuleActionResult{}, err
	}
	switch cfg.Action {
	case ActionViewLog:
		if viewer, ok := ctx.(core.LiveLogViewer); ok {
			if err := viewer.OpenLiveLog(cfg.LogFileToView); err != nil {
				return core.ModuleActionResult{}, err
			}
			return core.ModuleActionResult{Note: "Просмотр лога открыт."}, nil
		}

		_, err = m.StartLogView(ctx.Context(), cfg, nil, logstream.NewStdoutSink())
		if err != nil {
			return core.ModuleActionResult{}, err
		}
		return core.ModuleActionResult{Note: "Поток лога запущен в stdout."}, nil
	case ActionOrderCheck, ActionFrontTools:
		err = executeImmediateWithRuntime(ctx, services, func(taskServices core.ModuleServices) error {
			return m.Execute(ctx, taskServices.AssetManager, taskServices.WinUtils, cfg)
		})
		result := core.ModuleActionResult{}
		if err == nil {
			switch cfg.Action {
			case ActionOrderCheck:
				result.Note = "OrderCheck запущен."
			case ActionFrontTools:
				result.Note = "FrontTools запущен."
			}
		}
		return result, err
	default:
		return core.ModuleActionResult{}, errors.New("немедленное выполнение для этого действия не поддерживается")
	}
}

type consoleOutputWinUtils interface {
	WithConsoleOutput(stdout io.Writer, stderr io.Writer) core.WinUtils
}

func executeImmediateWithRuntime(ctx core.TaskContext, services core.ModuleServices, fn func(taskServices core.ModuleServices) error) error {
	writer := newImmediateTaskLogWriter(ctx)
	defer writer.Flush()

	taskServices := core.ModuleServices{
		AssetManager: withImmediateAssetManager(services.AssetManager, writer, ctx),
		WinUtils:     withImmediateWinUtils(services.WinUtils, writer),
	}

	return fn(taskServices)
}

func withImmediateAssetManager(am core.AssetManager, writer io.Writer, taskCtx core.TaskContext) core.AssetManager {
	manager, ok := am.(*assetmgr.Manager)
	if !ok {
		return am
	}

	return manager.WithTaskRuntime(
		writer,
		io.Discard,
		taskCtx.SetStatus,
		func(description string, percent int) {
			taskCtx.SetStatus("Скачивание " + description)
			taskCtx.SetProgress(percent)
		},
		taskCtx.SetCancelable,
		taskCtx.Context(),
	)
}

func withImmediateWinUtils(wu core.WinUtils, writer io.Writer) core.WinUtils {
	real, ok := wu.(consoleOutputWinUtils)
	if !ok {
		return wu
	}
	return real.WithConsoleOutput(writer, writer)
}

type immediateTaskLogWriter struct {
	mu     sync.Mutex
	ctx    core.TaskContext
	buffer strings.Builder
}

func newImmediateTaskLogWriter(ctx core.TaskContext) *immediateTaskLogWriter {
	return &immediateTaskLogWriter{ctx: ctx}
}

func (w *immediateTaskLogWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	w.buffer.Write(p)
	w.flushLocked(false)
	return len(p), nil
}

func (w *immediateTaskLogWriter) Flush() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.flushLocked(true)
}

func (w *immediateTaskLogWriter) flushLocked(force bool) {
	for {
		text := w.buffer.String()
		index := strings.IndexByte(text, '\n')
		if index == -1 {
			if force && text != "" {
				w.ctx.Info(strings.TrimSpace(text))
				w.buffer.Reset()
			}
			return
		}

		line := strings.TrimSpace(text[:index])
		if line != "" {
			w.ctx.Info(line)
		}

		rest := text[index+1:]
		w.buffer.Reset()
		w.buffer.WriteString(rest)
	}
}

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
	choice, err := tui.SelectItem([]tui.ChoiceItem{
		{Title: "Очистка временных файлов"},
		{Title: "Сборщик логов в архив"},
		{Title: "Просмотр лога в реальном времени"},
		{Title: "OrderCheck"},
		{Title: "FrontTools"},
	}, tui.SelectionConfig{
		Title:    "Утилиты обслуживания",
		Subtitle: "Esc для возврата в главное меню",
	})
	if err != nil {
		return nil, err
	}

	slog.Info("Пользователь выбрал пункт меню ServiceUtils", "choice", choice)

	switch choice {
	case 0:
		return &ServiceUtilsConfig{Action: ActionCleanTemp}, nil

	case 1:
		return m.configureCollectLogs(am)

	case 2:
		return m.configureViewLog(am)

	case 3:
		return m.configureOrderCheck(wu)

	case 4:
		return m.configureFrontTools()

	default:
		return nil, nil
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
		return m.runLogView(ctx, cfg)
	case ActionOrderCheck:
		return m.runOrderCheckFlow(ctx, am, wu, cfg)
	case ActionFrontTools:
		return m.runFrontToolsFlow(ctx, am, wu, cfg)
	}
	return nil
}

func (cfg *ServiceUtilsConfig) actionLabel() string {
	switch cfg.Action {
	case ActionCleanTemp:
		return "Очистка временных файлов"
	case ActionCollectLogs:
		return "Сбор логов"
	case ActionViewLog:
		return "Просмотр лога"
	case ActionOrderCheck:
		return "OrderCheck"
	case ActionFrontTools:
		return "FrontTools"
	default:
		return "Неизвестно"
	}
}

func (m *Module) StartLogView(parent context.Context, cfg *ServiceUtilsConfig, service *logstream.Service, sinks ...logstream.Sink) (*logstream.Handle, error) {
	if cfg == nil {
		return nil, errors.New("конфигурация ServiceUtils не задана")
	}
	if cfg.Action != ActionViewLog {
		return nil, errors.New("конфигурация не относится к просмотру лога")
	}
	if service == nil {
		service = logstream.NewService()
	}

	activeSinks := make([]logstream.Sink, 0, len(sinks))
	for _, sink := range sinks {
		if sink != nil {
			activeSinks = append(activeSinks, sink)
		}
	}
	if len(activeSinks) == 0 {
		return nil, errors.New("для просмотра лога не задан ни один sink")
	}

	sink := logstream.NewMultiSink(activeSinks...)
	return service.StartTail(parent, logstream.TailRequest{
		FilePath:     cfg.LogFileToView,
		StartLines:   50,
		PollInterval: 500 * time.Millisecond,
		IdleTimeout:  30 * time.Second,
		Sink:         sink,
	})
}

func (m *Module) taskConfig(config any) (*ServiceUtilsConfig, error) {
	cfg, ok := config.(*ServiceUtilsConfig)
	if !ok || cfg == nil {
		return nil, fmt.Errorf("неверный конфиг задачи для модуля %s", m.ID())
	}
	return cfg, nil
}

// --- CONFIGURE HELPERS ---

func (m *Module) configureCollectLogs(am core.AssetManager) (*ServiceUtilsConfig, error) {
	daysStr, err := tui.PromptText(tui.InputConfig{
		Title:        "Период сбора логов",
		Subtitle:     "Укажите количество дней, например 7",
		Placeholder:  "7",
		InitialValue: "7",
		Validate: func(value string) error {
			days, err := strconv.Atoi(strings.TrimSpace(value))
			if err != nil || days <= 0 {
				return errors.New("нужно указать положительное число")
			}
			return nil
		},
	})
	if err != nil {
		return nil, err
	}
	days, err := strconv.Atoi(strings.TrimSpace(daysStr))
	if err != nil || days <= 0 {
		return nil, errors.New("некорректное число")
	}

	slog.Debug("Поиск доступных директорий логов")
	availableDirs := m.findLogDirectories(am.Cfg())
	if len(availableDirs) == 0 {
		return nil, errors.New("директории логов не найдены")
	}

	items := make([]tui.ChoiceItem, 0, len(availableDirs))
	for _, dir := range availableDirs {
		items = append(items, tui.ChoiceItem{Title: dir})
	}

	indices, err := tui.SelectItems(items, tui.SelectionConfig{
		Title:    "Доступные директории логов",
		Subtitle: "Пробел отметить, Enter подтвердить",
		Multi:    true,
	})
	if err != nil {
		return nil, err
	}

	selectedDirs := make([]string, 0, len(indices))
	for _, idx := range indices {
		if idx >= 0 && idx < len(availableDirs) {
			selectedDirs = append(selectedDirs, availableDirs[idx])
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

	dirItems := make([]tui.ChoiceItem, 0, len(dirList))
	for _, dir := range dirList {
		dirItems = append(dirItems, tui.ChoiceItem{
			Title:       dir,
			Description: fmt.Sprintf("Файлов за сегодня: %d", len(dirsWithTodayLogs[dir])),
		})
	}
	choice, err := tui.SelectItem(dirItems, tui.SelectionConfig{
		Title:    "Папки с логами за сегодня",
		Subtitle: "Выберите папку для просмотра",
	})
	if err != nil {
		return nil, err
	}
	if choice < 0 || choice >= len(dirList) {
		return nil, errors.New("неверный выбор")
	}
	selectedDir := dirList[choice]
	files := dirsWithTodayLogs[selectedDir]

	fileItems := make([]tui.ChoiceItem, 0, len(files))
	for _, file := range files {
		fileItems = append(fileItems, tui.ChoiceItem{
			Title:       filepath.Base(file),
			Description: file,
		})
	}
	choice, err = tui.SelectItem(fileItems, tui.SelectionConfig{
		Title:    "Файлы для просмотра",
		Subtitle: selectedDir,
		Search:   len(fileItems) > 8,
	})
	if err != nil {
		return nil, err
	}
	if choice < 0 || choice >= len(files) {
		return nil, errors.New("неверный выбор")
	}
	selectedFile := files[choice]
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
	_, iikoFrontDb, errType := m.detectIikoFrontDb()

	if errType != nil {
		slog.Warn("Не удалось определить тип БД iikoFront", "error", errType)
	}

	if errAlc != nil && errType != nil {
		slog.Error("Ни одна база данных не найдена")
		return nil, errors.New("базы данных не найдены")
	}

	targetDb := iikoFrontDb
	if errAlc == nil {
		slog.Info("Найдена БД Алкоплагина", "path", alcoholDb)
		items := []tui.ChoiceItem{
			{Title: "Алкоплагин", Description: alcoholDb},
		}
		if iikoFrontDb != "" {
			items = append(items, tui.ChoiceItem{Title: "iikoFront", Description: iikoFrontDb})
		}
		choice, err := tui.SelectItem(items, tui.SelectionConfig{
			Title:    "Выберите базу данных",
			Subtitle: "Esc оставит текущий вариант",
		})
		if err != nil {
			return nil, err
		}
		if choice == 0 {
			targetDb = alcoholDb
		} else if choice == 1 && iikoFrontDb != "" {
			targetDb = iikoFrontDb
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
	dbType, dbPath, err := m.detectIikoFrontDb()
	if err != nil {
		slog.Warn("Не удалось определить тип БД, fallback to sdf", "error", err)
		tui.Warn("Не удалось определить тип БД автоматически. Используем .sdf по умолчанию.")
		dbType = "sdf"
		appData := os.ExpandEnv("$APPDATA")
		dbPath = filepath.Join(appData, "iiko", "CashServer", "EntitiesStorage", "Entities", "entities.sdf")
	} else {
		slog.Info("Определен тип БД", "type", dbType, "path", dbPath)
	}
	return &ServiceUtilsConfig{
		Action:             ActionFrontTools,
		DatabaseType:       dbType,
		TargetDatabasePath: dbPath,
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

func (m *Module) runLogView(ctx core.TaskContext, cfg *ServiceUtilsConfig) error {
	ctx.SetStatus("Просмотр лога")
	ctx.Info("Файл: " + cfg.LogFileToView)
	slog.Info("Запуск просмотра лога", "file", cfg.LogFileToView)

	handle, err := m.StartLogView(ctx.Context(), cfg, logstream.NewService(), logstream.NewTaskContextSink(ctx))
	if err != nil {
		slog.Error("Не удалось запустить просмотр лога", "file", cfg.LogFileToView, "error", err)
		return err
	}

	err = handle.Wait()
	switch {
	case err == nil:
		ctx.Info("Новых строк нет более 30 секунд. Просмотр завершен.")
		return nil
	case errors.Is(err, context.Canceled):
		ctx.Warn("Просмотр лога остановлен.")
		return err
	default:
		return err
	}
}

func (m *Module) runOrderCheckFlow(ctx core.TaskContext, am core.AssetManager, wu core.WinUtils, cfg *ServiceUtilsConfig) error {
	ctx.SetStatus("Запуск OrderCheck")
	slog.Info("Начало процесса запуска OrderCheck")

	// 1. Проверка наличия ассета
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

	// 2. Логика Graceful Shutdown (ТОЛЬКО для entities.db/sdf)
	dbFileName := strings.ToLower(filepath.Base(cfg.TargetDatabasePath))
	if dbFileName == "entities.db" || dbFileName == "entities.sdf" {
		slog.Info("Выбрана основная БД iikoFront (entities), проверка запущенных процессов", "db", dbFileName)
		const iikoProcessName = "iikoFront"

		isRunning, err := wu.IsProcessRunning(iikoProcessName)
		if err != nil {
			slog.Warn("Не удалось проверить статус процесса iikoFront", "error", err)
		}

		if isRunning {
			ctx.Info("ВНИМАНИЕ: Обнаружен запущенный iikoFront.")
			ctx.Info("Для работы с entities требуется завершение кассовой программы.")
			ctx.Info("Попытка корректного завершения...")

			if err := wu.GracefulShutdownProcess(iikoProcessName); err != nil {
				slog.Warn("Ошибка отправки команды закрытия", "error", err)
			}

			// Цикл ожидания (30 сек)
			const maxRetries = 6
			const retryInterval = 5 * time.Second
			stopped := false

			for i := 0; i < maxRetries; i++ {
				running, _ := wu.IsProcessRunning(iikoProcessName)
				if !running {
					stopped = true
					break
				}
				// Индикация ожидания в статусе
				ctx.SetStatus(fmt.Sprintf("Ожидание закрытия iikoFront (%d/%d)...", i+1, maxRetries))
				if err := waitForTaskContext(ctx, retryInterval); err != nil {
					return err
				}
			}

			if !stopped {
				// Не прерываем выполнение, просто предупреждаем
				ctx.Warn("Не удалось автоматически закрыть iikoFront за отведенное время.")
				ctx.Warn("OrderCheck попытается закрыть процесс самостоятельно при запуске.")
				slog.Warn("Таймаут ожидания закрытия iikoFront, продолжение запуска OrderCheck")
			} else {
				ctx.Success("iikoFront успешно остановлен.")
				slog.Info("iikoFront остановлен перед запуском OrderCheck")
			}
		}
	} else {
		slog.Info("Выбрана БД, отличная от entities, остановка iikoFront не требуется", "db", dbFileName)
	}

	// 3. Запуск OrderCheck
	ctx.SetStatus("Запуск OrderCheck...")
	ctx.Info(fmt.Sprintf("Открытие БД: %s", cfg.TargetDatabasePath))
	slog.Info("Выполнение команды", "exe", exePath, "arg", cfg.TargetDatabasePath)

	err = wu.StartDetachedProcess(exePath, cfg.TargetDatabasePath)
	if err == nil {
		ctx.Success("OrderCheck запущен.")
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
	workingDir := filepath.Dir(cfg.TargetDatabasePath)
	if wu.IsAdmin() {
		ctx.Info("Запуск процесса...")
		slog.Info("Запуск FrontTools (Admin)", "path", exePath, "working_dir", workingDir)
		err := wu.StartDetachedProcessInDir(exePath, workingDir)
		if err == nil {
			ctx.Success("FrontTools запущен.")
		}
		return err
	} else {
		ctx.Warn("Попытка запуска через Планировщик (требуются права)...")
		slog.Info("Запуск FrontTools через TaskScheduler", "path", exePath, "working_dir", workingDir)
		taskName := "goMH_FrontTools_Run"
		if err := wu.CreateScheduledTask(taskName, exePath, "", workingDir); err != nil {
			return err
		}
		_, err := wu.RunCommand("schtasks", "/Run", "/TN", taskName)
		if err == nil {
			ctx.Success("FrontTools запущен.")
		}
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

func (m *Module) detectIikoFrontDb() (string, string, error) {
	appData := os.ExpandEnv("$APPDATA")
	base := filepath.Join(appData, "iiko", "CashServer", "EntitiesStorage", "Entities", "entities")
	if _, err := os.Stat(base + ".db"); err == nil {
		return "db", base + ".db", nil
	}
	if _, err := os.Stat(base + ".sdf"); err == nil {
		return "sdf", base + ".sdf", nil
	}
	return "", "", errors.New("not found")
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

func waitForTaskContext(ctx core.TaskContext, delay time.Duration) error {
	if delay <= 0 {
		return nil
	}

	timer := time.NewTimer(delay)
	defer timer.Stop()

	select {
	case <-ctx.Context().Done():
		return ctx.Context().Err()
	case <-timer.C:
		return nil
	}
}
