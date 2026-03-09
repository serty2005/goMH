package tui

import (
	"context"
	"errors"
	"fmt"
	"goMH/logstream"
	"path/filepath"
	"strings"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type liveLogOverlayTickMsg struct{}

type LiveLogOverlayRenderStyles struct {
	Title    lipgloss.Style
	Subtitle lipgloss.Style
	Panel    lipgloss.Style
	Key      lipgloss.Style
	Help     lipgloss.Style
	Error    lipgloss.Style
	Status   lipgloss.Style
	Text     lipgloss.Style
	Muted    lipgloss.Style
}

type liveLogOverlayState struct {
	mu             sync.Mutex
	service        *logstream.Service
	handle         *logstream.Handle
	sink           *liveLogOverlaySink
	filePath       string
	lines          []string
	visible        bool
	paused         bool
	scrollTop      int
	unreadCount    int
	lastError      string
	viewportHeight int
}

type liveLogOverlaySnapshot struct {
	filePath    string
	lines       []string
	visible     bool
	paused      bool
	scrollTop   int
	unreadCount int
	lastError   string
}

type liveLogOverlaySink struct {
	mu    sync.Mutex
	lines []string
}

var globalLiveLogOverlay = &liveLogOverlayState{}

func liveLogOverlayTickCmd() tea.Cmd {
	return tea.Tick(250*time.Millisecond, func(time.Time) tea.Msg {
		return liveLogOverlayTickMsg{}
	})
}

func OpenLiveLogOverlay(filePath string) error {
	return globalLiveLogOverlay.open(filePath)
}

func ShutdownLiveLogOverlay() {
	globalLiveLogOverlay.shutdown()
}

func consumeLiveLogOverlayUpdates() {
	globalLiveLogOverlay.step()
}

func liveLogOverlayVisible() bool {
	return globalLiveLogOverlay.isVisible()
}

func liveLogOverlayHint() string {
	return globalLiveLogOverlay.hint()
}

func handleLiveLogOverlayKey(msg tea.KeyMsg, height int) bool {
	return globalLiveLogOverlay.handleKey(msg, height)
}

func renderLiveLogOverlay(width int, height int, styles LiveLogOverlayRenderStyles) string {
	return globalLiveLogOverlay.render(width, height, styles)
}

func (s *liveLogOverlayState) open(filePath string) error {
	filePath = strings.TrimSpace(filePath)
	if filePath == "" {
		return errors.New("не указан файл для просмотра")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.filePath == filePath && s.handle != nil {
		s.visible = true
		s.unreadCount = 0
		s.lastError = ""
		return nil
	}

	s.shutdownLocked()

	service := s.service
	if service == nil {
		service = logstream.NewService()
	}
	sink := &liveLogOverlaySink{}
	handle, err := service.StartTail(context.Background(), logstream.TailRequest{
		FilePath:     filePath,
		StartLines:   -1,
		PollInterval: 500 * time.Millisecond,
		Sink:         sink,
	})
	if err != nil {
		return err
	}

	s.service = service
	s.handle = handle
	s.sink = sink
	s.filePath = filePath
	s.lines = nil
	s.visible = true
	s.paused = false
	s.scrollTop = 0
	s.unreadCount = 0
	s.lastError = ""
	return nil
}

func (s *liveLogOverlayState) shutdown() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.shutdownLocked()
	s.filePath = ""
	s.lines = nil
	s.visible = false
	s.paused = false
	s.scrollTop = 0
	s.unreadCount = 0
	s.lastError = ""
	s.viewportHeight = 0
}

func (s *liveLogOverlayState) shutdownLocked() {
	if s.handle != nil {
		s.handle.Cancel()
	}
	s.handle = nil
	s.sink = nil
}

func (s *liveLogOverlayState) isVisible() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.filePath != "" && s.visible
}

func (s *liveLogOverlayState) isActiveLocked() bool {
	return strings.TrimSpace(s.filePath) != ""
}

func (s *liveLogOverlayState) toggleVisibleLocked() bool {
	if !s.isActiveLocked() {
		s.visible = false
		return false
	}
	s.visible = !s.visible
	if s.visible {
		s.unreadCount = 0
		if !s.paused {
			s.scrollToBottomLocked()
		}
	}
	return s.visible
}

func (s *liveLogOverlayState) handleKey(msg tea.KeyMsg, height int) bool {
	key := msg.String()

	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.isActiveLocked() {
		return false
	}

	s.viewportHeight = liveLogOverlayContentHeight(height)
	if key == "shift+tab" {
		s.toggleVisibleLocked()
		return true
	}
	if !s.visible {
		return false
	}

	switch key {
	case "esc":
		s.shutdownLocked()
		s.filePath = ""
		s.lines = nil
		s.visible = false
		s.paused = false
		s.scrollTop = 0
		s.unreadCount = 0
		s.lastError = ""
		s.viewportHeight = 0
		return true
	case " ":
		s.paused = !s.paused
		if !s.paused {
			s.scrollToBottomLocked()
		}
	case "up", "k":
		s.paused = true
		s.scrollTop--
		s.normalizeScrollLocked()
	case "down", "j":
		s.paused = true
		s.scrollTop++
		s.normalizeScrollLocked()
	case "pgup":
		s.paused = true
		s.scrollTop -= 8
		s.normalizeScrollLocked()
	case "pgdown":
		s.paused = true
		s.scrollTop += 8
		s.normalizeScrollLocked()
	case "home":
		s.paused = true
		s.scrollTop = 0
		s.normalizeScrollLocked()
	case "end":
		s.paused = true
		s.scrollToBottomLocked()
	}
	return true
}

func (s *liveLogOverlayState) hint() string {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.isActiveLocked() || s.visible {
		return ""
	}

	return fmt.Sprintf("Shift+Tab Просмотр лога [%s - %d новых строк]", filepath.Base(s.filePath), s.unreadCount)
}

func (s *liveLogOverlayState) step() {
	s.mu.Lock()
	sink := s.sink
	handle := s.handle
	s.mu.Unlock()

	if sink != nil {
		newLines := sink.Drain()
		if len(newLines) > 0 {
			s.mu.Lock()
			s.lines = append(s.lines, newLines...)
			const maxLines = 4000
			if len(s.lines) > maxLines {
				trimmed := len(s.lines) - maxLines
				s.lines = append([]string(nil), s.lines[trimmed:]...)
				s.scrollTop = max(0, s.scrollTop-trimmed)
			}
			if s.visible {
				if !s.paused {
					s.scrollToBottomLocked()
				} else {
					s.normalizeScrollLocked()
				}
			} else {
				s.unreadCount += len(newLines)
			}
			s.mu.Unlock()
		}
	}

	if handle == nil {
		return
	}

	select {
	case <-handle.Done():
		err := handle.Wait()
		s.mu.Lock()
		if s.handle == handle {
			s.handle = nil
			s.sink = nil
		}
		if err != nil && !errors.Is(err, context.Canceled) {
			s.lastError = err.Error()
		}
		s.mu.Unlock()
	default:
	}
}

func (s *liveLogOverlayState) render(width int, height int, styles LiveLogOverlayRenderStyles) string {
	if width <= 0 || height <= 0 {
		return ""
	}

	snapshot := s.snapshot(height)

	header := []string{
		styles.Title.Render("Просмотр лога в реальном времени"),
	}
	if snapshot.filePath == "" {
		header = append(header, styles.Muted.Render("Файл лога не выбран."))
	} else {
		state := "LIVE"
		if snapshot.paused {
			state = "ПАУЗА"
		}
		header = append(header, styles.Subtitle.Render(truncateText(
			fmt.Sprintf("%s | %s | %s", filepath.Base(snapshot.filePath), state, snapshot.filePath),
			max(1, width-6),
		)))
	}

	contentHeight := liveLogOverlayContentHeight(height)
	viewLines, firstLine, lastLine := visibleLiveLogLines(snapshot.lines, snapshot.scrollTop, contentHeight)
	body := make([]string, 0, contentHeight)
	if len(viewLines) == 0 {
		body = append(body, styles.Muted.Render("Ожидание новых строк..."))
	} else {
		for _, line := range viewLines {
			body = append(body, styles.Text.Render(truncateText(line, max(1, width-6))))
		}
	}
	for len(body) < contentHeight {
		body = append(body, "")
	}

	infoText := "Строк пока нет."
	if len(snapshot.lines) > 0 {
		infoText = fmt.Sprintf("Строки %d-%d из %d", firstLine, lastLine, len(snapshot.lines))
	}
	if snapshot.lastError != "" {
		infoText += " | ошибка: " + snapshot.lastError
	}

	footer := []string{
		styles.Help.Render(strings.Join([]string{
			styles.Key.Render("Space") + " пауза/лайв",
			styles.Key.Render("Up/Down") + " прокрутка",
			styles.Key.Render("PgUp/PgDn") + " листать",
		}, "   ")),
		styles.Help.Render(strings.Join([]string{
			styles.Key.Render("Home/End") + " начало/конец",
			styles.Key.Render("Shift+Tab") + " свернуть",
			styles.Key.Render("Esc") + " закрыть",
		}, "   ")),
	}

	lines := append(header, "")
	lines = append(lines, body...)
	lines = append(lines, "")
	statusLine := styles.Status.Width(max(1, width-6)).Render(truncateText(infoText, max(1, width-6)))
	if snapshot.lastError != "" {
		statusLine = styles.Error.Width(max(1, width-6)).Render(truncateText(infoText, max(1, width-6)))
	}
	lines = append(lines, statusLine)
	lines = append(lines, footer...)

	content := styles.Panel.Width(max(1, width-4)).Render(strings.Join(lines, "\n"))
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, content)
}

func (s *liveLogOverlayState) snapshot(height int) liveLogOverlaySnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.viewportHeight = liveLogOverlayContentHeight(height)
	if s.visible && !s.paused {
		s.scrollToBottomLocked()
	} else {
		s.normalizeScrollLocked()
	}

	return liveLogOverlaySnapshot{
		filePath:    s.filePath,
		lines:       append([]string(nil), s.lines...),
		visible:     s.visible,
		paused:      s.paused,
		scrollTop:   s.scrollTop,
		unreadCount: s.unreadCount,
		lastError:   s.lastError,
	}
}

func (s *liveLogOverlayState) scrollToBottomLocked() {
	height := s.viewportHeight
	if height <= 0 {
		height = 1
	}
	s.scrollTop = max(0, len(s.lines)-height)
}

func (s *liveLogOverlayState) normalizeScrollLocked() {
	if len(s.lines) == 0 {
		s.scrollTop = 0
		return
	}

	height := s.viewportHeight
	if height <= 0 {
		height = 1
	}
	maxTop := max(0, len(s.lines)-height)
	if s.scrollTop < 0 {
		s.scrollTop = 0
	}
	if s.scrollTop > maxTop {
		s.scrollTop = maxTop
	}
}

func visibleLiveLogLines(lines []string, scrollTop int, height int) ([]string, int, int) {
	if len(lines) == 0 || height <= 0 {
		return nil, 0, 0
	}

	if scrollTop < 0 {
		scrollTop = 0
	}
	maxTop := max(0, len(lines)-height)
	if scrollTop > maxTop {
		scrollTop = maxTop
	}

	end := scrollTop + height
	if end > len(lines) {
		end = len(lines)
	}
	return lines[scrollTop:end], scrollTop + 1, end
}

func liveLogOverlayContentHeight(totalHeight int) int {
	height := totalHeight - 8
	if height < 1 {
		return 1
	}
	return height
}

func (s *liveLogOverlaySink) WriteLine(line string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lines = append(s.lines, line)
	return nil
}

func (s *liveLogOverlaySink) Close() error {
	return nil
}

func (s *liveLogOverlaySink) Drain() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.lines) == 0 {
		return nil
	}
	lines := append([]string(nil), s.lines...)
	s.lines = s.lines[:0]
	return lines
}
