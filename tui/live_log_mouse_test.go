package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

func TestDashboardLogViewerMouseSelectsLine(t *testing.T) {
	model := newTestDashboardModel()
	model.logViewer = dashboardLogViewerState{
		filePath:     `C:\logs\goMH.log`,
		lines:        []string{"first", "second", "third"},
		selectedLine: -1,
		visible:      true,
	}

	rect := model.logViewerRect()
	msg := tea.MouseMsg{
		X:      rect.x,
		Y:      rect.y + 2,
		Action: tea.MouseActionPress,
		Button: tea.MouseButtonLeft,
	}

	updatedModel, cmd := model.Update(msg)
	updated := updatedModel.(dashboardModel)

	if cmd != nil {
		t.Fatal("log viewer mouse select should not enqueue commands")
	}
	if updated.logViewer.selectedLine != 0 {
		t.Fatalf("expected selected line 0, got %d", updated.logViewer.selectedLine)
	}
	if updated.lastMessage != "Строка лога выбрана." {
		t.Fatalf("unexpected status message: %q", updated.lastMessage)
	}
}

func TestDashboardLogViewerMouseRightClickCopiesFullLine(t *testing.T) {
	model := newTestDashboardModel()
	model.logViewer = dashboardLogViewerState{
		filePath:     `C:\logs\goMH.log`,
		lines:        []string{"full first line", "full second line"},
		selectedLine: -1,
		visible:      true,
	}

	var copied string
	originalClipboard := clipboardWriteAll
	clipboardWriteAll = func(text string) error {
		copied = text
		return nil
	}
	defer func() {
		clipboardWriteAll = originalClipboard
	}()

	rect := model.logViewerRect()
	msg := tea.MouseMsg{
		X:      rect.x,
		Y:      rect.y + 3,
		Action: tea.MouseActionPress,
		Button: tea.MouseButtonRight,
	}

	updatedModel, _ := model.Update(msg)
	updated := updatedModel.(dashboardModel)

	if copied != "full second line" {
		t.Fatalf("expected clipboard to contain full line, got %q", copied)
	}
	if updated.logViewer.selectedLine != 1 {
		t.Fatalf("expected selected line 1, got %d", updated.logViewer.selectedLine)
	}
	if updated.lastMessage != "Строка лога скопирована." {
		t.Fatalf("unexpected status message: %q", updated.lastMessage)
	}
}

func TestLiveLogOverlayMouseRightClickCopiesFullLine(t *testing.T) {
	state := &liveLogOverlayState{
		filePath:     `C:\logs\goMH.log`,
		lines:        []string{"overlay first", "overlay second"},
		selectedLine: -1,
		visible:      true,
	}

	var copied string
	originalClipboard := clipboardWriteAll
	clipboardWriteAll = func(text string) error {
		copied = text
		return nil
	}
	defer func() {
		clipboardWriteAll = originalClipboard
	}()

	panelStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		Padding(0, 1)
	layout := liveLogOverlayLayout(120, 30, panelStyle)
	handled, note, err := state.handleMouse(tea.MouseMsg{
		X:      layout.contentX,
		Y:      layout.bodyY + 1,
		Action: tea.MouseActionPress,
		Button: tea.MouseButtonRight,
	}, 120, 30, panelStyle)
	if err != nil {
		t.Fatalf("handleMouse returned error: %v", err)
	}
	if !handled {
		t.Fatal("expected overlay mouse event to be handled")
	}
	if copied != "overlay second" {
		t.Fatalf("expected clipboard to contain full line, got %q", copied)
	}
	if state.selectedLine != 1 {
		t.Fatalf("expected selected line 1, got %d", state.selectedLine)
	}
	if note != "Строка лога скопирована." {
		t.Fatalf("unexpected note: %q", note)
	}
}
