package autostart

import (
	"strings"
	"testing"

	"goMH/core"

	tea "github.com/charmbracelet/bubbletea"
)

func TestModelTogglesEntryAndSavesChanges(t *testing.T) {
	mdl := newModel([]core.AutostartEntry{
		{
			ID:        "registry|user|Run|App",
			Name:      "App",
			Source:    core.AutostartSourceRegistryRun,
			Scope:     core.AutostartScopeUser,
			Enabled:   true,
			CanToggle: true,
		},
	})

	updated, _ := mdl.Update(tea.KeyMsg{Type: tea.KeySpace})
	mdl = updated.(model)
	if mdl.entries[0].Enabled {
		t.Fatalf("entry should be disabled after space")
	}
	if !mdl.dirty() {
		t.Fatalf("model should be dirty after toggling entry")
	}

	updated, _ = mdl.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	mdl = updated.(model)
	if !mdl.saved {
		t.Fatalf("model should be saved")
	}
	if len(mdl.result.Changes) != 1 {
		t.Fatalf("len(changes) = %d, want 1", len(mdl.result.Changes))
	}
	if mdl.result.Changes[0].Enabled {
		t.Fatalf("saved change should disable entry")
	}
}

func TestModelAddRunOnceEntry(t *testing.T) {
	mdl := newModel(nil)
	mdl.adding = addModeKind
	mdl.addKind = core.AutostartRegistryKeyRunOnce
	mdl.pathInput.SetValue(`C:\Tools\agent.lnk`)
	mdl.argsInput.SetValue("--once")

	mdl.finishAdd()

	if len(mdl.creates) != 1 {
		t.Fatalf("len(creates) = %d, want 1", len(mdl.creates))
	}
	create := mdl.creates[0]
	if create.RegistryKey != core.AutostartRegistryKeyRunOnce {
		t.Fatalf("registry key = %q, want RunOnce", create.RegistryKey)
	}
	if create.Path != `C:\Tools\agent.lnk` {
		t.Fatalf("path = %q", create.Path)
	}
	if create.Arguments != "--once" {
		t.Fatalf("arguments = %q", create.Arguments)
	}
}

func TestModelShowsScanProgress(t *testing.T) {
	mdl := newScanningModel(nil)

	updated, _ := mdl.Update(scanProgressMsg{
		Area:  "Реестр HKCU Run",
		Found: 3,
	})
	mdl = updated.(model)

	if !mdl.scanning {
		t.Fatalf("model should remain in scanning state")
	}
	if mdl.scanArea != "Реестр HKCU Run" {
		t.Fatalf("scan area = %q", mdl.scanArea)
	}
	if mdl.scanFound != 3 {
		t.Fatalf("scan found = %d, want 3", mdl.scanFound)
	}
	if view := mdl.View(); !strings.Contains(view, "Реестр HKCU Run") || !strings.Contains(view, "3") {
		t.Fatalf("scan view should contain area and found count, got %q", view)
	}
}

func TestModelFiltersAndShowsCellDetails(t *testing.T) {
	mdl := newModel([]core.AutostartEntry{
		{
			ID:        "registry|user|Run|App",
			Name:      "App",
			Source:    core.AutostartSourceRegistryRun,
			Scope:     core.AutostartScopeUser,
			Enabled:   true,
			Command:   `C:\App\app.exe --hidden`,
			CanToggle: true,
		},
		{
			ID:        "task|Agent",
			Name:      "Agent",
			Source:    core.AutostartSourceScheduledTask,
			Scope:     core.AutostartScopeMachine,
			Enabled:   false,
			Command:   `C:\Agent\agent.exe`,
			CanToggle: true,
		},
	})

	mdl.sourceFilter = core.AutostartSourceRegistryRun
	if got := len(mdl.filteredEntries()); got != 1 {
		t.Fatalf("registry filter count = %d, want 1", got)
	}
	mdl.sourceFilter = ""
	mdl.statusFilter = statusFilterDisabled
	if got := len(mdl.filteredEntries()); got != 1 {
		t.Fatalf("disabled filter count = %d, want 1", got)
	}

	mdl.statusFilter = statusFilterAll
	updated, _ := mdl.Update(tea.KeyMsg{Type: tea.KeyRight})
	mdl = updated.(model)
	if mdl.selectedColumn != columnCommand {
		t.Fatalf("selected column = %v, want command", mdl.selectedColumn)
	}
	updated, _ = mdl.Update(tea.KeyMsg{Type: tea.KeySpace})
	mdl = updated.(model)
	if !mdl.detail.visible || !strings.Contains(mdl.detail.body, "--hidden") {
		t.Fatalf("command detail not opened: %#v", mdl.detail)
	}

	updated, _ = mdl.Update(tea.KeyMsg{Type: tea.KeyEsc})
	mdl = updated.(model)
	updated, _ = mdl.Update(tea.KeyMsg{Type: tea.KeyLeft})
	mdl = updated.(model)
	updated, _ = mdl.Update(tea.KeyMsg{Type: tea.KeySpace})
	mdl = updated.(model)
	if !mdl.detail.visible || !strings.Contains(mdl.detail.body, "App") {
		t.Fatalf("name detail not opened: %#v", mdl.detail)
	}
}

func TestModelPastesFromClipboardInAddMode(t *testing.T) {
	mdl := newModel(nil)
	mdl.adding = addModePath
	mdl.pathInput.Focus()
	mdl.clipboardRead = func() (string, error) {
		return `C:\Tools\agent.exe`, nil
	}

	updated, _ := mdl.Update(tea.KeyMsg{Type: tea.KeyCtrlV})
	mdl = updated.(model)
	if got := mdl.pathInput.Value(); got != `C:\Tools\agent.exe` {
		t.Fatalf("path input = %q", got)
	}

	mdl.pathInput.SetValue("")
	updated, _ = mdl.Update(tea.MouseMsg{Action: tea.MouseActionPress, Button: tea.MouseButtonRight})
	mdl = updated.(model)
	if got := mdl.pathInput.Value(); got != `C:\Tools\agent.exe` {
		t.Fatalf("right-click paste path input = %q", got)
	}
}
