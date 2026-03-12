package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type dashboardTaskLogOverlayState struct {
	taskID       string
	title        string
	lines        []string
	selectedLine int
	visible      bool
	paused       bool
	scrollTop    int
}

func (m *dashboardModel) openTaskLogOverlay(task dashboardTask) {
	m.taskLogOverlay = dashboardTaskLogOverlayState{
		taskID:       task.Snapshot.ID,
		title:        task.Snapshot.Title,
		lines:        append([]string(nil), task.Logs...),
		selectedLine: -1,
		visible:      true,
	}
	m.scrollTaskLogOverlayToBottom()
}

func (m *dashboardModel) closeTaskLogOverlay() {
	m.taskLogOverlay = dashboardTaskLogOverlayState{}
}

func (m *dashboardModel) syncTaskLogOverlay(tasks []dashboardTask) {
	if m.taskLogOverlay.taskID == "" {
		return
	}

	for _, task := range tasks {
		if task.Snapshot.ID != m.taskLogOverlay.taskID {
			continue
		}

		prevLen := len(m.taskLogOverlay.lines)
		m.taskLogOverlay.title = task.Snapshot.Title
		m.taskLogOverlay.lines = append([]string(nil), task.Logs...)
		if m.taskLogOverlay.selectedLine >= len(m.taskLogOverlay.lines) {
			m.taskLogOverlay.selectedLine = -1
		}

		if !m.taskLogOverlay.paused && len(m.taskLogOverlay.lines) != prevLen {
			m.scrollTaskLogOverlayToBottom()
		} else {
			m.normalizeTaskLogOverlayScroll()
		}
		return
	}

	m.normalizeTaskLogOverlayScroll()
}

func (m dashboardModel) handleTaskLogOverlayKey(msg tea.KeyMsg) (dashboardModel, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c":
		return m, tea.Quit
	case "shift+tab", "esc":
		m.closeTaskLogOverlay()
		m.applyResult("Полный лог задачи свернут.", nil)
		return m, nil
	case " ":
		m.taskLogOverlay.paused = !m.taskLogOverlay.paused
		if m.taskLogOverlay.paused {
			m.applyResult("Автопрокрутка лога поставлена на паузу.", nil)
		} else {
			m.scrollTaskLogOverlayToBottom()
			m.applyResult("Автопрокрутка лога возобновлена.", nil)
		}
		return m, nil
	case "up", "k":
		m.taskLogOverlay.paused = true
		m.taskLogOverlay.scrollTop--
	case "down", "j":
		m.taskLogOverlay.paused = true
		m.taskLogOverlay.scrollTop++
	case "pgup":
		m.taskLogOverlay.paused = true
		m.taskLogOverlay.scrollTop -= 8
	case "pgdown":
		m.taskLogOverlay.paused = true
		m.taskLogOverlay.scrollTop += 8
	case "home":
		m.taskLogOverlay.paused = true
		m.taskLogOverlay.scrollTop = 0
	case "end":
		m.taskLogOverlay.paused = true
		m.scrollTaskLogOverlayToBottom()
		return m, nil
	default:
		return m, nil
	}

	m.normalizeTaskLogOverlayScroll()
	return m, nil
}

func (m dashboardModel) renderTaskLogOverlay() string {
	contentHeight := liveLogOverlayContentHeight(m.height)
	viewLines, firstLine, lastLine := visibleLiveLogLines(m.taskLogOverlay.lines, m.taskLogOverlay.scrollTop, contentHeight)

	header := []string{
		m.titleStyle.Render("Лог задачи из очереди"),
		m.mutedStyle.Render(truncateText(m.taskLogOverlay.title, max(1, m.width-6))),
		"",
	}

	body := make([]string, 0, contentHeight)
	if len(viewLines) == 0 {
		body = append(body, m.mutedStyle.Render("Строк пока нет."))
	} else {
		for idx, line := range viewLines {
			rendered := truncateText(line, max(1, m.width-6))
			if firstLine+idx-1 == m.taskLogOverlay.selectedLine {
				rendered = m.statusStyle.Width(max(1, m.width-6)).Render(rendered)
			}
			body = append(body, rendered)
		}
	}
	for len(body) < contentHeight {
		body = append(body, "")
	}

	infoText := "Строк пока нет."
	if len(m.taskLogOverlay.lines) > 0 {
		infoText = fmt.Sprintf("Строки %d-%d из %d", firstLine, lastLine, len(m.taskLogOverlay.lines))
	}
	if m.taskLogOverlay.paused {
		infoText += " | ПАУЗА"
	} else {
		infoText += " | LIVE"
	}

	footer := []string{
		m.mutedStyle.Render(strings.Join([]string{
			m.keyStyle.Render("Space") + " пауза/лайв",
			m.keyStyle.Render("Up/Down") + " прокрутка",
			m.keyStyle.Render("PgUp/PgDn") + " листать",
		}, "   ")),
		m.mutedStyle.Render(strings.Join([]string{
			m.keyStyle.Render("Home/End") + " начало/конец",
			m.keyStyle.Render("Shift+Tab") + " свернуть",
			m.keyStyle.Render("Esc") + " закрыть",
		}, "   ")),
	}

	lines := append(header, body...)
	lines = append(lines, "")
	lines = append(lines, m.statusStyle.Width(max(1, m.width-6)).Render(truncateText(infoText, max(1, m.width-6))))
	lines = append(lines, footer...)

	content := m.panelStyle.Width(max(1, m.width-4)).Render(strings.Join(lines, "\n"))
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, content)
}

func (m *dashboardModel) handleTaskLogOverlayMouse(msg tea.MouseMsg) (bool, string, error) {
	if msg.Action != tea.MouseActionPress {
		return false, "", nil
	}
	if msg.Button != tea.MouseButtonLeft && msg.Button != tea.MouseButtonRight {
		return false, "", nil
	}

	lineIndex, ok := m.taskLogOverlayLineIndexAt(msg.X, msg.Y)
	if !ok {
		return false, "", nil
	}

	m.taskLogOverlay.selectedLine = lineIndex
	if msg.Button != tea.MouseButtonRight {
		return true, "Строка лога выбрана.", nil
	}
	if lineIndex < 0 || lineIndex >= len(m.taskLogOverlay.lines) {
		return true, "", nil
	}
	if err := copyTextToClipboard(m.taskLogOverlay.lines[lineIndex]); err != nil {
		return true, "", err
	}
	return true, "Строка лога скопирована.", nil
}

func (m dashboardModel) taskLogOverlayLineIndexAt(x int, y int) (int, bool) {
	layout := liveLogOverlayLayout(m.width, m.height, m.panelStyle)
	if x < layout.contentX || x >= layout.contentX+layout.contentWidth {
		return 0, false
	}
	if y < layout.bodyY || y >= layout.bodyY+layout.bodyHeight {
		return 0, false
	}

	viewLines, firstLine, _ := visibleLiveLogLines(m.taskLogOverlay.lines, m.taskLogOverlay.scrollTop, layout.bodyHeight)
	if len(viewLines) == 0 {
		return 0, false
	}

	line := y - layout.bodyY
	if line < 0 || line >= len(viewLines) {
		return 0, false
	}
	return firstLine - 1 + line, true
}

func (m *dashboardModel) scrollTaskLogOverlayToBottom() {
	height := liveLogOverlayContentHeight(m.height)
	if height < 1 {
		height = 1
	}
	m.taskLogOverlay.scrollTop = max(0, len(m.taskLogOverlay.lines)-height)
}

func (m *dashboardModel) normalizeTaskLogOverlayScroll() {
	if len(m.taskLogOverlay.lines) == 0 {
		m.taskLogOverlay.scrollTop = 0
		m.taskLogOverlay.selectedLine = -1
		return
	}

	height := liveLogOverlayContentHeight(m.height)
	if height < 1 {
		height = 1
	}
	maxTop := max(0, len(m.taskLogOverlay.lines)-height)
	if m.taskLogOverlay.scrollTop < 0 {
		m.taskLogOverlay.scrollTop = 0
	}
	if m.taskLogOverlay.scrollTop > maxTop {
		m.taskLogOverlay.scrollTop = maxTop
	}
	if m.taskLogOverlay.selectedLine >= len(m.taskLogOverlay.lines) {
		m.taskLogOverlay.selectedLine = -1
	}
}
