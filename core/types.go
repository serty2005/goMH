package core

import (
	"context"
	"goMH/config"
	"net/netip"
	"time"

	"golang.org/x/sys/windows/registry"
)

// IPAddressDADState описывает состояние Windows Duplicate Address Detection.
type IPAddressDADState int

const (
	IPAddressDADInvalid IPAddressDADState = iota
	IPAddressDADTentative
	IPAddressDADDuplicate
	IPAddressDADDeprecated
	IPAddressDADPreferred
)

// NetworkIPv4Address описывает IPv4-адрес, назначенный интерфейсу Windows.
type NetworkIPv4Address struct {
	Address           netip.Addr
	PrefixLength      uint8
	PrefixOrigin      uint32
	SuffixOrigin      uint32
	DADState          IPAddressDADState
	ValidLifetime     time.Duration
	PreferredLifetime time.Duration
	CreationTimestamp int64
}

// NetworkInterfaceInfo содержит read-only snapshot сетевого интерфейса.
type NetworkInterfaceInfo struct {
	LUID            uint64
	GUID            string
	Index           uint32
	Alias           string
	Description     string
	MAC             string
	Type            uint32
	Virtual         bool
	Up              bool
	DHCPEnabled     bool
	IPv4Addresses   []NetworkIPv4Address
	DefaultGateways []netip.Addr
	DNSServers      []netip.Addr
}

// TemporaryIPv4Request задаёт единственную transient NetIO-запись.
type TemporaryIPv4Request struct {
	InterfaceLUID     uint64
	InterfaceIndex    uint32
	Address           netip.Addr
	PrefixLength      uint8
	ValidLifetime     time.Duration
	PreferredLifetime time.Duration
}

// TemporaryIPv4Info идентифицирует transient-запись для проверки и удаления.
type TemporaryIPv4Info struct {
	InterfaceLUID     uint64
	InterfaceIndex    uint32
	Address           netip.Addr
	PrefixLength      uint8
	DADState          IPAddressDADState
	ValidLifetime     time.Duration
	PreferredLifetime time.Duration
	CreationTimestamp int64
}

// IPv4RouteInfo описывает выбранный Windows маршрут и source address.
type IPv4RouteInfo struct {
	InterfaceLUID  uint64
	InterfaceIndex uint32
	SourceAddress  netip.Addr
	NextHop        netip.Addr
	Metric         uint32
}

// TaskContext определяет методы для взаимодействия логики с интерфейсом (CLI или GUI).
type TaskContext interface {
	Context() context.Context
	Info(msg string)
	Warn(msg string)
	Error(msg string)
	Success(msg string)
	// SetStatus устанавливает текстовое описание текущего этапа (например, "Скачивание...")
	SetStatus(text string)
	// SetProgress устанавливает процент выполнения (0-100). -1 для неопределенного прогресса.
	SetProgress(percent int)
	// SetCancelable переключает доступность безопасной отмены для текущего этапа задачи.
	SetCancelable(enabled bool)
}

// ScannerInfo содержит информацию о найденном устройстве-сканере.
type ScannerInfo struct {
	Port        string // Например, "COM3"
	Caption     string // Дружелюбное имя, например "USB-SERIAL CH340 (COM3)"
	PNPDeviceID string // Аппаратный ID, например, "USB\VID_2912&PID_0005&MI_00\..."
}

// WinUtils определяет контракт для утилит, специфичных для Windows.
// Модули будут зависеть от этого интерфейса, а не от конкретного пакета winutils.
type AutostartSource string

const (
	AutostartSourceRegistryRun     AutostartSource = "registry_run"
	AutostartSourceRegistryRunOnce AutostartSource = "registry_runonce"
	AutostartSourceStartupFolder   AutostartSource = "startup_folder"
	AutostartSourceScheduledTask   AutostartSource = "scheduled_task"
)

type AutostartScope string

const (
	AutostartScopeUser    AutostartScope = "user"
	AutostartScopeMachine AutostartScope = "machine"
)

type AutostartRegistryKey string

const (
	AutostartRegistryKeyRun     AutostartRegistryKey = "Run"
	AutostartRegistryKeyRunOnce AutostartRegistryKey = "RunOnce"
)

type AutostartEntry struct {
	ID               string
	Name             string
	Source           AutostartSource
	Scope            AutostartScope
	Enabled          bool
	Command          string
	TargetPath       string
	Arguments        string
	WorkingDirectory string
	RegistryKey      AutostartRegistryKey
	RegistryValue    string
	FilePath         string
	TaskName         string
	CanToggle        bool
}

type AutostartChange struct {
	Entry   AutostartEntry
	Enabled bool
}

type AutostartEdit struct {
	Original         AutostartEntry
	Name             string
	Path             string
	Arguments        string
	WorkingDirectory string
}

type AutostartScanProgress struct {
	Area  string
	Found int
}

type AutostartCreateRequest struct {
	Name             string
	RegistryKey      AutostartRegistryKey
	Scope            AutostartScope
	Path             string
	Arguments        string
	WorkingDirectory string
}

type ShortcutInfo struct {
	TargetPath       string
	Arguments        string
	WorkingDirectory string
}

type WinUtils interface {
	RunCommand(name string, args ...string) (string, error)
	RunCommandWithEnv(env map[string]string, name string, args ...string) (string, error)
	StartDetachedProcess(name string, args ...string) error
	StartDetachedProcessInDir(name string, workingDir string, args ...string) error
	ServiceExists(serviceName string) (bool, error)
	AddDefenderExclusion(path string) error
	SetServiceTriggers(serviceName string, triggers []string) error
	Is64BitOS() bool
	GetComPorts() ([]string, error)
	GetScanners() ([]ScannerInfo, error)
	IsProcessRunning(processName string) (bool, error)
	GracefulShutdownProcess(processName string) error
	GetFileVersion(filePath string) (string, error)
	ListArchiveContents(archivePath string) ([]string, error)
	CreateScheduledTask(taskName, executablePath, arguments, workingDir string) error
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
	MoveFile(src, dst string) error
	MoveDir(src, dst string) error
	ReadRegistryKey(rootKey registry.Key, path, valueName string) (string, error)
	ListAutostartEntries() ([]AutostartEntry, error)
	ListAutostartEntriesWithProgress(progress func(AutostartScanProgress)) ([]AutostartEntry, error)
	ApplyAutostartChanges(changes []AutostartChange) error
	AddRegistryAutostartEntry(req AutostartCreateRequest) error
	DeleteRegistryAutostartValue(scope AutostartScope, key AutostartRegistryKey, valueName string) error
	RegistryAutostartValueExists(scope AutostartScope, key AutostartRegistryKey, valueName string) bool
	ResolveShortcut(path string) (ShortcutInfo, error)
	RequiresAdminElevation(exePath string) (bool, error)
	AddScheduledAutostartTask(name, exePath, arguments string) error
	UninstallSystemApp(partialName string) error
	ExtractArchive(archivePath, destDir string, fullPaths bool) error
	GetDesktopDir() (string, error)
	CreateShortcut(targetPath, shortcutPath, arguments string) error
	Reboot() error
	CollectTLSInfo() string
	CollectProxyInfo() string
	ListNetworkInterfaces() ([]NetworkInterfaceInfo, error)
	CreateTemporaryIPv4(req TemporaryIPv4Request) (TemporaryIPv4Info, error)
	GetTemporaryIPv4(interfaceLUID uint64, interfaceIndex uint32, address netip.Addr) (TemporaryIPv4Info, error)
	SetTemporaryIPv4Lifetimes(info TemporaryIPv4Info, valid, preferred time.Duration) (TemporaryIPv4Info, error)
	DeleteTemporaryIPv4(info TemporaryIPv4Info) error
	GetBestRouteIPv4(interfaceLUID uint64, interfaceIndex uint32, source, destination netip.Addr) (IPv4RouteInfo, error)
	CreateOneShotScheduledTask(taskName, executablePath string, arguments []string, workingDir string, runAt time.Time) error
	ScheduledTaskExists(taskName string) (bool, error)
	OpenURL(rawURL string) error
}

// AssetManager определяет контракт для менеджера ресурсов.
type AssetManager interface {
	Get(assetName string) (string, error)
	DownloadHTTPWithProgress(httpURL, localPath string) (bool, error)
	DownloadFTPWithProgress(ftpCfg config.FTPConfig, ftpPath, localPath string) (bool, error)
	GetFastestFTP(ftpConfigs []config.FTPConfig, testFilePath string) (config.FTPConfig, error)
	ExtractFile(zipPath, pathInZip, destPath string) error
	ListFTP(ftpCfg config.FTPConfig, path string) ([]FTPEntry, error)
	DownloadToCache(assetName string) (string, error)
	ProcessFromCache(assetName, cachePath string) error
	PurgeAsset(assetName string) error
	UnpackToFlatDir(assetName, cachePath, destDir string) error
	Cfg() *config.Config
}

// Installer — базовый интерфейс модуля приложения.
type Installer interface {
	ID() string
	MenuText() string
	Run(am AssetManager, wu WinUtils) error
}

// ModuleServices объединяет зависимости, доступные модулям на уровне приложения.
type ModuleServices struct {
	AssetManager AssetManager
	WinUtils     WinUtils
}

// ModuleTaskSpec описывает задачу, которую модуль хочет поставить в очередь.
type ModuleTaskSpec struct {
	Title     string
	Signature string
	Exclusive bool
}

type ModuleRunMode int

const (
	ModuleRunModeQueue ModuleRunMode = iota
	ModuleRunModeImmediate
)

// ModuleActionResult описывает результат постановки задачи или немедленного действия.
type ModuleActionResult struct {
	Note       string
	TaskID     string
	SelectTask bool
}

// TaskConfirmation описывает сводку параметров перед запуском действия.
type TaskConfirmation struct {
	Details      []string
	ConfirmLabel string
}

// TaskConfirmationProvider позволяет конфигу вернуть сводку параметров для финального подтверждения.
type TaskConfirmationProvider interface {
	TaskConfirmation() TaskConfirmation
}

// LiveLogViewer позволяет интерфейсу перехватить запуск live-просмотра лога
// и показать его во встроенной панели вместо прямого вывода в консоль.
type LiveLogViewer interface {
	OpenLiveLog(filePath string) error
}

// ModuleTaskPlan описывает, как модуль должен быть выполнен после конфигурации.
type ModuleTaskPlan struct {
	Mode             ModuleRunMode
	Task             ModuleTaskSpec
	Result           ModuleActionResult
	SkipConfirmation bool
}

// QueueModule описывает единый контракт модуля для конфигурации, сборки task spec и выполнения.
type QueueModule interface {
	Installer
	ConfigureTask(ctx TaskContext, services ModuleServices) (any, error)
	BuildTask(config any) (ModuleTaskPlan, error)
	ExecuteTask(ctx TaskContext, services ModuleServices, config any) error
}

// ImmediateModuleAction позволяет модулю выполнить особое действие без постановки в очередь.
type ImmediateModuleAction interface {
	ExecuteImmediate(ctx TaskContext, services ModuleServices, config any) (ModuleActionResult, error)
}

type FTPEntry struct {
	Name string
	Type uint
}

// PatchInfo представляет информацию о патче для отображения в интерфейсе
type PatchInfo struct {
	ShortName   string
	Description string
	FullURL     string
	BuildNumber int
	ChangeNote  string
}
