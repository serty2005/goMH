package autostart

import (
	"testing"

	"goMH/core"
)

type editWinUtilsFake struct {
	creates []core.AutostartCreateRequest
	deletes []string
}

func (f *editWinUtilsFake) AddRegistryAutostartEntry(req core.AutostartCreateRequest) error {
	f.creates = append(f.creates, req)
	return nil
}

func (f *editWinUtilsFake) DeleteRegistryAutostartValue(scope core.AutostartScope, key core.AutostartRegistryKey, valueName string) error {
	f.deletes = append(f.deletes, valueName)
	return nil
}

func TestApplyAutostartEditUpdatesRegistryValueBeforeDeletingOldName(t *testing.T) {
	fake := &editWinUtilsFake{}

	err := applyAutostartEdit(fake, core.AutostartEdit{
		Original: core.AutostartEntry{
			ID:            "registry|user|Run|App",
			Name:          "App",
			Source:        core.AutostartSourceRegistryRun,
			Scope:         core.AutostartScopeUser,
			RegistryKey:   core.AutostartRegistryKeyRun,
			RegistryValue: "App",
		},
		Name:      "Agent",
		Path:      `C:\Program Files\Agent\agent.exe`,
		Arguments: "--minimized",
	})
	if err != nil {
		t.Fatalf("applyAutostartEdit returned error: %v", err)
	}
	if len(fake.creates) != 1 {
		t.Fatalf("len(creates) = %d, want 1", len(fake.creates))
	}
	if got := fake.creates[0].Name; got != "Agent" {
		t.Fatalf("created name = %q, want Agent", got)
	}
	if got := fake.creates[0].Path; got != `C:\Program Files\Agent\agent.exe` {
		t.Fatalf("created path = %q", got)
	}
	if len(fake.deletes) != 1 || fake.deletes[0] != "App" {
		t.Fatalf("deletes = %#v, want old App value", fake.deletes)
	}
}
