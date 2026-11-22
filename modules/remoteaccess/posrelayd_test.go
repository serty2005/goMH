package remoteaccess

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"goMH/config"
	"goMH/core"
)

func TestInstallPOSRelayd(t *testing.T) {
	testErr := errors.New("сбой тестового шага")
	for _, tc := range []struct {
		name    string
		oldExe  bool
		failAt  string
		wantOps []string
	}{
		{
			name:   "переустановка",
			oldExe: true,
			wantOps: []string{"download", "remove service and processes", "clean", "exclusion", "unpack",
				"install", "start", "triggers", "status"},
		},
		{
			name: "без старого exe всё равно удаляем службу и завершаем процессы",
			wantOps: []string{"download", "remove service and processes", "clean", "exclusion", "unpack",
				"install", "start", "triggers", "status"},
		},
		{
			name:    "ошибка загрузки не останавливает агент",
			failAt:  "download",
			wantOps: []string{"download"},
		},
		{
			name:    "если службу не удалось удалить, не трогаем файлы",
			oldExe:  true,
			failAt:  "remove service and processes",
			wantOps: []string{"download", "remove service and processes"},
		},
		{
			name:    "если файлы остались заблокированы, не распаковываем архив",
			oldExe:  true,
			failAt:  "clean",
			wantOps: []string{"download", "remove service and processes", "clean"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			installDir := filepath.Join(root, "POSRelayd")
			var gotOps []string
			step := func(op string) error {
				gotOps = append(gotOps, op)
				if op == tc.failAt {
					return testErr
				}
				return nil
			}
			assets := &posRelaydTestAssets{t: t, cfg: config.Config{RootPath: root}, step: step}
			wu := &posRelaydTestWinUtils{t: t, installDir: installDir, step: step}
			if tc.oldExe {
				wu.oldExePath = filepath.Join(installDir, "posrelaydsc.exe")
			}
			err := (&Module{}).ExecuteTask(core.NewSilentTaskContext(context.Background()), core.ModuleServices{
				AssetManager: assets,
				WinUtils:     wu,
			}, &RemoteAccessConfig{Tool: ToolPOSRelayd})
			if tc.failAt == "" && err != nil || tc.failAt != "" && !errors.Is(err, testErr) {
				t.Fatalf("неожиданная ошибка на шаге %q: %v", tc.failAt, err)
			}
			if !slices.Equal(gotOps, tc.wantOps) {
				t.Errorf("шаги установки: %v, ожидались %v", gotOps, tc.wantOps)
			}
		})
	}
}

type posRelaydTestAssets struct {
	core.AssetManager
	t    *testing.T
	cfg  config.Config
	step func(string) error
}

func (a *posRelaydTestAssets) Cfg() *config.Config { return &a.cfg }

func (a *posRelaydTestAssets) DownloadToCache(name string) (string, error) {
	if name != "POSRelayd_Agent" {
		a.t.Fatalf("неверный ресурс: %s", name)
	}
	return filepath.Join(a.cfg.RootPath, "cache", "POSRelayd.zip"), a.step("download")
}

func (a *posRelaydTestAssets) UnpackToFlatDir(name, archive, dest string) error {
	if name != "POSRelayd_Agent" || archive != filepath.Join(a.cfg.RootPath, "cache", "POSRelayd.zip") ||
		dest != filepath.Join(a.cfg.RootPath, "POSRelayd") {
		a.t.Fatalf("неверные аргументы распаковки: %q %q %q", name, archive, dest)
	}
	if err := a.step("unpack"); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dest, "posrelaydsc.exe"), nil, 0600)
}

type posRelaydTestWinUtils struct {
	core.WinUtils
	t          *testing.T
	installDir string
	oldExePath string
	step       func(string) error
}

func (w *posRelaydTestWinUtils) RemoveServiceWithProcesses(_ context.Context, serviceName string, processNames ...string) error {
	wantProcesses := []string{"prrd.exe", "prcbh.exe", "mhupdater.exe", "posrelaydsc.exe"}
	if serviceName != "MH_POSRelayd" || !slices.Equal(processNames, wantProcesses) {
		w.t.Fatalf("неверная служба или процессы: %q %v", serviceName, processNames)
	}
	return w.step("remove service and processes")
}

func (w *posRelaydTestWinUtils) FindFileRecursive(_, pattern string) (string, error) {
	if pattern == "posrelayd*.exe" && w.oldExePath != "" {
		return w.oldExePath, nil
	}
	return "", os.ErrNotExist
}

func (w *posRelaydTestWinUtils) GetStartupFolders() (string, string, error) {
	return "", "", os.ErrNotExist
}

func (w *posRelaydTestWinUtils) GetDesktopDir() (string, error) {
	return "", os.ErrNotExist
}

func (w *posRelaydTestWinUtils) FindScheduledTaskByPath(string) (string, error) { return "", nil }

func (w *posRelaydTestWinUtils) CleanDirectory(path string) error {
	if path != w.installDir {
		w.t.Fatalf("неверный каталог очистки: %s", path)
	}
	return w.step("clean")
}

func (w *posRelaydTestWinUtils) AddDefenderExclusion(string) error { return w.step("exclusion") }

func (w *posRelaydTestWinUtils) RunCommand(name string, args ...string) (string, error) {
	switch {
	case name == "schtasks":
		return "", nil
	case name == filepath.Join(w.installDir, "posrelaydsc.exe") && strings.Join(args, " ") == "--startup auto install":
		return "", w.step("install")
	case name == filepath.Join(w.installDir, "posrelaydsc.exe") && slices.Equal(args, []string{"start"}):
		return "", w.step("start")
	default:
		w.t.Fatalf("неожиданная команда: %q %v", name, args)
		return "", nil
	}
}

func (w *posRelaydTestWinUtils) SetServiceTriggers(name string, triggers []string) error {
	if name != "MH_POSRelayd" || !slices.Equal(triggers, []string{"start/machinepolicy", "start/userpolicy"}) {
		w.t.Fatalf("неверная служба или триггеры: %q %v", name, triggers)
	}
	return w.step("triggers")
}

func (w *posRelaydTestWinUtils) GetServiceStatus(string) (string, error) {
	return "RUNNING", w.step("status")
}
