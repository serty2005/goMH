package tui

import (
	"fmt"
	"slices"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type ConfigEditorField struct {
	ID          int
	Name        string
	Description string
	Value       string
	Boolean     bool
	Null        bool
	Nullable    bool
}

type ConfigEditorOptions struct {
	Title    string
	Subtitle string
	Validate func([]ConfigEditorField) error
}

type configEditorModel struct {
	options       ConfigEditorOptions
	fields        []ConfigEditorField
	original      []ConfigEditorField
	filtered      []int
	cursor        int
	width, height int
	search        textinput.Model
	input         textinput.Model
	editing       bool
	discard       bool
	discardChoice bool
	saved         bool
	errText       string
}

func newConfigEditorModel(fields []ConfigEditorField, options ConfigEditorOptions) *configEditorModel {
	search := textinput.New()
	search.Prompt = "Поиск: "
	search.Placeholder = "название, путь или описание параметра"
	search.CharLimit = 256
	search.Focus()
	input := textinput.New()
	input.Prompt = "Значение: "
	input.CharLimit = 0
	m := &configEditorModel{options: options, fields: slices.Clone(fields), original: slices.Clone(fields), search: search, input: input, width: 100, height: 30}
	m.filter()
	return m
}

func (m *configEditorModel) Init() tea.Cmd {
	return tea.Batch(textinput.Blink, liveLogOverlayTickCmd())
}

func (m *configEditorModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.search.Width = max(1, msg.Width-20)
		m.input.Width = max(1, msg.Width-22)
		return m, nil
	case liveLogOverlayTickMsg:
		consumeLiveLogOverlayUpdates()
		return m, liveLogOverlayTickCmd()
	case tea.MouseMsg:
		if !m.editing && !m.discard && !liveLogOverlayVisible() {
			switch msg.Button {
			case tea.MouseButtonWheelUp:
				m.move(-3)
			case tea.MouseButtonWheelDown:
				m.move(3)
			}
		}
		return m, nil
	case tea.KeyMsg:
		if handleLiveLogOverlayKey(msg, m.height) {
			return m, nil
		}
		key := msg.String()
		if m.discard {
			switch key {
			case "left", "right", "tab":
				m.discardChoice = !m.discardChoice
			case "esc", "ctrl+c":
				m.discard = false
			case "enter":
				if m.discardChoice {
					return m, tea.Quit
				}
				m.discard = false
			}
			return m, nil
		}
		if m.editing {
			switch key {
			case "esc":
				m.endEdit(false)
				return m, nil
			case "ctrl+c":
				m.endEdit(true)
				return m, m.requestExit()
			case "enter", "ctrl+s":
				m.endEdit(true)
				if key == "ctrl+s" {
					return m, m.save()
				}
				return m, nil
			}
			var cmd tea.Cmd
			m.input, cmd = m.input.Update(msg)
			return m, cmd
		}
		switch key {
		case "ctrl+s":
			return m, m.save()
		case "esc":
			if m.search.Value() != "" {
				m.search.SetValue("")
				m.filter()
				return m, nil
			}
			return m, m.requestExit()
		case "ctrl+c":
			return m, m.requestExit()
		case "up":
			m.move(-1)
			return m, nil
		case "down", "tab":
			m.move(1)
			return m, nil
		case "shift+tab":
			m.move(-1)
			return m, nil
		case "pgup":
			m.move(-m.pageSize())
			return m, nil
		case "pgdown":
			m.move(m.pageSize())
			return m, nil
		case "home":
			m.cursor = 0
			return m, nil
		case "end":
			m.cursor = max(0, len(m.filtered)-1)
			return m, nil
		case " ", "enter":
			if index := m.current(); index >= 0 {
				field := &m.fields[index]
				m.errText = ""
				if field.Boolean {
					if strings.EqualFold(strings.TrimSpace(field.Value), "true") && !field.Null {
						field.Value = "false"
					} else {
						field.Value = "true"
					}
					field.Null = false
				} else if key == "enter" {
					m.editing = true
					m.search.Blur()
					m.input.SetValue(field.Value)
					m.input.CursorEnd()
					return m, m.input.Focus()
				}
			}
			return m, nil
		case "ctrl+n":
			if index := m.current(); index >= 0 && m.fields[index].Nullable {
				m.fields[index].Null = !m.fields[index].Null
			}
			return m, nil
		case "ctrl+r":
			if index := m.current(); index >= 0 {
				m.fields[index] = m.original[index]
				m.errText = ""
			}
			return m, nil
		}
	}
	var cmd tea.Cmd
	m.search, cmd = m.search.Update(msg)
	m.filter()
	return m, cmd
}

func (m *configEditorModel) endEdit(apply bool) {
	if index := m.current(); apply && index >= 0 {
		m.fields[index].Value = m.input.Value()
		m.fields[index].Null = false
	}
	m.editing = false
	m.input.Blur()
	m.search.Focus()
}

func (m *configEditorModel) current() int {
	if m.cursor < 0 || m.cursor >= len(m.filtered) {
		return -1
	}
	return m.filtered[m.cursor]
}

func (m *configEditorModel) move(delta int) {
	m.cursor = max(0, min(m.cursor+delta, len(m.filtered)-1))
}

func (m *configEditorModel) filter() {
	selected := m.current()
	query := strings.ToLower(strings.TrimSpace(m.search.Value()))
	m.filtered = nil
	for i, field := range m.fields {
		if strings.Contains(strings.ToLower(field.Name+" "+field.Description), query) {
			m.filtered = append(m.filtered, i)
		}
	}
	m.cursor = 0
	for pos, index := range m.filtered {
		if index == selected {
			m.cursor = pos
			break
		}
	}
}

func (m *configEditorModel) changed(index int) bool {
	a, b := m.fields[index], m.original[index]
	return a.Null != b.Null || (!a.Null && a.Value != b.Value)
}

func (m *configEditorModel) changes() []ConfigEditorField {
	var fields []ConfigEditorField
	for i := range m.fields {
		if m.changed(i) {
			fields = append(fields, m.fields[i])
		}
	}
	return fields
}

func (m *configEditorModel) requestExit() tea.Cmd {
	if len(m.changes()) == 0 {
		return tea.Quit
	}
	m.discard, m.discardChoice = true, false
	return nil
}

func (m *configEditorModel) save() tea.Cmd {
	if m.options.Validate != nil {
		if err := m.options.Validate(m.changes()); err != nil {
			m.errText = err.Error()
			return nil
		}
	}
	m.saved = true
	return tea.Quit
}

func (m *configEditorModel) pageSize() int { return max(1, m.height-17) }

func editorDisplay(value string) string {
	return strings.Map(func(r rune) rune {
		if r < 32 || r == 127 {
			return ' '
		}
		return r
	}, value)
}

func (m *configEditorModel) View() string {
	theme := DefaultTheme()
	if liveLogOverlayVisible() {
		return renderLiveLogOverlay(m.width, m.height, LiveLogOverlayRenderStyles{
			Title: theme.Title, Subtitle: theme.Subtitle, Panel: theme.PanelFocus, Key: theme.Key,
			Help: theme.Help, Error: theme.Error, Status: theme.Status, Text: theme.Item, Muted: theme.ItemMuted,
		})
	}
	if m.width < 60 || m.height < 18 {
		return "Увеличьте окно терминала до 60 × 18 для редактора config.xml."
	}
	width := m.width - 8
	line := func(s string) string { return truncateText(editorDisplay(s), width) }
	lines := []string{
		theme.Title.Render(line(m.options.Title)),
		theme.Subtitle.Render(line(m.options.Subtitle)),
		theme.Warning.Render(line("Перед изменением закройте Front. После сохранения запустите его заново.")),
		m.search.View(),
		theme.Subtitle.Render(fmt.Sprintf("Параметры: %d/%d   Изменено: %d   * — несохранённое изменение", len(m.filtered), len(m.fields), len(m.changes()))),
	}
	start, end := visibleRange(m.cursor, len(m.filtered), m.pageSize())
	for pos := start; pos < end; pos++ {
		index := m.filtered[pos]
		field := m.fields[index]
		marker := "  "
		if m.changed(index) {
			marker = "* "
		}
		value := editorDisplay(field.Value)
		if field.Null {
			value = "<не задано: xsi:nil>"
		} else if field.Boolean {
			if strings.EqualFold(strings.TrimSpace(field.Value), "true") {
				value = "[x] true"
			} else {
				value = "[ ] false"
			}
		} else if value == "" {
			value = `""`
		}
		nameWidth := min(width*2/3, 70)
		name := lipgloss.NewStyle().Width(nameWidth).Render(truncateText(editorDisplay(field.Name), nameWidth))
		row := truncateText(marker+name+"  "+value, width-2)
		if pos == m.cursor {
			row = theme.ItemFocus.Render(row)
		}
		lines = append(lines, row)
	}
	if len(m.filtered) == 0 {
		lines = append(lines, theme.ItemMuted.Render("Ничего не найдено. Esc — очистить поиск."))
	}
	for len(lines) < 5+m.pageSize() {
		lines = append(lines, "")
	}
	selected, detail := "", ""
	if index := m.current(); index >= 0 {
		field := m.fields[index]
		selected = field.Name
		detail = field.Description
		if field.Nullable {
			detail += "  Ctrl+N — переключить «не задано» (xsi:nil)."
		}
	}
	lines = append(lines, theme.Key.Render(line(selected)), theme.Subtitle.Render(line(detail)))
	if m.editing {
		lines = append(lines, m.input.View())
	} else if index := m.current(); index >= 0 {
		lines = append(lines, line("Значение: "+m.fields[index].Value))
	} else {
		lines = append(lines, "")
	}
	status := theme.Error.Render(line(m.errText))
	if m.discard {
		choices := "[Продолжить редактирование]   Выйти без сохранения"
		if m.discardChoice {
			choices = "Продолжить редактирование   [Выйти без сохранения]"
		}
		status = theme.Warning.Render(line(choices))
	}
	lines = append(lines, status)
	help := "↑↓ выбор · Пробел флаг · Enter ввод · Ctrl+S сохранить"
	help2 := "Поиск: ввод текста · Ctrl+R сброс · Esc назад"
	if m.editing {
		help = "Enter применить · Ctrl+S сохранить · Esc отменить"
		help2 = "Ctrl+V вставить · Значения сохраняются без обрезки пробелов"
	} else if m.discard {
		help, help2 = "Есть правки. ←→ выбор · Enter подтвердить", "Esc — продолжить редактирование"
	}
	lines = append(lines, theme.Help.Render(line(help)), theme.Help.Render(line(help2)))
	return theme.PanelFocus.Width(width + theme.PanelFocus.GetHorizontalPadding()).Render(strings.Join(lines, "\n"))
}

func EditConfigFields(fields []ConfigEditorField, options ConfigEditorOptions) ([]ConfigEditorField, bool, error) {
	m := newConfigEditorModel(fields, options)
	result, err := tea.NewProgram(m, tea.WithAltScreen(), tea.WithMouseCellMotion()).Run()
	if err != nil {
		return nil, false, err
	}
	final := result.(*configEditorModel)
	return final.changes(), final.saved, nil
}
