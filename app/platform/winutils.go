package platform

import (
	"goMH/core"
	"goMH/winutils"
	"io"

	"golang.org/x/sys/windows/registry"
)

type RealWinUtils struct {
	runtime *winutils.Runtime
}

func NewRealWinUtils() *RealWinUtils {
	return &RealWinUtils{runtime: winutils.NewRuntime()}
}

func (rw *RealWinUtils) WithConsoleOutput(stdout io.Writer, stderr io.Writer) core.WinUtils {
	return &RealWinUtils{
		runtime: rw.withRuntime().WithConsoleOutput(stdout, stderr),
	}
}

func (rw *RealWinUtils) ConsoleWriter() io.Writer {
	return rw.withRuntime().ConsoleWriter()
}

func (rw *RealWinUtils) withRuntime() *winutils.Runtime {
	if rw == nil || rw.runtime == nil {
		return winutils.NewRuntime()
	}
	return rw.runtime
}

func (rw *RealWinUtils) RunCommand(name string, args ...string) (string, error) {
	return winutils.RunCommand(name, args...)
}

func (rw *RealWinUtils) StartDetachedProcess(name string, args ...string) error {
	return rw.withRuntime().StartDetachedProcess(name, args...)
}

func (rw *RealWinUtils) StartDetachedProcessInDir(name string, workingDir string, args ...string) error {
	return rw.withRuntime().StartDetachedProcessInDir(name, workingDir, args...)
}

func (rw *RealWinUtils) ServiceExists(serviceName string) (bool, error) {
	return winutils.ServiceExists(serviceName)
}

func (rw *RealWinUtils) AddDefenderExclusion(path string) error {
	return rw.withRuntime().AddDefenderExclusion(path)
}

func (rw *RealWinUtils) SetServiceTriggers(serviceName string, triggers []string) error {
	return rw.withRuntime().SetServiceTriggers(serviceName, triggers)
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

func (rw *RealWinUtils) GracefulShutdownProcess(processName string) error {
	return winutils.GracefulShutdownProcess(processName)
}

func (rw *RealWinUtils) CreateScheduledTask(taskName, executablePath, arguments, workingDir string) error {
	return rw.withRuntime().CreateScheduledTask(taskName, executablePath, arguments, workingDir)
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

func (rw *RealWinUtils) FindNewestFileByPattern(root, pattern string) (string, error) {
	return winutils.FindNewestFileByPattern(root, pattern)
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

func (rw *RealWinUtils) IsAdmin() bool {
	return winutils.IsAdmin()
}

func (rw *RealWinUtils) CopyFile(src, dst string) error {
	return winutils.CopyFile(src, dst)
}

func (rw *RealWinUtils) CopyDir(src, dst string) error {
	return winutils.CopyDir(src, dst)
}

func (rw *RealWinUtils) MoveFile(src, dst string) error {
	return winutils.MoveFile(src, dst)
}

func (rw *RealWinUtils) MoveDir(src, dst string) error {
	return winutils.MoveDir(src, dst)
}

func (rw *RealWinUtils) ReadRegistryKey(rootKey registry.Key, path, valueName string) (string, error) {
	return winutils.ReadRegistryKey(rootKey, path, valueName)
}

func (rw *RealWinUtils) ListAutostartEntries() ([]core.AutostartEntry, error) {
	return winutils.ListAutostartEntries()
}

func (rw *RealWinUtils) ListAutostartEntriesWithProgress(progress func(core.AutostartScanProgress)) ([]core.AutostartEntry, error) {
	return winutils.ListAutostartEntriesWithProgress(progress)
}

func (rw *RealWinUtils) ApplyAutostartChanges(changes []core.AutostartChange) error {
	return winutils.ApplyAutostartChanges(changes)
}

func (rw *RealWinUtils) AddRegistryAutostartEntry(req core.AutostartCreateRequest) error {
	return winutils.AddRegistryAutostartEntry(req)
}

func (rw *RealWinUtils) DeleteRegistryAutostartValue(scope core.AutostartScope, key core.AutostartRegistryKey, valueName string) error {
	return winutils.DeleteRegistryAutostartValue(scope, key, valueName)
}

func (rw *RealWinUtils) RegistryAutostartValueExists(scope core.AutostartScope, key core.AutostartRegistryKey, valueName string) bool {
	return winutils.RegistryAutostartValueExists(scope, key, valueName)
}

func (rw *RealWinUtils) ResolveShortcut(path string) (core.ShortcutInfo, error) {
	return winutils.ResolveShortcut(path)
}

func (rw *RealWinUtils) RequiresAdminElevation(exePath string) (bool, error) {
	return winutils.RequiresAdminElevation(exePath)
}

func (rw *RealWinUtils) AddScheduledAutostartTask(name, exePath, arguments string) error {
	return winutils.AddScheduledAutostartTask(name, exePath, arguments)
}

func (rw *RealWinUtils) UninstallSystemApp(partialName string) error {
	return rw.withRuntime().UninstallSystemApp(partialName)
}

func (rw *RealWinUtils) ExtractArchive(archivePath, destDir string, fullPaths bool) error {
	return winutils.ExtractArchive(archivePath, destDir, fullPaths)
}

func (rw *RealWinUtils) GetDesktopDir() (string, error) {
	return winutils.GetDesktopDir()
}

func (rw *RealWinUtils) CreateShortcut(targetPath, shortcutPath, arguments string) error {
	return rw.withRuntime().CreateShortcut(targetPath, shortcutPath, arguments)
}

func (rw *RealWinUtils) Reboot() error {
	return winutils.Reboot()
}

func (rw *RealWinUtils) CollectTLSInfo() string {
	return rw.withRuntime().CollectTLSInfo()
}

func (rw *RealWinUtils) CollectProxyInfo() string {
	return rw.withRuntime().CollectProxyInfo()
}
