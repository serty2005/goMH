package assetmgr

import (
	"archive/zip"
	"fmt"
	"goMH/config"
	"goMH/core"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jlaffaye/ftp"
	"github.com/schollz/progressbar/v3"
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
		_, err = m.DownloadFTPWithProgress(parsedURL.Path, localCachePath)
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
		if err := copyFile(cachePath, filepath.Join(finalDestPath, fileName)); err != nil {
			return fmt.Errorf("ошибка копирования '%s': %w", fileName, err)
		}
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
// ftpPath - это путь на сервере, например /distr/iiko/Setup.Front.exe
func (m *Manager) DownloadFTPWithProgress(ftpPath, localPath string) (bool, error) {
	fileName := filepath.Base(ftpPath)

	// Убедимся, что директория для сохранения файла существует
	if err := os.MkdirAll(filepath.Dir(localPath), 0755); err != nil {
		return false, fmt.Errorf("не удалось создать директорию %s: %w", filepath.Dir(localPath), err)
	}

	c, err := ftp.Dial(m.cfg.FTP.Host, ftp.DialWithTimeout(10*time.Second))
	if err != nil {
		return false, fmt.Errorf("не удалось подключиться к FTP: %w", err)
	}
	defer c.Quit()

	if err := c.Login(m.cfg.FTP.User, m.cfg.FTP.Pass); err != nil {
		return false, fmt.Errorf("ошибка входа на FTP: %w", err)
	}

	remoteSize, err := c.FileSize(ftpPath)
	if err != nil {
		fmt.Printf("Предупреждение: не удалось получить размер файла '%s' на FTP: %v. Загрузка будет выполнена без проверки.\n", fileName, err)
		remoteSize = -1
	}

	if fi, err := os.Stat(localPath); err == nil {
		if remoteSize > 0 && fi.Size() == remoteSize {
			fmt.Printf("Файл '%s' уже существует и размер совпадает. Пропускаем.\n", fileName)
			return true, nil
		}
		fmt.Printf("Файл '%s' существует, но размер отличается. Перезагрузка...\n", fileName)
	}

	resp, err := c.Retr(ftpPath)
	if err != nil {
		return false, fmt.Errorf("не удалось начать скачивание с FTP: %w", err)
	}
	defer resp.Close()

	destFile, err := os.Create(localPath)
	if err != nil {
		return false, fmt.Errorf("не удалось создать локальный файл: %w", err)
	}
	defer destFile.Close()

	bar := CreateProgressBar(remoteSize, fileName)
	if _, err := io.Copy(io.MultiWriter(destFile, bar), resp); err != nil {
		os.Remove(localPath)
		return false, fmt.Errorf("ошибка во время копирования потока: %w", err)
	}

	return false, nil
}

// DownloadHTTPWithProgress скачивает файл по HTTP с проверкой размера и прогресс-баром.
func (m *Manager) DownloadHTTPWithProgress(httpURL, localPath string) (bool, error) {
	fileName := filepath.Base(httpURL)

	// Убедимся, что директория для сохранения файла существует
	if err := os.MkdirAll(filepath.Dir(localPath), 0755); err != nil {
		return false, fmt.Errorf("не удалось создать директорию %s: %w", filepath.Dir(localPath), err)
	}

	req, err := http.NewRequest("GET", httpURL, nil)
	if err != nil {
		return false, err
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return false, fmt.Errorf("bad status: %s", resp.Status)
	}

	remoteSize := resp.ContentLength

	if fi, err := os.Stat(localPath); err == nil {
		if remoteSize > 0 && fi.Size() == remoteSize {
			fmt.Printf("Файл '%s' уже существует и размер совпадает. Пропускаем.\n", fileName)
			return true, nil
		}
		fmt.Printf("Файл '%s' существует, но размер отличается. Перезагрузка...\n", fileName)
	}

	destFile, err := os.Create(localPath)
	if err != nil {
		return false, err
	}
	defer destFile.Close()

	bar := CreateProgressBar(remoteSize, fileName)
	if _, err := io.Copy(io.MultiWriter(destFile, bar), resp.Body); err != nil {
		os.Remove(localPath)
		return false, err
	}

	return false, nil
}

func (m *Manager) ListFTP(path string) ([]core.FTPEntry, error) {
	c, err := ftp.Dial(m.cfg.FTP.Host, ftp.DialWithTimeout(10*time.Second))
	if err != nil {
		return nil, err
	}
	defer c.Quit()

	if err := c.Login(m.cfg.FTP.User, m.cfg.FTP.Pass); err != nil {
		return nil, err
	}

	entries, err := c.List(path)
	if err != nil {
		return nil, err
	}

	// Конвертируем []*ftp.Entry в []core.FTPEntry
	result := make([]core.FTPEntry, len(entries))
	for i, e := range entries {
		result[i] = core.FTPEntry{
			Name: e.Name,
			Type: uint(e.Type),
		}
	}
	return result, nil
}

// --- Вспомогательные функции ---

// createProgressBar создает и настраивает общий прогресс-бар для скачиваний.
func CreateProgressBar(totalSize int64, description string) *progressbar.ProgressBar {
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

// copyFile копирует файл из src в dst.
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, in)
	return err
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
