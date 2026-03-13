package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestSelectionDigitShortcutSelectsVisibleItem(t *testing.T) {
	model := newSelectionModel([]ChoiceItem{
		{Title: "First"},
		{Title: "Second"},
		{Title: "Third"},
	}, SelectionConfig{})
	model.width = 100
	model.height = 30

	updatedModel, cmd := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'2'}})
	updated := updatedModel.(*selectionModel)

	if updated.cursor != 1 {
		t.Fatalf("expected cursor on second item, got %d", updated.cursor)
	}
	if cmd == nil {
		t.Fatal("expected selection command")
	}

	msg := cmd()
	done, ok := msg.(selectionDoneMsg)
	if !ok {
		t.Fatalf("expected selectionDoneMsg, got %T", msg)
	}
	if len(done.indices) != 1 || done.indices[0] != 1 {
		t.Fatalf("expected index 1, got %+v", done.indices)
	}
}

func TestSelectionDigitShortcutCanBeDisabledForSearch(t *testing.T) {
	model := newSelectionModel([]ChoiceItem{
		{Title: "8.7.6032.0", FilterValue: "8.7.6032.0 8760320"},
		{Title: "8.8.100.0", FilterValue: "8.8.100.0 881000"},
	}, SelectionConfig{
		Search:           true,
		DisableShortcuts: true,
	})
	model.width = 100
	model.height = 30

	updatedModel, cmd := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'8'}})
	updated := updatedModel.(*selectionModel)

	if cmd != nil {
		if _, ok := cmd().(selectionDoneMsg); ok {
			t.Fatal("did not expect immediate selection when shortcuts are disabled")
		}
	}
	if got := updated.input.Value(); got != "8" {
		t.Fatalf("expected digit to be added to filter, got %q", got)
	}
	if len(updated.filtered) != 2 {
		t.Fatalf("expected 2 filtered results, got %d", len(updated.filtered))
	}
}

func TestSelectionMouseHoverAndClick(t *testing.T) {
	model := newSelectionModel([]ChoiceItem{
		{Title: "First"},
		{Title: "Second", Description: "Description"},
		{Title: "Third"},
	}, SelectionConfig{})
	model.width = 100
	model.height = 30

	layout := model.layout()
	hoverMsg := tea.MouseMsg{
		X:      layout.itemsX,
		Y:      layout.itemsY + 1,
		Action: tea.MouseActionMotion,
		Button: tea.MouseButtonNone,
	}
	hoveredModel, _ := model.Update(hoverMsg)
	hovered := hoveredModel.(*selectionModel)

	if hovered.cursor != 1 {
		t.Fatalf("expected hover on second item, got %d", hovered.cursor)
	}

	clickMsg := tea.MouseMsg{
		X:      layout.itemsX,
		Y:      layout.itemsY + 1,
		Action: tea.MouseActionPress,
		Button: tea.MouseButtonLeft,
	}
	clickedModel, cmd := hovered.Update(clickMsg)
	clicked := clickedModel.(*selectionModel)

	if clicked.cursor != 1 {
		t.Fatalf("expected cursor to stay on second item, got %d", clicked.cursor)
	}
	if cmd == nil {
		t.Fatal("expected click selection command")
	}

	msg := cmd()
	done, ok := msg.(selectionDoneMsg)
	if !ok {
		t.Fatalf("expected selectionDoneMsg, got %T", msg)
	}
	if len(done.indices) != 1 || done.indices[0] != 1 {
		t.Fatalf("expected second item, got %+v", done.indices)
	}
}
