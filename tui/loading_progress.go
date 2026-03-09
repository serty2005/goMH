package tui

import (
	"fmt"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type progressResultMsg[T any] struct {
	value T
	err   error
}

type progressTickMsg struct{}

type ProgressUpdater interface {
	Update(percent int, status string, detail string)
}

type progressState struct {
	mu      sync.Mutex
	percent int
	status  string
	detail  string
}

type progressUpdater struct {
	state *progressState
}

func (u progressUpdater) Update(percent int, status string, detail string) {
	if u.state == nil {
		return
	}

	u.state.mu.Lock()
	defer u.state.mu.Unlock()

	switch {
	case percent < 0:
		percent = 0
	case percent > 100:
		percent = 100
	}

	u.state.percent = percent
	if status != "" {
		u.state.status = status
	}
	u.state.detail = detail
}

type progressModel[T any] struct {
	theme  Theme
	title  string
	state  *progressState
	value  T
	err    error
	done   bool
	width  int
	height int
	fn     func(ProgressUpdater) (T, error)
}

func RunWithProgress[T any](title string, status string, fn func(ProgressUpdater) (T, error)) (T, error) {
	model := progressModel[T]{
		theme: DefaultTheme(),
		title: title,
		state: &progressState{
			status: status,
		},
		fn: fn,
	}

	program := tea.NewProgram(&model, tea.WithAltScreen())
	finalModel, err := program.Run()
	if err != nil {
		var zero T
		return zero, err
	}

	resultModel, ok := finalModel.(*progressModel[T])
	if !ok {
		var zero T
		return zero, fmt.Errorf("не удалось получить результат окна прогресса")
	}
	return resultModel.value, resultModel.err
}

func (m *progressModel[T]) Init() tea.Cmd {
	return tea.Batch(m.startCmd(), progressTickCmd(), liveLogOverlayTickCmd())
}

func (m *progressModel[T]) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil
	case progressTickMsg:
		if m.done {
			return m, nil
		}
		return m, progressTickCmd()
	case liveLogOverlayTickMsg:
		consumeLiveLogOverlayUpdates()
		return m, liveLogOverlayTickCmd()
	case progressResultMsg[T]:
		m.value = msg.value
		m.err = msg.err
		m.done = true
		return m, tea.Quit
	case tea.KeyMsg:
		if handleLiveLogOverlayKey(msg, m.height) {
			return m, nil
		}
		switch msg.String() {
		case "ctrl+c":
			var zero T
			m.value = zero
			m.err = ErrExitToMainMenu
			m.done = true
			return m, tea.Quit
		}
	}
	return m, nil
}

func (m *progressModel[T]) View() string {
	if m.width <= 0 || m.height <= 0 {
		return ""
	}
	if liveLogOverlayVisible() {
		return renderLiveLogOverlay(m.width, m.height, LiveLogOverlayRenderStyles{
			Title:    m.theme.Title,
			Subtitle: m.theme.Subtitle,
			Panel:    m.theme.PanelFocus,
			Key:      m.theme.Key,
			Help:     m.theme.Help,
			Error:    m.theme.Error,
			Status:   m.theme.Status,
			Text:     m.theme.Item,
			Muted:    m.theme.ItemMuted,
		})
	}

	percent, status, detail := m.snapshot()
	barWidth := minInt(maxInt(32, m.width-16), 72)
	lines := []string{
		m.theme.Title.Render(m.title),
		"",
		m.theme.Status.Render(" " + truncateText(status, barWidth+10) + " "),
		renderProgressBar(barWidth, percent),
	}
	if detail != "" {
		lines = append(lines, m.theme.ItemMuted.Render(truncateText(detail, barWidth+10)))
	}
	if hint := liveLogOverlayHint(); hint != "" {
		lines = append(lines, "", m.theme.Help.Render(hint))
	}

	panel := m.theme.PanelFocus.Width(minInt(m.width-4, barWidth+10)).Render(stringsJoin(lines))
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, panel)
}

func (m *progressModel[T]) startCmd() tea.Cmd {
	return func() tea.Msg {
		updater := progressUpdater{state: m.state}
		value, err := m.fn(updater)
		return progressResultMsg[T]{value: value, err: err}
	}
}

func (m *progressModel[T]) snapshot() (int, string, string) {
	m.state.mu.Lock()
	defer m.state.mu.Unlock()
	return m.state.percent, m.state.status, m.state.detail
}

func progressTickCmd() tea.Cmd {
	return tea.Tick(120*time.Millisecond, func(time.Time) tea.Msg {
		return progressTickMsg{}
	})
}
