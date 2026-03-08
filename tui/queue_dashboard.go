package tui

import (
	"fmt"
	"goMH/taskqueue"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type DashboardModule struct {
	ID    string
	Title string
}

type DashboardActionResult struct {
	Note         string
	SelectTaskID string
}

type DashboardController struct {
	LoadTasks       func() ([]taskqueue.TaskSnapshot, map[string][]string)
	OnEnqueue       func(module DashboardModule) (DashboardActionResult, error)
	OnRemoveTask    func(taskID string) (string, error)
	OnClearFinished func() (string, error)
	IsQueueStarted  func() bool
}

type dashboardTask struct {
	Snapshot taskqueue.TaskSnapshot
	Logs     []string
}

type refreshMsg struct {
	tasks        []dashboardTask
	queueStarted bool
}

type actionDoneMsg struct {
	note         string
	err          error
	selectTaskID string
}

type refreshTickMsg struct{}
type spinnerTickMsg struct{}

type dashboardRect struct {
	x      int
	y      int
	width  int
	height int
}

type dashboardLayout struct {
	modules dashboardRect
	queue   dashboardRect
}

type dashboardModel struct {
	modules       []DashboardModule
	controller    DashboardController
	runOutsideUI  func(fn func() error) error
	tasks         []dashboardTask
	moduleIndex   int
	taskIndex     int
	focus         int
	width         int
	height        int
	busy          bool
	lastMessage   string
	lastError     string
	pendingTaskID string
	queueStarted  bool
	spinnerIndex  int
	spinnerFrames []string
	titleStyle    lipgloss.Style
	panelStyle    lipgloss.Style
	focusStyle    lipgloss.Style
	mutedStyle    lipgloss.Style
	keyStyle      lipgloss.Style
	errorStyle    lipgloss.Style
	statusStyle   lipgloss.Style
}

func RunQueueDashboard(modules []DashboardModule, controller DashboardController) error {
	var program *tea.Program

	model := dashboardModel{
		modules:    modules,
		controller: controller,
		runOutsideUI: func(fn func() error) error {
			if program == nil {
				return fn()
			}
			if err := program.ReleaseTerminal(); err != nil {
				return err
			}
			defer program.RestoreTerminal()
			return fn()
		},
		spinnerFrames: []string{"|", "/", "-", "\\"},
		titleStyle: lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#F3E9D2")),
		panelStyle: lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("#5F6F52")).
			Padding(0, 1),
		focusStyle: lipgloss.NewStyle().
			Foreground(lipgloss.Color("#FAF8F1")).
			Background(lipgloss.Color("#3A4D39")).
			Bold(true),
		mutedStyle: lipgloss.NewStyle().
			Foreground(lipgloss.Color("#BFCBA8")),
		keyStyle: lipgloss.NewStyle().
			Foreground(lipgloss.Color("#E7B10A")).
			Bold(true),
		errorStyle: lipgloss.NewStyle().
			Foreground(lipgloss.Color("#F56C6C")),
		statusStyle: lipgloss.NewStyle().
			Foreground(lipgloss.Color("#FAF8F1")).
			Background(lipgloss.Color("#3A4D39")).
			Padding(0, 1),
	}

	program = tea.NewProgram(model, tea.WithAltScreen(), tea.WithMouseAllMotion())
	_, err := program.Run()
	return err
}

func (m dashboardModel) Init() tea.Cmd {
	return tea.Batch(tea.WindowSize(), m.fetchTasks(), refreshTickCmd(), spinnerTickCmd())
}

func (m dashboardModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil

	case refreshTickMsg:
		return m, tea.Batch(m.fetchTasks(), refreshTickCmd())

	case spinnerTickMsg:
		if len(m.spinnerFrames) > 0 {
			m.spinnerIndex = (m.spinnerIndex + 1) % len(m.spinnerFrames)
		}
		return m, spinnerTickCmd()

	case refreshMsg:
		return m.handleRefresh(msg)

	case actionDoneMsg:
		return m.handleActionDone(msg)

	case tea.MouseMsg:
		if m.busy {
			return m, nil
		}
		return m.handleMouse(msg)

	case tea.KeyMsg:
		return m.handleKey(msg)
	}

	return m, nil
}

func (m dashboardModel) View() string {
	if m.width <= 0 || m.height <= 0 {
		return ""
	}

	status := m.renderStatusLine()
	footer := m.renderFooter()
	leftWidth := max(48, ((m.width - 3) * 2 / 3))
	rightWidth := m.width - leftWidth - 3
	if rightWidth < 28 {
		rightWidth = 28
		leftWidth = max(48, m.width-rightWidth-3)
	}

	statusHeight := max(1, lipgloss.Height(status))
	footerHeight := max(1, lipgloss.Height(footer))
	_, panelFrameHeight := m.panelStyle.GetFrameSize()
	availableHeight := m.height - statusHeight - footerHeight
	if availableHeight < (panelFrameHeight+1)*2 {
		availableHeight = (panelFrameHeight + 1) * 2
	}

	topPanelsTotalHeight := availableHeight * 2 / 3
	logPanelTotalHeight := availableHeight - topPanelsTotalHeight
	minPanelTotalHeight := panelFrameHeight + 1
	if topPanelsTotalHeight < minPanelTotalHeight {
		topPanelsTotalHeight = minPanelTotalHeight
		logPanelTotalHeight = availableHeight - topPanelsTotalHeight
	}
	if logPanelTotalHeight < minPanelTotalHeight {
		logPanelTotalHeight = minPanelTotalHeight
		topPanelsTotalHeight = availableHeight - logPanelTotalHeight
	}

	topPanelsContentHeight := max(1, topPanelsTotalHeight-panelFrameHeight)
	logPanelContentHeight := max(1, logPanelTotalHeight-panelFrameHeight)

	leftPanel := m.panelStyle.Width(leftWidth).Height(topPanelsContentHeight).Render(m.renderModules(leftWidth-4, topPanelsContentHeight))
	rightPanel := m.panelStyle.Width(rightWidth).Height(topPanelsContentHeight).Render(m.renderQueue(rightWidth-4, topPanelsContentHeight))
	logPanel := m.panelStyle.Width(m.width).Height(logPanelContentHeight).Render(m.renderLogs(m.width-4, logPanelContentHeight))

	return lipgloss.JoinVertical(
		lipgloss.Left,
		lipgloss.JoinHorizontal(lipgloss.Top, leftPanel, rightPanel),
		logPanel,
		status,
		footer,
	)
}

func (m dashboardModel) fetchTasks() tea.Cmd {
	return func() tea.Msg {
		if m.controller.LoadTasks == nil {
			return refreshMsg{}
		}
		snapshots, logs := m.controller.LoadTasks()
		tasks := make([]dashboardTask, 0, len(snapshots))
		for _, snapshot := range snapshots {
			tasks = append(tasks, dashboardTask{
				Snapshot: snapshot,
				Logs:     append([]string(nil), logs[snapshot.ID]...),
			})
		}
		started := false
		if m.controller.IsQueueStarted != nil {
			started = m.controller.IsQueueStarted()
		}
		return refreshMsg{
			tasks:        tasks,
			queueStarted: started,
		}
	}
}

func (m dashboardModel) runBlocking(fn func() (DashboardActionResult, error)) tea.Cmd {
	return func() tea.Msg {
		var (
			result DashboardActionResult
			err    error
		)
		runErr := m.runOutsideUI(func() error {
			result, err = fn()
			return err
		})
		if runErr != nil {
			return actionDoneMsg{err: runErr}
		}
		return actionDoneMsg{note: result.Note, err: err, selectTaskID: result.SelectTaskID}
	}
}

func (m *dashboardModel) startEnqueueCurrentModule() (dashboardModel, tea.Cmd) {
	module := m.currentModule()
	if m.controller.OnEnqueue == nil || module.ID == "" {
		return *m, nil
	}
	m.busy = true
	m.lastMessage = "Настройка задачи..."
	m.lastError = ""
	return *m, m.runBlocking(func() (DashboardActionResult, error) {
		return m.controller.OnEnqueue(module)
	})
}

func (m dashboardModel) handleRefresh(msg refreshMsg) (dashboardModel, tea.Cmd) {
	currentTaskID := ""
	if current := m.currentTask(); current != nil {
		currentTaskID = current.Snapshot.ID
	}
	m.tasks = visibleDashboardTasks(msg.tasks)
	m.queueStarted = msg.queueStarted
	selectedTaskID, forceQueueFocus := m.resolveTaskSelection(currentTaskID)
	m.taskIndex = findTaskIndex(m.tasks, selectedTaskID)
	if forceQueueFocus && selectedTaskID != "" {
		m.focus = 1
	}
	m.normalize()
	return m, nil
}

func (m dashboardModel) handleActionDone(msg actionDoneMsg) (dashboardModel, tea.Cmd) {
	m.busy = false
	m.applyResult(msg.note, msg.err)
	if msg.err == nil && msg.selectTaskID != "" {
		m.focus = 1
		m.pendingTaskID = msg.selectTaskID
	}
	return m, m.fetchTasks()
}

func (m dashboardModel) handleKey(msg tea.KeyMsg) (dashboardModel, tea.Cmd) {
	if m.busy {
		return m.handleBusyKey(msg)
	}

	switch msg.String() {
	case "ctrl+c", "q":
		return m, tea.Quit
	case "tab", "right":
		m.moveFocus(1)
		m.normalize()
		return m, nil
	case "shift+tab", "left":
		m.moveFocus(-1)
		m.normalize()
		return m, nil
	case "up", "k":
		m.moveSelection(-1)
		return m, nil
	case "down", "j":
		m.moveSelection(1)
		return m, nil
	case "a", " ", "enter":
		if m.focus != 0 {
			return m, nil
		}
		return m.startEnqueueCurrentModule()
	case "backspace", "delete":
		return m.removeCurrentTask()
	case "c":
		return m.clearFinishedTasks()
	}

	if index, ok := m.shortcutModuleIndex(msg.String()); ok {
		m.moduleIndex = index
		m.focus = 0
		m.normalize()
		return m.startEnqueueCurrentModule()
	}

	return m, nil
}

func (m dashboardModel) handleBusyKey(msg tea.KeyMsg) (dashboardModel, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c", "q":
		return m, tea.Quit
	default:
		return m, nil
	}
}

func (m *dashboardModel) moveSelection(delta int) {
	if m.focus == 0 {
		m.moduleIndex += delta
	} else {
		m.taskIndex += delta
	}
	m.normalize()
}

func (m dashboardModel) removeCurrentTask() (dashboardModel, tea.Cmd) {
	if m.focus != 1 || m.controller.OnRemoveTask == nil {
		return m, nil
	}
	task := m.currentTask()
	if task == nil {
		return m, nil
	}
	note, err := m.controller.OnRemoveTask(task.Snapshot.ID)
	m.applyResult(note, err)
	return m, m.fetchTasks()
}

func (m dashboardModel) clearFinishedTasks() (dashboardModel, tea.Cmd) {
	if m.controller.OnClearFinished == nil {
		return m, nil
	}
	note, err := m.controller.OnClearFinished()
	m.applyResult(note, err)
	return m, m.fetchTasks()
}

func (m *dashboardModel) applyResult(note string, err error) {
	if err != nil {
		m.lastError = err.Error()
		m.lastMessage = ""
		return
	}
	m.lastMessage = note
	m.lastError = ""
}

func (m dashboardModel) renderModules(width int, height int) string {
	lines := make([]string, 0, max(1, height))
	start, end := visibleRange(m.moduleIndex, len(m.modules), max(1, height))
	for i := start; i < end; i++ {
		line := truncateText(m.formatModuleLine(i, start, width), width)
		if i == m.moduleIndex && m.focus == 0 {
			line = m.focusStyle.Width(width).Render(line)
		} else if i == m.moduleIndex {
			line = lipgloss.NewStyle().Bold(true).Render(line)
		}
		lines = append(lines, line)
	}
	for len(lines) < height {
		lines = append(lines, "")
	}
	return strings.Join(lines, "\n")
}

func (m dashboardModel) renderRunningStatus(width int) []string {
	running := m.runningTask()
	if running != nil {
		title := fmt.Sprintf("%s Выполняется: %s", m.spinnerFrame(), running.Snapshot.Title)
		stage := running.Snapshot.StageText
		if running.Snapshot.Progress > 0 {
			stage = fmt.Sprintf("%s [%d%%]", stage, running.Snapshot.Progress)
		}
		return []string{
			m.statusStyle.Width(width).Render(truncateText(title, width)),
			truncateText(stage, width),
		}
	}

	if m.queueStarted {
		return []string{
			m.statusStyle.Width(width).Render("Очередь работает в фоне"),
			m.mutedStyle.Render("Выберите модуль и поставьте задачу."),
		}
	}

	return []string{
		m.statusStyle.Width(width).Render("Фоновая очередь не запущена"),
		m.mutedStyle.Render("Нажмите S, чтобы включить обработку очереди."),
	}
}

func (m dashboardModel) renderQueue(width int, height int) string {
	lines := make([]string, 0, max(1, height))
	if len(m.tasks) == 0 {
		lines = append(lines, m.mutedStyle.Render("Очередь пуста."))
		for len(lines) < height {
			lines = append(lines, "")
		}
		return strings.Join(lines, "\n")
	}

	start, end := visibleRange(m.taskIndex, len(m.tasks), max(1, height/3))
	for i := start; i < end; i++ {
		task := m.tasks[i]
		if task.Snapshot.State == taskqueue.TaskRunning && task.Snapshot.Progress > 0 {
			bar := renderProgressBar(width, task.Snapshot.Progress)
			if i == m.taskIndex && m.focus == 1 {
				bar = m.focusStyle.Width(width).Render(bar)
			} else if i == m.taskIndex {
				bar = lipgloss.NewStyle().Bold(true).Render(bar)
			}
			lines = append(lines, bar)
		} else {
			line := fmt.Sprintf("%s %s", formatTaskState(task.Snapshot.State), truncateText(task.Snapshot.Title, width-7))
			if i == m.taskIndex && m.focus == 1 {
				line = m.focusStyle.Width(width).Render(line)
			} else if i == m.taskIndex {
				line = lipgloss.NewStyle().Bold(true).Render(line)
			}
			lines = append(lines, line)
		}

		stage := truncateText(task.Snapshot.StageText, width)
		if task.Snapshot.LastError != "" {
			stage = m.errorStyle.Render(truncateText(task.Snapshot.LastError, width))
		} else {
			stage = m.mutedStyle.Render(stage)
		}
		lines = append(lines, stage)
		lines = append(lines, "")
	}
	if len(lines) > height {
		lines = lines[:height]
	}
	for len(lines) < height {
		lines = append(lines, "")
	}

	return strings.Join(lines, "\n")
}

func (m dashboardModel) renderStatusLine() string {
	text := " "
	if m.lastError != "" {
		text = "Ошибка: " + m.lastError
		return m.errorStyle.Width(m.width).Render(truncateText(text, m.width))
	}
	if m.lastMessage != "" || m.busy {
		text := m.lastMessage
		if m.busy {
			text = m.spinnerFrame() + " " + text
		}
		return m.mutedStyle.Width(m.width).Render(truncateText(text, m.width))
	}
	return m.mutedStyle.Width(m.width).Render(text)
}

func (m dashboardModel) renderFooter() string {
	firstLine := []string{
		m.keyStyle.Render("Tab") + " сменить панель",
		m.keyStyle.Render("1-9,0") + " быстрый запуск",
		m.keyStyle.Render("Enter") + " добавить",
	}
	secondLine := []string{
		m.keyStyle.Render("Del") + " удалить/остановить скачивание",
		m.keyStyle.Render("Мышь") + " выбор/клик",
		m.keyStyle.Render("C") + " очистить",
		m.keyStyle.Render("Q") + " выход",
	}
	helpStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#BFCBA8")).
		Width(m.width)
	return strings.Join([]string{
		helpStyle.Render(strings.Join(firstLine, "   ")),
		helpStyle.Render(strings.Join(secondLine, "   ")),
	}, "\n")
}

func (m dashboardModel) renderLogs(width int, height int) string {
	lines := []string{"Лог"}
	task := m.currentTask()
	if task == nil {
		lines = append(lines, m.mutedStyle.Render("Выберите задачу в очереди."))
	} else {
		lines = append(lines, m.mutedStyle.Render(truncateText(task.Snapshot.Title, width)))
		for _, line := range lastLines(task.Logs, 4) {
			lines = append(lines, truncateText(line, width))
		}
	}
	if len(lines) > height {
		lines = lines[:height]
	}
	for len(lines) < height {
		lines = append(lines, "")
	}
	return strings.Join(lines, "\n")
}

func (m *dashboardModel) normalize() {
	if m.focus < 0 || m.focus > 1 {
		m.focus = 0
	}
	if len(m.modules) == 0 {
		m.moduleIndex = 0
	} else {
		if m.moduleIndex < 0 {
			m.moduleIndex = 0
		}
		if m.moduleIndex >= len(m.modules) {
			m.moduleIndex = len(m.modules) - 1
		}
	}

	if len(m.tasks) == 0 {
		m.taskIndex = 0
		if m.focus == 1 {
			m.focus = 0
		}
		return
	}
	if m.taskIndex < 0 {
		m.taskIndex = 0
	}
	if m.taskIndex >= len(m.tasks) {
		m.taskIndex = len(m.tasks) - 1
	}
}

func (m dashboardModel) currentModule() DashboardModule {
	if len(m.modules) == 0 {
		return DashboardModule{}
	}
	return m.modules[m.moduleIndex]
}

func (m dashboardModel) currentTask() *dashboardTask {
	if len(m.tasks) == 0 {
		return nil
	}
	return &m.tasks[m.taskIndex]
}

func (m dashboardModel) runningTask() *dashboardTask {
	for i := range m.tasks {
		if m.tasks[i].Snapshot.State == taskqueue.TaskRunning {
			return &m.tasks[i]
		}
	}
	return nil
}

func (m dashboardModel) queueStats() (queued int, running int, success int) {
	for _, task := range m.tasks {
		switch task.Snapshot.State {
		case taskqueue.TaskQueued:
			queued++
		case taskqueue.TaskRunning:
			running++
		case taskqueue.TaskSuccess:
			success++
		}
	}
	return
}

func (m dashboardModel) spinnerFrame() string {
	if len(m.spinnerFrames) == 0 {
		return "*"
	}
	return m.spinnerFrames[m.spinnerIndex%len(m.spinnerFrames)]
}

func (m *dashboardModel) moveFocus(delta int) {
	if delta == 0 {
		return
	}
	if len(m.tasks) == 0 {
		m.focus = 0
		return
	}
	if delta > 0 {
		m.focus = (m.focus + 1) % 2
		return
	}
	m.focus--
	if m.focus < 0 {
		m.focus = 1
	}
}

func (m dashboardModel) shortcutModuleIndex(key string) (int, bool) {
	start, end := visibleRange(m.moduleIndex, len(m.modules), max(1, m.modulesRect().height))
	for pos := start; pos < end && pos < start+len(menuShortcutKeys); pos++ {
		if menuShortcutKeys[pos-start] == key {
			return pos, true
		}
	}
	return 0, false
}

func (m dashboardModel) formatModuleLine(index int, start int, width int) string {
	label := menuShortcutLabel(index - start)
	titleWidth := width - len(label) - 1
	if titleWidth < 0 {
		titleWidth = 0
	}
	return strings.TrimSpace(label + " " + truncateText(m.modules[index].Title, titleWidth))
}

func (m dashboardModel) handleMouse(msg tea.MouseMsg) (dashboardModel, tea.Cmd) {
	switch msg.Action {
	case tea.MouseActionMotion:
		if index, ok := m.moduleIndexAt(msg.X, msg.Y); ok {
			m.moduleIndex = index
			m.focus = 0
			m.normalize()
			return m, nil
		}
		if index, ok := m.taskIndexAt(msg.X, msg.Y); ok {
			m.taskIndex = index
			m.focus = 1
			m.normalize()
			return m, nil
		}
	case tea.MouseActionPress:
		if msg.Button != tea.MouseButtonLeft {
			return m, nil
		}
		if index, ok := m.moduleIndexAt(msg.X, msg.Y); ok {
			m.moduleIndex = index
			m.focus = 0
			m.normalize()
			return m.startEnqueueCurrentModule()
		}
		if index, ok := m.taskIndexAt(msg.X, msg.Y); ok {
			m.taskIndex = index
			m.focus = 1
			m.normalize()
			return m, nil
		}
	}
	return m, nil
}

func (m dashboardModel) moduleIndexAt(x int, y int) (int, bool) {
	rect := m.modulesRect()
	if !rect.contains(x, y) {
		return 0, false
	}
	start, end := visibleRange(m.moduleIndex, len(m.modules), max(1, rect.height))
	index := start + (y - rect.y)
	if index < start || index >= end {
		return 0, false
	}
	return index, true
}

func (m dashboardModel) taskIndexAt(x int, y int) (int, bool) {
	rect := m.queueRect()
	if !rect.contains(x, y) {
		return 0, false
	}
	start, end := visibleRange(m.taskIndex, len(m.tasks), max(1, rect.height/3))
	line := y - rect.y
	maxLine := (end - start) * 3
	if line < 0 || line >= maxLine {
		return 0, false
	}
	index := start + line/3
	if index < start || index >= end {
		return 0, false
	}
	return index, true
}

func (m dashboardModel) modulesRect() dashboardRect {
	return m.layout().modules
}

func (m dashboardModel) queueRect() dashboardRect {
	return m.layout().queue
}

func (m dashboardModel) layout() dashboardLayout {
	if m.width <= 0 || m.height <= 0 {
		return dashboardLayout{}
	}

	statusHeight := max(1, lipgloss.Height(m.renderStatusLine()))
	footerHeight := max(1, lipgloss.Height(m.renderFooter()))
	leftWidth := max(48, ((m.width - 3) * 2 / 3))
	rightWidth := m.width - leftWidth - 3
	if rightWidth < 28 {
		rightWidth = 28
		leftWidth = max(48, m.width-rightWidth-3)
	}

	panelFrameWidth, panelFrameHeight := m.panelStyle.GetFrameSize()
	availableHeight := m.height - statusHeight - footerHeight
	if availableHeight < (panelFrameHeight+1)*2 {
		availableHeight = (panelFrameHeight + 1) * 2
	}

	topPanelsTotalHeight := availableHeight * 2 / 3
	logPanelTotalHeight := availableHeight - topPanelsTotalHeight
	minPanelTotalHeight := panelFrameHeight + 1
	if topPanelsTotalHeight < minPanelTotalHeight {
		topPanelsTotalHeight = minPanelTotalHeight
		logPanelTotalHeight = availableHeight - topPanelsTotalHeight
	}
	if logPanelTotalHeight < minPanelTotalHeight {
		logPanelTotalHeight = minPanelTotalHeight
		topPanelsTotalHeight = availableHeight - logPanelTotalHeight
	}

	contentInsetX := panelFrameWidth / 2
	contentInsetY := panelFrameHeight / 2
	contentHeight := max(1, topPanelsTotalHeight-panelFrameHeight)

	return dashboardLayout{
		modules: dashboardRect{
			x:      contentInsetX,
			y:      contentInsetY,
			width:  leftWidth - panelFrameWidth,
			height: contentHeight,
		},
		queue: dashboardRect{
			x:      leftWidth + contentInsetX,
			y:      contentInsetY,
			width:  rightWidth - panelFrameWidth,
			height: contentHeight,
		},
	}
}

func (r dashboardRect) contains(x int, y int) bool {
	return x >= r.x && x < r.x+r.width && y >= r.y && y < r.y+r.height
}

func visibleDashboardTasks(tasks []dashboardTask) []dashboardTask {
	filtered := make([]dashboardTask, 0, len(tasks))
	for _, task := range tasks {
		if task.Snapshot.State == taskqueue.TaskSuccess {
			continue
		}
		filtered = append(filtered, task)
	}
	return filtered
}

func (m *dashboardModel) resolveTaskSelection(currentTaskID string) (string, bool) {
	if m.pendingTaskID != "" {
		taskID := m.pendingTaskID
		m.pendingTaskID = ""
		if hasTaskWithID(m.tasks, taskID) {
			return taskID, true
		}
	}

	running := firstTaskWithState(m.tasks, taskqueue.TaskRunning)
	if running != nil {
		if currentTaskID != running.Snapshot.ID {
			return running.Snapshot.ID, currentTaskID != ""
		}
		return running.Snapshot.ID, false
	}

	if currentTaskID != "" && hasTaskWithID(m.tasks, currentTaskID) {
		return currentTaskID, false
	}

	if len(m.tasks) > 0 {
		return m.tasks[0].Snapshot.ID, currentTaskID != ""
	}

	return "", false
}

func hasTaskWithID(tasks []dashboardTask, taskID string) bool {
	return findTaskIndex(tasks, taskID) >= 0
}

func firstTaskWithState(tasks []dashboardTask, state taskqueue.TaskState) *dashboardTask {
	for i := range tasks {
		if tasks[i].Snapshot.State == state {
			return &tasks[i]
		}
	}
	return nil
}

func formatTaskState(state taskqueue.TaskState) string {
	switch state {
	case taskqueue.TaskQueued:
		return "•"
	case taskqueue.TaskRunning:
		return ">"
	case taskqueue.TaskSuccess:
		return "+"
	case taskqueue.TaskFailed:
		return "!"
	default:
		return "?"
	}
}

func renderProgressBar(width int, percent int) string {
	if width < 12 {
		return truncateText(fmt.Sprintf("%d%%", percent), width)
	}

	barWidth := width - 7
	if barWidth < 10 {
		barWidth = 10
	}
	filled := (barWidth * percent) / 100
	if filled > barWidth {
		filled = barWidth
	}
	bar := strings.Repeat("#", filled) + strings.Repeat("-", barWidth-filled)
	return fmt.Sprintf("%3d%% [%s]", percent, bar)
}

func refreshTickCmd() tea.Cmd {
	return tea.Tick(250*time.Millisecond, func(time.Time) tea.Msg {
		return refreshTickMsg{}
	})
}

func spinnerTickCmd() tea.Cmd {
	return tea.Tick(120*time.Millisecond, func(time.Time) tea.Msg {
		return spinnerTickMsg{}
	})
}

func lastLines(lines []string, limit int) []string {
	if len(lines) <= limit {
		return lines
	}
	return lines[len(lines)-limit:]
}

func truncateText(text string, width int) string {
	if width <= 0 {
		return ""
	}
	runes := []rune(text)
	if len(runes) <= width {
		return text
	}
	if width <= 3 {
		return string(runes[:width])
	}
	return string(runes[:width-3]) + "..."
}

func findTaskIndex(tasks []dashboardTask, selectedID string) int {
	if selectedID == "" {
		if len(tasks) == 0 {
			return 0
		}
		return 0
	}
	for i, task := range tasks {
		if task.Snapshot.ID == selectedID {
			return i
		}
	}
	return -1
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
