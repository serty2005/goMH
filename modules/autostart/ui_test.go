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
	if !mdl.confirming {
		t.Fatalf("model should show confirmation before save")
	}
	updated, _ = mdl.Update(tea.KeyMsg{Type: tea.KeyEnter})
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

func TestModelMouseWheelScrollsAndHoverSelectsRowForSpaceToggle(t *testing.T) {
	entries := make([]core.AutostartEntry, 0, 10)
	for i := range 10 {
		entries = append(entries, core.AutostartEntry{
			ID:        string(rune('a' + i)),
			Name:      string(rune('A' + i)),
			Source:    core.AutostartSourceRegistryRun,
			Scope:     core.AutostartScopeUser,
			Enabled:   true,
			CanToggle: true,
		})
	}
	mdl := newModel(entries)
	mdl.height = 12

	updated, _ := mdl.Update(tea.MouseMsg{Button: tea.MouseButtonWheelDown})
	mdl = updated.(model)
	if mdl.scroll != 1 {
		t.Fatalf("scroll = %d, want 1", mdl.scroll)
	}

	updated, _ = mdl.Update(tea.MouseMsg{Action: tea.MouseActionMotion, Button: tea.MouseButtonNone, Y: mdl.listTop() + 2})
	mdl = updated.(model)
	if mdl.cursor != 3 {
		t.Fatalf("cursor = %d, want 3", mdl.cursor)
	}

	updated, _ = mdl.Update(tea.KeyMsg{Type: tea.KeySpace})
	mdl = updated.(model)
	if mdl.entries[3].Enabled {
		t.Fatalf("hovered entry should be disabled after space")
	}
}

func TestModelMouseClickStatusTogglesAndRowClickOpensEditor(t *testing.T) {
	mdl := newModel([]core.AutostartEntry{
		{
			ID:            "registry|user|Run|App",
			Name:          "App",
			Source:        core.AutostartSourceRegistryRun,
			Scope:         core.AutostartScopeUser,
			Enabled:       true,
			TargetPath:    `C:\Tools\app.exe`,
			Arguments:     "--silent",
			RegistryKey:   core.AutostartRegistryKeyRun,
			RegistryValue: "App",
			CanToggle:     true,
		},
	})
	mdl.width = 100
	mdl.height = 20

	updated, _ := mdl.Update(tea.MouseMsg{Action: tea.MouseActionPress, Button: tea.MouseButtonLeft, X: 52, Y: mdl.listTop()})
	mdl = updated.(model)
	if mdl.entries[0].Enabled {
		t.Fatalf("status click should disable entry")
	}
	if mdl.editing {
		t.Fatalf("status click should not open editor")
	}

	updated, _ = mdl.Update(tea.MouseMsg{Action: tea.MouseActionPress, Button: tea.MouseButtonLeft, X: 2, Y: mdl.listTop()})
	mdl = updated.(model)
	if !mdl.editing {
		t.Fatalf("row click should open editor")
	}
	if got := mdl.editNameInput.Value(); got != "App" {
		t.Fatalf("edit name input = %q", got)
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

func TestModelFiltersSpaceTogglesAndEnterOpensEditor(t *testing.T) {
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
	updated, _ := mdl.Update(tea.KeyMsg{Type: tea.KeySpace})
	mdl = updated.(model)
	if mdl.entries[0].Enabled {
		t.Fatalf("space should toggle current entry")
	}

	updated, _ = mdl.Update(tea.KeyMsg{Type: tea.KeyEnter})
	mdl = updated.(model)
	if !mdl.editing || mdl.editNameInput.Value() != "App" {
		t.Fatalf("enter should open editor for current row")
	}
}

func TestModelSearchFiltersByNameCommandAndReset(t *testing.T) {
	mdl := newModel([]core.AutostartEntry{
		{
			ID:      "registry|user|Run|App",
			Name:    "Front App",
			Command: `C:\Front\front.exe`,
		},
		{
			ID:      "registry|user|Run|Agent",
			Name:    "Agent",
			Command: `C:\Tools\agent.exe --silent`,
		},
	})

	updated, _ := mdl.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	mdl = updated.(model)
	updated, _ = mdl.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("agent")})
	mdl = updated.(model)
	updated, _ = mdl.Update(tea.KeyMsg{Type: tea.KeyEnter})
	mdl = updated.(model)

	entries := mdl.filteredEntries()
	if len(entries) != 1 {
		t.Fatalf("len(filtered entries) = %d, want 1", len(entries))
	}
	if entries[0].Name != "Agent" {
		t.Fatalf("filtered entry = %q, want Agent", entries[0].Name)
	}

	updated, _ = mdl.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'0'}})
	mdl = updated.(model)
	if mdl.searchQuery != "" {
		t.Fatalf("search query = %q, want empty", mdl.searchQuery)
	}
	if got := len(mdl.filteredEntries()); got != 2 {
		t.Fatalf("len(filtered entries after reset) = %d, want 2", got)
	}
}

func TestModelPastesFromClipboardInAddMode(t *testing.T) {
	mdl := newModel(nil)
	mdl.adding = addModePath
	mdl.pathInput.Focus()
	mdl.clipboardRead = func() (string, error) {
		return `"C:\Tools\agent.exe"`, nil
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

func TestModelFinishAddStripsSurroundingPathQuotes(t *testing.T) {
	mdl := newModel(nil)
	mdl.adding = addModePath
	mdl.pathInput.SetValue(` "C:\Program Files\Agent\agent.exe" `)
	mdl.argsInput.SetValue("--silent")

	mdl.finishAdd()

	if len(mdl.creates) != 1 {
		t.Fatalf("len(creates) = %d, want 1", len(mdl.creates))
	}
	if got := mdl.creates[0].Path; got != `C:\Program Files\Agent\agent.exe` {
		t.Fatalf("create path = %q", got)
	}
	if got := mdl.entries[0].Command; got != `C:\Program Files\Agent\agent.exe --silent` {
		t.Fatalf("entry command = %q", got)
	}
}

func TestModelSaveConfirmationAppliesOnlyCheckedChanges(t *testing.T) {
	mdl := newModel([]core.AutostartEntry{
		{ID: "one", Name: "One", Enabled: true, CanToggle: true},
		{ID: "two", Name: "Two", Enabled: true, CanToggle: true},
	})
	mdl.entries[0].Enabled = false
	mdl.entries[1].Enabled = false

	updated, _ := mdl.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	mdl = updated.(model)
	if !mdl.confirming {
		t.Fatalf("model should be confirming")
	}
	if len(mdl.confirmItems) != 2 {
		t.Fatalf("len(confirmItems) = %d, want 2", len(mdl.confirmItems))
	}

	updated, _ = mdl.Update(tea.KeyMsg{Type: tea.KeySpace})
	mdl = updated.(model)
	if mdl.confirmItems[0].Selected {
		t.Fatalf("first confirmation item should be unchecked")
	}

	updated, _ = mdl.Update(tea.KeyMsg{Type: tea.KeyEnter})
	mdl = updated.(model)
	if !mdl.saved {
		t.Fatalf("model should save selected items")
	}
	if len(mdl.result.Changes) != 1 {
		t.Fatalf("len(saved changes) = %d, want 1", len(mdl.result.Changes))
	}
	if mdl.result.Changes[0].Entry.ID != "two" {
		t.Fatalf("saved change id = %q, want two", mdl.result.Changes[0].Entry.ID)
	}
}

func TestModelSaveConfirmationWithNoCheckedItemsDoesNotApply(t *testing.T) {
	mdl := newModel([]core.AutostartEntry{
		{ID: "one", Name: "One", Enabled: true, CanToggle: true},
	})
	mdl.entries[0].Enabled = false

	updated, _ := mdl.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	mdl = updated.(model)
	updated, _ = mdl.Update(tea.KeyMsg{Type: tea.KeySpace})
	mdl = updated.(model)
	updated, _ = mdl.Update(tea.KeyMsg{Type: tea.KeyEnter})
	mdl = updated.(model)

	if mdl.saved {
		t.Fatalf("model should not save when all confirmation items are unchecked")
	}
	if !mdl.confirming {
		t.Fatalf("model should stay on confirmation screen")
	}
}

func TestModelEditRegistryEntryStagesSupportedFields(t *testing.T) {
	mdl := newModel([]core.AutostartEntry{
		{
			ID:            "registry|user|Run|App",
			Name:          "App",
			Source:        core.AutostartSourceRegistryRun,
			Scope:         core.AutostartScopeUser,
			Enabled:       true,
			TargetPath:    `C:\Tools\app.exe`,
			Arguments:     "--silent",
			RegistryKey:   core.AutostartRegistryKeyRun,
			RegistryValue: "App",
			CanToggle:     true,
		},
	})

	updated, _ := mdl.Update(tea.KeyMsg{Type: tea.KeyEnter})
	mdl = updated.(model)
	if !mdl.editing {
		t.Fatalf("enter should open editor")
	}
	if !strings.Contains(mdl.View(), "Run") {
		t.Fatalf("editor header should include launch mode")
	}

	mdl.editNameInput.SetValue("Agent")
	mdl.editPathInput.SetValue(`C:\Program Files\Agent\agent.exe`)
	mdl.editArgsInput.SetValue("--minimized")
	updated, _ = mdl.Update(tea.KeyMsg{Type: tea.KeyCtrlS})
	mdl = updated.(model)

	if mdl.editing {
		t.Fatalf("editor should close after staging supported edit")
	}
	if len(mdl.edits) != 1 {
		t.Fatalf("len(edits) = %d, want 1", len(mdl.edits))
	}
	edit := mdl.edits[0]
	if edit.Name != "Agent" || edit.Path != `C:\Program Files\Agent\agent.exe` || edit.Arguments != "--minimized" {
		t.Fatalf("edit = %#v", edit)
	}
}

func TestModelRightClickCopiesEditorFieldAndMarksCopied(t *testing.T) {
	mdl := newModel([]core.AutostartEntry{
		{
			ID:         "registry|user|Run|App",
			Name:       "App",
			Source:     core.AutostartSourceRegistryRun,
			Scope:      core.AutostartScopeUser,
			TargetPath: `C:\Tools\app.exe`,
		},
	})
	mdl.width = 120
	mdl.height = 30
	var copied string
	mdl.clipboardWrite = func(text string) error {
		copied = text
		return nil
	}

	updated, _ := mdl.Update(tea.KeyMsg{Type: tea.KeyEnter})
	mdl = updated.(model)
	updated, _ = mdl.Update(tea.MouseMsg{Action: tea.MouseActionPress, Button: tea.MouseButtonRight, X: 20, Y: mdl.editFieldY(editFieldPath)})
	mdl = updated.(model)

	if copied != `C:\Tools\app.exe` {
		t.Fatalf("copied = %q", copied)
	}
	if mdl.copiedEditField != editFieldPath {
		t.Fatalf("copied field = %v, want path", mdl.copiedEditField)
	}
	if !mdl.copiedBlinkOn {
		t.Fatalf("copied field should start blink feedback")
	}
}

func TestModelCtrlCCopiesFocusedEditorFieldInsteadOfClosing(t *testing.T) {
	mdl := newModel([]core.AutostartEntry{
		{
			ID:         "registry|user|Run|App",
			Name:       "App",
			Source:     core.AutostartSourceRegistryRun,
			Scope:      core.AutostartScopeUser,
			TargetPath: `C:\Tools\app.exe`,
		},
	})
	var copied string
	mdl.clipboardWrite = func(text string) error {
		copied = text
		return nil
	}

	updated, _ := mdl.Update(tea.KeyMsg{Type: tea.KeyEnter})
	mdl = updated.(model)
	mdl.focusEditField(editFieldPath)
	updated, _ = mdl.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	mdl = updated.(model)

	if !mdl.editing {
		t.Fatalf("ctrl+c should keep editor open")
	}
	if copied != `C:\Tools\app.exe` {
		t.Fatalf("copied = %q", copied)
	}
}

func TestModelEditedFieldsUseChangedBorderColor(t *testing.T) {
	mdl := newModel([]core.AutostartEntry{
		{
			ID:         "registry|user|Run|App",
			Name:       "App",
			Source:     core.AutostartSourceRegistryRun,
			Scope:      core.AutostartScopeUser,
			TargetPath: `C:\Tools\app.exe`,
			Arguments:  "--silent",
		},
	})

	updated, _ := mdl.Update(tea.KeyMsg{Type: tea.KeyEnter})
	mdl = updated.(model)
	unchanged := mdl.editFieldBorderColor(editFieldPath)
	mdl.editPathInput.SetValue(`C:\Tools\agent.exe`)
	changed := mdl.editFieldBorderColor(editFieldPath)

	if changed == unchanged {
		t.Fatalf("changed field should use a different border color")
	}
	if changed != colorEdited {
		t.Fatalf("changed field color = %q, want edited color", changed)
	}
}

func TestModelCopiedFieldBlinkMessageTogglesAndClearsFeedback(t *testing.T) {
	mdl := newModel([]core.AutostartEntry{
		{ID: "registry|user|Run|App", Name: "App", Source: core.AutostartSourceRegistryRun, TargetPath: `C:\Tools\app.exe`},
	})
	mdl.clipboardWrite = func(text string) error { return nil }

	updated, _ := mdl.Update(tea.KeyMsg{Type: tea.KeyEnter})
	mdl = updated.(model)
	updated, _ = mdl.copyEditField(editFieldPath)
	mdl = updated.(model)
	updated, _ = mdl.Update(copiedEditFieldBlinkMsg{field: editFieldPath, remaining: 1})
	mdl = updated.(model)
	if mdl.copiedEditField != editFieldPath || mdl.copiedBlinkOn {
		t.Fatalf("first blink should keep copied field and turn highlight off")
	}
	updated, _ = mdl.Update(copiedEditFieldBlinkMsg{field: editFieldPath, remaining: 0})
	mdl = updated.(model)
	if mdl.copiedEditField != editFieldNone {
		t.Fatalf("final blink should clear copied field feedback")
	}
}
