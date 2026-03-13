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

type distroResumeConfig struct {
	Config DistroInstallConfig `json:"config"`
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

	if err := m.Execute(ctx, am, wu, &resumeCfg.Config); err != nil {
		if isRebootResumeScheduled(err) {
			return nil
		}
		return err
	}

	ctx.Info("Очистка временных файлов возобновления...")
	_ = os.Remove(configPath)
	_ = wu.DeleteScheduledTaskByName(resumeTaskName)

	return nil
}

func (m *Module) scheduleResumeAfterReboot(ctx core.TaskContext, am core.AssetManager, wu core.WinUtils, cfg *DistroInstallConfig) error {
	ctx.Info("Подготовка к автоматическому возобновлению установки после перезагрузки...")

	tempDir := filepath.Join(am.Cfg().RootPath, "temp")
	if err := os.MkdirAll(tempDir, 0o755); err != nil {
		return fmt.Errorf("не удалось создать директорию для возобновления: %w", err)
	}

	resumeConfigPath := filepath.Join(tempDir, "distro_resume.json")
	resumeCfg := distroResumeConfig{Config: *cfg}
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

func requiresPendingRebootResume(cfg *DistroInstallConfig) bool {
	return cfg != nil &&
		cfg.Action == ActionInstallComponent &&
		strings.EqualFold(cfg.Brand, "iiko") &&
		cfg.Component.ID == "iiko_front"
}

func detectPendingRebootReasons() []string {
	var reasons []string

	if registryKeyExists(registry.LOCAL_MACHINE, `SOFTWARE\Microsoft\Windows\CurrentVersion\Component Based Servicing\RebootPending`) {
		reasons = append(reasons, "Component Based Servicing: RebootPending")
	}
	if registryKeyExists(registry.LOCAL_MACHINE, `SOFTWARE\Microsoft\Windows\CurrentVersion\WindowsUpdate\Auto Update\RebootRequired`) {
		reasons = append(reasons, "Windows Update: RebootRequired")
	}
	if registryValueExists(registry.LOCAL_MACHINE, `SYSTEM\CurrentControlSet\Control\Session Manager`, "PendingFileRenameOperations") {
		reasons = append(reasons, "Session Manager: PendingFileRenameOperations")
	}
	if registryValueExists(registry.LOCAL_MACHINE, `SYSTEM\CurrentControlSet\Control\Session Manager`, "PendingFileRenameOperations2") {
		reasons = append(reasons, "Session Manager: PendingFileRenameOperations2")
	}

	return reasons
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
