package assetmgr

import (
	"archive/zip"
	"context"
	"errors"
	"fmt"
	"goMH/config"
	"goMH/core"
	"goMH/tui"
	"html"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/jlaffaye/ftp"
	"github.com/schollz/progressbar/v3"
	"golang.org/x/sync/errgroup"
)

var htmlTagRegex = regexp.MustCompile(`<[^>]+>`)
var contentRangeRegex = regexp.MustCompile(`^bytes\s+(\d+)-(\d+)/(\d+|\*)$`)
var contentRangeUnsatisfiedRegex = regexp.MustCompile(`^bytes\s+\*/(\d+|\*)$`)

const (
	downloadMaxAttempts = 5
	downloadRetryDelay  = time.Second
)

type Manager struct {
	cfg     *config.Config
	runtime runtimeHooks
}

type runtimeHooks struct {
	stdout     io.Writer
	progress   io.Writer
	statusFn   func(string)
	percentFn  func(string, int)
	cancelFn   func(bool)
	runtimeCtx context.Context
}

func New(cfg *config.Config) (*Manager, error) {
	if err := os.MkdirAll(cfg.RootPath, 0755); err != nil {
		return nil, fmt.Errorf("не удалось создать корневую директорию %s: %w", cfg.RootPath, err)
	}
	if err := os.MkdirAll(cfg.AssetsCachePath, 0755); err != nil {
		return nil, fmt.Errorf("не удалось создать директорию кэша %s: %w", cfg.AssetsCachePath, err)
	}
	return &Manager{
		cfg: cfg,
		runtime: runtimeHooks{
			stdout:   os.Stdout,
			progress: os.Stderr,
		},
	}, nil
}

// Cfg предоставляет доступ к конфигурации из других пакетов.
func (m *Manager) Cfg() *config.Config {
	return m.cfg
}

func (m *Manager) WithTaskRuntime(stdout io.Writer, progress io.Writer, statusFn func(string), percentFn func(string, int), cancelFn func(bool), runtimeCtx context.Context) *Manager {
	cloned := *m
	cloned.runtime = runtimeHooks{
		stdout:     stdout,
		progress:   progress,
		statusFn:   statusFn,
		percentFn:  percentFn,
		cancelFn:   cancelFn,
		runtimeCtx: runtimeCtx,
	}
	if cloned.runtime.stdout == nil {
		cloned.runtime.stdout = io.Discard
	}
	if cloned.runtime.progress == nil {
		cloned.runtime.progress = io.Discard
	}
	if cloned.runtime.runtimeCtx == nil {
		cloned.runtime.runtimeCtx = context.Background()
	}
	return &cloned
}

func (m *Manager) ConsoleWriter() io.Writer {
	if m == nil || m.runtime.stdout == nil {
		return io.Discard
	}
	return m.runtime.stdout
}

func (m *Manager) DownloadToCache(assetName string) (string, error) {
	assetInfo, ok := m.cfg.AssetCatalog[assetName]
	if !ok {
		return "", fmt.Errorf("ресурс '%s' не найден в каталоге", assetName)
	}

	fileName := filepath.Base(assetInfo.URL)
	localCachePath := filepath.Join(m.cfg.AssetsCachePath, fileName)

	downloadMethod := strings.ToUpper(assetInfo.DownloadMethod)
	if downloadMethod == "" {
		downloadMethod = "HTTP"
	}

	var err error
	if downloadMethod == "HTTP" {
		_, err = m.DownloadHTTPWithProgress(assetInfo.URL, localCachePath)
	} else if downloadMethod == "FTP" {
		parsedURL, _ := url.Parse(assetInfo.URL)
		_, err = m.DownloadFTPWithProgress(m.cfg.FTP[0], parsedURL.Path, localCachePath)
	} else {
		return "", fmt.Errorf("неизвестный метод загрузки: %s", downloadMethod)
	}

	if err != nil {
		return "", fmt.Errorf("ошибка при загрузке ресурса '%s' в кэш: %w", assetName, err)
	}

	return localCachePath, nil
}

// ProcessFromCache обрабатывает файл из кэша (копирует/распаковывает) в его конечную директорию.
func (m *Manager) ProcessFromCache(assetName, cachePath string) error {
	assetInfo, ok := m.cfg.AssetCatalog[assetName]
	if !ok {
		return fmt.Errorf("ресурс '%s' не найден в каталоге", assetName)
	}

	finalDestPath := filepath.Join(m.cfg.RootPath, assetInfo.Destination)
	fileName := filepath.Base(assetInfo.URL)

	if err := os.MkdirAll(finalDestPath, 0755); err != nil {
		return fmt.Errorf("не удалось создать конечную директорию %s: %w", finalDestPath, err)
	}

	switch assetInfo.Type {
	case "zip":
		if err := unzip(cachePath, finalDestPath); err != nil {
			return fmt.Errorf("ошибка распаковки '%s': %w", fileName, err)
		}
	case "file":
		break
	default:
		return fmt.Errorf("неизвестный тип ресурса: %s", assetInfo.Type)
	}

	m.consolePrintf("Ресурс '%s' успешно обработан из кэша в '%s'.\n", assetName, finalDestPath)
	return nil
}

// Метод Get теперь можно упростить, используя новые функции
func (m *Manager) Get(assetName string) (string, error) {
	cachePath, err := m.DownloadToCache(assetName)
	if err != nil {
		return "", err
	}

	if err := m.ProcessFromCache(assetName, cachePath); err != nil {
		return "", err
	}

	assetInfo := m.cfg.AssetCatalog[assetName]
	return filepath.Join(m.cfg.RootPath, assetInfo.Destination), nil
}

// DownloadFTPWithProgress скачивает файл по FTP с проверкой размера и прогресс-баром.
// ftpCfg - конфигурация конкретного FTP-сервера для подключения.
// ftpPath - это путь на сервере, например /distr/iiko/Setup.Front.exe
func (m *Manager) DownloadFTPWithProgress(ftpCfg config.FTPConfig, ftpPath, localPath string) (bool, error) {
	fileName := sanitizeProgressLabel(filepath.Base(ftpPath))
	m.setCancelable(true)
	defer m.setCancelable(false)
	m.reportStatus("Скачивание " + fileName)
	m.reportProgress(fileName, 0)

	if err := os.MkdirAll(filepath.Dir(localPath), 0755); err != nil {
		return false, fmt.Errorf("не удалось создать директорию %s: %w", filepath.Dir(localPath), err)
	}

	var lastErr error
	for attempt := 1; attempt <= downloadMaxAttempts; attempt++ {
		alreadyDownloaded, err := m.downloadFTPAttempt(ftpCfg, ftpPath, localPath, fileName)
		if err == nil {
			return alreadyDownloaded, nil
		}
		if errors.Is(err, context.Canceled) {
			return false, err
		}

		lastErr = err
		if attempt == downloadMaxAttempts {
			break
		}

		m.consolePrintf("Попытка %d/%d загрузки '%s' завершилась ошибкой: %v. Повтор через %s.\n", attempt, downloadMaxAttempts, fileName, err, downloadRetryDelay)
		if err := m.waitDownloadRetry(); err != nil {
			return false, err
		}
	}

	return false, fmt.Errorf("не удалось скачать '%s' по FTP после %d попыток: %w", fileName, downloadMaxAttempts, lastErr)
}

// DownloadHTTPWithProgress скачивает файл по HTTP с проверкой размера и прогресс-баром.
func (m *Manager) DownloadHTTPWithProgress(httpURL, localPath string) (bool, error) {
	fileName := sanitizeProgressLabel(filepath.Base(httpURL))
	m.setCancelable(true)
	defer m.setCancelable(false)
	m.reportStatus("Скачивание " + fileName)
	m.reportProgress(fileName, 0)

	if err := os.MkdirAll(filepath.Dir(localPath), 0755); err != nil {
		return false, fmt.Errorf("не удалось создать директорию %s: %w", filepath.Dir(localPath), err)
	}

	var lastErr error
	for attempt := 1; attempt <= downloadMaxAttempts; attempt++ {
		alreadyDownloaded, err := m.downloadHTTPAttempt(httpURL, localPath, fileName)
		if err == nil {
			return alreadyDownloaded, nil
		}
		if errors.Is(err, context.Canceled) {
			return false, err
		}

		lastErr = err
		if attempt == downloadMaxAttempts {
			break
		}

		m.consolePrintf("Попытка %d/%d загрузки '%s' завершилась ошибкой: %v. Повтор через %s.\n", attempt, downloadMaxAttempts, fileName, err, downloadRetryDelay)
		if err := m.waitDownloadRetry(); err != nil {
			return false, err
		}
	}

	return false, fmt.Errorf("не удалось скачать '%s' по HTTP после %d попыток: %w", fileName, downloadMaxAttempts, lastErr)
}

// UnpackToFlatDir распаковывает архив в указанную директорию,
// обрабатывая случай, когда все файлы в архиве находятся в одной корневой папке.
func (m *Manager) UnpackToFlatDir(assetName, cachePath, destDir string) error {
	// Создаем временную директорию для анализа внутри root-каталога проекта
	tempExtractDir := filepath.Join(m.cfg.RootPath, "temp", "unpack-check-"+filepath.Base(assetName))
	if err := os.MkdirAll(tempExtractDir, 0755); err != nil {
		return fmt.Errorf("не удалось создать временную директорию: %w", err)
	}
	defer os.RemoveAll(tempExtractDir)

	// Распаковываем во временную папку
	if err := unzip(cachePath, tempExtractDir); err != nil {
		return fmt.Errorf("ошибка при первичной распаковке '%s': %w", assetName, err)
	}

	// Анализируем содержимое временной папки
	entries, err := os.ReadDir(tempExtractDir)
	if err != nil {
		return fmt.Errorf("не удалось прочитать временную директорию: %w", err)
	}

	finalSourceDir := tempExtractDir
	// Если внутри только одна папка, то именно она является источником файлов
	if len(entries) == 1 && entries[0].IsDir() {
		finalSourceDir = filepath.Join(tempExtractDir, entries[0].Name())
	}

	// Копируем содержимое из определенной исходной папки в конечную
	return copyDirContents(finalSourceDir, destDir)
}

// --- Вспомогательные функции ---

// createProgressBar создает и настраивает общий прогресс-бар для скачиваний.
func (m *Manager) createProgressBar(totalSize int64, description string) *progressbar.ProgressBar {
	description = sanitizeProgressLabel(description)
	return progressbar.NewOptions64(
		totalSize,
		progressbar.OptionSetDescription(fmt.Sprintf("Скачивание %s", description)),
		progressbar.OptionSetWriter(m.progressWriter()),
		progressbar.OptionShowBytes(true),
		progressbar.OptionSetWidth(40),
		progressbar.OptionThrottle(100*time.Millisecond),
		progressbar.OptionShowCount(),
		progressbar.OptionOnCompletion(func() { m.consoleFprintProgress("\n") }),
		progressbar.OptionSpinnerType(14),
		progressbar.OptionFullWidth(),
		progressbar.OptionClearOnFinish(),
	)
}

func sanitizeProgressLabel(label string) string {
	label = html.UnescapeString(label)
	label = htmlTagRegex.ReplaceAllString(label, "")
	label = strings.Join(strings.Fields(label), " ")
	label = strings.TrimSpace(label)
	if label == "" {
		return "file"
	}

	runes := []rune(label)
	if len(runes) > 50 {
		return string(runes[:47]) + "..."
	}
	return label
}

type progressTracker struct {
	total       int64
	written     int64
	description string
	lastPercent int
	report      func(string, int)
}

func newProgressTracker(total int64, description string, initialWritten int64) *progressTracker {
	lastPercent := -1
	if total > 0 && initialWritten > 0 {
		lastPercent = clampPercent(int((initialWritten * 100) / total))
	}
	return &progressTracker{
		total:       total,
		written:     initialWritten,
		description: description,
		lastPercent: lastPercent,
	}
}

func (w *progressTracker) Write(p []byte) (int, error) {
	if w.total <= 0 {
		return len(p), nil
	}

	w.written += int64(len(p))
	percent := int((w.written * 100) / w.total)
	if percent > 100 {
		percent = 100
	}
	if percent != w.lastPercent {
		w.lastPercent = percent
		if w.report != nil {
			w.report(w.description, percent)
		}
	}
	return len(p), nil
}

func clampPercent(percent int) int {
	if percent < 0 {
		return 0
	}
	if percent > 100 {
		return 100
	}
	return percent
}

func (m *Manager) ListFTP(ftpCfg config.FTPConfig, path string) ([]core.FTPEntry, error) {
	hostWithPort := ftpCfg.Host
	if ftpCfg.Port != 0 && !strings.Contains(hostWithPort, ":") {
		hostWithPort = fmt.Sprintf("%s:%d", ftpCfg.Host, ftpCfg.Port)
	}

	c, err := ftp.Dial(hostWithPort, ftp.DialWithTimeout(10*time.Second))
	if err != nil {
		return nil, err
	}
	defer c.Quit()

	if err := c.Login(ftpCfg.User, ftpCfg.Pass); err != nil {
		return nil, err
	}

	entries, err := c.List(path)
	if err != nil {
		return nil, err
	}

	result := make([]core.FTPEntry, len(entries))
	for i, e := range entries {
		result[i] = core.FTPEntry{Name: e.Name, Type: uint(e.Type)}
	}
	return result, nil
}

// GetFastestFTP определяет самый быстрый FTP-сервер из списка, скачивая тестовый файл.
func (m *Manager) GetFastestFTP(ftpConfigs []config.FTPConfig, testFilePath string) (config.FTPConfig, error) {
	if len(ftpConfigs) == 0 {
		return config.FTPConfig{}, errors.New("список FTP-конфигураций для проверки пуст")
	}
	if len(ftpConfigs) == 1 {
		return ftpConfigs[0], nil // Если сервер один, он и есть самый быстрый
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second) // Общий таймаут на всю операцию
	defer cancel()

	resultChan := make(chan config.FTPConfig, 1) // Буферизованный канал, чтобы горутина не блокировалась
	g, gCtx := errgroup.WithContext(ctx)

	tui.Info("Запуск проверки скорости FTP-серверов...")
	for _, ftpCfg := range ftpConfigs {
		currentCfg := ftpCfg // Захватываем переменную для горутины
		g.Go(func() error {
			hostWithPort := currentCfg.Host
			if currentCfg.Port != 0 && !strings.Contains(hostWithPort, ":") {
				hostWithPort = fmt.Sprintf("%s:%d", currentCfg.Host, currentCfg.Port)
			}

			start := time.Now()
			c, err := ftp.Dial(hostWithPort, ftp.DialWithTimeout(5*time.Second))
			if err != nil {
				return nil // Просто игнорируем недоступные серверы
			}
			defer c.Quit()

			if err := c.Login(currentCfg.User, currentCfg.Pass); err != nil {
				return nil
			}

			resp, err := c.Retr(testFilePath)
			if err != nil {
				return nil
			}
			defer resp.Close()

			_, err = io.Copy(io.Discard, resp)
			if err != nil {
				return nil
			}

			elapsed := time.Since(start)
			tui.InfoF(" - Сервер %s: успешно (%.2f сек)", currentCfg.Host, elapsed.Seconds())

			select {
			case resultChan <- currentCfg:
			case <-gCtx.Done():
			}
			return nil
		})
	}

	select {
	case fastest := <-resultChan:
		cancel()
		_ = g.Wait()
		tui.SuccessF("Самый быстрый сервер: %s", fastest.Host)
		return fastest, nil
	case <-ctx.Done():
		_ = g.Wait()
		select {
		case fastest := <-resultChan:
			tui.SuccessF("Самый быстрый сервер (определен по таймауту): %s", fastest.Host)
			return fastest, nil
		default:
			return config.FTPConfig{}, errors.New("не удалось определить самый быстрый FTP-сервер (все недоступны или слишком медленные)")
		}
	}
}

// unzip распаковывает zip-архив.
func unzip(src, dest string) error {
	r, err := zip.OpenReader(src)
	if err != nil {
		return err
	}
	defer r.Close()

	for _, f := range r.File {
		fpath := filepath.Join(dest, f.Name)
		if !strings.HasPrefix(fpath, filepath.Clean(dest)+string(os.PathSeparator)) {
			return fmt.Errorf("небезопасный путь в архиве: %s", fpath)
		}

		if f.FileInfo().IsDir() {
			os.MkdirAll(fpath, os.ModePerm)
			continue
		}

		if err := os.MkdirAll(filepath.Dir(fpath), os.ModePerm); err != nil {
			return err
		}

		outFile, err := os.OpenFile(fpath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, f.Mode())
		if err != nil {
			return err
		}

		rc, err := f.Open()
		if err != nil {
			outFile.Close()
			return err
		}

		_, err = io.Copy(outFile, rc)
		outFile.Close()
		rc.Close()

		if err != nil {
			return err
		}
	}
	return nil
}

// copyDirContents рекурсивно копирует содержимое одной директории в другую.
func copyDirContents(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Формируем относительный путь, чтобы сохранить структуру
		relPath, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		dstPath := filepath.Join(dst, relPath)

		if info.IsDir() {
			return os.MkdirAll(dstPath, info.Mode())
		}

		// Копирование файла
		srcFile, err := os.Open(path)
		if err != nil {
			return err
		}
		defer srcFile.Close()

		dstFile, err := os.Create(dstPath)
		if err != nil {
			return err
		}
		defer dstFile.Close()

		_, err = io.Copy(dstFile, srcFile)
		return err
	})
}

// ExtractFile извлекает файл из zip-архива.
func (m *Manager) ExtractFile(zipPath, pathInZip, destPath string) error {
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return err
	}
	defer r.Close()

	pathInZip = filepath.ToSlash(pathInZip)

	for _, f := range r.File {
		// Сравниваем нормализованные пути
		if filepath.ToSlash(f.Name) == pathInZip {
			rc, err := f.Open()
			if err != nil {
				return err
			}
			defer rc.Close()

			if err := os.MkdirAll(filepath.Dir(destPath), 0755); err != nil {
				return err
			}

			outFile, err := os.Create(destPath)
			if err != nil {
				return err
			}
			defer outFile.Close()

			_, err = io.Copy(outFile, rc)
			return err
		}
	}
	return fmt.Errorf("файл '%s' не найден в архиве '%s'", pathInZip, zipPath)
}

// PurgeAsset удаляет файл ассета из кэша и его конечную директорию.
func (m *Manager) PurgeAsset(assetName string) error {
	assetInfo, ok := m.cfg.AssetCatalog[assetName]
	if !ok {
		// Не ошибка, если ассета нет в каталоге, просто ничего не делаем.
		return nil
	}

	// 1. Удаляем файл из кэша
	fileName := filepath.Base(assetInfo.URL)
	localCachePath := filepath.Join(m.cfg.AssetsCachePath, fileName)
	if _, err := os.Stat(localCachePath); err == nil {
		m.consolePrintf("Удаление файла из кэша: %s\n", localCachePath)
		if err := os.Remove(localCachePath); err != nil {
			// Не критичная ошибка, просто предупреждаем
			m.consolePrintf("Предупреждение: не удалось удалить файл из кэша %s: %v\n", localCachePath, err)
		}
	}

	// 2. Удаляем конечную директорию, если она указана
	if assetInfo.Destination != "" {
		finalDestPath := filepath.Join(m.cfg.RootPath, assetInfo.Destination)
		if _, err := os.Stat(finalDestPath); err == nil {
			m.consolePrintf("Удаление директории назначения: %s\n", finalDestPath)
			if err := os.RemoveAll(finalDestPath); err != nil {
				// Тоже не критично, но нужно предупредить
				m.consolePrintf("Предупреждение: не удалось удалить директорию назначения %s: %v\n", finalDestPath, err)
			}
		}
	}

	return nil
}

func (m *Manager) consolePrintf(format string, args ...interface{}) {
	writer := m.runtime.stdout
	if writer == nil {
		writer = io.Discard
	}
	fmt.Fprintf(writer, format, args...)
}

func (m *Manager) consoleFprintProgress(text string) {
	writer := m.runtime.progress
	if writer == nil {
		writer = io.Discard
	}
	fmt.Fprint(writer, text)
}

func (m *Manager) progressWriter() io.Writer {
	if m.runtime.progress == nil {
		return io.Discard
	}
	return m.runtime.progress
}

func (m *Manager) reportStatus(text string) {
	if m.runtime.statusFn != nil {
		m.runtime.statusFn(text)
	}
}

func (m *Manager) reportProgress(description string, percent int) {
	if m.runtime.percentFn != nil {
		m.runtime.percentFn(description, percent)
	}
}

func (m *Manager) setCancelable(enabled bool) {
	if m.runtime.cancelFn != nil {
		m.runtime.cancelFn(enabled)
	}
}

func (m *Manager) downloadContext() context.Context {
	if m.runtime.runtimeCtx == nil {
		return context.Background()
	}
	return m.runtime.runtimeCtx
}

func (m *Manager) wrapDownloadReader(reader io.Reader) io.Reader {
	return &downloadReader{
		ctx:    m.downloadContext(),
		reader: reader,
	}
}

func (m *Manager) downloadHTTPAttempt(httpURL, localPath, fileName string) (bool, error) {
	localSize, err := fileSize(localPath)
	if err != nil {
		return false, err
	}

	req, err := http.NewRequestWithContext(m.downloadContext(), "GET", httpURL, nil)
	if err != nil {
		return false, err
	}
	if localSize > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", localSize))
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()

	mode, resumeOffset, totalSize, err := resolveHTTPDownloadPlan(resp, localSize)
	if err != nil {
		if errors.Is(err, errHTTPRangeReset) {
			if removeErr := os.Remove(localPath); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
				return false, removeErr
			}
		}
		return false, err
	}

	if totalSize > 0 && resumeOffset == totalSize {
		m.consolePrintf("Файл '%s' уже существует и размер совпадает. Пропускаем.\n", fileName)
		m.reportProgress(fileName, 100)
		return true, nil
	}

	if mode == downloadModeRestart && localSize > 0 {
		m.consolePrintf("Файл '%s' существует, но сервер не подтвердил докачку. Перезапуск загрузки с начала.\n", fileName)
	}
	if mode == downloadModeResume && resumeOffset > 0 {
		m.consolePrintf("Возобновление загрузки '%s' с байта %d.\n", fileName, resumeOffset)
	}

	destFile, err := openDownloadFile(localPath, mode)
	if err != nil {
		return false, err
	}
	defer destFile.Close()

	initialPercent := initialDownloadPercent(totalSize, resumeOffset)
	m.reportProgress(fileName, initialPercent)
	bar, tracker := m.newDownloadProgress(totalSize, fileName, resumeOffset)
	reader := m.wrapDownloadReader(resp.Body)
	if _, err := io.Copy(io.MultiWriter(destFile, bar, tracker), reader); err != nil {
		return false, err
	}

	if totalSize > 0 {
		sizeAfterCopy, err := fileSize(localPath)
		if err != nil {
			return false, err
		}
		if sizeAfterCopy != totalSize {
			return false, fmt.Errorf("скачанный размер '%s' не совпадает с ожидаемым: %d != %d", fileName, sizeAfterCopy, totalSize)
		}
	}

	m.reportProgress(fileName, 100)
	return false, nil
}

func (m *Manager) downloadFTPAttempt(ftpCfg config.FTPConfig, ftpPath, localPath, fileName string) (bool, error) {
	hostWithPort := ftpCfg.Host
	if ftpCfg.Port != 0 && !strings.Contains(hostWithPort, ":") {
		hostWithPort = fmt.Sprintf("%s:%d", ftpCfg.Host, ftpCfg.Port)
	}

	c, err := ftp.Dial(hostWithPort, ftp.DialWithTimeout(10*time.Second))
	if err != nil {
		return false, fmt.Errorf("не удалось подключиться к FTP %s: %w", hostWithPort, err)
	}
	defer c.Quit()

	if err := c.Login(ftpCfg.User, ftpCfg.Pass); err != nil {
		return false, fmt.Errorf("ошибка входа на FTP %s: %w", hostWithPort, err)
	}

	remoteSize, err := c.FileSize(ftpPath)
	if err != nil {
		m.consolePrintf("Предупреждение: не удалось получить размер файла '%s' на FTP: %v. Загрузка будет выполнена без проверки.\n", fileName, err)
		remoteSize = -1
	}

	localSize, err := fileSize(localPath)
	if err != nil {
		return false, err
	}
	if remoteSize > 0 {
		switch {
		case localSize == remoteSize:
			m.consolePrintf("Файл '%s' уже существует и размер совпадает. Пропускаем.\n", fileName)
			m.reportProgress(fileName, 100)
			return true, nil
		case localSize > remoteSize:
			m.consolePrintf("Локальный файл '%s' больше удалённого. Перезапуск загрузки с начала.\n", fileName)
			if err := os.Remove(localPath); err != nil && !errors.Is(err, os.ErrNotExist) {
				return false, err
			}
			localSize = 0
		}
	}

	resp, mode, resumeOffset, err := startFTPDownload(c, ftpPath, localSize)
	if err != nil {
		return false, fmt.Errorf("не удалось начать скачивание с FTP: %w", err)
	}
	defer resp.Close()
	m.cancelFTPDownload(c, resp)

	if mode == downloadModeRestart && localSize > 0 {
		m.consolePrintf("FTP-сервер не поддержал докачку '%s'. Перезапуск загрузки с начала.\n", fileName)
	}
	if mode == downloadModeResume && resumeOffset > 0 {
		m.consolePrintf("Возобновление FTP-загрузки '%s' с байта %d.\n", fileName, resumeOffset)
	}

	destFile, err := openDownloadFile(localPath, mode)
	if err != nil {
		return false, fmt.Errorf("не удалось создать локальный файл: %w", err)
	}
	defer destFile.Close()

	initialPercent := initialDownloadPercent(remoteSize, resumeOffset)
	m.reportProgress(fileName, initialPercent)
	bar, tracker := m.newDownloadProgress(remoteSize, fileName, resumeOffset)
	reader := m.wrapDownloadReader(resp)
	if _, err := io.Copy(io.MultiWriter(destFile, bar, tracker), reader); err != nil {
		return false, fmt.Errorf("ошибка во время копирования потока: %w", err)
	}

	if remoteSize > 0 {
		sizeAfterCopy, err := fileSize(localPath)
		if err != nil {
			return false, err
		}
		if sizeAfterCopy != remoteSize {
			return false, fmt.Errorf("скачанный размер '%s' не совпадает с ожидаемым: %d != %d", fileName, sizeAfterCopy, remoteSize)
		}
	}

	m.reportProgress(fileName, 100)
	return false, nil
}

type downloadMode int

const (
	downloadModeRestart downloadMode = iota
	downloadModeResume
)

var errHTTPRangeReset = errors.New("reset partial download")

func resolveHTTPDownloadPlan(resp *http.Response, localSize int64) (downloadMode, int64, int64, error) {
	switch resp.StatusCode {
	case http.StatusOK:
		totalSize := resp.ContentLength
		if totalSize > 0 && localSize == totalSize {
			return downloadModeRestart, totalSize, totalSize, nil
		}
		return downloadModeRestart, 0, totalSize, nil
	case http.StatusPartialContent:
		start, totalSize, ok := parsePartialContentRange(resp.Header.Get("Content-Range"))
		if ok {
			if start != localSize {
				return downloadModeRestart, 0, 0, fmt.Errorf("сервер вернул неожиданный диапазон %q для локального смещения %d: %w", resp.Header.Get("Content-Range"), localSize, errHTTPRangeReset)
			}
			return downloadModeResume, localSize, totalSize, nil
		}
		totalSize = resp.ContentLength
		if totalSize > 0 {
			totalSize += localSize
		}
		return downloadModeResume, localSize, totalSize, nil
	case http.StatusRequestedRangeNotSatisfiable:
		totalSize, ok := parseUnsatisfiedContentRange(resp.Header.Get("Content-Range"))
		if ok && totalSize > 0 && localSize == totalSize {
			return downloadModeResume, totalSize, totalSize, nil
		}
		return downloadModeRestart, 0, 0, fmt.Errorf("сервер отклонил Range-запрос (%s): %w", resp.Status, errHTTPRangeReset)
	default:
		return downloadModeRestart, 0, 0, fmt.Errorf("bad status: %s", resp.Status)
	}
}

func parsePartialContentRange(header string) (int64, int64, bool) {
	matches := contentRangeRegex.FindStringSubmatch(strings.TrimSpace(header))
	if len(matches) != 4 {
		return 0, 0, false
	}

	start, err := parseInt64(matches[1])
	if err != nil {
		return 0, 0, false
	}
	totalSize := int64(-1)
	if matches[3] != "*" {
		totalSize, err = parseInt64(matches[3])
		if err != nil {
			return 0, 0, false
		}
	}
	return start, totalSize, true
}

func parseUnsatisfiedContentRange(header string) (int64, bool) {
	matches := contentRangeUnsatisfiedRegex.FindStringSubmatch(strings.TrimSpace(header))
	if len(matches) != 2 || matches[1] == "*" {
		return 0, false
	}
	totalSize, err := parseInt64(matches[1])
	if err != nil {
		return 0, false
	}
	return totalSize, true
}

func parseInt64(value string) (int64, error) {
	var result int64
	for _, r := range value {
		if r < '0' || r > '9' {
			return 0, fmt.Errorf("invalid integer %q", value)
		}
		result = result*10 + int64(r-'0')
	}
	return result, nil
}

func fileSize(path string) (int64, error) {
	fi, err := os.Stat(path)
	if err == nil {
		return fi.Size(), nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	return 0, err
}

func openDownloadFile(localPath string, mode downloadMode) (*os.File, error) {
	flags := os.O_CREATE | os.O_WRONLY
	if mode == downloadModeResume {
		flags |= os.O_APPEND
	} else {
		flags |= os.O_TRUNC
	}
	return os.OpenFile(localPath, flags, 0644)
}

func startFTPDownload(c *ftp.ServerConn, ftpPath string, localSize int64) (io.ReadCloser, downloadMode, int64, error) {
	if localSize > 0 {
		resp, err := c.RetrFrom(ftpPath, uint64(localSize))
		if err == nil {
			return resp, downloadModeResume, localSize, nil
		}
		resp, retryErr := c.Retr(ftpPath)
		if retryErr != nil {
			return nil, downloadModeRestart, 0, err
		}
		return resp, downloadModeRestart, 0, nil
	}

	resp, err := c.Retr(ftpPath)
	if err != nil {
		return nil, downloadModeRestart, 0, err
	}
	return resp, downloadModeRestart, 0, nil
}

func initialDownloadPercent(totalSize, offset int64) int {
	if totalSize <= 0 || offset <= 0 {
		return 0
	}
	return clampPercent(int((offset * 100) / totalSize))
}

func (m *Manager) newDownloadProgress(totalSize int64, fileName string, initialWritten int64) (*progressbar.ProgressBar, *progressTracker) {
	bar := m.createProgressBar(totalSize, fileName)
	if initialWritten > 0 {
		_ = bar.Add64(initialWritten)
	}
	tracker := newProgressTracker(totalSize, fileName, initialWritten)
	tracker.report = m.reportProgress
	return bar, tracker
}

func (m *Manager) waitDownloadRetry() error {
	timer := time.NewTimer(downloadRetryDelay)
	defer timer.Stop()

	select {
	case <-timer.C:
		return nil
	case <-m.downloadContext().Done():
		return m.downloadContext().Err()
	}
}

func (m *Manager) cancelFTPDownload(conn *ftp.ServerConn, reader io.Closer) {
	if m.runtime.runtimeCtx == nil {
		return
	}
	ctx := m.runtime.runtimeCtx
	go func() {
		<-ctx.Done()
		_ = reader.Close()
		_ = conn.Quit()
	}()
}

type downloadReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r *downloadReader) Read(p []byte) (int, error) {
	if r.ctx != nil {
		select {
		case <-r.ctx.Done():
			return 0, r.ctx.Err()
		default:
		}
	}
	return r.reader.Read(p)
}
