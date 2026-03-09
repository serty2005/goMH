package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestSelectionDigitShortcutSelectsVisibleItem(t *testing.T) {
	model := newSelectionModel([]ChoiceItem{
		{Title: "Первый"},
		{Title: "Второй"},
		{Title: "Третий"},
	}, SelectionConfig{})
	model.width = 100
	model.height = 30

	updatedModel, cmd := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'2'}})
	updated := updatedModel.(*selectionModel)

	if updated.cursor != 1 {
		t.Fatalf("ожидался переход на второй пункт, получено %d", updated.cursor)
	}
	if cmd == nil {
		t.Fatal("ожидалась команда подтверждения выбора")
	}

	msg := cmd()
	done, ok := msg.(selectionDoneMsg)
	if !ok {
		t.Fatalf("ожидалось сообщение selectionDoneMsg, получено %T", msg)
	}
	if len(done.indices) != 1 || done.indices[0] != 1 {
		t.Fatalf("ожидался выбор индекса 1, получено %+v", done.indices)
	}
}

func TestSelectionMouseHoverAndClick(t *testing.T) {
	model := newSelectionModel([]ChoiceItem{
		{Title: "Первый"},
		{Title: "Второй", Description: "Описание"},
		{Title: "Третий"},
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
		t.Fatalf("ожидался hover на втором пункте, получено %d", hovered.cursor)
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
		t.Fatalf("ожидалось сохранение выбора второго пункта, получено %d", clicked.cursor)
	}
	if cmd == nil {
		t.Fatal("ожидалась команда подтверждения выбора по клику")
	}

	msg := cmd()
	done, ok := msg.(selectionDoneMsg)
	if !ok {
		t.Fatalf("ожидалось сообщение selectionDoneMsg, получено %T", msg)
	}
	if len(done.indices) != 1 || done.indices[0] != 1 {
		t.Fatalf("ожидался выбор второго пункта, получено %+v", done.indices)
	}
}
