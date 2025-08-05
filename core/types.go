package core

import "goMH/config"

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
	IsProcessRunning(processName string) (bool, error)
	CreateScheduledTask(taskName, executablePath, workingDir string) error
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
