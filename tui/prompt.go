package tui

import (
	"errors"
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

var errPromptCancelled = errors.New("prompt_cancelled")

type ChoiceItem struct {
	Title       string
	Description string
	Meta        string
	FilterValue string
	Disabled    bool
}

type SelectionConfig struct {
	Title        string
	Subtitle     string
	Placeholder  string
	EmptyText    string
	Help         string
	Search       bool
	Multi        bool
	SelectedText string
}

type selectionDoneMsg struct {
	indices []int
	err     error
}

type selectionModel struct {
	theme     Theme
	config    SelectionConfig
	items     []ChoiceItem
	filtered  []int
	selected  map[int]bool
	cursor    int
	width     int
	height    int
	input     textinput.Model
	finished  bool
	result    []int
	resultErr error
}

func newSelectionModel(items []ChoiceItem, config SelectionConfig) *selectionModel {
	input := textinput.New()
	input.Cursor.Style = DefaultTheme().Key
	input.Placeholder = config.Placeholder
	input.Prompt = ""
	input.CharLimit = 256
	if config.Search {
		input.Focus()
	}

	model := &selectionModel{
		theme:    DefaultTheme(),
		config:   config,
		items:    items,
		filtered: make([]int, 0, len(items)),
		selected: map[int]bool{},
		input:    input,
	}
	model.applyFilter()
	return model
}

func (m *selectionModel) Init() tea.Cmd {
	return textinput.Blink
}

func (m *selectionModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil

	case selectionDoneMsg:
		m.finished = true
		m.result = msg.indices
		m.resultErr = msg.err
		return m, tea.Quit

	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c":
			return m, func() tea.Msg {
				return selectionDoneMsg{err: ErrExitToMainMenu}
			}
		case "esc":
			return m, func() tea.Msg {
				return selectionDoneMsg{err: errPromptCancelled}
			}
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
			return m, nil
		case "down", "j":
			if m.cursor < len(m.filtered)-1 {
				m.cursor++
			}
			return m, nil
		case "pgup":
			m.cursor -= 8
			if m.cursor < 0 {
				m.cursor = 0
			}
			return m, nil
		case "pgdown":
			m.cursor += 8
			if m.cursor >= len(m.filtered) {
				m.cursor = len(m.filtered) - 1
			}
			if m.cursor < 0 {
				m.cursor = 0
			}
			return m, nil
		case "home":
			m.cursor = 0
			return m, nil
		case "end":
			if len(m.filtered) > 0 {
				m.cursor = len(m.filtered) - 1
			}
			return m, nil
		case " ":
			if !m.config.Multi {
				break
			}
			index := m.currentIndex()
			if index < 0 || m.items[index].Disabled {
				return m, nil
			}
			if m.selected[index] {
				delete(m.selected, index)
			} else {
				m.selected[index] = true
			}
			return m, nil
		case "enter":
			if len(m.filtered) == 0 {
				return m, nil
			}
			if m.config.Multi {
				indices := make([]int, 0, len(m.selected))
				for _, originalIndex := range m.filtered {
					if m.selected[originalIndex] {
						indices = append(indices, originalIndex)
					}
				}
				if len(indices) == 0 {
					return m, nil
				}
				return m, func() tea.Msg {
					return selectionDoneMsg{indices: indices}
				}
			}
			index := m.currentIndex()
			if index < 0 || m.items[index].Disabled {
				return m, nil
			}
			return m, func() tea.Msg {
				return selectionDoneMsg{indices: []int{index}}
			}
		}
	}

	if m.config.Search {
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
		m.applyFilter()
		return m, cmd
	}

	return m, nil
}

func (m *selectionModel) View() string {
	width := m.width
	if width <= 0 {
		width = 100
	}
	panelWidth := minInt(width-4, 100)
	if panelWidth < 48 {
		panelWidth = width - 2
	}

	lines := []string{m.theme.Title.Render(m.config.Title)}
	if m.config.Subtitle != "" {
		lines = append(lines, m.theme.Subtitle.Render(m.config.Subtitle))
	}

	if m.config.Search {
		style := m.theme.Input
		if m.input.Focused() {
			style = m.theme.InputFocus
		}
		lines = append(lines, "")
		lines = append(lines, style.Width(maxInt(24, panelWidth-6)).Render(m.input.View()))
	}

	lines = append(lines, "")
	lines = append(lines, m.renderItems(panelWidth-6)...)

	help := m.footerHelp()
	if help != "" {
		lines = append(lines, "")
		lines = append(lines, help)
	}

	panelStyle := m.theme.PanelFocus
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, panelStyle.Width(panelWidth).Render(strings.Join(lines, "\n")))
}

func (m *selectionModel) renderItems(width int) []string {
	if len(m.filtered) == 0 {
		emptyText := m.config.EmptyText
		if emptyText == "" {
			emptyText = "Ничего не найдено."
		}
		return []string{m.theme.ItemMuted.Render(emptyText)}
	}

	start, end := visibleRange(m.cursor, len(m.filtered), 10)
	lines := make([]string, 0, (end-start)*2)
	for pos := start; pos < end; pos++ {
		originalIndex := m.filtered[pos]
		item := m.items[originalIndex]

		prefix := "  "
		if m.config.Multi {
			if m.selected[originalIndex] {
				prefix = "[x]"
			} else {
				prefix = "[ ]"
			}
		}
		text := strings.TrimSpace(strings.Join([]string{prefix, item.Title, item.Meta}, " "))
		text = truncateText(text, width)

		line := m.theme.Item.Render(text)
		if item.Disabled {
			line = m.theme.ItemMuted.Render(text)
		}
		if pos == m.cursor {
			line = m.theme.ItemFocus.Width(width + 2).Render(text)
		}
		lines = append(lines, line)

		if item.Description != "" {
			desc := m.theme.ItemMuted.Render(truncateText(item.Description, width))
			lines = append(lines, "  "+desc)
		}
	}
	return lines
}

func (m *selectionModel) footerHelp() string {
	parts := []string{}
	if m.config.Search {
		parts = append(parts, m.theme.Key.Render("Ввод")+" фильтр")
	}
	parts = append(parts, m.theme.Key.Render("↑↓")+" выбор")
	if m.config.Multi {
		parts = append(parts, m.theme.Key.Render("Space")+" отметить")
	}
	parts = append(parts, m.theme.Key.Render("Enter")+" подтвердить")
	parts = append(parts, m.theme.Key.Render("Esc")+" назад")
	if m.config.Help != "" {
		parts = append(parts, m.theme.ItemMuted.Render(m.config.Help))
	}
	return strings.Join(parts, "   ")
}

func (m *selectionModel) applyFilter() {
	m.filtered = m.filtered[:0]
	query := strings.TrimSpace(strings.ToLower(m.input.Value()))
	for index, item := range m.items {
		haystack := item.FilterValue
		if haystack == "" {
			haystack = strings.Join([]string{item.Title, item.Description, item.Meta}, " ")
		}
		if query == "" || strings.Contains(strings.ToLower(haystack), query) {
			m.filtered = append(m.filtered, index)
		}
	}
	if m.cursor >= len(m.filtered) {
		m.cursor = len(m.filtered) - 1
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
}

func (m *selectionModel) currentIndex() int {
	if len(m.filtered) == 0 || m.cursor < 0 || m.cursor >= len(m.filtered) {
		return -1
	}
	return m.filtered[m.cursor]
}

type InputConfig struct {
	Title        string
	Subtitle     string
	Placeholder  string
	InitialValue string
	Help         string
	Password     bool
	Validate     func(string) error
}

type inputDoneMsg struct {
	value string
	err   error
}

type inputModel struct {
	theme     Theme
	config    InputConfig
	width     int
	height    int
	input     textinput.Model
	errText   string
	result    string
	resultErr error
	finished  bool
}

func newInputModel(config InputConfig) *inputModel {
	input := textinput.New()
	input.Prompt = ""
	input.Placeholder = config.Placeholder
	input.SetValue(config.InitialValue)
	input.Focus()
	input.CharLimit = 1024
	if config.Password {
		input.EchoMode = textinput.EchoPassword
		input.EchoCharacter = '•'
	}
	return &inputModel{
		theme:  DefaultTheme(),
		config: config,
		input:  input,
	}
}

func (m *inputModel) Init() tea.Cmd {
	return textinput.Blink
}

func (m *inputModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil

	case inputDoneMsg:
		m.result = msg.value
		m.resultErr = msg.err
		m.finished = true
		return m, tea.Quit

	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c":
			return m, func() tea.Msg {
				return inputDoneMsg{err: ErrExitToMainMenu}
			}
		case "esc":
			return m, func() tea.Msg {
				return inputDoneMsg{err: errPromptCancelled}
			}
		case "enter":
			value := strings.TrimSpace(m.input.Value())
			if m.config.Validate != nil {
				if err := m.config.Validate(value); err != nil {
					m.errText = err.Error()
					return m, nil
				}
			}
			return m, func() tea.Msg {
				return inputDoneMsg{value: value}
			}
		}
	}

	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	m.errText = ""
	return m, cmd
}

func (m *inputModel) View() string {
	width := m.width
	if width <= 0 {
		width = 90
	}
	panelWidth := minInt(width-4, 84)
	if panelWidth < 44 {
		panelWidth = width - 2
	}

	lines := []string{m.theme.Title.Render(m.config.Title)}
	if m.config.Subtitle != "" {
		lines = append(lines, m.theme.Subtitle.Render(m.config.Subtitle))
	}
	lines = append(lines, "")
	lines = append(lines, m.theme.InputFocus.Width(maxInt(24, panelWidth-6)).Render(m.input.View()))
	if m.errText != "" {
		lines = append(lines, "")
		lines = append(lines, m.theme.Error.Render(m.errText))
	}

	helpParts := []string{
		m.theme.Key.Render("Enter") + " сохранить",
		m.theme.Key.Render("Esc") + " назад",
	}
	if m.config.Help != "" {
		helpParts = append(helpParts, m.theme.ItemMuted.Render(m.config.Help))
	}
	lines = append(lines, "")
	lines = append(lines, strings.Join(helpParts, "   "))

	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, m.theme.PanelFocus.Width(panelWidth).Render(strings.Join(lines, "\n")))
}

func runSelection(items []ChoiceItem, config SelectionConfig) ([]int, error) {
	model := newSelectionModel(items, config)
	program := tea.NewProgram(model, tea.WithAltScreen())
	finalModel, err := program.Run()
	if err != nil {
		return nil, err
	}
	resultModel, ok := finalModel.(*selectionModel)
	if !ok {
		return nil, fmt.Errorf("не удалось получить результат выбора")
	}
	switch resultModel.resultErr {
	case nil:
		return resultModel.result, nil
	case errPromptCancelled:
		return nil, nil
	default:
		return nil, resultModel.resultErr
	}
}

func runInput(config InputConfig) (string, error) {
	model := newInputModel(config)
	program := tea.NewProgram(model, tea.WithAltScreen())
	finalModel, err := program.Run()
	if err != nil {
		return "", err
	}
	resultModel, ok := finalModel.(*inputModel)
	if !ok {
		return "", fmt.Errorf("не удалось получить результат ввода")
	}
	switch resultModel.resultErr {
	case nil:
		return resultModel.result, nil
	case errPromptCancelled:
		return "", ErrExitToMainMenu
	default:
		return "", resultModel.resultErr
	}
}

func SelectItems(items []ChoiceItem, config SelectionConfig) ([]int, error) {
	return runSelection(items, config)
}

func SelectItem(items []ChoiceItem, config SelectionConfig) (int, error) {
	indices, err := runSelection(items, config)
	if err != nil || len(indices) == 0 {
		return -1, err
	}
	return indices[0], nil
}

func PromptText(config InputConfig) (string, error) {
	return runInput(config)
}

func Confirm(title, subtitle string, yesLabel string) (bool, error) {
	if yesLabel == "" {
		yesLabel = "Подтвердить"
	}
	index, err := SelectItem([]ChoiceItem{
		{Title: yesLabel},
		{Title: "Отмена"},
	}, SelectionConfig{
		Title:    title,
		Subtitle: subtitle,
	})
	if err != nil {
		return false, err
	}
	return index == 0, nil
}

func SelectStrings(title, subtitle string, items []string, search bool) (int, error) {
	choices := make([]ChoiceItem, 0, len(items))
	for _, item := range items {
		choices = append(choices, ChoiceItem{Title: item})
	}
	return SelectItem(choices, SelectionConfig{
		Title:       title,
		Subtitle:    subtitle,
		Search:      search,
		Placeholder: "Фильтр",
	})
}

func visibleRange(cursor, total, size int) (int, int) {
	if total <= size {
		return 0, total
	}
	start := cursor - size/2
	if start < 0 {
		start = 0
	}
	end := start + size
	if end > total {
		end = total
		start = end - size
	}
	return start, end
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
