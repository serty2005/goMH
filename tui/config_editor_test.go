package tui

import (
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

func editorFixture() *configEditorModel {
	return newConfigEditorModel([]ConfigEditorField{
		{ID: 3, Name: "AllowHandCardRoll", Value: "false", Boolean: true},
		{ID: 8, Name: "serverUrl", Value: "https://example.test/resto"},
		{ID: 10, Name: "nested/width", Nullable: true, Null: true},
	}, ConfigEditorOptions{Title: "Редактор config.xml", Subtitle: `C:\test\config.xml`})
}

func editorKey(m *configEditorModel, key tea.KeyType) tea.Cmd {
	_, cmd := m.Update(tea.KeyMsg{Type: key})
	return cmd
}

func TestConfigEditorSpaceAndFilteredIdentity(t *testing.T) {
	m := editorFixture()
	editorKey(m, tea.KeySpace)
	if m.fields[0].Value != "true" || len(m.changes()) != 1 {
		t.Fatal("space did not toggle boolean")
	}
	editorKey(m, tea.KeySpace)
	if len(m.changes()) != 0 {
		t.Fatal("reverted boolean is still dirty")
	}
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("SERVER")})
	if len(m.filtered) != 1 || m.current() != 1 {
		t.Fatal("case insensitive search failed")
	}
	editorKey(m, tea.KeyEnter)
	m.input.SetValue("  https://other.test/resto?a=1&b=2  ")
	editorKey(m, tea.KeyEnter)
	if !strings.HasPrefix(m.fields[1].Value, "  ") || m.changes()[0].ID != 8 || m.fields[0].Value != "false" {
		t.Fatal("filtered edit lost whitespace or changed wrong field")
	}
	editorKey(m, tea.KeyEsc)
	if m.search.Value() != "" || m.discard {
		t.Fatal("first Esc must clear search")
	}
	if cmd := editorKey(m, tea.KeyCtrlS); cmd == nil || !m.saved || len(m.changes()) != 1 {
		t.Fatal("save did not include hidden edit")
	}
}

func TestConfigEditorCancelAndDiscard(t *testing.T) {
	m := editorFixture()
	m.move(1)
	editorKey(m, tea.KeyEnter)
	m.input.SetValue("discard me")
	editorKey(m, tea.KeyEsc)
	if m.editing || len(m.changes()) != 0 {
		t.Fatal("cancelled input was applied")
	}
	m.move(-1)
	editorKey(m, tea.KeySpace)
	if cmd := editorKey(m, tea.KeyEsc); cmd != nil || !m.discard || m.discardChoice {
		t.Fatal("unsaved exit must default to keeping edits")
	}
	editorKey(m, tea.KeyEnter)
	if m.discard || len(m.changes()) != 1 {
		t.Fatal("continue editing lost changes")
	}
	editorKey(m, tea.KeyCtrlC)
	editorKey(m, tea.KeyRight)
	if cmd := editorKey(m, tea.KeyEnter); cmd == nil || m.saved {
		t.Fatal("discard must quit without saving")
	}
}

func TestConfigEditorNullResetAndValidation(t *testing.T) {
	m := editorFixture()
	m.move(2)
	editorKey(m, tea.KeyEnter)
	m.input.SetValue("")
	editorKey(m, tea.KeyEnter)
	if m.fields[2].Null || len(m.changes()) != 1 {
		t.Fatal("explicit empty must differ from nil")
	}
	editorKey(m, tea.KeyCtrlR)
	if !m.fields[2].Null || len(m.changes()) != 0 {
		t.Fatal("reset failed")
	}
	editorKey(m, tea.KeyCtrlN)
	if m.fields[2].Null {
		t.Fatal("nil toggle failed")
	}
	m.options.Validate = func([]ConfigEditorField) error { return errors.New("invalid value") }
	if cmd := editorKey(m, tea.KeyCtrlS); cmd != nil || m.saved || m.errText == "" {
		t.Fatal("validation error must keep editor open")
	}
}

func TestConfigEditorSaveWhileEditingAndNoMatches(t *testing.T) {
	m := editorFixture()
	m.move(1)
	editorKey(m, tea.KeyEnter)
	m.input.SetValue("https://new.test/resto")
	if cmd := editorKey(m, tea.KeyCtrlS); cmd == nil || !m.saved || m.fields[1].Value != "https://new.test/resto" {
		t.Fatal("Ctrl+S lost active input")
	}
	m = editorFixture()
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("not-found")})
	editorKey(m, tea.KeyDown)
	editorKey(m, tea.KeyEnter)
	editorKey(m, tea.KeySpace)
	if len(m.changes()) != 0 || m.editing || !strings.Contains(m.View(), "Ничего не найдено") {
		t.Fatal("empty search result mishandled")
	}
}

func TestConfigEditorViewFitsTerminal(t *testing.T) {
	for _, size := range []tea.WindowSizeMsg{{Width: 60, Height: 18}, {Width: 100, Height: 30}, {Width: 160, Height: 50}} {
		m := editorFixture()
		for i := range 120 {
			m.fields = append(m.fields, ConfigEditorField{ID: 100 + i, Name: strings.Repeat("long-name", 15), Value: strings.Repeat("long-value", 20)})
		}
		m.original = append([]ConfigEditorField(nil), m.fields...)
		m.filter()
		m.Update(size)
		editorKey(m, tea.KeyEnd)
		view := m.View()
		if lipgloss.Width(view) > size.Width || lipgloss.Height(view) > size.Height {
			t.Fatalf("view %dx%d exceeds %dx%d", lipgloss.Width(view), lipgloss.Height(view), size.Width, size.Height)
		}
	}
}
