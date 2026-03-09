package serviceutils

import (
	"testing"

	"goMH/core"
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
