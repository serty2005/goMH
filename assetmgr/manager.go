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
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/jlaffaye/ftp"
	"github.com/schollz/progressbar/v3"
	"golang.org/x/sync/errgroup"
)

var htmlTagRegex = regexp.MustCompile(`<[^>]+>`)
var contentRangeRegex = regexp.MustCompile(`^bytes\s+(\d+)-(\d+)/(\d+|\*)$`)

const (
	downloadMaxAttempts = 5
	downloadRetryDelay  = time.Second
)

type Manager struct {
	cfg *config.Config
}

func New(cfg *config.Config) (*Manager, error) {
	if err := os.MkdirAll(cfg.RootPath, 0755); err != nil {
		return nil, fmt.Errorf("не удалось создать корневую директорию %s: %w", cfg.RootPath, err)
	}
	if err := os.MkdirAll(cfg.AssetsCachePath, 0755); err != nil {
		return nil, fmt.Errorf("не удалось создать директорию кэша %s: %w", cfg.AssetsCachePath, err)
	}
	return &Manager{cfg: cfg}, nil
}

// Cfg предоставляет доступ к конфигурации из других пакетов.
func (m *Manager) Cfg() *config.Config {
	return m.cfg
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

	fmt.Printf("Ресурс '%s' успешно обработан из кэша в '%s'.\n", assetName, finalDestPath)
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
	partPath := localPath + ".part"

	if err := os.MkdirAll(filepath.Dir(localPath), 0755); err != nil {
		return false, fmt.Errorf("не удалось создать директорию %s: %w", filepath.Dir(localPath), err)
	}

	hostWithPort := ftpCfg.Host
	if ftpCfg.Port != 0 && !strings.Contains(hostWithPort, ":") {
		hostWithPort = fmt.Sprintf("%s:%d", ftpCfg.Host, ftpCfg.Port)
	}

	remoteSize := int64(-1)
	if fi, err := os.Stat(localPath); err == nil {
		if err := os.Rename(localPath, partPath); err != nil {
			return false, fmt.Errorf("не удалось подготовить файл для докачки '%s': %w", localPath, err)
		}
		if fi.Size() == 0 {
			_ = os.Remove(partPath)
		}
	}

	var lastErr error
	for attempt := 1; attempt <= downloadMaxAttempts; attempt++ {
		c, err := ftp.Dial(hostWithPort, ftp.DialWithTimeout(10*time.Second))
		if err != nil {
			lastErr = fmt.Errorf("не удалось подключиться к FTP %s: %w", hostWithPort, err)
			if !isRetriableDownloadError(lastErr) || attempt == downloadMaxAttempts {
				break
			}
			time.Sleep(downloadRetryDelay * time.Duration(attempt))
			continue
		}

		if err := c.Login(ftpCfg.User, ftpCfg.Pass); err != nil {
			_ = c.Quit()
			lastErr = fmt.Errorf("ошибка входа на FTP %s: %w", hostWithPort, err)
			if !isRetriableDownloadError(lastErr) || attempt == downloadMaxAttempts {
				break
			}
			time.Sleep(downloadRetryDelay * time.Duration(attempt))
			continue
		}

		if remoteSize <= 0 {
			sz, err := c.FileSize(ftpPath)
			if err != nil {
				fmt.Printf("Предупреждение: не удалось получить размер файла '%s' на FTP: %v. Загрузка будет выполнена без проверки.\n", fileName, err)
			} else {
				remoteSize = sz
			}
		}

		currentSize := int64(0)
		if fi, err := os.Stat(partPath); err == nil {
			currentSize = fi.Size()
		}

		if remoteSize > 0 {
			if currentSize == remoteSize {
				_ = c.Quit()
				if err := finalizePartFile(partPath, localPath); err != nil {
					return false, err
				}
				fmt.Printf("Файл '%s' уже существует и размер совпадает. Пропускаем.\n", fileName)
				return true, nil
			}
			if currentSize > remoteSize {
				if err := truncateFile(partPath); err != nil {
					_ = c.Quit()
					return false, err
				}
				currentSize = 0
			}
		}

		var resp *ftp.Response
		if currentSize > 0 {
			resp, err = c.RetrFrom(ftpPath, uint64(currentSize))
			if err != nil {
				if truncErr := truncateFile(partPath); truncErr != nil {
					_ = c.Quit()
					return false, truncErr
				}
				currentSize = 0
				resp, err = c.Retr(ftpPath)
			}
		} else {
			resp, err = c.Retr(ftpPath)
		}
		if err != nil {
			_ = c.Quit()
			lastErr = fmt.Errorf("не удалось начать скачивание с FTP: %w", err)
			if !isRetriableDownloadError(lastErr) || attempt == downloadMaxAttempts {
				break
			}
			time.Sleep(downloadRetryDelay * time.Duration(attempt))
			continue
		}

		flags := os.O_CREATE | os.O_WRONLY
		if currentSize > 0 {
			flags |= os.O_APPEND
		} else {
			flags |= os.O_TRUNC
		}
		destFile, err := os.OpenFile(partPath, flags, 0644)
		if err != nil {
			resp.Close()
			_ = c.Quit()
			return false, fmt.Errorf("не удалось открыть локальный файл для записи: %w", err)
		}

		bar := CreateProgressBar(remoteSize, fileName)
		if currentSize > 0 {
			_ = bar.Add64(currentSize)
		}
		written, copyErr := io.Copy(io.MultiWriter(destFile, bar), resp)

		closeErr := destFile.Close()
		respCloseErr := resp.Close()
		quitErr := c.Quit()

		if copyErr != nil {
			lastErr = fmt.Errorf("ошибка во время копирования потока FTP: %w", copyErr)
		} else if closeErr != nil {
			lastErr = closeErr
		} else if respCloseErr != nil {
			lastErr = respCloseErr
		} else if quitErr != nil {
			lastErr = quitErr
		} else {
			totalWritten := currentSize + written
			if remoteSize > 0 && totalWritten < remoteSize {
				lastErr = io.ErrUnexpectedEOF
			} else {
				if err := finalizePartFile(partPath, localPath); err != nil {
					return false, err
				}
				return false, nil
			}
		}

		if !isRetriableDownloadError(lastErr) || attempt == downloadMaxAttempts {
			break
		}
		time.Sleep(downloadRetryDelay * time.Duration(attempt))
	}

	return false, fmt.Errorf("ошибка скачивания FTP '%s' после %d попыток: %w", fileName, downloadMaxAttempts, lastErr)
}

// DownloadHTTPWithProgress скачивает файл по HTTP с проверкой размера и прогресс-баром.
func (m *Manager) DownloadHTTPWithProgress(httpURL, localPath string) (bool, error) {
	fileName := sanitizeProgressLabel(filepath.Base(httpURL))
	partPath := localPath + ".part"

	if err := os.MkdirAll(filepath.Dir(localPath), 0755); err != nil {
		return false, fmt.Errorf("не удалось создать директорию %s: %w", filepath.Dir(localPath), err)
	}

	client := &http.Client{Timeout: 90 * time.Second}
	remoteSize, _ := probeHTTPRemoteSize(client, httpURL)
	if fi, err := os.Stat(localPath); err == nil {
		if remoteSize > 0 && fi.Size() == remoteSize {
			fmt.Printf("Файл '%s' уже существует и размер совпадает. Пропускаем.\n", fileName)
			return true, nil
		}
		if err := os.Rename(localPath, partPath); err != nil {
			return false, fmt.Errorf("не удалось подготовить файл для докачки '%s': %w", localPath, err)
		}
		if fi.Size() == 0 {
			_ = os.Remove(partPath)
		}
	}

	var lastErr error
	for attempt := 1; attempt <= downloadMaxAttempts; attempt++ {
		currentSize := int64(0)
		if fi, err := os.Stat(partPath); err == nil {
			currentSize = fi.Size()
		}

		if remoteSize > 0 {
			if currentSize == remoteSize {
				if err := finalizePartFile(partPath, localPath); err != nil {
					return false, err
				}
				fmt.Printf("Файл '%s' уже существует и размер совпадает. Пропускаем.\n", fileName)
				return true, nil
			}
			if currentSize > remoteSize {
				if err := truncateFile(partPath); err != nil {
					return false, err
				}
				currentSize = 0
			}
		}

		req, err := http.NewRequest(http.MethodGet, httpURL, nil)
		if err != nil {
			return false, err
		}
		if currentSize > 0 {
			req.Header.Set("Range", fmt.Sprintf("bytes=%d-", currentSize))
		}

		resp, err := client.Do(req)
		if err != nil {
			lastErr = err
			if !isRetriableDownloadError(lastErr) || attempt == downloadMaxAttempts {
				break
			}
			time.Sleep(downloadRetryDelay * time.Duration(attempt))
			continue
		}

		if currentSize > 0 && resp.StatusCode == http.StatusOK {
			resp.Body.Close()
			if err := truncateFile(partPath); err != nil {
				return false, err
			}
			currentSize = 0
			req, err = http.NewRequest(http.MethodGet, httpURL, nil)
			if err != nil {
				return false, err
			}
			resp, err = client.Do(req)
			if err != nil {
				lastErr = err
				if !isRetriableDownloadError(lastErr) || attempt == downloadMaxAttempts {
					break
				}
				time.Sleep(downloadRetryDelay * time.Duration(attempt))
				continue
			}
		}

		if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent {
			bodyPreview, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
			resp.Body.Close()
			lastErr = fmt.Errorf("bad status: %s (%s)", resp.Status, strings.TrimSpace(string(bodyPreview)))
			if !isRetriableDownloadError(lastErr) || attempt == downloadMaxAttempts {
				break
			}
			time.Sleep(downloadRetryDelay * time.Duration(attempt))
			continue
		}

		expectedTotal := remoteSize
		if resp.StatusCode == http.StatusPartialContent {
			totalFromRange, rangeErr := parseContentRangeTotal(resp.Header.Get("Content-Range"))
			if rangeErr == nil && totalFromRange > 0 {
				expectedTotal = totalFromRange
			}
		} else if resp.ContentLength > 0 {
			expectedTotal = resp.ContentLength
		}

		flags := os.O_CREATE | os.O_WRONLY
		if currentSize > 0 && resp.StatusCode == http.StatusPartialContent {
			flags |= os.O_APPEND
		} else {
			flags |= os.O_TRUNC
			currentSize = 0
		}
		destFile, err := os.OpenFile(partPath, flags, 0644)
		if err != nil {
			resp.Body.Close()
			return false, err
		}

		bar := CreateProgressBar(expectedTotal, fileName)
		if currentSize > 0 {
			_ = bar.Add64(currentSize)
		}

		written, copyErr := io.Copy(io.MultiWriter(destFile, bar), resp.Body)
		closeErr := destFile.Close()
		respCloseErr := resp.Body.Close()

		if copyErr != nil {
			lastErr = copyErr
		} else if closeErr != nil {
			lastErr = closeErr
		} else if respCloseErr != nil {
			lastErr = respCloseErr
		} else {
			totalWritten := currentSize + written
			if expectedTotal > 0 && totalWritten < expectedTotal {
				lastErr = io.ErrUnexpectedEOF
			} else if expectedTotal > 0 && totalWritten > expectedTotal {
				lastErr = fmt.Errorf("получено больше данных, чем ожидалось: %d > %d", totalWritten, expectedTotal)
			} else {
				if err := finalizePartFile(partPath, localPath); err != nil {
					return false, err
				}
				return false, nil
			}
		}

		if !isRetriableDownloadError(lastErr) || attempt == downloadMaxAttempts {
			break
		}
		time.Sleep(downloadRetryDelay * time.Duration(attempt))
	}

	return false, fmt.Errorf("ошибка скачивания HTTP '%s' после %d попыток: %w", fileName, downloadMaxAttempts, lastErr)
}

func probeHTTPRemoteSize(client *http.Client, rawURL string) (int64, error) {
	req, err := http.NewRequest(http.MethodHead, rawURL, nil)
	if err != nil {
		return -1, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return -1, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 400 {
		return -1, fmt.Errorf("head status: %s", resp.Status)
	}

	if resp.ContentLength > 0 {
		return resp.ContentLength, nil
	}
	return -1, nil
}

func parseContentRangeTotal(headerVal string) (int64, error) {
	matches := contentRangeRegex.FindStringSubmatch(strings.TrimSpace(headerVal))
	if len(matches) != 4 {
		return -1, fmt.Errorf("invalid Content-Range: %q", headerVal)
	}
	if matches[3] == "*" {
		return -1, nil
	}
	total, err := strconv.ParseInt(matches[3], 10, 64)
	if err != nil {
		return -1, err
	}
	return total, nil
}

func truncateFile(path string) error {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_TRUNC, 0644)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	return f.Close()
}

func finalizePartFile(partPath, finalPath string) error {
	if _, err := os.Stat(partPath); err != nil {
		return err
	}
	_ = os.Remove(finalPath)
	if err := os.Rename(partPath, finalPath); err != nil {
		return fmt.Errorf("не удалось переместить временный файл '%s' в '%s': %w", partPath, finalPath, err)
	}
	return nil
}

func isRetriableDownloadError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		return true
	}
	msg := strings.ToLower(err.Error())
	retriableSubstrings := []string{
		"connection reset",
		"broken pipe",
		"timeout",
		"temporary",
		"unexpected eof",
		"eof",
		"use of closed network connection",
		"bad gateway",
		"service unavailable",
		"too many requests",
	}
	for _, part := range retriableSubstrings {
		if strings.Contains(msg, part) {
			return true
		}
	}
	return false
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
func CreateProgressBar(totalSize int64, description string) *progressbar.ProgressBar {
	description = sanitizeProgressLabel(description)
	return progressbar.NewOptions64(
		totalSize,
		progressbar.OptionSetDescription(fmt.Sprintf("Скачивание %s", description)),
		progressbar.OptionSetWriter(os.Stderr),
		progressbar.OptionShowBytes(true),
		progressbar.OptionSetWidth(40),
		progressbar.OptionThrottle(100*time.Millisecond),
		progressbar.OptionShowCount(),
		progressbar.OptionOnCompletion(func() { fmt.Fprint(os.Stderr, "\n") }),
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
		fmt.Printf("Удаление файла из кэша: %s\n", localCachePath)
		if err := os.Remove(localCachePath); err != nil {
			// Не критичная ошибка, просто предупреждаем
			fmt.Printf("Предупреждение: не удалось удалить файл из кэша %s: %v\n", localCachePath, err)
		}
	}

	// 2. Удаляем конечную директорию, если она указана
	if assetInfo.Destination != "" {
		finalDestPath := filepath.Join(m.cfg.RootPath, assetInfo.Destination)
		if _, err := os.Stat(finalDestPath); err == nil {
			fmt.Printf("Удаление директории назначения: %s\n", finalDestPath)
			if err := os.RemoveAll(finalDestPath); err != nil {
				// Тоже не критично, но нужно предупредить
				fmt.Printf("Предупреждение: не удалось удалить директорию назначения %s: %v\n", finalDestPath, err)
			}
		}
	}

	return nil
}
