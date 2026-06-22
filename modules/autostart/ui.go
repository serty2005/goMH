package autostart

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"goMH/core"

	"github.com/atotto/clipboard"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type addMode int

const (
	addModeNone              addMode = iota
	addModePath                       // шаг 1: путь до exe/lnk
	addModeCheckingElevation          // ожидание async-проверки прав
	addModeScope                      // шаг 2a: HKCU/HKLM (только если не нужна задача)
	addModeKind                       // шаг 2b: Run/RunOnce (только если не нужна задача)
	addModeArgs                       // шаг 3: аргументы
)

type elevationChecker func(exePath string) (bool, error)

type elevationCheckMsg struct {
	needsTask bool
	err       error
}

type statusFilter int

const (
	statusFilterAll statusFilter = iota
	statusFilterEnabled
	statusFilterDisabled
)

const (
	colorDefault lipgloss.Color = "#5F6F52"
	colorFocus   lipgloss.Color = "#A3B18A"
	colorEdited  lipgloss.Color = "#7DD3FC"
	colorCopied  lipgloss.Color = "#E7B10A"
)

type detailState struct {
	visible bool
	title   string
	body    string
}

type editField int

const (
	editFieldNone editField = iota
	editFieldName
	editFieldPath
	editFieldArgs
)

type confirmItemKind int

const (
	confirmItemChange confirmItemKind = iota
	confirmItemCreate
	confirmItemTaskCreate
	confirmItemEdit
)

type confirmItem struct {
	Kind     confirmItemKind
	Label    string
	Selected bool
	Change   core.AutostartChange
	Create   core.AutostartCreateRequest
	Edit     core.AutostartEdit
}

type copiedEditFieldBlinkMsg struct {
	field     editField
	remaining int
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
	entries         []core.AutostartEntry
	original        map[string]bool
	originalEntry   map[string]core.AutostartEntry
	creates         []core.AutostartCreateRequest
	edits           []core.AutostartEdit
	cursor          int
	scroll          int
	width           int
	height          int
	scanning        bool
	scanner         autostartScanner
	scanCh          chan scanProgressMsg
	scanArea        string
	scanFound       int
	adding           addMode
	addScope         core.AutostartScope
	addKind          core.AutostartRegistryKey
	addNeedsTask     bool
	elevationChecker elevationChecker
	taskCreates      []core.AutostartCreateRequest
	pathInput       textinput.Model
	argsInput       textinput.Model
	searchInput     textinput.Model
	searching       bool
	searchQuery     string
	clipboardRead   func() (string, error)
	clipboardWrite  func(string) error
	sourceFilter    core.AutostartSource
	statusFilter    statusFilter
	detail          detailState
	editing         bool
	editIndex       int
	editField       editField
	editNameInput   textinput.Model
	editPathInput   textinput.Model
	editArgsInput   textinput.Model
	copiedEditField editField
	copiedBlinkOn   bool
	confirming      bool
	confirmCursor   int
	confirmItems    []confirmItem
	result          Config
	saved           bool
	cancelled       bool
	errText         string
	titleStyle      lipgloss.Style
	headerStyle     lipgloss.Style
	focusStyle      lipgloss.Style
	mutedStyle      lipgloss.Style
	keyStyle        lipgloss.Style
	errorStyle      lipgloss.Style
	statusStyle     lipgloss.Style
}

func runUI(scanner autostartScanner, checkElevation elevationChecker) (*Config, error) {
	program := tea.NewProgram(newScanningModel(scanner, checkElevation), tea.WithAltScreen(), tea.WithMouseCellMotion())
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

func newScanningModel(scanner autostartScanner, checkElevation elevationChecker) model {
	mdl := newModel(nil)
	mdl.scanning = true
	mdl.scanner = scanner
	mdl.scanCh = make(chan scanProgressMsg, 8)
	mdl.scanArea = "Подготовка сканирования"
	mdl.elevationChecker = checkElevation
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
	editNameInput := textinput.New()
	editNameInput.Placeholder = "Имя записи"
	editNameInput.CharLimit = 256
	editPathInput := textinput.New()
	editPathInput.Placeholder = `C:\Path\app.exe`
	editPathInput.CharLimit = 1024
	editArgsInput := textinput.New()
	editArgsInput.Placeholder = "--argument value"
	editArgsInput.CharLimit = 1024

	original := make(map[string]bool, len(entries))
	originalEntry := make(map[string]core.AutostartEntry, len(entries))
	for _, entry := range entries {
		original[entry.ID] = entry.Enabled
		originalEntry[entry.ID] = entry
	}
	return model{
		entries:        append([]core.AutostartEntry(nil), entries...),
		original:       original,
		originalEntry:  originalEntry,
		addKind:        core.AutostartRegistryKeyRun,
		pathInput:      pathInput,
		argsInput:      argsInput,
		searchInput:    searchInput,
		clipboardRead:  clipboard.ReadAll,
		clipboardWrite: clipboard.WriteAll,
		statusFilter:   statusFilterAll,
		editIndex:      -1,
		editNameInput:  editNameInput,
		editPathInput:  editPathInput,
		editArgsInput:  editArgsInput,
		titleStyle:     lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#F3E9D2")),
		headerStyle:    lipgloss.NewStyle().Foreground(lipgloss.Color("#DAD7CD")).Bold(true),
		focusStyle:     lipgloss.NewStyle().Foreground(lipgloss.Color("#FAF8F1")).Background(lipgloss.Color("#3A4D39")).Bold(true),
		mutedStyle:     lipgloss.NewStyle().Foreground(lipgloss.Color("#BFCBA8")),
		keyStyle:       lipgloss.NewStyle().Foreground(lipgloss.Color("#E7B10A")).Bold(true),
		errorStyle:     lipgloss.NewStyle().Foreground(lipgloss.Color("#F56C6C")),
		statusStyle:    lipgloss.NewStyle().Foreground(lipgloss.Color("#FAF8F1")).Background(lipgloss.Color("#3A4D39")).Padding(0, 1),
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
	case elevationCheckMsg:
		if m.adding != addModeCheckingElevation {
			return m, nil
		}
		if msg.err == nil && msg.needsTask {
			m.addNeedsTask = true
			m.adding = addModeArgs
			m.argsInput.Focus()
			return m, textinput.Blink
		}
		m.addNeedsTask = false
		m.adding = addModeScope
		return m, nil
	case copiedEditFieldBlinkMsg:
		if m.copiedEditField != msg.field {
			return m, nil
		}
		if msg.remaining <= 0 {
			m.copiedEditField = editFieldNone
			m.copiedBlinkOn = false
			return m, nil
		}
		m.copiedBlinkOn = !m.copiedBlinkOn
		return m, m.copiedBlinkCmd(msg.field, msg.remaining-1)
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
		if m.confirming {
			return m.updateConfirmMouse(msg)
		}
		if m.editing {
			return m.updateEditMouse(msg)
		}
		if m.adding != addModeNone {
			return m.updateAddMouse(msg)
		}
		if msg.Button == tea.MouseButtonWheelDown {
			m.scrollBy(1)
			return m, nil
		}
		if msg.Button == tea.MouseButtonWheelUp {
			m.scrollBy(-1)
			return m, nil
		}
		if msg.Action == tea.MouseActionMotion {
			m.selectRowAt(msg.Y)
			return m, nil
		}
		if msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonLeft {
			row := msg.Y - m.listTop()
			if row >= 0 && row < m.visibleRows() {
				index := m.scroll + row
				if index >= 0 && index < len(m.filteredEntries()) {
					m.cursor = index
					if m.isStatusColumnX(msg.X) {
						m.toggleCurrent()
					} else {
						m.startEditCurrent()
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
		if m.confirming {
			return m.updateConfirm(msg)
		}
		if m.editing {
			return m.updateEdit(msg)
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
		case "enter":
			m.startEditCurrent()
			return m, nil
		case " ":
			m.toggleCurrent()
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
			m.startConfirm()
			return m, nil
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
	if m.confirming {
		return m.renderConfirm(width, height)
	}
	if m.editing {
		return m.renderEdit(width, height)
	}
	if m.detail.visible {
		return m.renderDetail(width, height)
	}
	lines := []string{
		m.titleStyle.Render("Управление автозапуском"),
		m.mutedStyle.Render("Space включить/выключить, Enter редактировать, F источник, E статус, A добавить, S сохранить, Esc назад"),
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
	case addModePath:
		switch msg.String() {
		case "ctrl+c", "esc":
			m.adding = addModeNone
			m.pathInput.Blur()
			return m, nil
		case "ctrl+v":
			return m.pasteIntoFocusedInput()
		case "enter":
			path := cleanAddedPath(m.pathInput.Value())
			if path == "" {
				m.errText = "Укажите путь до exe или lnk."
				return m, nil
			}
			m.errText = ""
			m.pathInput.Blur()
			if m.elevationChecker != nil && !strings.EqualFold(filepath.Ext(path), ".lnk") {
				m.adding = addModeCheckingElevation
				return m, checkElevationCmd(m.elevationChecker, path)
			}
			m.addNeedsTask = false
			m.adding = addModeScope
			return m, nil
		}
		var cmd tea.Cmd
		m.pathInput, cmd = m.pathInput.Update(msg)
		return m, cmd
	case addModeCheckingElevation:
		if msg.String() == "ctrl+c" || msg.String() == "esc" {
			m.adding = addModeNone
			return m, nil
		}
		return m, nil
	case addModeScope:
		switch msg.String() {
		case "ctrl+c", "esc":
			m.adding = addModePath
			m.pathInput.Focus()
			return m, textinput.Blink
		case "left", "up", "1":
			m.addScope = core.AutostartScopeUser
			return m, nil
		case "right", "down", "2":
			m.addScope = core.AutostartScopeMachine
			return m, nil
		case "tab", " ":
			if m.addScope == core.AutostartScopeUser {
				m.addScope = core.AutostartScopeMachine
			} else {
				m.addScope = core.AutostartScopeUser
			}
			return m, nil
		case "enter":
			m.adding = addModeKind
			return m, nil
		}
	case addModeKind:
		switch msg.String() {
		case "ctrl+c", "esc":
			m.adding = addModeScope
			return m, nil
		case "left", "up":
			m.addKind = core.AutostartRegistryKeyRun
			return m, nil
		case "right", "down":
			m.addKind = core.AutostartRegistryKeyRunOnce
			return m, nil
		case "tab", " ":
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
			m.adding = addModeArgs
			m.argsInput.Focus()
			return m, textinput.Blink
		}
	case addModeArgs:
		switch msg.String() {
		case "ctrl+c", "esc":
			m.argsInput.Blur()
			if m.addNeedsTask {
				m.adding = addModePath
				m.pathInput.Focus()
				return m, textinput.Blink
			}
			m.adding = addModeKind
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

func checkElevationCmd(checker elevationChecker, path string) tea.Cmd {
	return func() tea.Msg {
		needs, err := checker(path)
		return elevationCheckMsg{needsTask: needs, err: err}
	}
}

func (m model) updateAddMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	if m.adding == addModeScope {
		if msg.Action != tea.MouseActionPress || msg.Button != tea.MouseButtonLeft {
			return m, nil
		}
		switch {
		case m.addScopeHit(msg.X, msg.Y, core.AutostartScopeUser):
			m.addScope = core.AutostartScopeUser
		case m.addScopeHit(msg.X, msg.Y, core.AutostartScopeMachine):
			m.addScope = core.AutostartScopeMachine
		}
		return m, nil
	}
	if m.adding == addModeKind {
		if msg.Action != tea.MouseActionPress || msg.Button != tea.MouseButtonLeft {
			return m, nil
		}
		switch {
		case m.addKindHit(msg.X, msg.Y, core.AutostartRegistryKeyRun):
			m.addKind = core.AutostartRegistryKeyRun
		case m.addKindHit(msg.X, msg.Y, core.AutostartRegistryKeyRunOnce):
			m.addKind = core.AutostartRegistryKeyRunOnce
		}
		return m, nil
	}
	if msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonRight {
		return m.pasteIntoFocusedInput()
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

func (m model) updateConfirm(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c", "c":
		m.cancelled = true
		return m, tea.Quit
	case "esc", "b":
		m.confirming = false
		m.errText = ""
		return m, nil
	case "up", "k":
		m.moveConfirm(-1)
		return m, nil
	case "down", "j":
		m.moveConfirm(1)
		return m, nil
	case " ":
		m.toggleConfirmCurrent()
		return m, nil
	case "enter", "s":
		return m.saveConfirmed()
	}
	return m, nil
}

func (m model) updateConfirmMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	if msg.Button == tea.MouseButtonWheelDown {
		m.moveConfirm(1)
		return m, nil
	}
	if msg.Button == tea.MouseButtonWheelUp {
		m.moveConfirm(-1)
		return m, nil
	}
	if msg.Action != tea.MouseActionPress || msg.Button != tea.MouseButtonLeft {
		return m, nil
	}
	row := msg.Y - m.confirmListTop()
	if row < 0 || row >= len(m.confirmItems) {
		return m, nil
	}
	m.confirmCursor = row
	if msg.X <= 4 {
		m.toggleConfirmCurrent()
	}
	return m, nil
}

func (m model) updateEdit(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c":
		return m.copyEditField(m.editField)
	case "esc":
		m.stopEdit()
		return m, nil
	case "tab", "down":
		m.focusEditField(m.nextEditField(1))
		return m, textinput.Blink
	case "shift+tab", "up":
		m.focusEditField(m.nextEditField(-1))
		return m, textinput.Blink
	case "ctrl+s":
		m.finishEdit()
		return m, nil
	}
	var cmd tea.Cmd
	switch m.editField {
	case editFieldName:
		m.editNameInput, cmd = m.editNameInput.Update(msg)
	case editFieldPath:
		m.editPathInput, cmd = m.editPathInput.Update(msg)
	case editFieldArgs:
		m.editArgsInput, cmd = m.editArgsInput.Update(msg)
	}
	return m, cmd
}

func (m model) updateEditMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	if msg.Action != tea.MouseActionPress {
		return m, nil
	}
	field := m.editFieldAtY(msg.Y)
	if field == editFieldNone {
		return m, nil
	}
	if msg.Button == tea.MouseButtonRight {
		return m.copyEditField(field)
	}
	if msg.Button == tea.MouseButtonLeft {
		m.focusEditField(field)
		return m, textinput.Blink
	}
	return m, nil
}

func (m *model) startAdd() {
	m.adding = addModePath
	m.addScope = core.AutostartScopeUser
	m.addKind = core.AutostartRegistryKeyRun
	m.addNeedsTask = false
	m.pathInput.SetValue("")
	m.argsInput.SetValue("")
	m.pathInput.Focus()
	m.errText = ""
}

func (m *model) startSearch() {
	m.searching = true
	m.searchInput.SetValue(m.searchQuery)
	m.searchInput.Focus()
	m.errText = ""
}

func (m *model) startEditCurrent() {
	index := m.currentEntryIndex()
	if index < 0 || index >= len(m.entries) {
		return
	}
	entry := m.entries[index]
	m.editing = true
	m.editIndex = index
	m.editNameInput.SetValue(entry.Name)
	path := entry.TargetPath
	if path == "" && entry.Source == core.AutostartSourceStartupFolder {
		path = entry.FilePath
	}
	if path == "" {
		path = entry.Command
	}
	m.editPathInput.SetValue(path)
	m.editArgsInput.SetValue(entry.Arguments)
	m.focusEditField(editFieldName)
	m.copiedEditField = editFieldNone
	m.errText = ""
}

func (m *model) stopEdit() {
	m.editing = false
	m.editIndex = -1
	m.editNameInput.Blur()
	m.editPathInput.Blur()
	m.editArgsInput.Blur()
	m.copiedEditField = editFieldNone
	m.errText = ""
}

func (m *model) finishEdit() {
	if m.editIndex < 0 || m.editIndex >= len(m.entries) {
		m.stopEdit()
		return
	}
	entry := m.entries[m.editIndex]
	if !canEditEntry(entry) {
		m.errText = "Для этого источника сейчас доступно только включение/выключение."
		return
	}
	name := strings.TrimSpace(m.editNameInput.Value())
	path := cleanAddedPath(m.editPathInput.Value())
	args := strings.TrimSpace(m.editArgsInput.Value())
	if name == "" {
		m.errText = "Укажите имя записи."
		return
	}
	if path == "" {
		m.errText = "Укажите путь запуска."
		return
	}
	original := m.originalEntry[entry.ID]
	if original.ID == "" {
		original = entry
	}
	edit := core.AutostartEdit{
		Original:         original,
		Name:             name,
		Path:             path,
		Arguments:        args,
		WorkingDirectory: entry.WorkingDirectory,
	}
	m.stageEdit(edit)
	m.entries[m.editIndex].Name = name
	m.entries[m.editIndex].TargetPath = path
	m.entries[m.editIndex].Arguments = args
	m.entries[m.editIndex].Command = strings.TrimSpace(path + " " + args)
	m.entries[m.editIndex].RegistryValue = name
	m.stopEdit()
}

func (m *model) stageEdit(edit core.AutostartEdit) {
	for index, existing := range m.edits {
		if existing.Original.ID == edit.Original.ID {
			m.edits[index] = edit
			return
		}
	}
	m.edits = append(m.edits, edit)
}

func (m *model) focusEditField(field editField) {
	m.editField = field
	m.editNameInput.Blur()
	m.editPathInput.Blur()
	m.editArgsInput.Blur()
	switch field {
	case editFieldName:
		m.editNameInput.Focus()
	case editFieldPath:
		m.editPathInput.Focus()
	case editFieldArgs:
		m.editArgsInput.Focus()
	}
}

func (m model) nextEditField(delta int) editField {
	fields := []editField{editFieldName, editFieldPath, editFieldArgs}
	current := 0
	for index, field := range fields {
		if field == m.editField {
			current = index
			break
		}
	}
	current += delta
	if current < 0 {
		current = len(fields) - 1
	}
	if current >= len(fields) {
		current = 0
	}
	return fields[current]
}

func (m *model) finishAdd() {
	path := cleanAddedPath(m.pathInput.Value())
	if path == "" {
		m.errText = "Укажите путь до exe или lnk."
		return
	}
	name := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	args := strings.TrimSpace(m.argsInput.Value())
	req := core.AutostartCreateRequest{
		Name:      name,
		Path:      path,
		Arguments: args,
	}
	if m.addNeedsTask {
		m.taskCreates = append(m.taskCreates, req)
		m.entries = append(m.entries, core.AutostartEntry{
			ID:       fmt.Sprintf("task|%d", len(m.taskCreates)-1),
			Name:     name,
			Source:   core.AutostartSourceScheduledTask,
			Enabled:  true,
			Command:  strings.TrimSpace(path + " " + args),
			CanToggle: false,
		})
	} else {
		req.RegistryKey = m.addKind
		req.Scope = m.addScope
		m.creates = append(m.creates, req)
		m.entries = append(m.entries, core.AutostartEntry{
			ID:          fmt.Sprintf("new|%d", len(m.creates)-1),
			Name:        name,
			Source:      sourceForRegistryKey(m.addKind),
			Scope:       m.addScope,
			Enabled:     true,
			Command:     strings.TrimSpace(path + " " + args),
			RegistryKey: m.addKind,
			CanToggle:   false,
		})
	}
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

func (m *model) scrollBy(delta int) {
	count := len(m.filteredEntries())
	if count == 0 {
		m.scroll = 0
		m.cursor = 0
		return
	}
	maxScroll := max(0, count-m.visibleRows())
	m.scroll += delta
	if m.scroll < 0 {
		m.scroll = 0
	}
	if m.scroll > maxScroll {
		m.scroll = maxScroll
	}
	if m.cursor < m.scroll {
		m.cursor = m.scroll
	}
	if m.cursor >= m.scroll+m.visibleRows() {
		m.cursor = min(count-1, m.scroll+m.visibleRows()-1)
	}
}

func (m *model) selectRowAt(y int) {
	row := y - m.listTop()
	if row < 0 || row >= m.visibleRows() {
		return
	}
	index := m.scroll + row
	if index < 0 || index >= len(m.filteredEntries()) {
		return
	}
	m.cursor = index
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
	return len(m.creates) > 0 || len(m.taskCreates) > 0 || len(m.edits) > 0 || len(m.changes()) > 0
}

func (m model) changes() []core.AutostartChange {
	changes := make([]core.AutostartChange, 0)
	for _, entry := range m.entries {
		original, ok := m.original[entry.ID]
		if !ok || original == entry.Enabled {
			continue
		}
		originalEntry := m.originalEntry[entry.ID]
		if originalEntry.ID == "" {
			originalEntry = entry
			originalEntry.Enabled = original
		}
		changes = append(changes, core.AutostartChange{Entry: originalEntry, Enabled: entry.Enabled})
	}
	return changes
}

func (m *model) startConfirm() {
	m.confirmItems = m.buildConfirmItems()
	m.confirming = true
	m.confirmCursor = 0
	m.errText = ""
}

func (m model) buildConfirmItems() []confirmItem {
	items := make([]confirmItem, 0, len(m.changes())+len(m.creates)+len(m.taskCreates)+len(m.edits))
	for _, change := range m.changes() {
		action := "выключить"
		if change.Enabled {
			action = "включить"
		}
		items = append(items, confirmItem{
			Kind:     confirmItemChange,
			Label:    fmt.Sprintf("%s: %s", action, change.Entry.Name),
			Selected: true,
			Change:   change,
		})
	}
	for _, create := range m.creates {
		items = append(items, confirmItem{
			Kind:     confirmItemCreate,
			Label:    fmt.Sprintf("создать: %s (%s)", create.Name, create.RegistryKey),
			Selected: true,
			Create:   create,
		})
	}
	for _, create := range m.taskCreates {
		items = append(items, confirmItem{
			Kind:     confirmItemTaskCreate,
			Label:    fmt.Sprintf("создать задачу планировщика: %s", create.Name),
			Selected: true,
			Create:   create,
		})
	}
	for _, edit := range m.edits {
		items = append(items, confirmItem{
			Kind:     confirmItemEdit,
			Label:    fmt.Sprintf("изменить: %s", edit.Name),
			Selected: true,
			Edit:     edit,
		})
	}
	return items
}

func (m *model) moveConfirm(delta int) {
	if len(m.confirmItems) == 0 {
		m.confirmCursor = 0
		return
	}
	m.confirmCursor += delta
	if m.confirmCursor < 0 {
		m.confirmCursor = 0
	}
	if m.confirmCursor >= len(m.confirmItems) {
		m.confirmCursor = len(m.confirmItems) - 1
	}
}

func (m *model) toggleConfirmCurrent() {
	if m.confirmCursor < 0 || m.confirmCursor >= len(m.confirmItems) {
		return
	}
	m.confirmItems[m.confirmCursor].Selected = !m.confirmItems[m.confirmCursor].Selected
}

func (m model) saveConfirmed() (tea.Model, tea.Cmd) {
	var result Config
	for _, item := range m.confirmItems {
		if !item.Selected {
			continue
		}
		switch item.Kind {
		case confirmItemChange:
			result.Changes = append(result.Changes, item.Change)
		case confirmItemCreate:
			result.Creates = append(result.Creates, item.Create)
		case confirmItemTaskCreate:
			result.TaskCreates = append(result.TaskCreates, item.Create)
		case confirmItemEdit:
			result.Edits = append(result.Edits, item.Edit)
		}
	}
	if len(result.Changes) == 0 && len(result.Creates) == 0 && len(result.TaskCreates) == 0 && len(result.Edits) == 0 {
		m.errText = "Выберите хотя бы одно изменение для сохранения."
		return m, nil
	}
	m.result = result
	m.saved = true
	return m, tea.Quit
}

func (m *model) setEntries(entries []core.AutostartEntry) {
	m.entries = append([]core.AutostartEntry(nil), entries...)
	m.original = make(map[string]bool, len(entries))
	m.originalEntry = make(map[string]core.AutostartEntry, len(entries))
	for _, entry := range entries {
		m.original[entry.ID] = entry.Enabled
		m.originalEntry[entry.ID] = entry
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

func (m model) copyEditField(field editField) (tea.Model, tea.Cmd) {
	if field == editFieldNone || m.clipboardWrite == nil {
		return m, nil
	}
	value := m.editFieldValue(field)
	if err := m.clipboardWrite(value); err != nil {
		m.errText = fmt.Sprintf("Не удалось скопировать: %v", err)
		return m, nil
	}
	m.copiedEditField = field
	m.copiedBlinkOn = true
	m.errText = ""
	return m, m.copiedBlinkCmd(field, 4)
}

func (m model) copiedBlinkCmd(field editField, remaining int) tea.Cmd {
	return tea.Tick(180*time.Millisecond, func(time.Time) tea.Msg {
		return copiedEditFieldBlinkMsg{field: field, remaining: remaining}
	})
}

func (m model) editFieldValue(field editField) string {
	switch field {
	case editFieldName:
		return m.editNameInput.Value()
	case editFieldPath:
		return m.editPathInput.Value()
	case editFieldArgs:
		return m.editArgsInput.Value()
	default:
		return ""
	}
}

func (m model) editFieldAtY(y int) editField {
	for _, field := range []editField{editFieldName, editFieldPath, editFieldArgs} {
		fieldY := m.editFieldY(field)
		if y >= fieldY && y <= fieldY+2 {
			return field
		}
	}
	return editFieldNone
}

func (m model) editFieldY(field editField) int {
	top := m.editPanelTop()
	switch field {
	case editFieldName:
		return top + 5
	case editFieldPath:
		return top + 8
	case editFieldArgs:
		return top + 11
	default:
		return -1
	}
}

func (m model) editPanelTop() int {
	height := m.height
	if height <= 0 {
		height = 28
	}
	panelHeight := 16
	if height <= panelHeight {
		return 0
	}
	return (height - panelHeight) / 2
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

func (m model) renderEdit(width, height int) string {
	entry := core.AutostartEntry{}
	if m.editIndex >= 0 && m.editIndex < len(m.entries) {
		entry = m.entries[m.editIndex]
	}
	lines := []string{
		m.titleStyle.Render("Редактирование: " + editModeLabel(entry)),
		m.mutedStyle.Render("Ctrl+S сохранить, Esc назад, правый клик копирует поле"),
		"",
		"Имя",
		m.renderEditField(editFieldName, m.editNameInput.View(), width),
		"Путь",
		m.renderEditField(editFieldPath, m.editPathInput.View(), width),
		"Аргументы",
		m.renderEditField(editFieldArgs, m.editArgsInput.View(), width),
	}
	if !canEditEntry(entry) {
		lines = append(lines, "", m.mutedStyle.Render("Этот источник пока открыт только для просмотра и копирования полей."))
	}
	if m.errText != "" {
		lines = append(lines, "", m.errorStyle.Render(m.errText))
	}
	panel := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("#5F6F52")).Padding(1, 2).Width(min(100, max(44, width-8))).Render(strings.Join(lines, "\n"))
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, panel)
}

func (m model) renderEditField(field editField, value string, width int) string {
	color := m.editFieldBorderColor(field)
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(color).
		Padding(0, 1).
		Width(min(88, max(30, width-16))).
		Render(value)
}

func (m model) editFieldBorderColor(field editField) lipgloss.Color {
	if m.copiedEditField == field && m.copiedBlinkOn {
		return colorCopied
	}
	if m.editFieldChanged(field) {
		return colorEdited
	}
	if m.editField == field {
		return colorFocus
	}
	return colorDefault
}

func (m model) editFieldChanged(field editField) bool {
	if m.editIndex < 0 || m.editIndex >= len(m.entries) {
		return false
	}
	entry := m.entries[m.editIndex]
	switch field {
	case editFieldName:
		return strings.TrimSpace(m.editNameInput.Value()) != entry.Name
	case editFieldPath:
		originalPath := entry.TargetPath
		if originalPath == "" && entry.Source == core.AutostartSourceStartupFolder {
			originalPath = entry.FilePath
		}
		if originalPath == "" {
			originalPath = entry.Command
		}
		return cleanAddedPath(m.editPathInput.Value()) != originalPath
	case editFieldArgs:
		return strings.TrimSpace(m.editArgsInput.Value()) != entry.Arguments
	default:
		return false
	}
}

func (m model) renderConfirm(width, height int) string {
	lines := []string{
		m.titleStyle.Render("Подтверждение изменений"),
		m.mutedStyle.Render("Space выбрать, Enter/S применить, Esc назад, C отменить"),
		"",
	}
	for index, item := range m.confirmItems {
		check := "[ ]"
		if item.Selected {
			check = "[x]"
		}
		line := fmt.Sprintf("%s %s", check, item.Label)
		if index == m.confirmCursor {
			line = m.focusStyle.Width(max(1, min(100, width-10))).Render(line)
		}
		lines = append(lines, line)
	}
	if len(m.confirmItems) == 0 {
		lines = append(lines, m.mutedStyle.Render("Нет изменений для сохранения."))
	}
	if m.errText != "" {
		lines = append(lines, "", m.errorStyle.Render(m.errText))
	}
	panel := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("#5F6F52")).Padding(1, 2).Width(min(110, max(44, width-8))).Render(strings.Join(lines, "\n"))
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

func (m model) isStatusColumnX(x int) bool {
	nameWidth := 30
	sourceWidth := 16
	statusWidth := 8
	start := nameWidth + 2 + sourceWidth + 2
	return x >= start && x < start+statusWidth
}

func (m model) renderStatus() string {
	changes := len(m.changes())
	creates := len(m.creates) + len(m.taskCreates)
	save := "S сохранить"
	if !m.dirty() {
		save = "S сохранить (нет изменений)"
	}
	return m.statusStyle.Render(fmt.Sprintf("%s   A добавить   F источник   E статус   Space статус   Enter редактировать   Изменений: %d   Новых: %d", save, changes, creates))
}

func (m model) renderAdd(width, height int) string {
	lines := []string{
		m.titleStyle.Render("Добавление автозапуска"),
		m.mutedStyle.Render("Esc назад, Enter дальше"),
		"",
	}
	switch m.adding {
	case addModePath:
		lines = append(lines, "Полный путь до exe или lnk:", m.pathInput.View())
	case addModeCheckingElevation:
		lines = append(lines,
			"Проверка требований к правам запуска...",
			m.mutedStyle.Render("Пожалуйста, подождите. Esc отменить."),
		)
	case addModeScope:
		user := "Текущий пользователь (HKCU)"
		machine := "Все пользователи (HKLM)"
		if m.addScope == core.AutostartScopeUser {
			user = m.focusStyle.Render(user)
		} else {
			machine = m.focusStyle.Render(machine)
		}
		lines = append(lines,
			"Выберите область действия:",
			fmt.Sprintf("%s  %s", user, machine),
			m.mutedStyle.Render("1 Текущий пользователь   2 Все пользователи   Tab переключить"),
		)
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
			m.mutedStyle.Render("1 Run   2 RunOnce   Tab/Space переключить"),
		)
	case addModeArgs:
		lines = append(lines, "Параметры запуска:", m.argsInput.View())
		if m.addNeedsTask {
			lines = append(lines, m.mutedStyle.Render("Программа требует прав администратора — будет создана задача планировщика."))
		}
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

func (m model) confirmListTop() int {
	return 3
}

func (m model) addScopeOptionsY() int {
	return m.addPanelTop() + 6
}

func (m model) addScopeXRange(scope core.AutostartScope) (int, int) {
	left := m.addPanelLeft() + 3
	switch scope {
	case core.AutostartScopeMachine:
		return left + 30, left + 53
	default:
		return left, left + 28
	}
}

func (m model) addScopeHit(x, y int, scope core.AutostartScope) bool {
	if y != m.addScopeOptionsY() {
		return false
	}
	start, end := m.addScopeXRange(scope)
	return x >= start && x < end
}

func (m model) addKindOptionsY() int {
	return m.addPanelTop() + 6
}

func (m model) addKindHit(x, y int, key core.AutostartRegistryKey) bool {
	if y != m.addKindOptionsY() {
		return false
	}
	start, end := m.addKindXRange(key)
	return x >= start && x < end
}

func (m model) addKindXRange(key core.AutostartRegistryKey) (int, int) {
	left := m.addPanelLeft() + 3
	switch key {
	case core.AutostartRegistryKeyRunOnce:
		return left + 5, left + 12
	default:
		return left, left + 3
	}
}

func (m model) addPanelLeft() int {
	width := m.width
	if width <= 0 {
		width = 120
	}
	panelWidth := min(90, max(40, width-8))
	return max(0, (width-panelWidth)/2)
}

func (m model) addPanelTop() int {
	height := m.height
	if height <= 0 {
		height = 28
	}
	panelHeight := 10
	if height <= panelHeight {
		return 0
	}
	return (height - panelHeight) / 2
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

func canEditEntry(entry core.AutostartEntry) bool {
	return entry.Source == core.AutostartSourceRegistryRun || entry.Source == core.AutostartSourceRegistryRunOnce
}

func editModeLabel(entry core.AutostartEntry) string {
	switch entry.Source {
	case core.AutostartSourceRegistryRun:
		return "Run"
	case core.AutostartSourceRegistryRunOnce:
		return "RunOnce"
	case core.AutostartSourceStartupFolder:
		return "Startup folder"
	case core.AutostartSourceScheduledTask:
		return "Scheduled Task"
	default:
		return sourceLabel(entry)
	}
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
