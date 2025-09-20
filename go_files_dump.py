main.go
===== START main.go =====
go `
package main

import (
	"flag"
	"fmt"
	"goMH/assetmgr"
	"goMH/config"
	"goMH/core"
	"goMH/modules/dto"
	"goMH/modules/frpc"
	"goMH/modules/iiko"
	"goMH/modules/regime"
	"goMH/modules/remoteaccess"
	"goMH/modules/serviceutils"
	"goMH/modules/utm"
	"goMH/modules/vcomcaster"
	"goMH/tui"
	"goMH/winutils"
	"io"
	"net/http"

	"log"
	"os"
)

type RealWinUtils struct{}

func (rw *RealWinUtils) RunCommand(name string, args ...string) (string, error) {
	return winutils.RunCommand(name, args...)
}
func (rw *RealWinUtils) ServiceExists(serviceName string) (bool, error) {
	return winutils.ServiceExists(serviceName)
}
func (rw *RealWinUtils) AddDefenderExclusion(path string) error {
	return winutils.AddDefenderExclusion(path)
}
func (rw *RealWinUtils) SetServiceTriggers(serviceName string, triggers []string) error {
	return winutils.SetServiceTriggers(serviceName, triggers)
}
func (rw *RealWinUtils) Is64BitOS() bool {
	return winutils.Is64BitOS()
}
func (rw *RealWinUtils) GetComPorts() ([]string, error) {
	return winutils.GetComPorts()
}
func (rw *RealWinUtils) GetScanners() ([]core.ScannerInfo, error) {
	internalScanners, err := winutils.GetScanners()
	if err != nil {
		return nil, err
	}
	publicScanners := make([]core.ScannerInfo, 0, len(internalScanners))
	for _, scanner := range internalScanners {
		publicScanners = append(publicScanners, core.ScannerInfo{
			Port:        scanner.Port,
			Caption:     scanner.Caption,
			PNPDeviceID: scanner.PNPDeviceID,
		})
	}
	return publicScanners, nil
}
func (rw *RealWinUtils) IsProcessRunning(processName string) (bool, error) {
	return winutils.IsProcessRunning(processName)
}
func (rw *RealWinUtils) CreateScheduledTask(taskName, executablePath, workingDir string) error {
	return winutils.CreateScheduledTask(taskName, executablePath, workingDir)
}
func (rw *RealWinUtils) RunCommandWithEnv(env map[string]string, name string, args ...string) (string, error) {
	return winutils.RunCommandWithEnv(env, name, args...)
}
func (rw *RealWinUtils) GetFileVersion(filePath string) (string, error) {
	return winutils.GetFileVersion(filePath)
}
func (rw *RealWinUtils) ListArchiveContents(archivePath string) ([]string, error) {
	return winutils.ListArchiveContents(archivePath)
}
func (rw *RealWinUtils) FindFileRecursive(root, pattern string) (string, error) {
	return winutils.FindFileRecursive(root, pattern)
}
func (rw *RealWinUtils) GetStartupFolders() (string, string, error) {
	return winutils.GetStartupFolders()
}
func (rw *RealWinUtils) DeleteFile(path string) error {
	return winutils.DeleteFile(path)
}
func (rw *RealWinUtils) CleanDirectory(path string) error {
	return winutils.CleanDirectory(path)
}
func (rw *RealWinUtils) FindScheduledTaskByPath(exePath string) (string, error) {
	return winutils.FindScheduledTaskByPath(exePath)
}
func (rw *RealWinUtils) DeleteScheduledTaskByName(taskName string) error {
	return winutils.DeleteScheduledTaskByName(taskName)
}
func (rw *RealWinUtils) GetServiceStatus(serviceName string) (string, error) {
	return winutils.GetServiceStatus(serviceName)
}

// getConfigPath определяет, какой путь к конфигурации использовать:
// из флага, локальный или удаленный.
func getConfigPath(configFlag *string) (string, error) {
	const defaultConfigName = "config.json"
	const remoteConfigURL = "http://f.serty.top/distr/installer/config.json" // Используем http, если https недоступен

	// Проверяем, был ли флаг изменен пользователем
	flagWasSet := false
	flag.Visit(func(f *flag.Flag) {
		if f.Name == "config" {
			flagWasSet = true
		}
	})

	// Если флаг был явно задан (даже если он равен "config.json"), используем его значение
	if flagWasSet {
		tui.InfoF("Используется конфигурация, указанная в аргументе: %s", *configFlag)
		return *configFlag, nil
	}

	// Флаг не был задан, проверяем наличие config.json рядом с exe
	if _, err := os.Stat(defaultConfigName); err == nil {
		tui.InfoF("Найден локальный файл конфигурации: %s", defaultConfigName)
		return defaultConfigName, nil
	}

	// Локального файла нет, скачиваем с удаленного ресурса
	tui.Warn(fmt.Sprintf("Локальный %s не найден. Попытка загрузить конфигурацию с %s", defaultConfigName, remoteConfigURL))

	resp, err := http.Get(remoteConfigURL)
	if err != nil {
		return "", fmt.Errorf("не удалось выполнить запрос на скачивание конфигурации: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("сервер вернул ошибку при скачивании конфигурации: %s", resp.Status)
	}

	// Создаем временный файл для хранения конфигурации
	tempFile, err := os.CreateTemp("", "config-*.json")
	if err != nil {
		return "", fmt.Errorf("не удалось создать временный файл для конфигурации: %w", err)
	}
	defer tempFile.Close()

	_, err = io.Copy(tempFile, resp.Body)
	if err != nil {
		return "", fmt.Errorf("не удалось записать конфигурацию во временный файл: %w", err)
	}

	tui.Success("Конфигурация успешно загружена с удаленного сервера.")
	return tempFile.Name(), nil
}

func main() {
	// 0. Обработка аргументов командной строки
	configPathFlag := flag.String("config", "config.json", "Путь к файлу конфигурации (локальный или URL)")
	flag.Parse()

	// 1. Проверка прав администратора
	if !winutils.IsAdmin() {
		tui.Error("Ошибка: Для выполнения требуются права администратора.")
		tui.Error("Пожалуйста, запустите эту программу от имени Администратора.")
		fmt.Println("\nНажмите Enter для выхода...")
		fmt.Scanln()
		os.Exit(1)
	}
	tui.Success("Приложение запущено с правами администратора.")

	// 2. Получение пути к конфигурации (новая логика)
	finalConfigPath, err := getConfigPath(configPathFlag)
	if err != nil {
		log.Fatalf("Критическая ошибка: не удалось определить источник конфигурации: %v", err)
	}

	// 3. Загрузка конфигурации
	cfg, err := config.LoadConfig(finalConfigPath)
	if err != nil {
		log.Fatalf("Критическая ошибка: не удалось загрузить конфигурацию: %v", err)
	}

	// 4. Инициализация менеджера ресурсов
	assetManager, err := assetmgr.New(cfg)
	if err != nil {
		log.Fatalf("Критическая ошибка: не удалось инициализировать менеджер ресурсов: %v", err)
	}

	// Создаём реальный объект утилит
	RealWinUtils := &RealWinUtils{}

	// 5. Регистрация всех доступных модулей
	// map хранит core.Installer
	registeredModules := map[string]core.Installer{
		"VComCaster":   &vcomcaster.Module{},
		"iiko":         &iiko.Module{},
		"FRPC":         &frpc.Module{},
		"Regime":       &regime.Module{},
		"RemoteAccess": &remoteaccess.Module{},
		"ServiceUtils": &serviceutils.Module{},
		"DTO":          &dto.Module{},
		"UTM":          &utm.Module{},
	}

	// 6. Основной цикл меню
	for {
		var availableModules []tui.Installer
		for _, modDef := range cfg.Modules {
			if module, ok := registeredModules[modDef.ID]; ok {
				availableModules = append(availableModules, module)
			}
		}

		if len(availableModules) == 0 {
			log.Fatal("В конфигурации не определено ни одного доступного модуля.")
		}

		selected, err := tui.ShowMenu(availableModules)
		if err != nil {
			tui.Info("Выход из программы.")
			os.Exit(0)
		}

		selectedModule := selected.(core.Installer)

		err = selectedModule.Run(assetManager, RealWinUtils)
		if err != nil {
			tui.Error(fmt.Sprintf("\n--- ОПЕРАЦИЯ ЗАВЕРШИЛАСЬ С ОШИБКОЙ ---\n%v\n---------------------------------------\n", err))
		} else {
			tui.Success("\n--- Операция завершена успешно. ---")
		}

		fmt.Println("\nНажмите Enter, чтобы вернуться в главное меню...")
		fmt.Scanln()
	}
}

go `
===== END main.go =====

assetmgr/manager.go
===== START manager.go =====
go `
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

// UnpackToFlatDir распаковывает архив в указанную директорию,
// обрабатывая случай, когда все файлы в архиве находятся в одной корневой папке.
func (m *Manager) UnpackToFlatDir(assetName, cachePath, destDir string) error {
	// Создаем временную директорию для анализа
	tempExtractDir, err := os.MkdirTemp("", "unpack-check-*")
	if err != nil {
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

go `
===== END manager.go =====

config/config.go
===== START config.go =====
go `
package config

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
)

type IikoConfig struct {
	BaseFTPPath      string          `json:"base_ftp_path"`
	PatchesBaseURL   string          `json:"patches_base_url"`
	PatchRouteFile   string          `json:"patch_route_file"`
	ComponentsToFind []IikoComponent `json:"components_to_find"`
	CardPOS          IikoComponent   `json:"card_pos"`
}

type IikoComponent struct {
	ID          string `json:"id"`
	MenuText    string `json:"menu_text"`
	FileName    string `json:"file_name"`
	InstallArgs string `json:"install_args"`
	RunAfter    string `json:"run_after"`
	// Поля ниже не из JSON, а будут заполняться в рантайме
	Version string `json:"-"`
	FTPPath string `json:"-"`
}

type FrpcConfig struct {
	InstallPath     string           `json:"install_path"`
	ServiceName     string           `json:"service_name"`
	PortRange       string           `json:"port_range"`
	FrpcDownloadURL string           `json:"frpc_download_url"`
	NssmDownloadURL string           `json:"nssm_download_url"`
	ServerConfig    FrpcServerConfig `json:"server_config"`
}

type FrpcServerConfig struct {
	Host       string `json:"host"`
	APIPort    int    `json:"api_port"`
	TunnelPort int    `json:"tunnel_port"`
	User       string `json:"user"`
	Pass       string `json:"pass"`
}

type TeamViewerConfig struct {
	ShortURL string `json:"ShortURL"`
	ApiURL   string `json:"ApiURL"`
}

type MaintenanceConfig struct {
	TempPaths         []string `json:"TempPaths"`
	LogCollectorPaths []string `json:"LogCollectorPaths"`
}

// DTOConfig содержит настройки для установщика драйверов АТОЛ
type DTOConfig struct {
	MenuText    string `json:"menu_text"`
	AssetID     string `json:"asset_id"`
	InstallArgs string `json:"install_args"`
}

// UTMConfig содержит настройки для установщика УТМ
type UTMConfig struct {
	MenuText    string `json:"menu_text"`
	AssetID     string `json:"asset_id"`
	InstallArgs string `json:"install_args"`
}

type Config struct {
	RootPath          string               `json:"root_path"`
	AssetsCachePath   string               `json:"assets_cache_path"`
	FTP               FTPConfig            `json:"ftp_config"`
	Modules           []ModuleDef          `json:"modules"`
	FrpcConfig        FrpcConfig           `json:"frpc_config"`
	IikoConfig        IikoConfig           `json:"iiko_config"`
	AssetCatalog      map[string]AssetInfo `json:"asset_catalog"`
	TeamViewerConfig  TeamViewerConfig     `json:"TeamViewerConfig"`
	MaintenanceConfig MaintenanceConfig    `json:"MaintenanceConfig"`
	DTOConfig         DTOConfig            `json:"dto_config"`
	UTMConfig         UTMConfig            `json:"utm_config"`
}

type FTPConfig struct {
	Host string `json:"host"`
	User string `json:"user"`
	Pass string `json:"pass"`
}

type ModuleDef struct {
	ID string `json:"id"`
}

type AssetInfo struct {
	URL            string `json:"url"`
	Type           string `json:"type"`
	Destination    string `json:"destination"`
	DownloadMethod string `json:"download_method"`
}

// LoadConfig загружает конфигурацию из файла или по URL
func LoadConfig(pathOrURL string) (*Config, error) {
	var data []byte
	var err error

	// Проверяем, является ли строка URL-адресом
	if strings.HasPrefix(pathOrURL, "http://") || strings.HasPrefix(pathOrURL, "https://") {
		fmt.Printf("Загрузка конфигурации с URL: %s\n", pathOrURL)
		resp, errHttp := http.Get(pathOrURL)
		if errHttp != nil {
			return nil, fmt.Errorf("ошибка при загрузке конфигурации по HTTP: %w", errHttp)
		}
		defer resp.Body.Close()
		data, err = io.ReadAll(resp.Body)
	} else {
		fmt.Printf("Чтение локального файла конфигурации: %s\n", pathOrURL)
		data, err = os.ReadFile(pathOrURL)
	}

	if err != nil {
		return nil, fmt.Errorf("не удалось получить данные конфигурации: %w", err)
	}

	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("ошибка парсинга JSON конфигурации: %w", err)
	}

	return &cfg, nil
}

go `
===== END config.go =====

core/types.go
===== START types.go =====
go `
package core

import "goMH/config"

// ScannerInfo содержит информацию о найденном устройстве-сканере.
type ScannerInfo struct {
	Port        string // Например, "COM3"
	Caption     string // Дружелюбное имя, например "USB-SERIAL CH340 (COM3)"
	PNPDeviceID string // Аппаратный ID, например, "USB\VID_2912&PID_0005&MI_00\..."
}

// WinUtils определяет контракт для утилит, специфичных для Windows.
// Модули будут зависеть от этого интерфейса, а не от конкретного пакета winutils.
type WinUtils interface {
	RunCommand(name string, args ...string) (string, error)
	RunCommandWithEnv(env map[string]string, name string, args ...string) (string, error)
	ServiceExists(serviceName string) (bool, error)
	AddDefenderExclusion(path string) error
	SetServiceTriggers(serviceName string, triggers []string) error
	Is64BitOS() bool
	GetComPorts() ([]string, error)
	GetScanners() ([]ScannerInfo, error)
	IsProcessRunning(processName string) (bool, error)
	GetFileVersion(filePath string) (string, error)
	ListArchiveContents(archivePath string) ([]string, error)
	CreateScheduledTask(taskName, executablePath, workingDir string) error
	FindFileRecursive(root, pattern string) (string, error)
	GetStartupFolders() (user, common string, err error)
	DeleteFile(path string) error
	CleanDirectory(path string) error
	FindScheduledTaskByPath(exePath string) (string, error)
	DeleteScheduledTaskByName(taskName string) error
	GetServiceStatus(serviceName string) (string, error)
}

// AssetManager определяет контракт для менеджера ресурсов.
type AssetManager interface {
	Get(assetName string) (string, error)
	DownloadHTTPWithProgress(httpURL, localPath string) (bool, error)
	DownloadFTPWithProgress(ftpPath, localPath string) (bool, error)
	ExtractFile(zipPath, pathInZip, destPath string) error
	ListFTP(path string) ([]FTPEntry, error)
	DownloadToCache(assetName string) (string, error)
	ProcessFromCache(assetName, cachePath string) error
	PurgeAsset(assetName string) error
	UnpackToFlatDir(assetName, cachePath, destDir string) error
	Cfg() *config.Config
}

// Installer — это единый интерфейс для всех устанавливаемых модулей.
// Мы переносим его сюда, чтобы он был доступен всем.
type Installer interface {
	ID() string
	MenuText() string
	// Сигнатура Run теперь принимает интерфейсы, а не конкретные типы.
	Run(am AssetManager, wu WinUtils) error
}

type FTPEntry struct {
	Name string
	Type uint
}

go `
===== END types.go =====

modules/dto/atol.go
===== START atol.go =====
go `
package dto

import (
	"errors"
	"fmt"
	"goMH/core"
	"goMH/tui"
	"strings"
)

type Module struct{}

func (m *Module) ID() string {
	return "DTO"
}

func (m *Module) MenuText() string {
	return "Установить ДТО"
}

func (m *Module) Run(am core.AssetManager, wu core.WinUtils) error {
	cfg := am.Cfg().DTOConfig

	tui.Title(fmt.Sprintf("\n--- Начало установки: %s ---", m.MenuText()))

	if cfg.AssetID == "" {
		return errors.New("в секции 'dto_config' не указан asset_id")
	}

	tui.Info("Получение установщика через AssetManager...")
	// Используем assetmgr для скачивания файла в кэш, он сам выберет метод (HTTP/FTP)
	installerPath, err := am.DownloadToCache(cfg.AssetID)
	if err != nil {
		return fmt.Errorf("не удалось получить ассет '%s': %w", cfg.AssetID, err)
	}

	tui.Info("Запуск установки в тихом режиме...")
	tui.InfoF("Аргументы: %s", cfg.InstallArgs)
	args := strings.Fields(cfg.InstallArgs)

	_, err = wu.RunCommand(installerPath, args...)
	if err != nil {
		return fmt.Errorf("ошибка при установке ДТО: %w", err)
	}

	return nil
}

go `
===== END atol.go =====

modules/frpc/frpc.go
===== START frpc.go =====
go `
package frpc

import (
	"archive/zip"
	"bufio"
	"encoding/json"
	"fmt"
	"goMH/config"
	"goMH/core"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type Module struct {
	Cfg *config.FrpcConfig
}

type FrpsProxy struct {
	Name   string    `json:"name"`
	Conf   *FrpsConf `json:"conf"`
	Status string    `json:"status"`
}
type FrpsConf struct {
	RemotePort int `json:"remote_port"`
}
type FrpsProxyInfo struct {
	Proxies []FrpsProxy `json:"proxies"`
}

func (m *Module) ID() string { return "FRPC" }
func (m *Module) MenuText() string {
	return "Fast Reverse Proxy Client (проброс портов)"
}

func (m *Module) Run(am core.AssetManager, wu core.WinUtils) error {
	m.Cfg = &am.Cfg().FrpcConfig
	frpcExePath := filepath.Join(m.Cfg.InstallPath, "frpc.exe")
	if _, err := os.Stat(frpcExePath); err == nil {
		return m.runDiagnosticsWorkflow(am, wu)
	}
	return m.runFullInstallWorkflow(am, wu, false)
}
func (m *Module) runDiagnosticsWorkflow(am core.AssetManager, wu core.WinUtils) error {
	fmt.Println("\nОбнаружена существующая установка FRPC.")
	fmt.Print("Введите 'R' для добавления порта, 'C' для полной переустановки или 'U' для удаления (R/C/U): ")
	reader := bufio.NewReader(os.Stdin)
	choice, _ := reader.ReadString('\n')
	choice = strings.TrimSpace(strings.ToUpper(choice))
	switch choice {
	case "R":
		return m.runAddPortWorkflow(wu, true)
	case "C":
		fmt.Println("Выполняем полную переустановку...")
		return m.runFullInstallWorkflow(am, wu, true)
	case "U":
		fmt.Print("ВНИМАНИЕ: Это полностью удалит FRPC. Вы уверены? (y/n): ")
		confirm, _ := reader.ReadString('\n')
		if strings.TrimSpace(strings.ToLower(confirm)) != "y" {
			fmt.Println("Удаление отменено.")
			return nil
		}
		return m.uninstall(wu)
	default:
		fmt.Println("Отмена операции.")
		return nil
	}
}
func (m *Module) runFullInstallWorkflow(am core.AssetManager, wu core.WinUtils, isReinstall bool) error {
	if isReinstall {
		m.uninstall(wu)
	}
	_ = os.MkdirAll(m.Cfg.InstallPath, 0755)
	wu.AddDefenderExclusion(am.Cfg().RootPath)
	if err := m.downloadAndExtractComponents(am, wu); err != nil {
		return err
	}
	return m.runAddPortWorkflow(wu, false)
}
func (m *Module) runAddPortWorkflow(wu core.WinUtils, isAddingToExisting bool) error {
	reader := bufio.NewReader(os.Stdin)
	fmt.Print("Введите локальный порт для туннеля (например, 5985 для WinRM): ")
	localPortStr, _ := reader.ReadString('\n')
	localPortStr = strings.TrimSpace(localPortStr)
	if localPortStr == "" {
		localPortStr = "5985"
	}
	fmt.Print("Введите имя этого узла (например, SRV-BACKOFFICE-01): ")
	alias, _ := reader.ReadString('\n')
	alias = strings.TrimSpace(alias)
	freePort, err := m.findFreePort()
	if err != nil {
		return err
	}
	fmt.Printf("Выбран удаленный порт: %d\n", freePort)
	if err := m.updateFrpcIni(alias, localPortStr, strconv.Itoa(freePort)); err != nil {
		return err
	}
	if !isAddingToExisting {
		if err := m.setupNssmService(); err != nil {
			return err
		}
	}
	fmt.Println("Перезапускаем службу для применения изменений...")
	wu.RunCommand("sc.exe", "stop", m.Cfg.ServiceName)
	time.Sleep(2 * time.Second)
	wu.RunCommand("sc.exe", "start", m.Cfg.ServiceName)
	fmt.Println("\n--- Настройка FRPC завершена ---")
	return nil
}
func (m *Module) uninstall(wu core.WinUtils) error {
	fmt.Println("Остановка и удаление службы FRPC...")
	wu.RunCommand("sc.exe", "stop", m.Cfg.ServiceName)
	time.Sleep(2 * time.Second)
	wu.RunCommand("sc.exe", "delete", m.Cfg.ServiceName)
	fmt.Println("Удаление директории установки...")
	os.RemoveAll(m.Cfg.InstallPath)
	fmt.Println("Очистка завершена.")
	return nil
}

func (m *Module) downloadAndExtractComponents(am core.AssetManager, wu core.WinUtils) error {
	fmt.Println("\n--- Скачивание и распаковка компонентов ---")

	// 1. Скачиваем архив FRPC с помощью assetmgr
	frpcZipPath := filepath.Join(am.Cfg().AssetsCachePath, "frpc.zip")
	if _, err := am.DownloadHTTPWithProgress(m.Cfg.FrpcDownloadURL, frpcZipPath); err != nil {
		return fmt.Errorf("не удалось скачать FRPC: %w", err)
	}

	// 2. Находим путь к frpc.exe внутри архива
	frpcPathInZip, err := findPathInZip(frpcZipPath, "frpc.exe")
	if err != nil {
		return fmt.Errorf("не найден frpc.exe в архиве: %w", err)
	}

	// 3. Извлекаем frpc.exe с помощью assetmgr.ExtractFile
	frpcDestPath := filepath.Join(m.Cfg.InstallPath, "frpc.exe")
	if err := am.ExtractFile(frpcZipPath, frpcPathInZip, frpcDestPath); err != nil {
		return fmt.Errorf("не удалось извлечь frpc.exe: %w", err)
	}
	fmt.Println("frpc.exe успешно извлечен.")

	// 4. Скачиваем архив NSSM с помощью assetmgr
	nssmZipPath := filepath.Join(am.Cfg().AssetsCachePath, "nssm.zip")
	if _, err := am.DownloadHTTPWithProgress(m.Cfg.NssmDownloadURL, nssmZipPath); err != nil {
		return fmt.Errorf("не удалось скачать NSSM: %w", err)
	}

	// 5. Определяем архитектуру и путь к nssm.exe внутри архива
	archDir := "win32"
	if wu.Is64BitOS() {
		archDir = "win64"
		fmt.Println("Обнаружена 64-битная система. Ищем nssm.exe в папке win64.")
	} else {
		fmt.Println("Обнаружена 32-битная система. Ищем nssm.exe в папке win32.")
	}
	nssmSubPath := filepath.Join(archDir, "nssm.exe")

	nssmPathInZip, err := findPathInZip(nssmZipPath, nssmSubPath)
	if err != nil {
		return fmt.Errorf("не найден nssm.exe для архитектуры %s: %w", archDir, err)
	}

	// 6. Извлекаем nssm.exe с помощью assetmgr.ExtractFile
	nssmDestPath := filepath.Join(m.Cfg.InstallPath, "nssm.exe")
	if err := am.ExtractFile(nssmZipPath, nssmPathInZip, nssmDestPath); err != nil {
		return fmt.Errorf("не удалось извлечь nssm.exe: %w", err)
	}
	fmt.Println("nssm.exe успешно извлечен.")

	return nil
}

func findPathInZip(zipPath, targetSuffix string) (string, error) {
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return "", err
	}
	defer r.Close()

	// Нормализуем разделители
	targetSuffix = filepath.ToSlash(targetSuffix)

	for _, f := range r.File {
		if f.FileInfo().IsDir() {
			continue
		}
		if strings.HasSuffix(filepath.ToSlash(f.Name), targetSuffix) {
			return f.Name, nil // Возвращаем полный путь файла в архиве
		}
	}
	return "", fmt.Errorf("файл, заканчивающийся на '%s', не найден в архиве '%s'", targetSuffix, zipPath)
}

func (m *Module) findFreePort() (int, error) {
	fmt.Println("Получение информации о прокси с сервера FRPS...")
	apiURL := fmt.Sprintf("https://%s/api/proxy/tcp", m.Cfg.ServerConfig.Host)
	req, _ := http.NewRequest("GET", apiURL, nil)
	req.SetBasicAuth(m.Cfg.ServerConfig.User, m.Cfg.ServerConfig.Pass)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	var proxyInfo FrpsProxyInfo
	if err := json.Unmarshal(body, &proxyInfo); err != nil {
		return 0, fmt.Errorf("ошибка парсинга JSON ответа от FRPS: %w", err)
	}
	hasOfflineProxy := false
	for _, p := range proxyInfo.Proxies {
		if p.Status == "offline" {
			hasOfflineProxy = true
			break
		}
	}
	if hasOfflineProxy {
		fmt.Println("\nВНИМАНИЕ: Обнаружены оффлайн-прокси. Автоматический выбор порта рискован.")
		reader := bufio.NewReader(os.Stdin)
		for {
			fmt.Print("Пожалуйста, введите желаемый удаленный порт вручную: ")
			portStr, _ := reader.ReadString('\n')
			port, err := strconv.Atoi(strings.TrimSpace(portStr))
			if err != nil {
				fmt.Println("Неверный ввод. Пожалуйста, введите число.")
				continue
			}
			return port, nil
		}
	}
	fmt.Println("Все прокси онлайн. Выполняем автоматический поиск свободного порта...")
	usedPorts := make(map[int]bool)
	for _, p := range proxyInfo.Proxies {
		if p.Conf != nil {
			usedPorts[p.Conf.RemotePort] = true
		}
	}
	localUsedPorts := getLocalUsedPorts(filepath.Join(m.Cfg.InstallPath, "frpc.ini"))
	for _, p := range localUsedPorts {
		usedPorts[p] = true
	}
	parts := strings.Split(m.Cfg.PortRange, "-")
	startPort, _ := strconv.Atoi(parts[0])
	endPort, _ := strconv.Atoi(parts[1])
	for port := startPort; port <= endPort; port++ {
		if !usedPorts[port] {
			return port, nil
		}
	}
	return 0, fmt.Errorf("свободные порты в диапазоне %s не найдены", m.Cfg.PortRange)
}
func (m *Module) updateFrpcIni(alias, localPort, remotePort string) error { // ...
	iniPath := filepath.Join(m.Cfg.InstallPath, "frpc.ini")
	content, err := os.ReadFile(iniPath)
	var lines []string
	if err != nil {
		lines = []string{
			"[common]",
			"server_addr = " + m.Cfg.ServerConfig.Host,
			"server_port = " + strconv.Itoa(m.Cfg.ServerConfig.TunnelPort),
			"log_file = " + filepath.Join(m.Cfg.InstallPath, "frpc.log"),
			"log_level = info",
		}
	} else {
		lines = strings.Split(string(content), "\n")
	}
	newSectionName := fmt.Sprintf("[%s-MH]", alias)
	sectionExists := false
	for _, line := range lines {
		if strings.TrimSpace(line) == newSectionName {
			sectionExists = true
			break
		}
	}
	if !sectionExists {
		lines = append(lines, "", newSectionName, "type = tcp", "local_ip = 127.0.0.1", "local_port = "+localPort, "remote_port = "+remotePort)
		err = os.WriteFile(iniPath, []byte(strings.Join(lines, "\r\n")), 0644)
		if err != nil {
			return fmt.Errorf("не удалось записать в frpc.ini: %w", err)
		}
		fmt.Println("Новая секция добавлена в frpc.ini")
	} else {
		fmt.Printf("Секция '%s' уже существует. Пропускаем.\n", newSectionName)
	}
	return nil
}
func (m *Module) setupNssmService() error { // ...
	nssmExe := filepath.Join(m.Cfg.InstallPath, "nssm.exe")
	frpcExe := filepath.Join(m.Cfg.InstallPath, "frpc.exe")
	frpcIni := filepath.Join(m.Cfg.InstallPath, "frpc.ini")
	commands := [][]string{
		{"install", m.Cfg.ServiceName, frpcExe, "-c", frpcIni},
		{"set", m.Cfg.ServiceName, "Start", "SERVICE_AUTO_START"},
		{"set", m.Cfg.ServiceName, "AppDirectory", m.Cfg.InstallPath},
	}
	fmt.Printf("Создание и настройка службы '%s' с помощью nssm...\n", m.Cfg.ServiceName)
	for _, args := range commands {
		cmd := exec.Command(nssmExe, args...)
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("ошибка при выполнении nssm %s: %w", args[0], err)
		}
	}
	fmt.Println("Служба успешно настроена.")
	return nil
}
func getLocalUsedPorts(iniPath string) []int { // ...
	content, err := os.ReadFile(iniPath)
	if err != nil {
		return nil
	}
	re := regexp.MustCompile(`^\s*remote_port\s*=\s*(\d+)`)
	var ports []int
	for _, line := range strings.Split(string(content), "\n") {
		matches := re.FindStringSubmatch(line)
		if len(matches) > 1 {
			port, _ := strconv.Atoi(matches[1])
			ports = append(ports, port)
		}
	}
	return ports
}

go `
===== END frpc.go =====

modules/iiko/iiko.go
===== START iiko.go =====
go `
package iiko

import (
	"bufio"
	"errors"
	"fmt"
	"goMH/config"
	"goMH/core"
	"goMH/tui"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// DiscoveredVersions хранит найденные на FTP версии и их компоненты
type DiscoveredVersions map[string][]config.IikoComponent

var errUserChoseExit = errors.New("пользователь выбрал выход в главное меню")

type Module struct {
	Cfg *config.IikoConfig
}

func (m *Module) ID() string       { return "iiko" }
func (m *Module) MenuText() string { return "iiko (Front, Back, Card)" }

func (m *Module) Run(am core.AssetManager, wu core.WinUtils) error {
	m.Cfg = &am.Cfg().IikoConfig

	// 1. Сканируем FTP на предмет доступных версий
	tui.Info("Сканирование FTP на наличие дистрибутивов iiko...")
	discovered, err := m.discoverVersions(am)
	if err != nil {
		return fmt.Errorf("не удалось просканировать FTP: %w", err)
	}
	if len(discovered) == 0 {
		return fmt.Errorf("на FTP не найдено ни одной корректной версии iiko")
	}

	// 2. Показываем меню выбора дистрибутива
	selectedComponent, err := m.showDistroMenu(discovered)
	if err != nil {
		if errors.Is(err, errUserChoseExit) {
			tui.Info("Возврат в главное меню.")
			return nil
		}
		return err
	}

	// --- ИЗМЕНЕНИЕ ЛОГИКИ ---
	// 3. Сразу после выбора дистрибутива, если это Front, предлагаем выбрать патч.
	var selectedPatch IikoPatch
	var patchWasSelected bool

	if selectedComponent.ID == "Front" {
		// Вызываем новую функцию, которая только находит и предлагает выбрать патч
		selectedPatch, patchWasSelected, err = FindAndSelectPatch(am, selectedComponent.Version)
		if err != nil {
			// В случае серьезной ошибки прерываем установку
			return fmt.Errorf("критическая ошибка при выборе патча: %w", err)
		}
	}
	// --- КОНЕЦ ИЗМЕНЕНИЯ ЛОГИКИ ---

	distroName := "iiko " + selectedComponent.Version + " " + selectedComponent.MenuText
	if selectedComponent.ID == "iikoCard" {
		distroName = selectedComponent.MenuText
	}
	tui.Title(fmt.Sprintf("\n--- Начало установки %s ---", distroName))

	targetDir := filepath.Join(am.Cfg().RootPath, selectedComponent.Version)
	if selectedComponent.ID == "iikoCard" {
		targetDir = filepath.Join(am.Cfg().RootPath, "iikoCardPOS")
	}
	_ = os.MkdirAll(targetDir, 0755)

	installerPath := filepath.Join(targetDir, selectedComponent.FileName)

	// 4. Скачиваем основной установщик
	tui.Info("Скачивание основного дистрибутива...")
	_, err = am.DownloadFTPWithProgress(selectedComponent.FTPPath, installerPath)
	if err != nil {
		return fmt.Errorf("не удалось скачать установщик %s: %w", distroName, err)
	}

	// 5. Запускаем установщик
	exitCode, err := m.runInstaller(wu, installerPath, selectedComponent.InstallArgs, am.Cfg().RootPath)
	if err != nil {
		return err
	}
	if exitCode != 0 {
		return fmt.Errorf("установщик завершился с кодом ошибки: %d", exitCode)
	}

	tui.Success(fmt.Sprintf("\nУстановка %s успешно завершена.", distroName))

	// --- ИЗМЕНЕНИЕ ЛОГИКИ ---
	// 6. Применяем предварительно выбранный патч
	if patchWasSelected {
		if selectedComponent.RunAfter != "" {
			installDir := filepath.Dir(selectedComponent.RunAfter)
			// Вызываем функцию, которая только применяет патч
			err = applyPatch(am, wu, selectedPatch, installDir, targetDir)
			if err != nil {
				// Применение патча - важный шаг, в случае ошибки сообщаем о ней
				tui.Error(fmt.Sprintf("Критическая ошибка при применении патча: %v", err))
				tui.Error("Основная программа была установлена, но патч - нет. Попробуйте установить патч через Утилиты обслуживания.")
			}
		} else {
			tui.Warn("Не удалось применить патч, так как не указан путь установки iiko (run_after).")
		}
	}
	// --- КОНЕЦ ИЗМЕНЕНИЯ ЛОГИКИ ---

	// 7. Запуск приложения после установки
	if selectedComponent.RunAfter != "" {
		if _, err := os.Stat(selectedComponent.RunAfter); err == nil {
			tui.InfoF("Запуск %s...", selectedComponent.RunAfter)
			exec.Command(selectedComponent.RunAfter).Start()
		}
	}

	return nil
}

// --- Функции-помощники ---

func (m *Module) discoverVersions(am core.AssetManager) (DiscoveredVersions, error) {
	entries, err := am.ListFTP(m.Cfg.BaseFTPPath)
	if err != nil {
		return nil, err
	}

	discovered := make(DiscoveredVersions)
	versionRegex := regexp.MustCompile(`^\d{3}$`)

	for _, entry := range entries {
		// Проверяем, что это директория
		if entry.Type != 1 || !versionRegex.MatchString(entry.Name) {
			continue
		}
		version := entry.Name
		versionPath := filepath.Join(m.Cfg.BaseFTPPath, version)
		versionPath = strings.ReplaceAll(versionPath, "\\", "/") // FTP пути используют /

		filesInVersion, err := am.ListFTP(versionPath)
		if err != nil {
			continue
		}

		filesMap := make(map[string]bool)
		for _, file := range filesInVersion {
			filesMap[file.Name] = true
		}

		var foundComponents []config.IikoComponent
		for _, compTmpl := range m.Cfg.ComponentsToFind {
			if filesMap[compTmpl.FileName] {
				comp := compTmpl // Копируем шаблон
				comp.Version = version
				comp.FTPPath = versionPath + "/" + comp.FileName
				foundComponents = append(foundComponents, comp)
			}
		}

		if len(foundComponents) > 0 {
			discovered[version] = foundComponents
		}
	}
	return discovered, nil
}

func (m *Module) showDistroMenu(versions DiscoveredVersions) (config.IikoComponent, error) {
	reader := bufio.NewReader(os.Stdin)
	var menuOptions []config.IikoComponent
	var versionsSorted []string
	for v := range versions {
		versionsSorted = append(versionsSorted, v)
	}
	sort.Sort(sort.Reverse(sort.StringSlice(versionsSorted))) // Сначала новые версии

	for {
		tui.Title("\n--- Выберите дистрибутив iiko для установки ---")
		menuOptions = nil

		// Опция 0 - iikoCard
		cardPosOption := m.Cfg.CardPOS
		cardPosOption.ID = "iikoCard"
		cardPosOption.Version = "Card"
		cardPosOption.FTPPath = m.Cfg.BaseFTPPath + "/" + cardPosOption.FileName
		menuOptions = append(menuOptions, cardPosOption)
		fmt.Printf(" %d. %s\n", 0, cardPosOption.MenuText)

		// Остальные опции
		for _, version := range versionsSorted {
			fmt.Printf("--- Версия iiko %s ---\n", version)
			for _, comp := range versions[version] {
				menuOptions = append(menuOptions, comp)
				fmt.Printf(" %d. %s %s\n", len(menuOptions)-1, version, comp.MenuText)
			}
		}

		fmt.Println("\n 00. Назад в главное меню")
		fmt.Print("Введите номер пункта: ")

		choiceStr, _ := reader.ReadString('\n')
		choiceStr = strings.TrimSpace(choiceStr)

		if choiceStr == "00" {
			return config.IikoComponent{}, errUserChoseExit
		}

		choice, err := strconv.Atoi(choiceStr)
		if err != nil || choice < 0 || choice >= len(menuOptions) {
			tui.Error("Некорректный выбор. Попробуйте снова. 2 секунды...")
			time.Sleep(2 * time.Second)
			continue // Показываем меню заново
		}
		return menuOptions[choice], nil
	}
}

func (m *Module) runInstaller(wu core.WinUtils, installerPath, args, rootPath string) (int, error) {
	// Создаем путь для временного лог-файла
	logFileName := fmt.Sprintf("installer_log_%d.txt", time.Now().Unix())
	tempLogPath := filepath.Join(os.TempDir(), logFileName)

	// Формируем аргументы для установщика
	baseArgs := strings.Fields(args)
	finalArgs := append(baseArgs, "/log", tempLogPath)

	fmt.Printf("\nЗапуск установщика: %s с аргументами %v\n", installerPath, finalArgs)
	fmt.Println("... ИДЕТ УСТАНОВКА, ПОЖАЛУЙСТА, ОЖИДАЙТЕ ...")

	_, err := wu.RunCommand(installerPath, finalArgs...)
	var exitCode int
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			// Если ошибка не связана с кодом завершения (например, файл не найден),
			// возвращаем ее как критическую.
			return -1, fmt.Errorf("не удалось запустить установщик: %w", err)
		}
	} else {
		exitCode = 0
	}

	// Проверяем, был ли создан лог-файл
	if _, statErr := os.Stat(tempLogPath); statErr == nil {
		if exitCode != 0 {
			// Установка завершилась с ошибкой, ПЕРЕМЕЩАЕМ лог
			finalLogPath := filepath.Join(rootPath, logFileName)
			fmt.Printf("Установщик завершился с ошибкой. Сохраняем лог в: %s\n", finalLogPath)
			if renameErr := os.Rename(tempLogPath, finalLogPath); renameErr != nil {
				fmt.Printf("Предупреждение: не удалось переместить лог-файл: %v\n", renameErr)
				// Если переместить не удалось, пробуем хотя бы не удалять его из временной папки
			}
		} else {
			// Установка успешна, УДАЛЯЕМ временный лог
			os.Remove(tempLogPath)
		}
	}

	return exitCode, nil
}

go `
===== END iiko.go =====

modules/iiko/patcher.go
===== START patcher.go =====
go `
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

	// 3. Проверяем архив на "плоскость"
	tui.Info("Проверка содержимого архива...")
	contents, err := wu.ListArchiveContents(patchCachePath)
	if err != nil {
		return fmt.Errorf("не удалось прочитать содержимое архива: %w", err)
	}
	for _, fileInArchive := range contents {
		if strings.Contains(fileInArchive, "/") || strings.Contains(fileInArchive, "\\") {
			return fmt.Errorf("ошибка безопасности: архив '%s' содержит вложенные папки ('%s'). Установка отменена", patch.ShortName, fileInArchive)
		}
	}
	tui.Success("Архив успешно прошел проверку.")

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

		// Создаем конечный файл на диске
		destPath := filepath.Join(installDir, pathInArchive)
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
	for _, fileName := range filesToReplace {
		sourcePath := filepath.Join(installDir, fileName)
		destPath := filepath.Join(backupDir, fileName)

		// Копируем только если исходный файл существует
		if _, err := os.Stat(sourcePath); err == nil {
			sourceFile, err := os.Open(sourcePath)
			if err != nil {
				continue
			}
			defer sourceFile.Close()

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

go `
===== END patcher.go =====

modules/regime/regime.go
===== START regime.go =====
go `
package regime

import (
	"fmt"
	"goMH/core"
	"goMH/tui"
	"os"
	"path/filepath"
	"time"
)

type Module struct{}

func (m *Module) ID() string {
	return "Regime"
}

func (m *Module) MenuText() string {
	return "Regime (Локальный модуль ЧестныйЗнак)"
}

func (m *Module) Run(am core.AssetManager, wu core.WinUtils) error {
	tui.Title("\n--- Запуск установки/обновления Regime ---")

	// 1. Получаем ресурс (MSI-установщик) через assetmgr
	tui.Info("-> Этап 1: Получение установщика...")
	msiPath, err := am.DownloadToCache("Regime_Installer")
	if err != nil {
		return fmt.Errorf("не удалось получить ресурс 'Regime_Installer': %w", err)
	}

	// 2. Проверяем, установлена ли служба "regime"
	tui.Info("-> Этап 2: Проверка существующей установки...")
	const serviceName = "regime"
	isReinstall, err := wu.ServiceExists(serviceName)
	if err != nil {
		// Если сама проверка не удалась, это критическая ошибка.
		return fmt.Errorf("не удалось проверить наличие службы '%s': %w", serviceName, err)
	}

	// 3. Формируем аргументы для msiexec
	logDir := filepath.Join(am.Cfg().RootPath, "logs")
	_ = os.MkdirAll(logDir, 0755)
	logPath := filepath.Join(logDir, fmt.Sprintf("regime_install_%d.log", time.Now().Unix()))

	// Базовый набор аргументов
	args := []string{
		"/i", msiPath,
		"/qn", // Тихий режим без интерфейса
		"/norestart",
		"/L*v", logPath,
		"ADMINUSER=MH",
		"ADMINPASSWORD=mhrcadmin994525",
	}

	// Условное добавление флага переустановки
	if isReinstall {
		tui.Warn("Обнаружена существующая служба 'regime'. Будет выполнена переустановка с сохранением данных.")
		args = append(args, "REINSTALL_FLAG=1")
	} else {
		tui.Info("Новая установка 'regime'.")
	}

	// 4. Запуск установки с помощью msiexec
	tui.InfoF("-> Этап 3: Запуск установки %s...", filepath.Base(msiPath))
	tui.Info("Установка будет выполнена в тихом режиме. Это может занять несколько минут...")

	// Передаем слайс аргументов в RunCommand
	output, err := wu.RunCommand("msiexec.exe", args...)
	if err != nil {
		return fmt.Errorf("установщик msiexec завершился с ошибкой. Лог: %s. Вывод: %s. Ошибка: %w", logPath, output, err)
	}

	tui.SuccessF("Установка успешно завершена. Подробный лог сохранен в %s", logPath)
	return nil
}

go `
===== END regime.go =====

modules/remoteaccess/remoteaccess.go
===== START remoteaccess.go =====
go `
package remoteaccess

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"goMH/core"
	"goMH/tui"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

type Module struct{}

func (m *Module) ID() string { return "RemoteAccess" }
func (m *Module) MenuText() string {
	return "Установить средства удаленного доступа (TV, LM, Getad)"
}

// Структура для хранения информации о компоненте
type remoteComponent struct {
	ID             string
	Name           string
	ServiceName    string
	IsInstalled    bool
	AllowReinstall bool
	InstallFunc    func(am core.AssetManager, wu core.WinUtils) error
}

// Главная функция Run теперь управляет подменю
func (m *Module) Run(am core.AssetManager, wu core.WinUtils) error {
	// Инициализируем компоненты
	components := []*remoteComponent{
		{ID: "1", Name: "TeamViewer", ServiceName: "TeamViewer", InstallFunc: m.installTeamViewer, AllowReinstall: false},
		{ID: "2", Name: "LiteManager", ServiceName: "ROMService", InstallFunc: m.installLiteManager, AllowReinstall: false},
		{ID: "3", Name: "Getad Agent", ServiceName: "MH_Getad", InstallFunc: m.installGetad, AllowReinstall: true},
	}

	reader := bufio.NewReader(os.Stdin)

	// Основной цикл подменю
	for {
		tui.Title("\n--- Меню установки средств удаленного доступа ---")
		// Перед показом меню обновляем статусы
		m.checkStatuses(wu, components)

		// Отображаем меню
		for _, c := range components {
			status := tui.ColorRed + "[не установлено]" + tui.ColorReset
			menuText := fmt.Sprintf("Установить %s", c.Name)
			if c.IsInstalled {
				status = tui.ColorGreen + "[установлено]" + tui.ColorReset
				// Если разрешена переустановка, меняем текст
				if c.AllowReinstall {
					menuText = fmt.Sprintf("Переустановить %s", c.Name)
				}
			}
			fmt.Printf(" %s. %s %s\n", c.ID, menuText, status)
		}
		fmt.Println("\n 0. Назад в главное меню")
		fmt.Print("Выберите пункт: ")

		// Читаем выбор пользователя
		choiceStr, _ := reader.ReadString('\n')
		choiceStr = strings.TrimSpace(choiceStr)

		if choiceStr == "0" {
			return nil // Выход из подменю
		}

		// Находим выбранный компонент
		var chosenComponent *remoteComponent
		for _, c := range components {
			if c.ID == choiceStr {
				chosenComponent = c
				break
			}
		}

		// Если выбор корректен, запускаем установку
		if chosenComponent != nil {
			// Новая логика проверки
			// Блокируем только если компонент установлен И у него НЕТ флага AllowReinstall
			if chosenComponent.IsInstalled && !chosenComponent.AllowReinstall {
				tui.Warn(fmt.Sprintf("\n%s уже установлен. Для переустановки сначала удалите его стандартными средствами Windows.", chosenComponent.Name))
				fmt.Println("Нажмите Enter для продолжения...")
				_, _ = reader.ReadString('\n')
				continue
			}

			// Запускаем функцию установки
			err := chosenComponent.InstallFunc(am, wu)
			if err != nil {
				tui.Error(fmt.Sprintf("\n--- ОШИБКА при установке/переустановке %s ---\n%v\n---------------------------------------\n", chosenComponent.Name, err))
			} else {
				tui.Success(fmt.Sprintf("\n--- %s успешно установлен/переустановлен. ---", chosenComponent.Name))
			}
			fmt.Println("\nНажмите Enter, чтобы вернуться в меню...")
			reader.ReadString('\n')

		} else {
			tui.Error("\nНеверный выбор. Попробуйте снова.")
			time.Sleep(2 * time.Second)
		}
	}
}

// checkStatuses обновляет поле IsInstalled для каждого компонента
func (m *Module) checkStatuses(wu core.WinUtils, components []*remoteComponent) {
	for _, c := range components {
		installed, err := wu.ServiceExists(c.ServiceName)
		if err != nil {
			// Если проверка не удалась, считаем что не установлено, но выводим ошибку
			tui.Warn(fmt.Sprintf("Не удалось проверить статус службы %s: %v", c.ServiceName, err))
			c.IsInstalled = false
		} else {
			c.IsInstalled = installed
		}
	}
}

// --- Функции установки остаются такими же, как и были ---

// --- Установка TeamViewer ---
func (m *Module) installTeamViewer(am core.AssetManager, wu core.WinUtils) error {
	tui.Info("\n-> Начало установки TeamViewer...")
	cfg := am.Cfg().TeamViewerConfig

	// --- Шаг 1: Получение configId ---
	tui.InfoF("Запрос страницы: %s", cfg.ShortURL)
	client := &http.Client{}
	req, err := http.NewRequest("GET", cfg.ShortURL, nil)
	if err != nil {
		return fmt.Errorf("не удалось создать HTTP-запрос: %w", err)
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/91.0.4472.124 Safari/537.36")
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("не удалось выполнить HTTP-запрос: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("не удалось прочитать тело ответа: %w", err)
	}

	re := regexp.MustCompile(`var configId\s*=\s*"([^"]+)"`)
	matches := re.FindStringSubmatch(string(body))
	if len(matches) < 2 {
		return fmt.Errorf("не удалось найти configId на странице")
	}
	configID := matches[1]
	tui.InfoF("Найден configId: %s", configID)

	// --- Шаг 2: Запрос прямой ссылки от API ---
	type ApiRequestBody struct {
		ConfigID       string `json:"ConfigId"`
		Version        string `json:"Version"`
		IsCustomModule bool   `json:"IsCustomModule"`
		Subdomain      string `json:"Subdomain"`
		ConnectionID   string `json:"ConnectionId"`
	}
	reqBody := ApiRequestBody{
		ConfigID:       configID,
		Version:        "15",
		IsCustomModule: true,
		Subdomain:      "1",
		ConnectionID:   "",
	}
	jsonBody, _ := json.Marshal(reqBody)

	tui.InfoF("Запрос прямой ссылки от API: %s", cfg.ApiURL)
	apiReq, err := http.NewRequest("POST", cfg.ApiURL, bytes.NewBuffer(jsonBody))
	if err != nil {
		return fmt.Errorf("не удалось создать API-запрос: %w", err)
	}
	apiReq.Header.Set("Content-Type", "application/json;charset=UTF-8")
	apiReq.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/91.0.4472.124 Safari/537.36")
	apiReq.Header.Set("Accept", "application/json, text/plain, */*")

	apiResp, err := client.Do(apiReq)
	if err != nil {
		return fmt.Errorf("ошибка при выполнении API-запроса: %w", err)
	}
	defer apiResp.Body.Close()

	if apiResp.StatusCode != http.StatusOK {
		errorBody, _ := io.ReadAll(apiResp.Body)
		return fmt.Errorf("API вернуло ошибку: %s. Тело ответа: %s", apiResp.Status, string(errorBody))
	}

	directURLBody, err := io.ReadAll(apiResp.Body)
	if err != nil {
		return fmt.Errorf("не удалось прочитать ответ от API: %w", err)
	}
	directURL := strings.Trim(string(directURLBody), `"`)
	tui.InfoF("Получена прямая ссылка для скачивания")

	// --- Шаг 3: Скачивание файла с помощью assetmgr ---
	installerName := "TeamViewer_Setup.exe"
	installerPath := filepath.Join(am.Cfg().AssetsCachePath, installerName)

	if _, err := am.DownloadHTTPWithProgress(directURL, installerPath); err != nil {
		return fmt.Errorf("не удалось скачать установщик: %w", err)
	}

	// --- Шаг 4: Запуск установщика ---
	tui.Info("Запуск установщика TeamViewer в тихом режиме...")
	_, err = wu.RunCommand(installerPath, "/S")
	return err
}

// --- Установка LiteManager ---
func (m *Module) installLiteManager(am core.AssetManager, wu core.WinUtils) error {
	tui.Info("\n-> Начало установки LiteManager...")
	msiPath, err := am.DownloadToCache("LiteManager_Installer")
	if err != nil {
		return fmt.Errorf("не удалось скачать установщик LiteManager: %w", err)
	}

	tui.Info("Запуск установки LiteManager в тихом режиме...")
	_, err = wu.RunCommand("msiexec.exe", "/i", msiPath, "/quiet", "/norestart")
	return err
}

// --- Установка Getad ---
func (m *Module) installGetad(am core.AssetManager, wu core.WinUtils) error {
	const assetName = "Getad_Agent"
	const serviceName = "MH_Getad"
	const installSubDir = "getad" // Папка внутри C:\MH

	installDir := filepath.Join(am.Cfg().RootPath, installSubDir)

	tui.Title("\n--- Начало установки/переустановки Getad Agent ---")

	// --- ЭТАП 1: ПОИСК И ОЧИСТКА СТАРОЙ ВЕРСИИ ---
	tui.Info("-> Этап 1: Поиск и удаление предыдущих версий...")

	// Ищем старый getad*.exe
	oldExePath, err := wu.FindFileRecursive(am.Cfg().RootPath, "getad*.exe")
	if err == nil {
		tui.InfoF("Найдена предыдущая установка: %s", oldExePath)
		oldInstallDir := filepath.Dir(oldExePath)

		// 1.1 Удаление службы
		serviceExists, _ := wu.ServiceExists(serviceName)
		if serviceExists {
			tui.Info("Остановка и удаление службы...")
			_, _ = wu.RunCommand(oldExePath, "stop")
			time.Sleep(2 * time.Second) // Даем время на остановку
			_, err := wu.RunCommand(oldExePath, "remove")
			if err != nil {
				// Пробуем альтернативный вариант, если 'remove' не сработал
				_, _ = wu.RunCommand(oldExePath, "uninstall")
			}
		}

		// 1.2 Удаление из автозагрузки
		userStartup, commonStartup, err := wu.GetStartupFolders()
		if err == nil {
			for _, startupDir := range []string{userStartup, commonStartup} {
				entries, _ := os.ReadDir(startupDir)
				for _, entry := range entries {
					if strings.HasSuffix(strings.ToLower(entry.Name()), ".lnk") {
						// TODO: Нужна библиотека для чтения .lnk, чтобы проверить путь.
						// Пока что удаляем по имени файла getad*.lnk
						if strings.HasPrefix(strings.ToLower(entry.Name()), "getad") {
							shortcutPath := filepath.Join(startupDir, entry.Name())
							tui.InfoF("Удаление ярлыка из автозагрузки: %s", shortcutPath)
							_ = wu.DeleteFile(shortcutPath)
						}
					}
				}
			}
		}

		// 1.3 Удаление из планировщика
		taskName, _ := wu.FindScheduledTaskByPath(oldExePath)
		if taskName != "" {
			tui.InfoF("Удаление задачи '%s' из планировщика...", taskName)
			_ = wu.DeleteScheduledTaskByName(taskName)
		}

		// 1.4 Очистка папки установки (если это не корневая папка C:\MH)
		if strings.EqualFold(oldInstallDir, am.Cfg().RootPath) {
			tui.Warn("Getad установлен в корневую папку, очистка не производится.")
		} else {
			tui.InfoF("Очистка директории: %s", oldInstallDir)
			_ = wu.CleanDirectory(oldInstallDir)
		}

	} else {
		tui.Info("Предыдущих версий не найдено, выполняется чистая установка.")
	}
	// Очищаем целевую папку на всякий случай
	_ = wu.CleanDirectory(installDir)
	_ = os.MkdirAll(installDir, 0755)

	// --- ЭТАП 2: УСТАНОВКА НОВОЙ ВЕРСИИ ---
	tui.Info("\n-> Этап 2: Установка новой версии...")

	// 2.1 Добавляем исключение в антивирус
	tui.InfoF("Добавление пути '%s' в исключения Защитника Windows...", installDir)
	if err := wu.AddDefenderExclusion(installDir); err != nil {
		tui.Warn(fmt.Sprintf("Не удалось добавить исключение: %v", err))
	}

	// 2.2 Скачиваем и распаковываем
	tui.Info("Скачивание архива агента...")
	cachePath, err := am.DownloadToCache(assetName)
	if err != nil {
		return fmt.Errorf("не удалось скачать архив агента: %w", err)
	}

	tui.InfoF("Распаковка архива в '%s'...", installDir)
	// Используем новую "умную" распаковку
	if err := am.UnpackToFlatDir(assetName, cachePath, installDir); err != nil {
		return fmt.Errorf("не удалось распаковать архив агента: %w", err)
	}

	// 2.3 Установка и запуск службы
	serviceExe := filepath.Join(installDir, "getad-service.exe")
	if _, err := os.Stat(serviceExe); os.IsNotExist(err) {
		return fmt.Errorf("не найден исполняемый файл службы: %s", serviceExe)
	}

	tui.Info("Установка службы...")
	if _, err := wu.RunCommand(serviceExe, "--startup", "auto", "install"); err != nil {
		return fmt.Errorf("не удалось установить службу: %w", err)
	}

	tui.Info("Запуск службы...")
	if _, err := wu.RunCommand(serviceExe, "start"); err != nil {
		return fmt.Errorf("не удалось запустить службу: %w", err)
	}

	// 2.4 Настройка триггеров
	tui.Info("Настройка триггеров службы...")
	triggers := []string{"start/machinepolicy", "start/userpolicy"}
	if err := wu.SetServiceTriggers(serviceName, triggers); err != nil {
		tui.Warn(fmt.Sprintf("Не удалось установить триггеры: %v", err))
	}

	// --- ЭТАП 3: ПРОВЕРКА ---
	tui.Info("\n-> Этап 3: Проверка статуса службы...")
	time.Sleep(3 * time.Second) // Даем службе время на запуск
	status, err := wu.GetServiceStatus(serviceName)
	if err != nil {
		return fmt.Errorf("не удалось проверить статус службы: %w", err)
	}

	if status == "RUNNING" {
		tui.SuccessF("Служба '%s' успешно установлена и запущена.", serviceName)
	} else {
		return fmt.Errorf("служба '%s' установлена, но ее статус '%s', а не 'RUNNING'", serviceName, status)
	}

	return nil
}

go `
===== END remoteaccess.go =====

modules/serviceutils/serviceutils.go
===== START serviceutils.go =====
go `
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
	"goMH/modules/iiko"
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
		fmt.Println(" 4. Патчи iikoFront")
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
		case "4":
			err = m.updateIikoPatches(am, wu)
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

// --- Пункт 4: Обновление патчей iikoFront ---
func (m *Module) updateIikoPatches(am core.AssetManager, wu core.WinUtils) error {
	const iikoFrontDir = `C:\Program Files\iiko\iikoRMS\Front.Net`
	const iikoFrontExe = `iikoFront.Net.exe`

	frontExePath := filepath.Join(iikoFrontDir, iikoFrontExe)

	if _, err := os.Stat(frontExePath); os.IsNotExist(err) {
		return fmt.Errorf("установка iikoFront не найдена по стандартному пути: %s", iikoFrontDir)
	}

	tui.Info("Определение версии установленного iikoFront...")
	fullVersion, err := wu.GetFileVersion(frontExePath)
	if err != nil {
		return fmt.Errorf("не удалось определить версию файла %s: %w", iikoFrontExe, err)
	}
	tui.SuccessF("Найдена версия: %s", fullVersion)

	// Конвертируем полную версию в короткий формат "927"
	parts := strings.Split(fullVersion, ".")
	if len(parts) < 3 {
		return fmt.Errorf("некорректный формат версии: %s", fullVersion)
	}
	// Собираем из 9.2.7... -> 927
	shortVersion := parts[0] + parts[1] + parts[2][0:1]

	backupDir := filepath.Join(am.Cfg().RootPath, shortVersion)

	// Вызываем общий воркфлоу из модуля iiko
	return iiko.RunPatchWorkflow(am, wu, shortVersion, iikoFrontDir, backupDir)
}

go `
===== END serviceutils.go =====

modules/utm/utm.go
===== START utm.go =====
go `
package utm

import (
	"errors"
	"fmt"
	"goMH/core"
	"goMH/tui"
	"strings"
)

type Module struct{}

func (m *Module) ID() string {
	return "UTM"
}

func (m *Module) MenuText() string {
	return "Установить УТМ (ЕГАИС)"
}

func (m *Module) Run(am core.AssetManager, wu core.WinUtils) error {
	cfg := am.Cfg().UTMConfig

	tui.Title(fmt.Sprintf("\n--- Начало установки: %s ---", m.MenuText()))

	if cfg.AssetID == "" {
		return errors.New("в секции 'utm_config' не указан asset_id")
	}

	tui.Info("Получение установщика через AssetManager...")
	// Используем assetmgr для скачивания файла в кэш, он сам выберет метод (HTTP/FTP)
	installerPath, err := am.DownloadToCache(cfg.AssetID)
	if err != nil {
		return fmt.Errorf("не удалось получить ассет '%s': %w", cfg.AssetID, err)
	}

	tui.Info("Запуск установки...")
	tui.InfoF("Аргументы: %s", cfg.InstallArgs)
	args := strings.Fields(cfg.InstallArgs)

	_, err = wu.RunCommand(installerPath, args...)
	if err != nil {
		return fmt.Errorf("ошибка при установке УТМ: %w", err)
	}

	return nil
}

go `
===== END utm.go =====

modules/vcomcaster/vcomcaster.go
===== START vcomcaster.go =====
go `
// file: modules/vcomcaster/vcomcaster.go

package vcomcaster

import (
	"bufio"
	"errors"
	"fmt"
	"goMH/core"
	"goMH/tui"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/beevik/etree"
	"gopkg.in/ini.v1"
)

const (
	taskName        = "VComCaster Autostart"
	iikoProcessName = "iikoFront"
)

type Module struct{}

func (m *Module) ID() string       { return "VComCaster" }
func (m *Module) MenuText() string { return "VComCaster (для сканера штрих-кодов)" }

// Run - главная точка входа в модуль.
func (m *Module) Run(am core.AssetManager, wu core.WinUtils) error {
	vcomcasterBaseDir := filepath.Join(am.Cfg().RootPath, "vcomcaster")

	if _, err := os.Stat(vcomcasterBaseDir); err == nil {
		// Если директория есть, запускаем режим диагностики/удаления
		return m.runDiagnosticsWorkflow(wu, am, vcomcasterBaseDir)
	}

	// Если директории нет, запускаем режим установки
	return m.runInstallWorkflow(am, wu)
}

// extractDeviceID извлекает часть VID_...&PID_... из полного PNPDeviceID.
func extractDeviceID(pnpDeviceID string) string {
	// Регулярное выражение для поиска "VID_...&PID_..." после "USB\"
	// (?i) - регистронезависимый поиск
	// \\ - экранированный обратный слеш
	// ([^&\\]+) - захватывает все символы до следующего & или \
	re := regexp.MustCompile(`(?i)USB\\(VID_[^&]+&PID_[^&\\]+)`)
	matches := re.FindStringSubmatch(pnpDeviceID)

	if len(matches) > 1 {
		return matches[1] // Возвращаем первую захваченную группу
	}
	// Если не найдено, возвращаем пустую строку, конфиг будет без этого значения
	return ""
}

// --- РЕЖИМ УСТАНОВКИ ---
func (m *Module) runInstallWorkflow(am core.AssetManager, wu core.WinUtils) error {
	tui.Title("\n--- Запуск установки VComCaster ---")

	// 1. Получаем ресурсы
	tui.Info("-> Этап 1: Загрузка необходимых ресурсов...")
	vcomcasterDestPath, err := am.Get("VComCaster_Package")
	if err != nil {
		return fmt.Errorf("не удалось получить VComCaster_Package: %w", err)
	}
	com0comInstallerExe, err := am.DownloadToCache("Com0Com_Installer")
	if err != nil {
		return fmt.Errorf("не удалось скачать Com0Com_Installer в кэш: %w", err)
	}
	tui.SuccessF("Установщик com0com находится в кэше: %s", com0comInstallerExe)

	// 2. Установка com0com
	tui.Info("-> Этап 2: Установка com0com...")
	portsBefore, _ := wu.GetComPorts()

	com0comInstallDir := filepath.Join(vcomcasterDestPath, "com0com")
	_ = os.MkdirAll(com0comInstallDir, 0755)

	com0comEnv := map[string]string{
		"CNC_INSTALL_COMX_COMX_PORTS":      "YES",
		"CNC_INSTALL_CNCA0_CNCB0_PORTS":    "NO",
		"CNC_INSTALL_START_MENU_SHORTCUTS": "NO",
	}

	_, err = wu.RunCommandWithEnv(
		com0comEnv,
		com0comInstallerExe,
		"/S",
		fmt.Sprintf("/D=%s", com0comInstallDir),
	)
	if err != nil {
		return fmt.Errorf("ошибка при установке com0com: %w", err)
	}
	tui.Info("Установка com0com завершена, ожидание инициализации портов (5 сек)...")
	time.Sleep(5 * time.Second)

	portsAfter, _ := wu.GetComPorts()
	newPorts := findNewPorts(portsBefore, portsAfter)
	if len(newPorts) < 2 {
		tui.Warn("ПРЕДУПРЕЖДЕНИЕ: Не удалось определить созданные виртуальные COM-порты. Проверьте Диспетчер устройств.")
	} else {
		sort.Strings(newPorts)
		tui.SuccessF("Созданы виртуальные порты: %s и %s", newPorts[0], newPorts[1])
	}

	// 3. Определение сканера (НОВАЯ ЛОГИКА)
	tui.Info("-> Этап 3: Определение сканера...")
	scanners, err := wu.GetScanners()
	if err != nil {
		return fmt.Errorf("критическая ошибка при поиске сканеров: %w", err)
	}
	if len(scanners) == 0 {
		return errors.New("не найдено ни одного USB-сканера, подключенного к COM-порту. Проверьте подключение и драйверы")
	}

	tui.Title("\n--- Найдены следующие устройства ---")
	for i, scanner := range scanners {
		// Проверяем, содержит ли Caption (название продукта) уже имя порта.
		portInCaption := fmt.Sprintf("(%s)", scanner.Port)
		if strings.Contains(scanner.Caption, portInCaption) {
			// Если да, то просто выводим Caption как есть.
			fmt.Printf(" %d. %s\n", i+1, scanner.Caption)
		} else {
			// Если нет, то добавляем порт в скобках для красоты.
			fmt.Printf(" %d. %s (%s)\n", i+1, scanner.Caption, scanner.Port)
		}
	}
	fmt.Print("Выберите номер вашего сканера: ")

	reader := bufio.NewReader(os.Stdin)
	choiceStr, _ := reader.ReadString('\n')
	choice, err := strconv.Atoi(strings.TrimSpace(choiceStr))
	if err != nil || choice < 1 || choice > len(scanners) {
		return errors.New("неверный выбор, установка прервана")
	}

	selectedScanner := scanners[choice-1]
	scannerComPort := selectedScanner.Port
	scannerDeviceID := extractDeviceID(selectedScanner.PNPDeviceID)

	tui.SuccessF("Выбран сканер: %s на порту %s", selectedScanner.Caption, scannerComPort)
	if scannerDeviceID != "" {
		tui.SuccessF("Определен ID устройства: %s", scannerDeviceID)
	} else {
		tui.Warn("Не удалось определить VID/PID устройства. Поле device_id в конфиге будет пустым.")
	}

	// 4. Создание config.ini
	tui.Info("-> Этап 4: Создание config.ini...")
	outputPort := ""
	if len(newPorts) > 0 {
		outputPort = newPorts[0]
	}
	iniContent := fmt.Sprintf(
		"[app]\r\nautostart_listing = 1\r\nautoreconnect = 1\r\nlogs-autoclear-days = 2\r\n[device]\r\ndevice_id = %s\r\ninput_port = %s\r\noutput_port = %s\r\nport_baudrate = 115200\r\ncr = 0\r\nlf = 0\r\n[service]\r\namount_rm_char_id = 0\r\ntimeout_clearcash = 1.5\r\ntimeout_autoreconnect = 3\r\ntimeout_reconnect = 3",
		scannerDeviceID, scannerComPort, outputPort,
	)
	configPath := filepath.Join(vcomcasterDestPath, "config.ini")
	if err := os.WriteFile(configPath, []byte(iniContent), 0644); err != nil {
		return fmt.Errorf("не удалось создать config.ini: %w", err)
	}
	tui.Success("Файл config.ini успешно создан.")

	// 5. Финальная настройка
	tui.Info("-> Этап 5: Финальная настройка (Планировщик, запуск)...")
	vcomcasterExePath := filepath.Join(vcomcasterDestPath, "vcomcaster.exe")

	if err := wu.CreateScheduledTask(taskName, vcomcasterExePath, vcomcasterDestPath); err != nil {
		tui.Warn(fmt.Sprintf("ВНИМАНИЕ: Не удалось создать/обновить задачу в планировщике: %v", err))
	} else {
		tui.SuccessF("Задача '%s' в Планировщике Windows успешно создана/обновлена.", taskName)
	}

	if len(newPorts) > 1 {
		iikoPort := newPorts[1]
		if err := m.updateIikoConfig(wu, iikoPort); err != nil {
			tui.Warn(fmt.Sprintf("Не удалось автоматически обновить конфиг iiko: %v", err))
			tui.Warn(fmt.Sprintf("ВАЖНО: Пожалуйста, вручную укажите в настройках iiko порт сканера: %s", iikoPort))
		}
	} else {
		tui.Warn("Не удалось определить порт для iiko. Пропустили обновление конфига.")
	}

	tui.Info("Запуск vcomcaster.exe...")
	// Запуск GUI приложения без ожидания. Этот вызов остается прямым, т.к. не влияет на тестируемую логику.
	startCmd := exec.Command(vcomcasterExePath)
	startCmd.Dir = vcomcasterDestPath
	if err := startCmd.Start(); err != nil {
		return fmt.Errorf("не удалось запустить vcomcaster.exe: %w", err)
	}
	tui.Success("Приложение vcomcaster успешно запущено в фоновом режиме.")

	return nil
}

func (m *Module) updateIikoConfig(wu core.WinUtils, iikoPort string) error {
	const maxRetries = 3
	const retryDelay = 10 * time.Second

	configDir, err := os.UserConfigDir()
	if err != nil {
		return fmt.Errorf("не удалось найти директорию APPDATA: %w", err)
	}
	configPath := filepath.Join(configDir, "iiko", "CashServer", "config.xml")

	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		tui.InfoF("Файл конфигурации iiko не найден по пути: %s. Пропускаем.", configPath)
		tui.InfoF("После первого запуска iiko, пожалуйста, укажите порт сканера вручную: %s", iikoPort)
		return nil
	}
	tui.InfoF("Найден файл конфигурации iiko: %s", configPath)

	for i := 0; i < maxRetries; i++ {
		isRunning, err := wu.IsProcessRunning(iikoProcessName)
		if err != nil {
			return fmt.Errorf("не удалось проверить статус процесса iiko: %w", err)
		}
		if !isRunning {
			break
		}
		tui.Warn(fmt.Sprintf("Обнаружен запущенный процесс '%s'. Это может помешать сохранению файла.", iikoProcessName))
		tui.Warn(fmt.Sprintf("Пожалуйста, закройте iiko Front. Ожидание %v... (попытка %d из %d)", retryDelay, i+1, maxRetries))
		time.Sleep(retryDelay)

		if i == maxRetries-1 {
			return fmt.Errorf("процесс '%s' все еще запущен после %d попыток. Изменение отменено", iikoProcessName, maxRetries)
		}
	}

	doc := etree.NewDocument()
	if err := doc.ReadFromFile(configPath); err != nil {
		return fmt.Errorf("ошибка чтения XML файла: %w", err)
	}

	root := doc.SelectElement("config")
	if root == nil {
		return fmt.Errorf("корневой элемент <config> не найден в файле %s. Изменение отменено", configPath)
	}

	portElement := root.SelectElement("comBarcodeScanerPort")
	if portElement == nil {
		tui.Info("Элемент <comBarcodeScanerPort> не найден. Создаем его.")
		portElement = root.CreateElement("comBarcodeScanerPort")
	}

	tui.InfoF("Обновляем порт сканера в конфиге iiko на '%s'...", iikoPort)
	portElement.SetText(iikoPort)

	doc.Indent(2)
	if err := doc.WriteToFile(configPath); err != nil {
		return fmt.Errorf("ошибка сохранения XML файла: %w", err)
	}

	tui.Success("Конфигурация iiko успешно обновлена.")
	return nil
}

// --- РЕЖИМ ДИАГНОСТИКИ И УДАЛЕНИЯ ---
func (m *Module) runDiagnosticsWorkflow(wu core.WinUtils, am core.AssetManager, baseDir string) error {
	tui.Title("\n--- Обнаружена существующая установка. Запуск диагностики... ---")

	var problems []string
	configPath := filepath.Join(baseDir, "config.ini")
	com0comUninstallerPath := filepath.Join(baseDir, "com0com", "uninstall.exe")

	if _, err := os.Stat(configPath); err != nil {
		problems = append(problems, "x Файл конфигурации config.ini не найден.")
	}
	if _, err := os.Stat(com0comUninstallerPath); err != nil {
		problems = append(problems, "x Деинсталлятор com0com не найден.")
	}
	if _, err := wu.RunCommand("schtasks", "/Query", "/TN", taskName); err != nil {
		problems = append(problems, fmt.Sprintf("x Задача '%s' в Планировщике не найдена.", taskName))
	}

	if len(problems) == 0 {
		tui.Success("\n[ДИАГНОСТИКА] Проблем не обнаружено. Система выглядит настроенной.")
	} else {
		tui.Warn("\n[ДИАГНОСТИКА] Обнаружены следующие проблемы:")
		for _, p := range problems {
			tui.Warn(p)
		}
	}

	fmt.Println("\nВыберите действие:")
	fmt.Println(" 1. Полностью удалить VComCaster")
	fmt.Println(" 2. Выполнить переустановку (сначала удалит, потом нужно запустить снова)")
	fmt.Println(" 3. Вернуться в главное меню")
	fmt.Print("Ваш выбор: ")

	reader := bufio.NewReader(os.Stdin)
	choice, _ := reader.ReadString('\n')
	choice = strings.TrimSpace(choice)

	switch choice {
	case "1":
		return m.runUninstallation(wu, am) // <-- Передаем am
	case "2":
		return m.runReinstallation(wu, am) // <-- Вызываем новую функцию
	case "3":
		tui.Info("Операция отменена. Возврат в главное меню.")
		return nil
	default:
		tui.Warn("Неверный выбор. Возврат в главное меню.")
		return nil
	}
}

// Новая функция переустановки
func (m *Module) runReinstallation(wu core.WinUtils, am core.AssetManager) error {
	tui.Title("\n--- Начало процесса переустановки VComCaster ---")

	tui.Info("-> Остановка процесса 'vcomcaster.exe'...")
	_, _ = wu.RunCommand("taskkill", "/F", "/IM", "vcomcaster.exe")

	tui.InfoF("-> Удаление задачи '%s' из Планировщика...", taskName)
	if _, err := wu.RunCommand("schtasks", "/Delete", "/TN", taskName, "/F"); err != nil {
		tui.Warn("   (Предупреждение: не удалось удалить задачу, возможно, ее и не было)")
	}

	tui.Info("-> Очистка старых ассетов VComCaster...")
	if err := am.PurgeAsset("VComCaster_Package"); err != nil {
		tui.Warn(fmt.Sprintf("Произошла ошибка при очистке ассета VComCaster_Package: %v", err))
	}
	if err := am.PurgeAsset("Com0Com_Installer"); err != nil {
		tui.Warn(fmt.Sprintf("Произошла ошибка при очистке ассета Com0Com_Installer: %v", err))
	}
	tui.Success("Старые ассеты очищены.")

	tui.Info("\n--- Запуск новой установки ---")
	// Просто вызываем основной воркфлоу установки
	return m.runInstallWorkflow(am, wu)
}

// Функция полного удаления
func (m *Module) runUninstallation(wu core.WinUtils, am core.AssetManager) error { // <-- Добавляем am в аргументы
	tui.Title("\n--- Начало процесса полного удаления ---")
	baseDir := filepath.Join(am.Cfg().RootPath, "vcomcaster")

	tui.Info("-> Остановка процесса 'vcomcaster.exe'...")
	_, _ = wu.RunCommand("taskkill", "/F", "/IM", "vcomcaster.exe")

	tui.InfoF("-> Удаление задачи '%s'...", taskName)
	if _, err := wu.RunCommand("schtasks", "/Delete", "/TN", taskName, "/F"); err != nil {
		tui.Warn("   (Предупреждение: не удалось удалить задачу, возможно, ее и не было)")
	}

	uninstallerPath := filepath.Join(baseDir, "com0com", "uninstall.exe")
	installPath := filepath.Join(baseDir, "com0com")
	if _, err := os.Stat(uninstallerPath); err == nil {
		tui.Info("-> Запуск деинсталлятора com0com...")
		if _, err := wu.RunCommand(uninstallerPath, "/S", fmt.Sprintf("_?=%s", installPath)); err != nil {
			tui.Warn(fmt.Sprintf("   (Предупреждение: деинсталлятор com0com завершился с ошибкой: %v)", err))
		} else {
			tui.Success("   com0com удален.")
		}
	} else {
		tui.Info("-> Деинсталлятор com0com не найден, пропуск.")
	}

	tui.Info("-> Очистка ассетов и директорий VComCaster...")
	if err := am.PurgeAsset("VComCaster_Package"); err != nil {
		tui.Warn(fmt.Sprintf("Произошла ошибка при очистке ассета VComCaster_Package: %v", err))
	}
	if err := am.PurgeAsset("Com0Com_Installer"); err != nil {
		tui.Warn(fmt.Sprintf("Произошла ошибка при очистке ассета Com0Com_Installer: %v", err))
	}

	tui.Success("\nПолное удаление успешно завершено.")
	return nil
}

// Вспомогательные функции
func findNewPorts(before, after []string) []string {
	beforeMap := make(map[string]bool)
	for _, port := range before {
		beforeMap[port] = true
	}
	var newPorts []string
	for _, port := range after {
		if !beforeMap[port] {
			newPorts = append(newPorts, port)
		}
	}
	return newPorts
}

func readConfig(path string) (*ini.File, error) {
	cfg, err := ini.Load(path)
	if err != nil {
		return nil, err
	}
	return cfg, nil
}

go `
===== END vcomcaster.go =====

tui/menu.go
===== START menu.go =====
go `
package tui

import (
	"bufio"
	"fmt"
	"goMH/core"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
)

// Installer - локальный интерфейс, чтобы не импортировать main
type Installer core.Installer

func clearScreen() {
	if runtime.GOOS == "windows" {
		cmd := exec.Command("cmd", "/c", "cls")
		cmd.Stdout = os.Stdout
		_ = cmd.Run()
	} else {
		fmt.Print("\033[H\033[2J")
	}
}

func ShowMenu(modules []Installer) (Installer, error) {
	reader := bufio.NewReader(os.Stdin)

	for {
		clearScreen()
		fmt.Println(ColorYellow + "==================================================" + ColorReset)
		fmt.Println(ColorYellow + "      МЕНЮ УСТАНОВЩИКА MYHORECA (golang)          " + ColorReset)
		fmt.Println(ColorYellow + "==================================================" + ColorReset)
		fmt.Println()

		for i, mod := range modules {
			// Используем стандартный fmt.Printf, но можем добавить цвет, если хотим
			fmt.Printf(" %d. %s\n", i+1, mod.MenuText())
		}
		fmt.Println()
		fmt.Println(" Q. Выход")
		fmt.Println()
		fmt.Print("Введите номер пункта и нажмите Enter: ")

		choiceStr, _ := reader.ReadString('\n')
		choiceStr = strings.TrimSpace(choiceStr)

		if strings.EqualFold(choiceStr, "q") {
			return nil, fmt.Errorf("пользователь выбрал выход")
		}

		choiceInt, err := strconv.Atoi(choiceStr)
		if err != nil || choiceInt < 1 || choiceInt > len(modules) {
			Error("\nНеверный выбор. Нажмите Enter, чтобы попробовать снова.")
			_, _ = reader.ReadString('\n') // Ожидаем нажатия Enter
			continue
		}

		return modules[choiceInt-1], nil
	}
}

go `
===== END menu.go =====

tui/output.go
===== START output.go =====
go `
package tui

import "fmt"

// Используем простые ANSI-коды, они хорошо работают в современных терминалах Windows.
const (
	ColorReset  = "\033[0m"
	ColorRed    = "\033[31m"
	ColorGreen  = "\033[32m"
	ColorYellow = "\033[33m"
	ColorBlue   = "\033[34m"
	ColorCyan   = "\033[36m"
)

// Info выводит информационное сообщение (голубой цвет).
func Info(msg string) {
	fmt.Println(ColorCyan + msg + ColorReset)
}

// InfoF форматирует и выводит информационное сообщение.
func InfoF(format string, a ...interface{}) {
	Info(fmt.Sprintf(format, a...))
}

// Success выводит сообщение об успехе (зеленый цвет).
func Success(msg string) {
	fmt.Println(ColorGreen + msg + ColorReset)
}

// SuccessF форматирует и выводит сообщение об успехе.
func SuccessF(format string, a ...interface{}) {
	Success(fmt.Sprintf(format, a...))
}

// Warn выводит предупреждение (желтый цвет).
func Warn(msg string) {
	fmt.Println(ColorYellow + msg + ColorReset)
}

// Error выводит сообщение об ошибке (красный цвет).
func Error(msg string) {
	fmt.Println(ColorRed + msg + ColorReset)
}

// Title выводит заголовок (синий цвет).
func Title(msg string) {
	fmt.Println(ColorBlue + msg + ColorReset)
}

go `
===== END output.go =====

winutils/utils.go
===== START utils.go =====
go `
package winutils

import (
	"context"
	"encoding/csv"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"
	"unsafe"

	"github.com/mholt/archives"
	"go.bug.st/serial/enumerator"
	"golang.org/x/sys/windows"
)

// IsAdmin остается без изменений
func IsAdmin() bool {
	_, err := os.Open("\\\\.\\PHYSICALDRIVE0")
	return err == nil
}

// RunCommand остается без изменений
func RunCommand(name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	cmd.Env = append(os.Environ(), "LANG=en_US.UTF-8")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("ошибка выполнения '%s %v': %v, вывод: %s", name, args, err, string(output))
	}
	return strings.TrimSpace(string(output)), nil
}

func RunCommandWithEnv(env map[string]string, name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)

	// Собираем переменные окружения
	newEnv := os.Environ() // Начинаем с существующих
	for key, value := range env {
		newEnv = append(newEnv, fmt.Sprintf("%s=%s", key, value))
	}
	cmd.Env = newEnv // Устанавливаем их для команды

	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("ошибка выполнения '%s %v' с кастомным env: %v, вывод: %s", name, args, err, string(output))
	}
	return strings.TrimSpace(string(output)), nil
}

// CreateScheduledTask создает или обновляет задачу в Планировщике Windows через импорт XML.
func CreateScheduledTask(taskName, executablePath, workingDir string) error {
	fmt.Printf("Создание/обновление задачи '%s' через XML...\n", taskName)

	// 1. Генерируем XML-содержимое для задачи
	xmlContent, err := generateTaskXML(taskName, executablePath, workingDir)
	if err != nil {
		return fmt.Errorf("не удалось сгенерировать XML для задачи: %w", err)
	}

	// 2. Создаем временный файл для XML
	tempFile, err := os.CreateTemp("", "task-*.xml")
	if err != nil {
		return fmt.Errorf("не удалось создать временный XML-файл: %w", err)
	}
	// Гарантируем удаление временного файла после завершения функции
	defer os.Remove(tempFile.Name())

	// 3. Записываем XML во временный файл
	if _, err := tempFile.Write([]byte(xmlContent)); err != nil {
		tempFile.Close() // Закрываем файл перед попыткой удаления
		return fmt.Errorf("не удалось записать XML во временный файл: %w", err)
	}
	tempFile.Close() // Важно закрыть файл перед тем, как его прочитает schtasks

	// 4. Используем schtasks для создания/обновления задачи из XML
	// Флаг /F (Force) автоматически перезаписывает задачу, если она уже существует.
	output, err := RunCommand("schtasks", "/Create", "/TN", taskName, "/XML", tempFile.Name(), "/F")
	if err != nil {
		return fmt.Errorf("не удалось создать задачу из XML: %w", err)
	}

	fmt.Printf("Задача '%s' успешно создана/обновлена. Вывод schtasks: %s\n", taskName, output)
	return nil
}

// generateTaskXML создает строку с XML-описанием задачи.
func generateTaskXML(taskName, executablePath, workingDir string) (string, error) {
	currentUser, err := user.Current()
	if err != nil {
		return "", fmt.Errorf("не удалось определить текущего пользователя: %w", err)
	}
	// SID пользователя в формате S-1-5-21... или "BUILTIN\Administrators"
	// Для современных систем лучше использовать его имя.
	userID := currentUser.Username

	absExecutablePath, err := filepath.Abs(executablePath)
	if err != nil {
		return "", err
	}

	absWorkingDir, err := filepath.Abs(workingDir)
	if err != nil {
		return "", err
	}

	// Шаблон XML для задачи
	xmlTemplate := `<?xml version="1.0" encoding="UTF-16"?>
<Task version="1.2" xmlns="http://schemas.microsoft.com/windows/2004/02/mit/task">
  <RegistrationInfo>
    <Date>%s</Date>
    <Author>%s</Author>
    <Description>Autostart for %s</Description>
  </RegistrationInfo>
  <Triggers>
    <LogonTrigger>
      <Enabled>true</Enabled>
      <UserId>%s</UserId>
    </LogonTrigger>
  </Triggers>
  <Principals>
    <Principal id="Author">
      <UserId>%s</UserId>
      <LogonType>InteractiveToken</LogonType>
      <RunLevel>HighestAvailable</RunLevel>
    </Principal>
  </Principals>
  <Settings>
    <MultipleInstancesPolicy>IgnoreNew</MultipleInstancesPolicy>
    <DisallowStartIfOnBatteries>false</DisallowStartIfOnBatteries>
    <StopIfGoingOnBatteries>false</StopIfGoingOnBatteries>
    <AllowHardTerminate>true</AllowHardTerminate>
    <StartWhenAvailable>true</StartWhenAvailable>
    <RunOnlyIfNetworkAvailable>false</RunOnlyIfNetworkAvailable>
    <ExecutionTimeLimit>PT0S</ExecutionTimeLimit>
    <Priority>7</Priority>
  </Settings>
  <Actions Context="Author">
    <Exec>
      <Command>"%s"</Command>
      <WorkingDirectory>%s</WorkingDirectory>
    </Exec>
  </Actions>
</Task>`

	// Форматируем XML с нужными данными
	currentTime := time.Now().Format(time.RFC3339)
	return fmt.Sprintf(xmlTemplate,
		currentTime,
		userID,
		taskName,
		userID,
		userID,
		absExecutablePath, // Команда
		absWorkingDir,     // Рабочая директория
	), nil
}

// GetComPorts остается без изменений
func GetComPorts() ([]string, error) {
	out, err := RunCommand("wmic", "path", "Win32_SerialPort", "get", "DeviceID")
	if err != nil {
		return nil, fmt.Errorf("не удалось получить список COM портов через WMIC: %w", err)
	}

	lines := strings.Split(string(out), "\n")
	ports := []string{}
	re := regexp.MustCompile(`(COM\d+)`)

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if re.MatchString(line) {
			ports = append(ports, line)
		}
	}
	return ports, nil
}

func IsProcessRunning(processName string) (bool, error) {
	// 1. Получаем список ВСЕХ процессов в формате CSV без заголовков.
	// Эта команда завершится успешно, даже если процессов много или мало.
	out, err := RunCommand("tasklist", "/NH", "/FO", "CSV")
	if err != nil {
		// Если сама команда tasklist не смогла выполниться, это серьезная ошибка.
		return false, fmt.Errorf("не удалось выполнить tasklist: %w", err)
	}

	// 2. Используем встроенный в Go CSV-парсер для анализа вывода.
	r := csv.NewReader(strings.NewReader(out))
	records, err := r.ReadAll()
	if err != nil {
		return false, fmt.Errorf("не удалось распарсить CSV-вывод tasklist: %w", err)
	}

	// 3. Итерируем по списку процессов и ищем нужный.
	for _, record := range records {
		// Первая колонка в выводе tasklist - это "Image Name".
		// Например, "iikoFront.Net.exe"
		if len(record) > 0 {
			imageName := record[0]
			// Проверяем, начинается ли имя процесса с искомой строки.
			// Это покрывает случаи вроде "iikoFront.exe", "iikoFront.Net.exe" и т.д.
			if strings.HasPrefix(strings.ToLower(imageName), strings.ToLower(processName)) {
				// Нашли!
				return true, nil
			}
		}
	}

	// Если прошли весь список и не нашли, значит, процесс не запущен.
	return false, nil
}

func ManageService(action, serviceName string) error {
	_, err := RunCommand("sc.exe", action, serviceName)
	if err != nil {
		// Ошибки от sc.exe часто не являются критичными (например, попытка остановить уже остановленную службу).
		// Мы просто логируем их как предупреждение.
		fmt.Printf("Предупреждение при выполнении 'sc %s %s': %v\n", action, serviceName, err)
		// Возвращаем nil, чтобы не прерывать выполнение скрипта.
		return nil
	}
	fmt.Printf("Команда 'sc %s %s' выполнена.\n", action, serviceName)
	return nil
}

func AddDefenderExclusion(path string) error {
	// Эта команда PowerShell требует запуска от имени администратора.
	powerShellCommand := fmt.Sprintf("Add-MpPreference -ExclusionPath '%s'", path)
	_, err := RunCommand("powershell", "-NoProfile", "-Command", powerShellCommand)
	if err != nil {
		// Ошибка может означать, что Defender не активен, или исключение уже существует.
		// Логируем как предупреждение.
		fmt.Printf("Предупреждение при добавлении исключения для Defender: %v\n", err)
		return nil
	}
	fmt.Printf("Путь '%s' добавлен в исключения Defender (или уже был там).\n", path)
	return nil
}

// Is64BitOS проверяет, является ли операционная система 64-битной.
func Is64BitOS() bool {
	// runtime.GOARCH вернет "amd64" для 64-битных систем
	// и "386" для 32-битных.
	return runtime.GOARCH == "amd64"
}

func ServiceExists(serviceName string) (bool, error) {
	// sc.exe query <serviceName> вернет ошибку, если служба не найдена.
	// Мы ищем конкретный текст ошибки, чтобы отличить "не найдено" от других проблем.
	_, err := RunCommand("sc.exe", "query", serviceName)
	if err == nil {
		// Команда выполнилась без ошибок, значит служба существует.
		return true, nil
	}

	// Проверяем, является ли ошибка именно той, что нам нужна.
	// Код 1060: The specified service does not exist as an installed service.
	// Текст может быть локализован, но поиск по коду или стандартной английской фразе надежен.
	if strings.Contains(err.Error(), "does not exist") || strings.Contains(err.Error(), "1060") {
		// Это ожидаемая "ошибка", если службы нет. Для нас это не ошибка.
		return false, nil
	}

	// Если произошла другая, непредвиденная ошибка (например, нет прав), возвращаем ее.
	return false, fmt.Errorf("не удалось выполнить проверку службы '%s': %w", serviceName, err)
}

// SetServiceTriggers устанавливает триггеры запуска для службы Windows.
// triggers - это слайс строк, например ["start/machinepolicy", "start/userpolicy"]
func SetServiceTriggers(serviceName string, triggers []string) error {
	args := []string{"triggerinfo", serviceName}
	args = append(args, triggers...)

	output, err := RunCommand("sc.exe", args...)
	if err != nil {
		// Ошибка здесь может быть критичной, поэтому возвращаем ее.
		return fmt.Errorf("не удалось установить триггеры для службы '%s': %s. Ошибка: %w", serviceName, output, err)
	}
	fmt.Printf("Триггеры для службы '%s' успешно установлены.\n", serviceName)
	return nil
}

// scannerInfo - это локальная, неэкспортируемая структура для внутреннего использования.
type scannerInfo struct {
	Port        string
	Caption     string
	PNPDeviceID string
}

// GetScanners теперь возвращает срез локальных структур и не зависит от пакета core.// Использует нативную библиотеку для перечисления ВСЕХ COM-портов, включая виртуальные.
func GetScanners() ([]scannerInfo, error) {
	ports, err := enumerator.GetDetailedPortsList()
	if err != nil {
		return nil, fmt.Errorf("не удалось получить список портов через нативную библиотеку: %w", err)
	}

	if len(ports) == 0 {
		return []scannerInfo{}, nil
	}

	var scanners []scannerInfo
	for _, port := range ports {
		// Нас интересуют только USB-устройства
		if port.IsUSB {
			// Формируем Caption, похожий на тот, что в Диспетчере устройств
			caption := port.Product

			// Формируем PNPDeviceID из доступной информации
			pnpDeviceID := fmt.Sprintf("USB\\VID_%s&PID_%s", port.VID, port.PID)

			scanners = append(scanners, scannerInfo{
				Port:        port.Name,   // e.g., "COM5"
				Caption:     caption,     // e.g., "ATOL USB (COM5)"
				PNPDeviceID: pnpDeviceID, // e.g., "USB\VID_2912&PID_0005"
			})
		}
	}

	return scanners, nil
}

// GetFileVersion читает информацию о версии файла напрямую через WinAPI.
func GetFileVersion(path string) (string, error) {
	size, err := windows.GetFileVersionInfoSize(path, nil)
	if err != nil {
		return "", fmt.Errorf("GetFileVersionInfoSize failed for %s: %w", path, err)
	}
	if size == 0 {
		return "", fmt.Errorf("no version info found in %s", path)
	}

	buffer := make([]byte, size)
	err = windows.GetFileVersionInfo(path, 0, size, unsafe.Pointer(&buffer[0]))
	if err != nil {
		return "", fmt.Errorf("GetFileVersionInfo failed for %s: %w", path, err)
	}

	var fixedInfo *windows.VS_FIXEDFILEINFO
	var len uint32
	err = windows.VerQueryValue(unsafe.Pointer(&buffer[0]), "\\", unsafe.Pointer(&fixedInfo), &len)
	if err != nil {
		return "", fmt.Errorf("VerQueryValue failed: could not find fixed file info block: %w", err)
	}
	if fixedInfo == nil {
		return "", fmt.Errorf("не найдена структура VS_FIXEDFILEINFO")
	}

	if fixedInfo.Signature != 0xFEEF04BD {
		return "", fmt.Errorf("invalid fixed file info signature")
	}

	major := uint16(fixedInfo.FileVersionMS >> 16)
	minor := uint16(fixedInfo.FileVersionMS & 0xffff)
	patch := uint16(fixedInfo.FileVersionLS >> 16)
	build := uint16(fixedInfo.FileVersionLS & 0xffff)

	return fmt.Sprintf("%d.%d.%d.%d", major, minor, patch, build), nil
}

// ListArchiveContents возвращает список путей файлов внутри архива.
func ListArchiveContents(archivePath string) ([]string, error) {
	var filePaths []string

	// Создаем виртуальную файловую систему из архива
	fsys, err := archives.FileSystem(context.Background(), archivePath, nil)
	if err != nil {
		return nil, fmt.Errorf("не удалось открыть архив %s как файловую систему: %w", archivePath, err)
	}

	// Проходим по всем файлам в виртуальной ФС
	err = fs.WalkDir(fsys, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err // Прерываем обход при ошибке
		}
		if d.IsDir() {
			return nil // Пропускаем директории
		}
		filePaths = append(filePaths, path)
		return nil
	})

	if err != nil {
		return nil, fmt.Errorf("не удалось прочитать содержимое архива %s: %w", archivePath, err)
	}

	return filePaths, nil
}

// FindFileRecursive ищет файл по шаблону, начиная с корневой директории.
func FindFileRecursive(root, pattern string) (string, error) {
	var foundPath string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			matched, _ := filepath.Match(pattern, d.Name())
			if matched {
				foundPath = path
				return fs.ErrExist // Прерываем поиск, как только нашли
			}
		}
		return nil
	})
	if err == fs.ErrExist {
		return foundPath, nil
	}
	if err != nil {
		return "", err
	}
	return "", fmt.Errorf("файл по шаблону '%s' не найден в '%s'", pattern, root)
}

// GetStartupFolders возвращает пути к папкам автозагрузки для текущего пользователя и для всех пользователей.
func GetStartupFolders() (user, common string, err error) {
	// Папка автозагрузки текущего пользователя
	userConfigDir, err := os.UserConfigDir()
	if err != nil {
		return "", "", err
	}
	user = filepath.Join(userConfigDir, "..", "Roaming", "Microsoft", "Windows", "Start Menu", "Programs", "Startup")

	// Общая папка автозагрузки
	common = os.ExpandEnv(`%ProgramData%\Microsoft\Windows\Start Menu\Programs\StartUp`)
	return user, common, nil
}

// DeleteFile просто удаляет файл.
func DeleteFile(path string) error {
	return os.Remove(path)
}

// CleanDirectory удаляет все содержимое директории, не удаляя саму директорию.
func CleanDirectory(path string) error {
	dir, err := os.ReadDir(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // Если папки нет, считать задачу выполненной
		}
		return err
	}
	for _, d := range dir {
		err := os.RemoveAll(filepath.Join(path, d.Name()))
		if err != nil {
			return err
		}
	}
	return nil
}

// FindScheduledTaskByPath ищет задачу в планировщике по пути к исполняемому файлу.
func FindScheduledTaskByPath(exePath string) (string, error) {
	out, err := RunCommand("schtasks", "/Query", "/V", "/FO", "CSV")
	if err != nil {
		return "", err
	}

	r := csv.NewReader(strings.NewReader(out))
	records, err := r.ReadAll()
	if err != nil {
		return "", err
	}

	absExePath, _ := filepath.Abs(exePath)

	for _, record := range records {
		if len(record) > 8 {
			taskName := record[0]
			taskToRun := record[8]
			// Сравниваем абсолютные пути, чтобы избежать неоднозначности
			absTaskPath, _ := filepath.Abs(strings.Trim(taskToRun, `"`))
			if strings.EqualFold(absTaskPath, absExePath) {
				return taskName, nil
			}
		}
	}
	return "", nil // Не найдено - не ошибка
}

// DeleteScheduledTaskByName удаляет задачу по имени.
func DeleteScheduledTaskByName(taskName string) error {
	_, err := RunCommand("schtasks", "/Delete", "/TN", taskName, "/F")
	return err
}

// GetServiceStatus возвращает статус службы (например, "RUNNING", "STOPPED").
// Функция ищет непереводимые английские ключевые слова статуса,
// что делает ее нечувствительной к языку операционной системы.
func GetServiceStatus(serviceName string) (string, error) {
	out, err := RunCommand("sc.exe", "query", serviceName)
	if err != nil {
		if strings.Contains(err.Error(), "1060") { // Служба не существует
			return "NOT_FOUND", nil
		}
		return "", err
	}

	// Этот шаблон ищет одно из стандартных, непереводимых состояний службы.
	// Они всегда выводятся в верхнем регистре на английском языке.
	re := regexp.MustCompile(`(STOPPED|START_PENDING|STOP_PENDING|RUNNING|CONTINUE_PENDING|PAUSE_PENDING|PAUSED)`)
	matches := re.FindStringSubmatch(out)

	if len(matches) > 1 {
		// Возвращаем первое найденное совпадение, например, "RUNNING"
		return matches[1], nil
	}

	return "UNKNOWN", fmt.Errorf("не удалось определить статус службы из вывода sc.exe")
}

go `
===== END utils.go =====

