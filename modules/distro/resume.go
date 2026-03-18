package distro

import (
	"encoding/json"
	"fmt"
	"goMH/core"
	"goMH/tui"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/sys/windows/registry"
)

const resumeTaskName = "goMH_Distro_Resume"
const maxResumeReboots = 2

const (
	pendingRebootReasonComponentBasedServicing             = "Обслуживание компонентов Windows: RebootPending"
	pendingRebootReasonWindowsUpdateRebootRequired         = "Центр обновления Windows: RebootRequired"
	pendingRebootReasonSessionManagerPendingFileRenameOps  = "Диспетчер сеансов: PendingFileRenameOperations"
	pendingRebootReasonSessionManagerPendingFileRenameOps2 = "Диспетчер сеансов: PendingFileRenameOperations2"
)

type distroResumeConfig struct {
	Config        DistroInstallConfig `json:"config"`
	ResumeAttempt int                 `json:"resume_attempt"`
	Assets        distroResumeAssets  `json:"assets,omitempty"`
}

type distroResumeAssets struct {
	InstallerPath string `json:"installer_path,omitempty"`
	PatchPath     string `json:"patch_path,omitempty"`
}

type rebootResumeScheduledError struct{}

func (e *rebootResumeScheduledError) Error() string {
	return "resume scheduled after reboot"
}

func isRebootResumeScheduled(err error) bool {
	_, ok := err.(*rebootResumeScheduledError)
	return ok
}

type installerPendingRebootError struct {
	ExitCode int
	LogPath  string
	Output   string
}

func (e *installerPendingRebootError) Error() string {
	return fmt.Sprintf("код возврата %d. Лог: %s. Вывод: %s", e.ExitCode, e.LogPath, e.Output)
}

func (m *Module) Resume(am core.AssetManager, wu core.WinUtils, configPath string) error {
	ctx := tui.NewConsoleContext()
	ctx.SetStatus("Возобновление установки iiko/Syrve после перезагрузки")

	data, err := os.ReadFile(configPath)
	if err != nil {
		return fmt.Errorf("не удалось прочитать файл возобновления: %w", err)
	}

	var resumeCfg distroResumeConfig
	if err := json.Unmarshal(data, &resumeCfg); err != nil {
		return fmt.Errorf("ошибка парсинга конфига возобновления: %w", err)
	}

	resumeCfg.Config.resumeAttempt = resumeCfg.ResumeAttempt
	resumeCfg.Config.preparedInstallerPath = resumeCfg.Assets.InstallerPath
	resumeCfg.Config.preparedPatchPath = resumeCfg.Assets.PatchPath

	if err := m.Execute(ctx, am, wu, &resumeCfg.Config); err != nil {
		if isRebootResumeScheduled(err) {
			return nil
		}
		_ = wu.DeleteScheduledTaskByName(resumeTaskName)
		return err
	}

	ctx.Info("Очистка временных файлов возобновления...")
	_ = os.Remove(configPath)
	_ = wu.DeleteScheduledTaskByName(resumeTaskName)

	return nil
}

func (m *Module) scheduleResumeAfterReboot(ctx core.TaskContext, am core.AssetManager, wu core.WinUtils, cfg *DistroInstallConfig, assets *componentInstallAssets) error {
	ctx.Info("Подготовка к автоматическому возобновлению установки после перезагрузки...")

	if assets != nil {
		if err := m.ensureComponentAssetsPrepared(ctx, am, cfg, assets); err != nil {
			return err
		}
	}

	tempDir := filepath.Join(am.Cfg().RootPath, "temp")
	if err := os.MkdirAll(tempDir, 0o755); err != nil {
		return fmt.Errorf("не удалось создать директорию для возобновления: %w", err)
	}

	resumeConfigPath := filepath.Join(tempDir, "distro_resume.json")
	resumeCfg := distroResumeConfig{
		Config:        *cfg,
		ResumeAttempt: cfg.resumeAttempt + 1,
		Assets: distroResumeAssets{
			InstallerPath: cfg.preparedInstallerPath,
			PatchPath:     cfg.preparedPatchPath,
		},
	}
	data, err := json.Marshal(resumeCfg)
	if err != nil {
		return fmt.Errorf("не удалось сериализовать конфиг возобновления: %w", err)
	}
	if err := os.WriteFile(resumeConfigPath, data, 0o600); err != nil {
		return fmt.Errorf("не удалось сохранить конфиг возобновления: %w", err)
	}

	exePath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("не удалось определить путь к goMH: %w", err)
	}

	args := fmt.Sprintf("-module %s -resume \"%s\"", m.ID(), resumeConfigPath)
	if err := wu.CreateScheduledTask(resumeTaskName, exePath, args, filepath.Dir(exePath)); err != nil {
		return fmt.Errorf("не удалось создать задачу автозапуска: %w", err)
	}

	ctx.Success("Задача автозапуска создана. Система будет перезагружена для завершения обновлений Windows.")
	time.Sleep(2 * time.Second)

	if err := wu.Reboot(); err != nil {
		return fmt.Errorf("не удалось инициировать перезагрузку: %w", err)
	}

	return &rebootResumeScheduledError{}
}

type componentInstallAssets struct {
	downloadURL   string
	installerPath string
	versionDir    string
	patchPath     string
}

func shouldCheckPendingRebootBeforeInstall(cfg *DistroInstallConfig) bool {
	return requiresPendingRebootResume(cfg) && cfg.resumeAttempt == 0
}

func patchCachePath(am core.AssetManager, patch core.PatchInfo) string {
	ext := filepath.Ext(patch.Description)
	if ext == "" {
		ext = ".7z"
	}
	fileName := patch.ShortName + ext
	return filepath.Join(am.Cfg().AssetsCachePath, fileName)
}

func (m *Module) resolveComponentInstallAssets(am core.AssetManager, cfg *DistroInstallConfig) (*componentInstallAssets, error) {
	if cfg == nil {
		return nil, fmt.Errorf("конфиг установки не задан")
	}

	downloadURL := cfg.Component.URLTemplate
	if cfg.Version != "" {
		downloadURL = strings.Replace(downloadURL, "{{VERSION}}", cfg.Version, 1)
	}

	folderName := fmt.Sprintf("%s_%s", cfg.Brand, cfg.Version)
	if cfg.Version == "" {
		folderName = cfg.Component.ID
	}

	versionDir := filepath.Join(am.Cfg().RootPath, folderName)
	installerPath := cfg.preparedInstallerPath
	if installerPath == "" {
		installerPath = filepath.Join(versionDir, filepath.Base(downloadURL))
	}

	assets := &componentInstallAssets{
		downloadURL:   downloadURL,
		installerPath: installerPath,
		versionDir:    versionDir,
	}
	if cfg.Patch != nil {
		patchPath := cfg.preparedPatchPath
		if patchPath == "" {
			patchPath = patchCachePath(am, *cfg.Patch)
		}
		assets.patchPath = patchPath
	}

	return assets, nil
}

func (m *Module) ensureComponentAssetsPrepared(ctx core.TaskContext, am core.AssetManager, cfg *DistroInstallConfig, assets *componentInstallAssets) error {
	if assets == nil {
		return nil
	}

	ctx.Info("Предзагрузка дистрибутива перед перезагрузкой...")
	if strings.HasPrefix(assets.downloadURL, "http") {
		if _, err := am.DownloadHTTPWithProgress(assets.downloadURL, assets.installerPath); err != nil {
			return fmt.Errorf("не удалось предзагрузить дистрибутив: %w", err)
		}
	} else {
		ftpCfg := am.Cfg().FTP[0]
		if _, err := am.DownloadFTPWithProgress(ftpCfg, assets.downloadURL, assets.installerPath); err != nil {
			return fmt.Errorf("не удалось предзагрузить дистрибутив: %w", err)
		}
	}
	cfg.preparedInstallerPath = assets.installerPath

	if cfg.Patch != nil && assets.patchPath != "" {
		ctx.Info(fmt.Sprintf("Предзагрузка патча %s перед перезагрузкой...", cfg.Patch.ShortName))
		if _, err := am.DownloadHTTPWithProgress(cfg.Patch.FullURL, assets.patchPath); err != nil {
			return fmt.Errorf("не удалось предзагрузить патч %s: %w", cfg.Patch.ShortName, err)
		}
		cfg.preparedPatchPath = assets.patchPath
	}

	return nil
}

func requiresPendingRebootResume(cfg *DistroInstallConfig) bool {
	return cfg != nil &&
		cfg.Action == ActionInstallComponent &&
		strings.EqualFold(cfg.Brand, "iiko") &&
		cfg.Component.ID == "iiko_front"
}

func detectPendingWindowsUpdateReasons() []string {
	return filterPendingWindowsUpdateReasons(detectPendingRebootReasons())
}

func detectPendingRebootReasons() []string {
	var reasons []string

	if registryKeyExists(registry.LOCAL_MACHINE, `SOFTWARE\Microsoft\Windows\CurrentVersion\Component Based Servicing\RebootPending`) {
		reasons = append(reasons, pendingRebootReasonComponentBasedServicing)
	}
	if registryKeyExists(registry.LOCAL_MACHINE, `SOFTWARE\Microsoft\Windows\CurrentVersion\WindowsUpdate\Auto Update\RebootRequired`) {
		reasons = append(reasons, pendingRebootReasonWindowsUpdateRebootRequired)
	}
	if registryValueExists(registry.LOCAL_MACHINE, `SYSTEM\CurrentControlSet\Control\Session Manager`, "PendingFileRenameOperations") {
		reasons = append(reasons, pendingRebootReasonSessionManagerPendingFileRenameOps)
	}
	if registryValueExists(registry.LOCAL_MACHINE, `SYSTEM\CurrentControlSet\Control\Session Manager`, "PendingFileRenameOperations2") {
		reasons = append(reasons, pendingRebootReasonSessionManagerPendingFileRenameOps2)
	}

	return reasons
}

func filterPendingWindowsUpdateReasons(reasons []string) []string {
	filtered := make([]string, 0, len(reasons))
	for _, reason := range reasons {
		if reason == pendingRebootReasonWindowsUpdateRebootRequired {
			filtered = append(filtered, reason)
		}
	}
	return filtered
}

func registryKeyExists(root registry.Key, path string) bool {
	key, err := registry.OpenKey(root, path, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	_ = key.Close()
	return true
}

func registryValueExists(root registry.Key, path, valueName string) bool {
	key, err := registry.OpenKey(root, path, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	defer key.Close()

	if values, _, err := key.GetStringsValue(valueName); err == nil {
		return len(values) > 0
	}
	if value, _, err := key.GetStringValue(valueName); err == nil {
		return strings.TrimSpace(value) != ""
	}
	if value, _, err := key.GetIntegerValue(valueName); err == nil {
		return value != 0
	}

	return false
}

func installerLogIndicatesPendingReboot(logPath, output string) bool {
	if installerTextIndicatesPendingReboot(output) {
		return true
	}

	data, err := os.ReadFile(logPath)
	if err != nil {
		return false
	}
	return installerTextIndicatesPendingReboot(string(data))
}

func installerTextIndicatesPendingReboot(text string) bool {
	normalized := strings.ToLower(text)
	return strings.Contains(normalized, "bundle condition evaluated to false: not rebootpending or wixbundleinstalled") ||
		(strings.Contains(normalized, "rebootpending = 1") &&
			strings.Contains(normalized, "not rebootpending or wixbundleinstalled"))
}
