package autostart

import (
	"fmt"
	"path/filepath"
	"strings"

	"goMH/core"

	"github.com/atotto/clipboard"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type addMode int

const (
	addModeNone addMode = iota
	addModeKind
	addModePath
	addModeArgs
)

type statusFilter int

const (
	statusFilterAll statusFilter = iota
	statusFilterEnabled
	statusFilterDisabled
)

type selectedColumn int

const (
	columnStatus selectedColumn = iota
	columnName
	columnCommand
)

type detailState struct {
	visible bool
	title   string
	body    string
}

type autostartScanner func(func(core.AutostartScanProgress)) ([]core.AutostartEntry, error)

type scanProgressMsg struct {
	Area  string
	Found int
}

type scanDoneMsg struct {
	entries []core.AutostartEntry
	err     error
}

type scanProgressClosedMsg struct{}

type model struct {
	entries        []core.AutostartEntry
	original       map[string]bool
	creates        []core.AutostartCreateRequest
	cursor         int
	scroll         int
	width          int
	height         int
	scanning       bool
	scanner        autostartScanner
	scanCh         chan scanProgressMsg
	scanArea       string
	scanFound      int
	adding         addMode
	addKind        core.AutostartRegistryKey
	pathInput      textinput.Model
	argsInput      textinput.Model
	searchInput    textinput.Model
	searching      bool
	searchQuery    string
	clipboardRead  func() (string, error)
	sourceFilter   core.AutostartSource
	statusFilter   statusFilter
	selectedColumn selectedColumn
	detail         detailState
	result         Config
	saved          bool
	cancelled      bool
	errText        string
	titleStyle     lipgloss.Style
	headerStyle    lipgloss.Style
	focusStyle     lipgloss.Style
	mutedStyle     lipgloss.Style
	keyStyle       lipgloss.Style
	errorStyle     lipgloss.Style
	statusStyle    lipgloss.Style
}

func runUI(scanner autostartScanner) (*Config, error) {
	program := tea.NewProgram(newScanningModel(scanner), tea.WithAltScreen(), tea.WithMouseCellMotion())
	finalModel, err := program.Run()
	if err != nil {
		return nil, err
	}
	result, ok := finalModel.(model)
	if !ok || result.cancelled || !result.saved {
		return nil, nil
	}
	return &result.result, nil
}

func newScanningModel(scanner autostartScanner) model {
	mdl := newModel(nil)
	mdl.scanning = true
	mdl.scanner = scanner
	mdl.scanCh = make(chan scanProgressMsg, 8)
	mdl.scanArea = "Подготовка сканирования"
	return mdl
}

func newModel(entries []core.AutostartEntry) model {
	pathInput := textinput.New()
	pathInput.Placeholder = `C:\Path\app.exe или C:\Path\app.lnk`
	pathInput.CharLimit = 1024
	argsInput := textinput.New()
	argsInput.Placeholder = "--argument value"
	argsInput.CharLimit = 1024
	searchInput := textinput.New()
	searchInput.Placeholder = "поиск по имени или команде"
	searchInput.CharLimit = 256

	original := make(map[string]bool, len(entries))
	for _, entry := range entries {
		original[entry.ID] = entry.Enabled
	}
	return model{
		entries:       append([]core.AutostartEntry(nil), entries...),
		original:      original,
		addKind:       core.AutostartRegistryKeyRun,
		pathInput:     pathInput,
		argsInput:     argsInput,
		searchInput:   searchInput,
		clipboardRead: clipboard.ReadAll,
		statusFilter:  statusFilterAll,
		titleStyle:    lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#F3E9D2")),
		headerStyle:   lipgloss.NewStyle().Foreground(lipgloss.Color("#DAD7CD")).Bold(true),
		focusStyle:    lipgloss.NewStyle().Foreground(lipgloss.Color("#FAF8F1")).Background(lipgloss.Color("#3A4D39")).Bold(true),
		mutedStyle:    lipgloss.NewStyle().Foreground(lipgloss.Color("#BFCBA8")),
		keyStyle:      lipgloss.NewStyle().Foreground(lipgloss.Color("#E7B10A")).Bold(true),
		errorStyle:    lipgloss.NewStyle().Foreground(lipgloss.Color("#F56C6C")),
		statusStyle:   lipgloss.NewStyle().Foreground(lipgloss.Color("#FAF8F1")).Background(lipgloss.Color("#3A4D39")).Padding(0, 1),
	}
}

func (m model) Init() tea.Cmd {
	if m.scanning && m.scanner != nil {
		return tea.Batch(m.startScan(), waitScanProgress(m.scanCh))
	}
	return textinput.Blink
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case scanProgressMsg:
		m.scanArea = msg.Area
		m.scanFound = msg.Found
		return m, waitScanProgress(m.scanCh)
	case scanProgressClosedMsg:
		return m, nil
	case scanDoneMsg:
		m.scanning = false
		m.errText = ""
		if msg.err != nil {
			m.errText = fmt.Sprintf("Автозапуск отсканирован частично: %v", msg.err)
		}
		m.setEntries(msg.entries)
		return m, nil
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil
	case tea.MouseMsg:
		if m.detail.visible {
			if msg.Action == tea.MouseActionPress {
				m.detail = detailState{}
			}
			return m, nil
		}
		if m.adding != addModeNone {
			if msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonRight {
				return m.pasteIntoFocusedInput()
			}
			return m, nil
		}
		if msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonLeft {
			row := msg.Y - m.listTop()
			if row >= 0 && row < m.visibleRows() {
				index := m.scroll + row
				if index >= 0 && index < len(m.filteredEntries()) {
					m.cursor = index
					m.selectedColumn = m.columnAt(msg.X, m.width)
					if m.selectedColumn == columnName || m.selectedColumn == columnCommand {
						m.openCurrentDetail()
					} else {
						m.toggleCurrent()
					}
				}
			}
		}
		return m, nil
	case tea.KeyMsg:
		if m.detail.visible {
			switch msg.String() {
			case "ctrl+c", "esc", " ", "enter":
				m.detail = detailState{}
				return m, nil
			}
			return m, nil
		}
		if m.adding != addModeNone {
			return m.updateAdd(msg)
		}
		if m.searching {
			return m.updateSearch(msg)
		}
		switch msg.String() {
		case "ctrl+c", "esc":
			m.cancelled = true
			return m, tea.Quit
		case "up", "k":
			m.move(-1)
			return m, nil
		case "down", "j":
			m.move(1)
			return m, nil
		case "pgup":
			m.move(-m.visibleRows())
			return m, nil
		case "pgdown":
			m.move(m.visibleRows())
			return m, nil
		case "home":
			m.cursor = 0
			m.ensureCursorVisible()
			return m, nil
		case "end":
			m.cursor = len(m.filteredEntries()) - 1
			m.ensureCursorVisible()
			return m, nil
		case "left":
			m.selectedColumn = columnName
			return m, nil
		case "right":
			m.selectedColumn = columnCommand
			return m, nil
		case " ":
			if m.selectedColumn == columnName || m.selectedColumn == columnCommand {
				m.openCurrentDetail()
			} else {
				m.toggleCurrent()
			}
			return m, nil
		case "a":
			m.startAdd()
			return m, nil
		case "f":
			m.cycleSourceFilter()
			return m, nil
		case "e":
			m.cycleStatusFilter()
			return m, nil
		case "0":
			m.sourceFilter = ""
			m.statusFilter = statusFilterAll
			m.searchQuery = ""
			m.searchInput.SetValue("")
			m.cursor = 0
			m.scroll = 0
			return m, nil
		case "/":
			m.startSearch()
			return m, textinput.Blink
		case "s":
			if !m.dirty() {
				return m, nil
			}
			m.result = Config{Changes: m.changes(), Creates: append([]core.AutostartCreateRequest(nil), m.creates...)}
			m.saved = true
			return m, tea.Quit
		}
	}
	return m, nil
}

func (m model) View() string {
	width := m.width
	if width <= 0 {
		width = 120
	}
	height := m.height
	if height <= 0 {
		height = 28
	}
	if m.scanning {
		return m.renderScan(width, height)
	}
	if m.adding != addModeNone {
		return m.renderAdd(width, height)
	}
	if m.searching {
		return m.renderSearch(width, height)
	}
	if m.detail.visible {
		return m.renderDetail(width, height)
	}
	lines := []string{
		m.titleStyle.Render("Управление автозапуском"),
		m.mutedStyle.Render("Space действие по ячейке, Left имя/путь, Right команда, F источник, E статус, A добавить, S сохранить, Esc назад"),
		m.renderFilters(),
		"",
		m.headerStyle.Render(m.rowText(core.AutostartEntry{Name: "Имя", Command: "Команда"}, "Источник", "Статус", width)),
	}
	if len(m.filteredEntries()) == 0 {
		lines = append(lines, m.mutedStyle.Render("Автозапуски не найдены. Нажмите A, чтобы добавить запись."))
	} else {
		for _, line := range m.renderRows(width) {
			lines = append(lines, line)
		}
	}
	lines = append(lines, "")
	lines = append(lines, m.renderStatus())
	if m.errText != "" {
		lines = append(lines, m.errorStyle.Render(m.errText))
	}
	return lipgloss.Place(width, height, lipgloss.Left, lipgloss.Top, strings.Join(lines, "\n"))
}

func (m model) updateAdd(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch m.adding {
	case addModeKind:
		switch msg.String() {
		case "ctrl+c", "esc":
			m.adding = addModeNone
			return m, nil
		case "left", "right", "tab", " ":
			if m.addKind == core.AutostartRegistryKeyRun {
				m.addKind = core.AutostartRegistryKeyRunOnce
			} else {
				m.addKind = core.AutostartRegistryKeyRun
			}
			return m, nil
		case "1":
			m.addKind = core.AutostartRegistryKeyRun
			return m, nil
		case "2":
			m.addKind = core.AutostartRegistryKeyRunOnce
			return m, nil
		case "enter":
			m.adding = addModePath
			m.pathInput.Focus()
			return m, textinput.Blink
		}
	case addModePath:
		switch msg.String() {
		case "ctrl+c", "esc":
			m.adding = addModeNone
			m.pathInput.Blur()
			return m, nil
		case "ctrl+v":
			return m.pasteIntoFocusedInput()
		case "enter":
			if strings.TrimSpace(m.pathInput.Value()) == "" {
				m.errText = "Укажите путь до exe или lnk."
				return m, nil
			}
			m.errText = ""
			m.pathInput.Blur()
			m.argsInput.Focus()
			m.adding = addModeArgs
			return m, textinput.Blink
		}
		var cmd tea.Cmd
		m.pathInput, cmd = m.pathInput.Update(msg)
		return m, cmd
	case addModeArgs:
		switch msg.String() {
		case "ctrl+c", "esc":
			m.adding = addModeNone
			m.argsInput.Blur()
			return m, nil
		case "ctrl+v":
			return m.pasteIntoFocusedInput()
		case "enter":
			m.finishAdd()
			return m, nil
		}
		var cmd tea.Cmd
		m.argsInput, cmd = m.argsInput.Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m model) updateSearch(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c", "esc":
		m.searching = false
		m.searchInput.Blur()
		return m, nil
	case "enter":
		m.searchQuery = strings.TrimSpace(m.searchInput.Value())
		m.searching = false
		m.searchInput.Blur()
		m.cursor = 0
		m.scroll = 0
		return m, nil
	}
	var cmd tea.Cmd
	m.searchInput, cmd = m.searchInput.Update(msg)
	return m, cmd
}

func (m *model) startAdd() {
	m.adding = addModeKind
	m.addKind = core.AutostartRegistryKeyRun
	m.pathInput.SetValue("")
	m.argsInput.SetValue("")
	m.errText = ""
}

func (m *model) startSearch() {
	m.searching = true
	m.searchInput.SetValue(m.searchQuery)
	m.searchInput.Focus()
	m.errText = ""
}

func (m *model) finishAdd() {
	path := cleanAddedPath(m.pathInput.Value())
	if path == "" {
		m.errText = "Укажите путь до exe или lnk."
		return
	}
	name := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	req := core.AutostartCreateRequest{
		Name:        name,
		RegistryKey: m.addKind,
		Scope:       core.AutostartScopeUser,
		Path:        path,
		Arguments:   strings.TrimSpace(m.argsInput.Value()),
	}
	m.creates = append(m.creates, req)
	m.entries = append(m.entries, core.AutostartEntry{
		ID:          fmt.Sprintf("new|%d", len(m.creates)-1),
		Name:        name,
		Source:      sourceForRegistryKey(m.addKind),
		Scope:       core.AutostartScopeUser,
		Enabled:     true,
		Command:     strings.TrimSpace(path + " " + req.Arguments),
		RegistryKey: m.addKind,
		CanToggle:   false,
	})
	m.cursor = len(m.filteredEntries()) - 1
	m.ensureCursorVisible()
	m.adding = addModeNone
	m.pathInput.Blur()
	m.argsInput.Blur()
	m.errText = ""
}

func (m *model) toggleCurrent() {
	index := m.currentEntryIndex()
	if index < 0 || index >= len(m.entries) {
		return
	}
	if !m.entries[index].CanToggle {
		return
	}
	m.entries[index].Enabled = !m.entries[index].Enabled
}

func (m *model) move(delta int) {
	count := len(m.filteredEntries())
	if count == 0 {
		m.cursor = 0
		return
	}
	m.cursor += delta
	if m.cursor < 0 {
		m.cursor = 0
	}
	if m.cursor >= count {
		m.cursor = count - 1
	}
	m.ensureCursorVisible()
}

func (m *model) ensureCursorVisible() {
	rows := m.visibleRows()
	count := len(m.filteredEntries())
	if m.cursor >= count {
		m.cursor = count - 1
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
	if m.cursor < m.scroll {
		m.scroll = m.cursor
	}
	if m.cursor >= m.scroll+rows {
		m.scroll = m.cursor - rows + 1
	}
	if m.scroll < 0 {
		m.scroll = 0
	}
}

func (m model) dirty() bool {
	return len(m.creates) > 0 || len(m.changes()) > 0
}

func (m model) changes() []core.AutostartChange {
	changes := make([]core.AutostartChange, 0)
	for _, entry := range m.entries {
		original, ok := m.original[entry.ID]
		if !ok || original == entry.Enabled {
			continue
		}
		changes = append(changes, core.AutostartChange{Entry: entry, Enabled: entry.Enabled})
	}
	return changes
}

func (m *model) setEntries(entries []core.AutostartEntry) {
	m.entries = append([]core.AutostartEntry(nil), entries...)
	m.original = make(map[string]bool, len(entries))
	for _, entry := range entries {
		m.original[entry.ID] = entry.Enabled
	}
	m.cursor = 0
	m.scroll = 0
	m.scanFound = len(entries)
}

func (m model) filteredEntries() []core.AutostartEntry {
	indexes := m.filteredEntryIndexes()
	entries := make([]core.AutostartEntry, 0, len(indexes))
	for _, index := range indexes {
		entries = append(entries, m.entries[index])
	}
	return entries
}

func (m model) filteredEntryIndexes() []int {
	indexes := make([]int, 0, len(m.entries))
	for index, entry := range m.entries {
		if m.sourceFilter != "" && entry.Source != m.sourceFilter {
			continue
		}
		switch m.statusFilter {
		case statusFilterEnabled:
			if !entry.Enabled {
				continue
			}
		case statusFilterDisabled:
			if entry.Enabled {
				continue
			}
		}
		if !m.matchesSearch(entry) {
			continue
		}
		indexes = append(indexes, index)
	}
	return indexes
}

func (m model) matchesSearch(entry core.AutostartEntry) bool {
	query := strings.ToLower(strings.TrimSpace(m.searchQuery))
	if query == "" {
		return true
	}
	haystack := strings.ToLower(strings.Join([]string{
		entry.Name,
		entry.Command,
		entry.TargetPath,
		entry.FilePath,
		entry.Arguments,
	}, "\n"))
	return strings.Contains(haystack, query)
}

func (m model) currentEntryIndex() int {
	indexes := m.filteredEntryIndexes()
	if m.cursor < 0 || m.cursor >= len(indexes) {
		return -1
	}
	return indexes[m.cursor]
}

func (m *model) cycleSourceFilter() {
	order := []core.AutostartSource{
		"",
		core.AutostartSourceRegistryRun,
		core.AutostartSourceRegistryRunOnce,
		core.AutostartSourceStartupFolder,
		core.AutostartSourceScheduledTask,
	}
	next := 0
	for index, source := range order {
		if source == m.sourceFilter {
			next = (index + 1) % len(order)
			break
		}
	}
	m.sourceFilter = order[next]
	m.cursor = 0
	m.scroll = 0
}

func (m *model) cycleStatusFilter() {
	m.statusFilter = (m.statusFilter + 1) % 3
	m.cursor = 0
	m.scroll = 0
}

func (m *model) openCurrentDetail() {
	index := m.currentEntryIndex()
	if index < 0 {
		return
	}
	entry := m.entries[index]
	switch m.selectedColumn {
	case columnName:
		body := strings.TrimSpace(strings.Join(nonEmpty([]string{entry.Name, entry.TargetPath, entry.FilePath}), "\n"))
		if body == "" {
			body = entry.Name
		}
		m.detail = detailState{visible: true, title: "Имя и путь", body: body}
	case columnCommand:
		body := entry.Command
		if body == "" {
			body = strings.TrimSpace(strings.Join(nonEmpty([]string{entry.TargetPath, entry.Arguments}), " "))
		}
		m.detail = detailState{visible: true, title: "Команда запуска", body: body}
	}
}

func (m model) renderFilters() string {
	search := m.searchQuery
	if search == "" {
		search = "нет"
	}
	return m.mutedStyle.Render(fmt.Sprintf(
		"Фильтр источник: %s   статус: %s   поиск: %s",
		sourceFilterLabel(m.sourceFilter),
		statusFilterLabel(m.statusFilter),
		search,
	))
}

func (m model) renderScan(width, height int) string {
	area := m.scanArea
	if area == "" {
		area = "Подготовка сканирования"
	}
	lines := []string{
		m.titleStyle.Render("Анализ автозапуска"),
		"",
		"Область проверки: " + area,
		fmt.Sprintf("Найдено записей: %d", m.scanFound),
		"",
		m.mutedStyle.Render("Пожалуйста, подождите..."),
	}
	panel := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("#5F6F52")).Padding(1, 2).Width(min(90, max(40, width-8))).Render(strings.Join(lines, "\n"))
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, panel)
}

func (m model) renderDetail(width, height int) string {
	lines := []string{
		m.titleStyle.Render(m.detail.title),
		"",
		wrapText(m.detail.body, min(100, max(30, width-12))),
		"",
		m.mutedStyle.Render("Space/Enter/Esc закрыть"),
	}
	panel := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("#5F6F52")).Padding(1, 2).Width(min(110, max(40, width-8))).Render(strings.Join(lines, "\n"))
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, panel)
}

func (m model) renderSearch(width, height int) string {
	lines := []string{
		m.titleStyle.Render("Поиск автозапуска"),
		m.mutedStyle.Render("Enter применить, Esc отменить"),
		"",
		m.searchInput.View(),
	}
	panel := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("#5F6F52")).Padding(1, 2).Width(min(90, max(40, width-8))).Render(strings.Join(lines, "\n"))
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, panel)
}

func (m model) renderRows(width int) []string {
	rows := m.visibleRows()
	start := m.scroll
	filtered := m.filteredEntryIndexes()
	end := min(len(filtered), start+rows)
	lines := make([]string, 0, end-start)
	for index := start; index < end; index++ {
		entry := m.entries[filtered[index]]
		status := "[ ]"
		if entry.Enabled {
			status = "[x]"
		}
		text := m.rowText(entry, sourceLabel(entry), status, width)
		if !entry.CanToggle {
			text = m.mutedStyle.Render(text)
		}
		if index == m.cursor {
			text = m.focusStyle.Width(max(1, width-2)).Render(text)
		}
		lines = append(lines, text)
	}
	return lines
}

func (m model) rowText(entry core.AutostartEntry, source, status string, width int) string {
	nameWidth := 30
	sourceWidth := 16
	statusWidth := 8
	commandWidth := max(16, width-nameWidth-sourceWidth-statusWidth-8)
	return fmt.Sprintf("%-*s  %-*s  %-*s  %s",
		nameWidth, truncate(entry.Name, nameWidth),
		sourceWidth, truncate(source, sourceWidth),
		statusWidth, status,
		truncate(entry.Command, commandWidth),
	)
}

func (m model) columnAt(x int, width int) selectedColumn {
	nameWidth := 30
	sourceWidth := 16
	statusWidth := 8
	if x < nameWidth+2 {
		return columnName
	}
	if x < nameWidth+sourceWidth+statusWidth+6 {
		return columnStatus
	}
	return columnCommand
}

func (m model) renderStatus() string {
	changes := len(m.changes())
	creates := len(m.creates)
	save := "S сохранить"
	if !m.dirty() {
		save = "S сохранить (нет изменений)"
	}
	column := "статус"
	if m.selectedColumn == columnName {
		column = "имя/путь"
	}
	if m.selectedColumn == columnCommand {
		column = "команда"
	}
	return m.statusStyle.Render(fmt.Sprintf("%s   A добавить   F источник   E статус   Ячейка: %s   Изменений: %d   Новых: %d", save, column, changes, creates))
}

func (m model) renderAdd(width, height int) string {
	lines := []string{
		m.titleStyle.Render("Добавление автозапуска"),
		m.mutedStyle.Render("Esc отменить, Enter дальше"),
		"",
	}
	switch m.adding {
	case addModeKind:
		run := "Run"
		runOnce := "RunOnce"
		if m.addKind == core.AutostartRegistryKeyRun {
			run = m.focusStyle.Render("Run")
		} else {
			runOnce = m.focusStyle.Render("RunOnce")
		}
		lines = append(lines,
			"Выберите тип записи:",
			fmt.Sprintf("%s  %s", run, runOnce),
			m.mutedStyle.Render("1 Run   2 RunOnce   Left/Right переключить"),
		)
	case addModePath:
		lines = append(lines, "Полный путь до exe или lnk:", m.pathInput.View())
	case addModeArgs:
		lines = append(lines, "Параметры запуска:", m.argsInput.View())
	}
	if m.errText != "" {
		lines = append(lines, "", m.errorStyle.Render(m.errText))
	}
	panel := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("#5F6F52")).Padding(1, 2).Width(min(90, max(40, width-8))).Render(strings.Join(lines, "\n"))
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, panel)
}

func (m model) visibleRows() int {
	rows := m.height - 8
	if rows < 6 {
		return 6
	}
	return rows
}

func (m model) listTop() int {
	return 5
}

func (m model) startScan() tea.Cmd {
	scanner := m.scanner
	progressCh := m.scanCh
	return func() tea.Msg {
		if scanner == nil {
			close(progressCh)
			return scanDoneMsg{}
		}
		entries, err := scanner(func(progress core.AutostartScanProgress) {
			progressCh <- scanProgressMsg{Area: progress.Area, Found: progress.Found}
		})
		close(progressCh)
		return scanDoneMsg{entries: entries, err: err}
	}
}

func waitScanProgress(progressCh <-chan scanProgressMsg) tea.Cmd {
	return func() tea.Msg {
		if progressCh == nil {
			return scanProgressClosedMsg{}
		}
		msg, ok := <-progressCh
		if !ok {
			return scanProgressClosedMsg{}
		}
		return msg
	}
}

func (m model) pasteIntoFocusedInput() (tea.Model, tea.Cmd) {
	if m.clipboardRead == nil {
		return m, nil
	}
	text, err := m.clipboardRead()
	if err != nil {
		m.errText = fmt.Sprintf("Не удалось вставить из буфера: %v", err)
		return m, nil
	}
	if m.adding == addModePath {
		text = cleanAddedPath(text)
		appendToInput(&m.pathInput, text)
	}
	if m.adding == addModeArgs {
		appendToInput(&m.argsInput, text)
	}
	m.errText = ""
	return m, nil
}

func appendToInput(input *textinput.Model, text string) {
	pos := input.Position()
	value := input.Value()
	if pos < 0 || pos > len(value) {
		pos = len(value)
	}
	input.SetValue(value[:pos] + text + value[pos:])
	input.SetCursor(pos + len(text))
}

func cleanAddedPath(value string) string {
	return strings.Trim(strings.TrimSpace(value), `"`)
}

func sourceForRegistryKey(key core.AutostartRegistryKey) core.AutostartSource {
	if key == core.AutostartRegistryKeyRunOnce {
		return core.AutostartSourceRegistryRunOnce
	}
	return core.AutostartSourceRegistryRun
}

func sourceLabel(entry core.AutostartEntry) string {
	scope := "User"
	if entry.Scope == core.AutostartScopeMachine {
		scope = "Machine"
	}
	switch entry.Source {
	case core.AutostartSourceRegistryRun:
		return scope + " Run"
	case core.AutostartSourceRegistryRunOnce:
		return scope + " RunOnce"
	case core.AutostartSourceStartupFolder:
		return scope + " Startup"
	case core.AutostartSourceScheduledTask:
		return "Task Scheduler"
	default:
		return string(entry.Source)
	}
}

func sourceFilterLabel(source core.AutostartSource) string {
	if source == "" {
		return "все"
	}
	return sourceLabel(core.AutostartEntry{Source: source})
}

func statusFilterLabel(filter statusFilter) string {
	switch filter {
	case statusFilterEnabled:
		return "включенные"
	case statusFilterDisabled:
		return "выключенные"
	default:
		return "все"
	}
}

func truncate(value string, width int) string {
	value = strings.ReplaceAll(value, "\n", " ")
	if len(value) <= width {
		return value
	}
	if width <= 3 {
		return value[:max(0, width)]
	}
	return value[:width-3] + "..."
}

func wrapText(value string, width int) string {
	if width <= 0 {
		return value
	}
	var lines []string
	for _, rawLine := range strings.Split(value, "\n") {
		line := strings.TrimSpace(rawLine)
		for len(line) > width {
			lines = append(lines, line[:width])
			line = line[width:]
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

func nonEmpty(values []string) []string {
	out := values[:0]
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			out = append(out, value)
		}
	}
	return out
}
