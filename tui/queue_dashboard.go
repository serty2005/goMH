package tui

import (
	"context"
	"errors"
	"fmt"
	"goMH/logstream"
	"goMH/taskqueue"
	"io"
	"path/filepath"
	"strings"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

const DashboardLogViewerModuleID = "__dashboard_log_viewer__"

type DashboardModule struct {
	ID    string
	Title string
}

type DashboardActionResult struct {
	Note            string
	SelectTaskID    string
	LogViewFilePath string
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
	note            string
	err             error
	selectTaskID    string
	LogViewFilePath string
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

type dashboardLogViewerState struct {
	service     *logstream.Service
	handle      *logstream.Handle
	sink        *dashboardLogSink
	filePath    string
	lines       []string
	visible     bool
	paused      bool
	scrollTop   int
	unreadCount int
	lastError   string
}

type dashboardModel struct {
	modules        []DashboardModule
	controller     DashboardController
	runInteractive func(fn func() (DashboardActionResult, error)) tea.Cmd
	tasks          []dashboardTask
	moduleIndex    int
	taskIndex      int
	focus          int
	width          int
	height         int
	busy           bool
	lastMessage    string
	lastError      string
	pendingTaskID  string
	queueStarted   bool
	logViewer      dashboardLogViewerState
	spinnerIndex   int
	spinnerFrames  []string
	titleStyle     lipgloss.Style
	panelStyle     lipgloss.Style
	focusStyle     lipgloss.Style
	mutedStyle     lipgloss.Style
	keyStyle       lipgloss.Style
	errorStyle     lipgloss.Style
	statusStyle    lipgloss.Style
}

func RunQueueDashboard(modules []DashboardModule, controller DashboardController) error {
	model := dashboardModel{
		modules:        modules,
		controller:     controller,
		runInteractive: runDashboardInteractive,
		spinnerFrames:  []string{"|", "/", "-", "\\"},
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

	program := tea.NewProgram(model, tea.WithAltScreen(), tea.WithMouseAllMotion())
	_, err := program.Run()
	ShutdownLiveLogOverlay()
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
		consumeLiveLogOverlayUpdates()
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
		if liveLogOverlayVisible() {
			return m, nil
		}
		if m.busy {
			return m, nil
		}
		return m.handleMouse(msg)

	case tea.KeyMsg:
		if handleLiveLogOverlayKey(msg, m.height) {
			return m, nil
		}
		return m.handleKey(msg)
	}

	return m, nil
}

func (m dashboardModel) View() string {
	if m.width <= 0 || m.height <= 0 {
		return ""
	}
	if liveLogOverlayVisible() {
		return renderLiveLogOverlay(m.width, m.height, LiveLogOverlayRenderStyles{
			Title:    m.titleStyle,
			Subtitle: m.mutedStyle,
			Panel:    m.panelStyle,
			Key:      m.keyStyle,
			Help:     m.mutedStyle,
			Error:    m.errorStyle,
			Status:   m.statusStyle,
			Text:     lipgloss.NewStyle(),
			Muted:    m.mutedStyle,
		})
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
	topPanelsTotalHeight, taskLogTotalHeight, viewerTotalHeight := computeDashboardHeights(availableHeight, panelFrameHeight, m.logViewer.visible)
	topPanelsContentHeight := max(1, topPanelsTotalHeight-panelFrameHeight)
	taskLogContentHeight := max(1, taskLogTotalHeight-panelFrameHeight)

	leftPanel := m.panelStyle.Width(leftWidth).Height(topPanelsContentHeight).Render(m.renderModules(leftWidth-4, topPanelsContentHeight))
	rightPanel := m.panelStyle.Width(rightWidth).Height(topPanelsContentHeight).Render(m.renderQueue(rightWidth-4, topPanelsContentHeight))
	panels := []string{
		lipgloss.JoinHorizontal(lipgloss.Top, leftPanel, rightPanel),
	}
	if m.logViewer.visible {
		viewerContentHeight := max(1, viewerTotalHeight-panelFrameHeight)
		viewerPanel := m.panelStyle.Width(m.width).Height(viewerContentHeight).Render(m.renderLiveLogViewer(m.width-4, viewerContentHeight))
		panels = append(panels, viewerPanel)
	}
	taskLogPanel := m.panelStyle.Width(m.width).Height(taskLogContentHeight).Render(m.renderLogs(m.width-4, taskLogContentHeight))
	panels = append(panels, taskLogPanel, status, footer)
	return lipgloss.JoinVertical(lipgloss.Left, panels...)
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

func (m *dashboardModel) startEnqueueCurrentModule() (dashboardModel, tea.Cmd) {
	module := m.currentModule()
	if module.ID == DashboardLogViewerModuleID && m.logViewerConfigured() {
		m.setLogViewerVisible(!m.logViewer.visible)
		if m.logViewer.visible {
			m.applyResult("Просмотр лога развернут.", nil)
		} else {
			m.applyResult("Просмотр лога свернут.", nil)
		}
		return *m, nil
	}
	if m.controller.OnEnqueue == nil || module.ID == "" {
		return *m, nil
	}
	m.busy = true
	m.lastMessage = "Настройка задачи..."
	m.lastError = ""
	if m.runInteractive == nil {
		m.runInteractive = runDashboardInteractive
	}
	return *m, m.runInteractive(func() (DashboardActionResult, error) {
		return m.controller.OnEnqueue(module)
	})
}

type dashboardInteractiveCommand struct {
	run    func() (DashboardActionResult, error)
	result DashboardActionResult
}

func (c *dashboardInteractiveCommand) Run() error {
	if c.run == nil {
		return nil
	}
	result, err := c.run()
	c.result = result
	return err
}

func (c *dashboardInteractiveCommand) SetStdin(io.Reader)  {}
func (c *dashboardInteractiveCommand) SetStdout(io.Writer) {}
func (c *dashboardInteractiveCommand) SetStderr(io.Writer) {}

func runDashboardInteractive(fn func() (DashboardActionResult, error)) tea.Cmd {
	command := &dashboardInteractiveCommand{run: fn}
	return tea.Exec(command, func(err error) tea.Msg {
		return actionDoneMsg{
			note:            command.result.Note,
			err:             err,
			selectTaskID:    command.result.SelectTaskID,
			LogViewFilePath: command.result.LogViewFilePath,
		}
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
	if m.logViewer.visible {
		return m.handleLogViewerKey(msg)
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

func (m dashboardModel) handleLogViewerKey(msg tea.KeyMsg) (dashboardModel, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c", "q":
		return m, tea.Quit
	case "esc", "enter":
		m.setLogViewerVisible(false)
		m.applyResult("Просмотр лога свернут.", nil)
		return m, nil
	case " ":
		m.logViewer.paused = !m.logViewer.paused
		if m.logViewer.paused {
			m.applyResult("Автопрокрутка лога поставлена на паузу.", nil)
		} else {
			m.applyResult("Автопрокрутка лога возобновлена.", nil)
			m.scrollLogViewerToBottom()
		}
		return m, nil
	case "up", "k":
		m.scrollLogViewer(-1)
		return m, nil
	case "down", "j":
		m.scrollLogViewer(1)
		return m, nil
	case "pgup":
		m.scrollLogViewer(-8)
		return m, nil
	case "pgdown":
		m.scrollLogViewer(8)
		return m, nil
	case "home":
		m.logViewer.scrollTop = 0
		return m, nil
	case "end":
		m.scrollLogViewerToBottom()
		return m, nil
	}

	if index, ok := m.shortcutModuleIndex(msg.String()); ok && m.modules[index].ID == DashboardLogViewerModuleID {
		m.moduleIndex = index
		m.focus = 0
		m.setLogViewerVisible(false)
		m.applyResult("Просмотр лога свернут.", nil)
		return m, nil
	}

	return m, nil
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
	helpStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#BFCBA8")).
		Width(m.width)

	if m.logViewer.visible {
		return strings.Join([]string{
			helpStyle.Render(strings.Join([]string{
				m.keyStyle.Render("Space") + " пауза/лайв",
				m.keyStyle.Render("Up/Down") + " прокрутка",
				m.keyStyle.Render("PgUp/PgDn") + " листать",
			}, "   ")),
			helpStyle.Render(strings.Join([]string{
				m.keyStyle.Render("Home/End") + " начало/конец",
				m.keyStyle.Render("Esc") + " свернуть",
				m.keyStyle.Render("Q") + " выход",
			}, "   ")),
		}, "\n")
	}

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
	lines := []string{
		helpStyle.Render(strings.Join(firstLine, "   ")),
		helpStyle.Render(strings.Join(secondLine, "   ")),
	}
	if hint := liveLogOverlayHint(); hint != "" {
		lines = append(lines, helpStyle.Render(hint))
	}
	return strings.Join(lines, "\n")
}

func (m dashboardModel) renderLiveLogViewer(width int, height int) string {
	lines := []string{"Просмотр лога"}
	if m.logViewer.filePath == "" {
		lines = append(lines, m.mutedStyle.Render("Файл для live-просмотра ещё не выбран."))
	} else {
		state := "LIVE"
		if m.logViewer.paused {
			state = "ПАУЗА"
		}
		status := fmt.Sprintf("%s | %s", filepath.Base(m.logViewer.filePath), state)
		if m.logViewer.lastError != "" {
			status += " | ошибка: " + m.logViewer.lastError
		}
		lines = append(lines, m.mutedStyle.Render(truncateText(status, width)))

		contentHeight := max(0, height-3)
		viewLines, firstLine, lastLine := m.visibleLogViewerLines(contentHeight)
		if len(viewLines) == 0 {
			lines = append(lines, m.mutedStyle.Render("Ожидание новых строк..."))
		} else {
			for _, line := range viewLines {
				lines = append(lines, truncateText(line, width))
			}
			if contentHeight > 0 {
				indicator := fmt.Sprintf("Строки %d-%d из %d", firstLine, lastLine, len(m.logViewer.lines))
				if m.logViewer.unreadCount > 0 {
					indicator += fmt.Sprintf(" | новых с момента раскрытия: %d", m.logViewer.unreadCount)
				}
				lines = append(lines, m.mutedStyle.Render(truncateText(indicator, width)))
			}
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
	m.normalizeLogViewerScroll(0)
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
	if m.modules[index].ID == DashboardLogViewerModuleID {
		return strings.TrimSpace(label + " " + truncateText(m.logViewerButtonText(titleWidth), titleWidth))
	}
	return strings.TrimSpace(label + " " + truncateText(m.modules[index].Title, titleWidth))
}

func (m dashboardModel) handleMouse(msg tea.MouseMsg) (dashboardModel, tea.Cmd) {
	if m.logViewer.visible {
		if msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonLeft {
			if index, ok := m.moduleIndexAt(msg.X, msg.Y); ok && m.modules[index].ID == DashboardLogViewerModuleID {
				m.moduleIndex = index
				m.focus = 0
				m.normalize()
				m.setLogViewerVisible(false)
				m.applyResult("Просмотр лога свернут.", nil)
			}
		}
		return m, nil
	}

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
	topPanelsTotalHeight, _, _ := computeDashboardHeights(availableHeight, panelFrameHeight, m.logViewer.visible)

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

func computeDashboardHeights(availableHeight int, panelFrameHeight int, viewerVisible bool) (int, int, int) {
	minPanelTotalHeight := panelFrameHeight + 1
	if !viewerVisible {
		if availableHeight < minPanelTotalHeight*2 {
			availableHeight = minPanelTotalHeight * 2
		}
		topPanelsTotalHeight := availableHeight * 2 / 3
		taskLogTotalHeight := availableHeight - topPanelsTotalHeight
		if topPanelsTotalHeight < minPanelTotalHeight {
			topPanelsTotalHeight = minPanelTotalHeight
			taskLogTotalHeight = availableHeight - topPanelsTotalHeight
		}
		if taskLogTotalHeight < minPanelTotalHeight {
			taskLogTotalHeight = minPanelTotalHeight
			topPanelsTotalHeight = availableHeight - taskLogTotalHeight
		}
		return topPanelsTotalHeight, taskLogTotalHeight, 0
	}

	if availableHeight < minPanelTotalHeight*3 {
		availableHeight = minPanelTotalHeight * 3
	}
	topPanelsTotalHeight := availableHeight / 2
	viewerTotalHeight := availableHeight / 3
	taskLogTotalHeight := availableHeight - topPanelsTotalHeight - viewerTotalHeight
	for taskLogTotalHeight < minPanelTotalHeight {
		if topPanelsTotalHeight > viewerTotalHeight && topPanelsTotalHeight > minPanelTotalHeight {
			topPanelsTotalHeight--
		} else if viewerTotalHeight > minPanelTotalHeight {
			viewerTotalHeight--
		} else {
			break
		}
		taskLogTotalHeight = availableHeight - topPanelsTotalHeight - viewerTotalHeight
	}
	if viewerTotalHeight < minPanelTotalHeight {
		viewerTotalHeight = minPanelTotalHeight
		topPanelsTotalHeight = availableHeight - taskLogTotalHeight - viewerTotalHeight
	}
	if topPanelsTotalHeight < minPanelTotalHeight {
		topPanelsTotalHeight = minPanelTotalHeight
		taskLogTotalHeight = availableHeight - topPanelsTotalHeight - viewerTotalHeight
	}
	return topPanelsTotalHeight, taskLogTotalHeight, viewerTotalHeight
}

func (m dashboardModel) logViewerConfigured() bool {
	return strings.TrimSpace(m.logViewer.filePath) != ""
}

func (m *dashboardModel) setLogViewerVisible(visible bool) {
	if !m.logViewerConfigured() {
		m.logViewer.visible = false
		return
	}
	m.logViewer.visible = visible
	if visible {
		m.logViewer.unreadCount = 0
		if !m.logViewer.paused {
			m.scrollLogViewerToBottom()
		}
	}
}

func (m *dashboardModel) openLogViewer(filePath string) error {
	filePath = strings.TrimSpace(filePath)
	if filePath == "" {
		return errors.New("не указан файл для просмотра")
	}
	if m.logViewer.filePath == filePath && m.logViewer.handle != nil {
		m.setLogViewerVisible(true)
		m.logViewer.lastError = ""
		return nil
	}

	m.closeLogViewer()
	service := m.logViewer.service
	if service == nil {
		service = logstream.NewService()
	}
	sink := &dashboardLogSink{}
	handle, err := service.StartTail(context.Background(), logstream.TailRequest{
		FilePath:     filePath,
		StartLines:   200,
		PollInterval: 500 * time.Millisecond,
		Sink:         sink,
	})
	if err != nil {
		return err
	}

	m.logViewer = dashboardLogViewerState{
		service:   service,
		handle:    handle,
		sink:      sink,
		filePath:  filePath,
		visible:   true,
		scrollTop: 0,
	}
	m.lastError = ""
	m.lastMessage = "Просмотр лога открыт во встроенной панели."
	return nil
}

func (m *dashboardModel) closeLogViewer() {
	if m.logViewer.handle != nil {
		m.logViewer.handle.Cancel()
	}
	m.logViewer.handle = nil
	m.logViewer.sink = nil
}

func (m *dashboardModel) consumeLogViewerUpdates() {
	if m.logViewer.sink != nil {
		newLines := m.logViewer.sink.Drain()
		if len(newLines) > 0 {
			m.logViewer.lines = append(m.logViewer.lines, newLines...)
			const maxLogViewerLines = 4000
			if len(m.logViewer.lines) > maxLogViewerLines {
				trimmed := len(m.logViewer.lines) - maxLogViewerLines
				m.logViewer.lines = append([]string(nil), m.logViewer.lines[trimmed:]...)
				m.logViewer.scrollTop = max(0, m.logViewer.scrollTop-trimmed)
			}
			if m.logViewer.visible {
				if m.logViewer.paused {
					m.normalizeLogViewerScroll(0)
				} else {
					m.scrollLogViewerToBottom()
				}
			} else {
				m.logViewer.unreadCount += len(newLines)
			}
		}
	}

	if m.logViewer.handle == nil {
		return
	}
	select {
	case <-m.logViewer.handle.Done():
		err := m.logViewer.handle.Wait()
		m.logViewer.handle = nil
		if err != nil && !errors.Is(err, context.Canceled) {
			m.logViewer.lastError = err.Error()
		}
	default:
	}
}

func (m *dashboardModel) visibleLogViewerLines(height int) ([]string, int, int) {
	if height <= 0 || len(m.logViewer.lines) == 0 {
		return nil, 0, 0
	}
	maxTop := max(0, len(m.logViewer.lines)-height)
	top := m.logViewer.scrollTop
	if top < 0 {
		top = 0
	}
	if top > maxTop {
		top = maxTop
	}
	bottom := top + height
	if bottom > len(m.logViewer.lines) {
		bottom = len(m.logViewer.lines)
	}
	return m.logViewer.lines[top:bottom], top + 1, bottom
}

func (m *dashboardModel) scrollLogViewer(delta int) {
	m.logViewer.scrollTop += delta
	m.normalizeLogViewerScroll(0)
}

func (m *dashboardModel) scrollLogViewerToBottom() {
	m.normalizeLogViewerScroll(0)
	maxTop := max(0, len(m.logViewer.lines)-m.logViewerContentHeight())
	m.logViewer.scrollTop = maxTop
}

func (m dashboardModel) logViewerContentHeight() int {
	if m.height <= 0 || !m.logViewer.visible {
		return 0
	}
	statusHeight := max(1, lipgloss.Height(m.renderStatusLine()))
	footerHeight := max(1, lipgloss.Height(m.renderFooter()))
	_, panelFrameHeight := m.panelStyle.GetFrameSize()
	availableHeight := m.height - statusHeight - footerHeight
	_, _, viewerTotalHeight := computeDashboardHeights(availableHeight, panelFrameHeight, true)
	return max(0, viewerTotalHeight-panelFrameHeight-3)
}

func (m *dashboardModel) normalizeLogViewerScroll(height int) {
	if len(m.logViewer.lines) == 0 {
		m.logViewer.scrollTop = 0
		return
	}
	if height > 0 {
		maxTop := max(0, len(m.logViewer.lines)-height)
		if m.logViewer.scrollTop > maxTop {
			m.logViewer.scrollTop = maxTop
		}
	}
	if m.logViewer.scrollTop < 0 {
		m.logViewer.scrollTop = 0
	}
	if m.logViewer.scrollTop >= len(m.logViewer.lines) {
		m.logViewer.scrollTop = len(m.logViewer.lines) - 1
	}
}

func (m dashboardModel) logViewerButtonText(width int) string {
	if !m.logViewerConfigured() {
		return truncateText("Просмотр лога [файл не выбран]", width)
	}
	action := "развернуть"
	if m.logViewer.visible {
		action = "свернуть"
	}
	status := action
	if m.logViewer.unreadCount > 0 {
		status = fmt.Sprintf("%s, +%d строк", action, m.logViewer.unreadCount)
	}
	if m.logViewer.lastError != "" {
		status = "ошибка"
	}
	text := fmt.Sprintf("Просмотр лога: %s [%s]", filepath.Base(m.logViewer.filePath), status)
	return truncateText(text, width)
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

	if currentTaskID != "" && hasTaskWithID(m.tasks, currentTaskID) {
		return currentTaskID, false
	}

	running := firstTaskWithState(m.tasks, taskqueue.TaskRunning)
	if running != nil {
		return running.Snapshot.ID, currentTaskID != ""
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

type dashboardLogSink struct {
	mu    sync.Mutex
	lines []string
}

func (s *dashboardLogSink) WriteLine(line string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lines = append(s.lines, line)
	return nil
}

func (s *dashboardLogSink) Close() error {
	return nil
}

func (s *dashboardLogSink) Drain() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.lines) == 0 {
		return nil
	}
	lines := append([]string(nil), s.lines...)
	s.lines = s.lines[:0]
	return lines
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
