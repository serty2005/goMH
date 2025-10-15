package core

import (
	"goMH/config"

	"golang.org/x/sys/windows/registry"
)

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
	FindNewestFileByPattern(root, pattern string) (string, error)
	GetStartupFolders() (user, common string, err error)
	DeleteFile(path string) error
	CleanDirectory(path string) error
	FindScheduledTaskByPath(exePath string) (string, error)
	DeleteScheduledTaskByName(taskName string) error
	GetServiceStatus(serviceName string) (string, error)
	IsAdmin() bool
	CopyFile(src, dst string) error
	CopyDir(src, dst string) error
	MoveDir(src, dst string) error
	ReadRegistryKey(rootKey registry.Key, path, valueName string) (string, error)
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
