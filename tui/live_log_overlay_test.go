package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestLiveLogOverlayManualScrollEnablesPauseAndMovesCursor(t *testing.T) {
	defer ShutdownLiveLogOverlay()

	globalLiveLogOverlay.mu.Lock()
	globalLiveLogOverlay.filePath = `C:\logs\goMH.log`
	globalLiveLogOverlay.visible = true
	globalLiveLogOverlay.paused = false
	globalLiveLogOverlay.viewportHeight = 3
	globalLiveLogOverlay.lines = []string{"1", "2", "3", "4", "5"}
	globalLiveLogOverlay.scrollTop = 2
	globalLiveLogOverlay.mu.Unlock()

	handled := handleLiveLogOverlayKey(tea.KeyMsg{Type: tea.KeyUp}, 10)
	if !handled {
		t.Fatal("клавиша прокрутки должна обрабатываться overlay")
	}

	globalLiveLogOverlay.mu.Lock()
	defer globalLiveLogOverlay.mu.Unlock()
	if !globalLiveLogOverlay.paused {
		t.Fatal("ручная прокрутка должна автоматически переводить overlay в паузу")
	}
	if globalLiveLogOverlay.scrollTop != 1 {
		t.Fatalf("ожидалась прокрутка вверх до 1, получено %d", globalLiveLogOverlay.scrollTop)
	}
}

func TestLiveLogOverlayEscClosesFlowCompletely(t *testing.T) {
	defer ShutdownLiveLogOverlay()

	globalLiveLogOverlay.mu.Lock()
	globalLiveLogOverlay.filePath = `C:\logs\goMH.log`
	globalLiveLogOverlay.visible = true
	globalLiveLogOverlay.unreadCount = 5
	globalLiveLogOverlay.lines = []string{"1", "2"}
	globalLiveLogOverlay.mu.Unlock()

	handled := handleLiveLogOverlayKey(tea.KeyMsg{Type: tea.KeyEsc}, 20)
	if !handled {
		t.Fatal("Esc должен обрабатываться overlay")
	}
	if liveLogOverlayVisible() {
		t.Fatal("после Esc overlay должен полностью закрываться")
	}
	if hint := liveLogOverlayHint(); hint != "" {
		t.Fatalf("после Esc не должно оставаться подсказки, получено %q", hint)
	}
}
