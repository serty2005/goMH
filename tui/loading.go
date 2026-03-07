package tui

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type loadingResultMsg[T any] struct {
	value T
	err   error
}

type loadingModel[T any] struct {
	theme         Theme
	title         string
	subtitle      string
	value         T
	err           error
	done          bool
	spinnerFrames []string
	spinnerIndex  int
	width         int
	height        int
	fn            func() (T, error)
}

func RunWithSpinner[T any](title string, subtitle string, fn func() (T, error)) (T, error) {
	model := loadingModel[T]{
		theme:         DefaultTheme(),
		title:         title,
		subtitle:      subtitle,
		spinnerFrames: []string{"|", "/", "-", "\\"},
		fn:            fn,
	}

	program := tea.NewProgram(&model, tea.WithAltScreen())
	finalModel, err := program.Run()
	if err != nil {
		var zero T
		return zero, err
	}

	resultModel, ok := finalModel.(*loadingModel[T])
	if !ok {
		var zero T
		return zero, fmt.Errorf("не удалось получить результат ожидания")
	}
	return resultModel.value, resultModel.err
}

func (m *loadingModel[T]) Init() tea.Cmd {
	return tea.Batch(m.startCmd(), spinnerTickCmd())
}

func (m *loadingModel[T]) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil
	case spinnerTickMsg:
		if m.done {
			return m, nil
		}
		m.spinnerIndex = (m.spinnerIndex + 1) % len(m.spinnerFrames)
		return m, spinnerTickCmd()
	case loadingResultMsg[T]:
		m.value = msg.value
		m.err = msg.err
		m.done = true
		return m, tea.Quit
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c":
			var zero T
			m.err = ErrExitToMainMenu
			m.value = zero
			m.done = true
			return m, tea.Quit
		}
	}
	return m, nil
}

func (m *loadingModel[T]) View() string {
	if m.width <= 0 || m.height <= 0 {
		return ""
	}

	spinner := m.spinnerFrames[m.spinnerIndex%len(m.spinnerFrames)]
	lines := []string{
		m.theme.Title.Render(m.title),
		"",
		m.theme.Status.Render(" " + spinner + " " + m.subtitle + " "),
	}
	panel := m.theme.PanelFocus.Width(minInt(m.width-4, 90)).Render(stringsJoin(lines))
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, panel)
}

func (m *loadingModel[T]) startCmd() tea.Cmd {
	return func() tea.Msg {
		value, err := m.fn()
		return loadingResultMsg[T]{value: value, err: err}
	}
}

func stringsJoin(lines []string) string {
	if len(lines) == 0 {
		return ""
	}
	result := lines[0]
	for i := 1; i < len(lines); i++ {
		result += "\n" + lines[i]
	}
	return result
}
