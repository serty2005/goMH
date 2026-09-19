package distro

import (
	"context"
	"errors"
	"goMH/core"
	"goMH/modules/frontconfig"
	"testing"
)

type editorWinUtils struct {
	core.WinUtils
	path     string
	original []byte
	updated  []byte
	err      error
}

func (w *editorWinUtils) SaveFileWithBackup(path string, original, updated []byte) (string, error) {
	w.path, w.original, w.updated = path, original, updated
	return "test.bak", w.err
}

func TestFrontConfigImmediateTask(t *testing.T) {
	cfg := &DistroInstallConfig{Action: ActionEditFrontConfig, FrontConfig: &frontconfig.Config{
		Snapshot: core.ConfigFileSnapshot{Path: "test.xml", Data: []byte("original")},
		Updated:  []byte("<config><enabled>true</enabled></config>"),
	}}
	m := &Module{}
	plan, err := m.BuildTask(cfg)
	if err != nil || plan.Mode != core.ModuleRunModeImmediate || !plan.SkipConfirmation {
		t.Fatalf("editor must save immediately after Ctrl+S: %+v, %v", plan, err)
	}
	wu := &editorWinUtils{}
	ctx := core.NewSilentTaskContext(context.Background())
	if err := m.ExecuteTask(ctx, core.ModuleServices{WinUtils: wu}, cfg); err != nil {
		t.Fatal(err)
	}
	if wu.path != "test.xml" || string(wu.original) != "original" || string(wu.updated) != string(cfg.FrontConfig.Updated) {
		t.Fatal("save did not use original snapshot")
	}
	conflict := errors.New("conflict")
	wu.err = conflict
	if err := m.ExecuteTask(ctx, core.ModuleServices{WinUtils: wu}, cfg); !errors.Is(err, conflict) {
		t.Fatal("save failure not propagated")
	}
	if _, err := m.BuildTask(&DistroInstallConfig{Action: ActionEditFrontConfig}); err == nil {
		t.Fatal("missing editor config accepted")
	}
}
