package serviceutils

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"goMH/assetmgr"
	"goMH/config"
	"goMH/core"
	"goMH/modules/autostart"
)

func TestBuildTaskRunsOrderCheckAndFrontToolsImmediately(t *testing.T) {
	module := &Module{}

	testCases := []struct {
		name string
		cfg  *ServiceUtilsConfig
	}{
		{
			name: "ordercheck",
			cfg: &ServiceUtilsConfig{
				Action:             ActionOrderCheck,
				TargetDatabasePath: `C:\iiko\entities.db`,
			},
		},
		{
			name: "fronttools",
			cfg: &ServiceUtilsConfig{
				Action:       ActionFrontTools,
				DatabaseType: "db",
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			plan, err := module.BuildTask(tc.cfg)
			if err != nil {
				t.Fatalf("BuildTask returned error: %v", err)
			}
			if plan.Mode != core.ModuleRunModeImmediate {
				t.Fatalf("expected immediate mode, got %v", plan.Mode)
			}
			if !plan.SkipConfirmation {
				t.Fatal("expected immediate launch without confirmation")
			}
			if plan.Result.Note == "" {
				t.Fatal("expected non-empty immediate result note")
			}
		})
	}
}

func TestBuildTaskKeepsQueuedModeForCollectLogs(t *testing.T) {
	module := &Module{}
	cfg := &ServiceUtilsConfig{
		Action:  ActionCollectLogs,
		LogDays: 3,
		LogDirs: []string{`C:\logs`},
	}

	plan, err := module.BuildTask(cfg)
	if err != nil {
		t.Fatalf("BuildTask returned error: %v", err)
	}
	if plan.Mode != core.ModuleRunModeQueue {
		t.Fatalf("expected queued mode, got %v", plan.Mode)
	}
	if plan.SkipConfirmation {
		t.Fatal("did not expect skip confirmation for queued task")
	}
}

func TestServiceUtilsMenuShowsNetworkDiagLast(t *testing.T) {
	items := serviceUtilsMenuItems()
	if len(items) == 0 {
		t.Fatal("expected service utils menu items")
	}
	last := items[len(items)-1]
	if last.Title != "Диагностика сети" {
		t.Fatalf("last service utils item = %q, want %q", last.Title, "Диагностика сети")
	}
}

func TestBuildTaskRunsAutostartImmediately(t *testing.T) {
	module := &Module{}
	cfg := &ServiceUtilsConfig{
		Action:          ActionAutostart,
		AutostartConfig: &autostart.Config{},
	}

	plan, err := module.BuildTask(cfg)
	if err != nil {
		t.Fatalf("BuildTask returned error: %v", err)
	}
	if plan.Mode != core.ModuleRunModeImmediate {
		t.Fatalf("expected immediate mode, got %v", plan.Mode)
	}
	if !plan.SkipConfirmation {
		t.Fatal("expected autostart launch without extra serviceutils confirmation")
	}
}

func TestCleanupExpiredLogFilesRemovesOnlySupportedExtensionsOlderThanCutoff(t *testing.T) {
	module := &Module{}
	root := t.TempDir()
	logDir := filepath.Join(root, "logs")
	nestedDir := filepath.Join(logDir, "nested")
	if err := os.MkdirAll(nestedDir, 0755); err != nil {
		t.Fatalf("mkdir log dirs: %v", err)
	}

	oldTime := time.Now().AddDate(0, 0, -(cleanLogRetentionDays + 1))
	newTime := time.Now().AddDate(0, 0, -7)

	oldLog := writeTestFile(t, filepath.Join(logDir, "old.log"), "old log", oldTime)
	oldZip := writeTestFile(t, filepath.Join(nestedDir, "old.zip"), "old zip", oldTime)
	oldGz := writeTestFile(t, filepath.Join(logDir, "old.gz"), "old gz", oldTime)
	oldTxt := writeTestFile(t, filepath.Join(logDir, "old.txt"), "old txt", oldTime)
	newLog := writeTestFile(t, filepath.Join(logDir, "new.log"), "new log", newTime)
	expectedFreed := fileSize(t, oldLog) + fileSize(t, oldZip) + fileSize(t, oldGz)

	freedBytes, removedCount := module.cleanupExpiredLogFiles(core.NewSilentTaskContext(nil), []string{logDir}, time.Now().AddDate(0, 0, -cleanLogRetentionDays))

	if removedCount != 3 {
		t.Fatalf("expected 3 removed files, got %d", removedCount)
	}
	if freedBytes != expectedFreed {
		t.Fatalf("expected %d freed bytes, got %d", expectedFreed, freedBytes)
	}

	assertFileMissing(t, oldLog)
	assertFileMissing(t, oldZip)
	assertFileMissing(t, oldGz)
	assertFileExists(t, oldTxt)
	assertFileExists(t, newLog)
}

func TestCleanTempFilesAlsoCleansExpiredLogsFromConfiguredLogCollectorPaths(t *testing.T) {
	module := &Module{}
	root := t.TempDir()

	tempDir := filepath.Join(root, "temp-clean")
	if err := os.MkdirAll(tempDir, 0755); err != nil {
		t.Fatalf("mkdir temp dir: %v", err)
	}
	tempFile := writeTestFile(t, filepath.Join(tempDir, "temp.tmp"), "temp", time.Now())

	profilesRoot := filepath.Join(root, "profiles")
	logDir := filepath.Join(profilesRoot, "user1", "logs")
	if err := os.MkdirAll(logDir, 0755); err != nil {
		t.Fatalf("mkdir wildcard log dir: %v", err)
	}
	oldLog := writeTestFile(t, filepath.Join(logDir, "expired.log"), "expired", time.Now().AddDate(0, 0, -(cleanLogRetentionDays+2)))
	newLog := writeTestFile(t, filepath.Join(logDir, "fresh.log"), "fresh", time.Now().AddDate(0, 0, -2))
	oldTxt := writeTestFile(t, filepath.Join(logDir, "expired.txt"), "keep", time.Now().AddDate(0, 0, -(cleanLogRetentionDays+2)))

	am := newTestAssetManager(t, &config.Config{
		RootPath:        filepath.Join(root, "app-root"),
		AssetsCachePath: filepath.Join(root, "cache"),
		MaintenanceConfig: config.MaintenanceConfig{
			TempPaths:         []string{tempDir},
			LogCollectorPaths: []string{filepath.Join(profilesRoot, "*", "logs")},
		},
	})

	if err := module.cleanTempFiles(core.NewSilentTaskContext(nil), am); err != nil {
		t.Fatalf("cleanTempFiles returned error: %v", err)
	}

	assertFileMissing(t, tempFile)
	assertFileMissing(t, oldLog)
	assertFileExists(t, newLog)
	assertFileExists(t, oldTxt)
}

func newTestAssetManager(t *testing.T, cfg *config.Config) *assetmgr.Manager {
	t.Helper()

	manager, err := assetmgr.New(cfg)
	if err != nil {
		t.Fatalf("create asset manager: %v", err)
	}
	return manager
}

func writeTestFile(t *testing.T, path, contents string, modTime time.Time) string {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatalf("mkdir for %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(contents), 0644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	if err := os.Chtimes(path, modTime, modTime); err != nil {
		t.Fatalf("chtimes %s: %v", path, err)
	}
	return path
}

func fileSize(t *testing.T, path string) int64 {
	t.Helper()

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	return info.Size()
}

func assertFileExists(t *testing.T, path string) {
	t.Helper()

	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected file to exist %s: %v", path, err)
	}
}

func assertFileMissing(t *testing.T, path string) {
	t.Helper()

	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("expected file to be missing %s, stat err=%v", path, err)
	}
}
