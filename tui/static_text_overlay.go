package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type SelectionOverlayContent struct {
	Title    string
	Subtitle string
	Lines    []string
}

type SelectionOverlayProvider func(index int) (SelectionOverlayContent, bool, error)

type staticTextOverlayState struct {
	title        string
	subtitle     string
	lines        []string
	selectedLine int
	visible      bool
	scrollTop    int
}

func (s *staticTextOverlayState) Open(content SelectionOverlayContent) {
	s.title = strings.TrimSpace(content.Title)
	s.subtitle = strings.TrimSpace(content.Subtitle)
	s.lines = append([]string(nil), content.Lines...)
	s.selectedLine = -1
	s.visible = true
	s.scrollTop = 0
}

func (s *staticTextOverlayState) Close() {
	*s = staticTextOverlayState{}
}

func (s *staticTextOverlayState) HandleKey(msg tea.KeyMsg, height int, closeKeys ...string) bool {
	if !s.visible {
		return false
	}

	key := msg.String()
	for _, closeKey := range closeKeys {
		if key == closeKey {
			s.Close()
			return true
		}
	}

	switch key {
	case "up", "k":
		s.scrollTop--
	case "down", "j":
		s.scrollTop++
	case "pgup":
		s.scrollTop -= 8
	case "pgdown":
		s.scrollTop += 8
	case "home":
		s.scrollTop = 0
	case "end":
		s.scrollToBottom(height)
	default:
		return false
	}

	s.normalizeScroll(height)
	return true
}

func (s *staticTextOverlayState) HandleMouse(msg tea.MouseMsg, width int, height int, panelStyle lipgloss.Style) (bool, string, error) {
	if !s.visible || msg.Action != tea.MouseActionPress {
		return false, "", nil
	}
	if msg.Button != tea.MouseButtonLeft && msg.Button != tea.MouseButtonRight {
		return false, "", nil
	}

	lineIndex, ok := s.lineIndexAt(msg.X, msg.Y, width, height, panelStyle)
	if !ok {
		return false, "", nil
	}

	s.selectedLine = lineIndex
	if msg.Button != tea.MouseButtonRight {
		return true, "Строка выбрана.", nil
	}
	if lineIndex < 0 || lineIndex >= len(s.lines) {
		return true, "", nil
	}
	if err := copyTextToClipboard(s.lines[lineIndex]); err != nil {
		return true, "", err
	}
	return true, "Строка скопирована.", nil
}

func (s *staticTextOverlayState) Render(width int, height int, styles LiveLogOverlayRenderStyles, closeLabel string) string {
	contentHeight := liveLogOverlayContentHeight(height)
	viewLines, firstLine, lastLine := visibleLiveLogLines(s.lines, s.scrollTop, contentHeight)

	header := []string{styles.Title.Render(s.title)}
	if s.subtitle != "" {
		header = append(header, styles.Subtitle.Render(truncateText(s.subtitle, max(1, width-6))))
	}
	header = append(header, "")

	body := make([]string, 0, contentHeight)
	if len(viewLines) == 0 {
		body = append(body, styles.Muted.Render("Нет данных для отображения."))
	} else {
		for idx, line := range viewLines {
			rendered := truncateText(line, max(1, width-6))
			if firstLine+idx-1 == s.selectedLine {
				rendered = styles.Status.Width(max(1, width-6)).Render(rendered)
			}
			body = append(body, rendered)
		}
	}
	for len(body) < contentHeight {
		body = append(body, "")
	}

	infoText := "Строк пока нет."
	if len(s.lines) > 0 {
		infoText = fmt.Sprintf("Строки %d-%d из %d", firstLine, lastLine, len(s.lines))
	}

	footer := []string{
		styles.Help.Render(strings.Join([]string{
			styles.Key.Render("Up/Down") + " прокрутка",
			styles.Key.Render("PgUp/PgDn") + " листать",
			styles.Key.Render("Home/End") + " начало/конец",
		}, "   ")),
		styles.Help.Render(strings.Join([]string{
			styles.Key.Render("Мышь") + " выбрать/копировать",
			styles.Key.Render(closeLabel) + " закрыть",
		}, "   ")),
	}

	lines := append(header, body...)
	lines = append(lines, "")
	lines = append(lines, styles.Status.Width(max(1, width-6)).Render(truncateText(infoText, max(1, width-6))))
	lines = append(lines, footer...)

	panelWidth := minInt(max(48, width-2), width-2)
	if panelWidth <= 0 {
		panelWidth = width
	}
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, styles.Panel.Width(panelWidth).Render(strings.Join(lines, "\n")))
}

func (s *staticTextOverlayState) lineIndexAt(x int, y int, width int, height int, panelStyle lipgloss.Style) (int, bool) {
	layout := liveLogOverlayLayout(width, height, panelStyle)
	if x < layout.contentX || x >= layout.contentX+layout.contentWidth {
		return 0, false
	}
	if y < layout.bodyY || y >= layout.bodyY+layout.bodyHeight {
		return 0, false
	}

	viewLines, firstLine, _ := visibleLiveLogLines(s.lines, s.scrollTop, layout.bodyHeight)
	if len(viewLines) == 0 {
		return 0, false
	}

	line := y - layout.bodyY
	if line < 0 || line >= len(viewLines) {
		return 0, false
	}
	return firstLine - 1 + line, true
}

func (s *staticTextOverlayState) normalizeScroll(height int) {
	if len(s.lines) == 0 {
		s.scrollTop = 0
		s.selectedLine = -1
		return
	}

	contentHeight := liveLogOverlayContentHeight(height)
	maxTop := max(0, len(s.lines)-contentHeight)
	if s.scrollTop < 0 {
		s.scrollTop = 0
	}
	if s.scrollTop > maxTop {
		s.scrollTop = maxTop
	}
	if s.selectedLine >= len(s.lines) {
		s.selectedLine = -1
	}
}

func (s *staticTextOverlayState) scrollToBottom(height int) {
	contentHeight := liveLogOverlayContentHeight(height)
	s.scrollTop = max(0, len(s.lines)-contentHeight)
}
