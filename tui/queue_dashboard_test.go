package tui

import (
	"goMH/taskqueue"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

func TestDashboardRefreshFiltersCompletedAndSelectsRunningTask(t *testing.T) {
	model := newTestDashboardModel()
	model.focus = 1
	model.tasks = []dashboardTask{
		{Snapshot: taskqueue.TaskSnapshot{ID: "done", State: taskqueue.TaskSuccess}},
	}

	updatedModel, _ := model.Update(refreshMsg{
		tasks: []dashboardTask{
			{Snapshot: taskqueue.TaskSnapshot{ID: "done", State: taskqueue.TaskSuccess}},
			{Snapshot: taskqueue.TaskSnapshot{ID: "queued", State: taskqueue.TaskQueued}},
			{Snapshot: taskqueue.TaskSnapshot{ID: "running", State: taskqueue.TaskRunning}},
		},
		queueStarted: true,
	})

	updated := updatedModel.(dashboardModel)
	if len(updated.tasks) != 2 {
		t.Fatalf("ожидалось 2 видимые задачи, получено %d", len(updated.tasks))
	}
	if updated.tasks[0].Snapshot.ID != "queued" || updated.tasks[1].Snapshot.ID != "running" {
		t.Fatalf("неожиданный состав очереди: %+v", updated.tasks)
	}
	if updated.currentTask() == nil || updated.currentTask().Snapshot.ID != "running" {
		t.Fatalf("ожидалось переключение на активную задачу, получено %+v", updated.currentTask())
	}
	if updated.focus != 1 {
		t.Fatalf("ожидался фокус на очереди, получено %d", updated.focus)
	}
}

func TestDashboardEnterOnQueueDoesNotEnqueue(t *testing.T) {
	model := newTestDashboardModel()
	calls := 0
	model.focus = 1
	model.tasks = []dashboardTask{
		{Snapshot: taskqueue.TaskSnapshot{ID: "queued", State: taskqueue.TaskQueued}},
	}
	model.controller.OnEnqueue = func(module DashboardModule) (DashboardActionResult, error) {
		calls++
		return DashboardActionResult{SelectTaskID: module.ID}, nil
	}

	updatedModel, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	updated := updatedModel.(dashboardModel)

	if cmd != nil {
		t.Fatal("команда запуска не должна создаваться при Enter на панели очереди")
	}
	if calls != 0 {
		t.Fatalf("обработчик меню не должен вызываться, вызовов: %d", calls)
	}
	if updated.busy {
		t.Fatal("модель не должна переходить в busy-состояние")
	}
}

func TestDashboardRefreshKeepsManualQueueSelection(t *testing.T) {
	model := newTestDashboardModel()
	model.focus = 1
	model.tasks = []dashboardTask{
		{Snapshot: taskqueue.TaskSnapshot{ID: "running", State: taskqueue.TaskRunning}},
		{Snapshot: taskqueue.TaskSnapshot{ID: "queued", State: taskqueue.TaskQueued}},
	}
	model.taskIndex = 1

	updatedModel, _ := model.Update(refreshMsg{
		tasks: []dashboardTask{
			{Snapshot: taskqueue.TaskSnapshot{ID: "running", State: taskqueue.TaskRunning}},
			{Snapshot: taskqueue.TaskSnapshot{ID: "queued", State: taskqueue.TaskQueued}},
			{Snapshot: taskqueue.TaskSnapshot{ID: "queued-2", State: taskqueue.TaskQueued}},
		},
		queueStarted: true,
	})

	updated := updatedModel.(dashboardModel)
	if updated.currentTask() == nil {
		t.Fatal("ожидалась выбранная задача после refresh")
	}
	if updated.currentTask().Snapshot.ID != "queued" {
		t.Fatalf("ручной выбор не должен сбрасываться на выполняемую задачу, получено %q", updated.currentTask().Snapshot.ID)
	}
	if updated.taskIndex != 1 {
		t.Fatalf("ожидался сохраненный индекс выбранной задачи, получено %d", updated.taskIndex)
	}
}

func TestDashboardDigitShortcutStartsModule(t *testing.T) {
	model := newTestDashboardModel()
	calls := 0
	lastModuleID := ""
	model.focus = 1
	model.controller.OnEnqueue = func(module DashboardModule) (DashboardActionResult, error) {
		calls++
		lastModuleID = module.ID
		return DashboardActionResult{SelectTaskID: module.ID}, nil
	}

	updatedModel, cmd := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'2'}})
	updated := updatedModel.(dashboardModel)

	if !updated.busy {
		t.Fatal("модель должна перейти в busy-состояние после цифрового запуска")
	}
	if updated.focus != 0 {
		t.Fatalf("ожидался фокус на меню, получено %d", updated.focus)
	}
	if updated.moduleIndex != 1 {
		t.Fatalf("ожидался переход на модуль с индексом 1, получено %d", updated.moduleIndex)
	}
	if cmd == nil {
		t.Fatal("ожидалась команда запуска модуля")
	}

	msg := cmd()
	done, ok := msg.(actionDoneMsg)
	if !ok {
		t.Fatalf("ожидалось сообщение actionDoneMsg, получено %T", msg)
	}
	if calls != 1 {
		t.Fatalf("ожидался один вызов обработчика, получено %d", calls)
	}
	if lastModuleID != "m2" {
		t.Fatalf("ожидался запуск модуля m2, получено %s", lastModuleID)
	}
	if done.selectTaskID != "m2" {
		t.Fatalf("ожидался selectTaskID m2, получено %s", done.selectTaskID)
	}
}

func TestDashboardMouseHoverAndQueueClick(t *testing.T) {
	model := newTestDashboardModel()
	model.tasks = []dashboardTask{
		{Snapshot: taskqueue.TaskSnapshot{ID: "t1", State: taskqueue.TaskQueued}},
		{Snapshot: taskqueue.TaskSnapshot{ID: "t2", State: taskqueue.TaskQueued}},
	}

	modulesRect := model.modulesRect()
	hoverMsg := tea.MouseMsg{
		X:      modulesRect.x,
		Y:      modulesRect.y + 1,
		Action: tea.MouseActionMotion,
		Button: tea.MouseButtonNone,
	}
	hoveredModel, _ := model.Update(hoverMsg)
	hovered := hoveredModel.(dashboardModel)

	if hovered.focus != 0 {
		t.Fatalf("ожидался фокус на меню после наведения, получено %d", hovered.focus)
	}
	if hovered.moduleIndex != 1 {
		t.Fatalf("ожидался выбор второго пункта меню, получено %d", hovered.moduleIndex)
	}

	queueRect := hovered.queueRect()
	clickMsg := tea.MouseMsg{
		X:      queueRect.x,
		Y:      queueRect.y + 3,
		Action: tea.MouseActionPress,
		Button: tea.MouseButtonLeft,
	}
	clickedModel, cmd := hovered.Update(clickMsg)
	clicked := clickedModel.(dashboardModel)

	if cmd != nil {
		t.Fatal("клик по очереди не должен запускать команду меню")
	}
	if clicked.focus != 1 {
		t.Fatalf("ожидался фокус на очереди после клика, получено %d", clicked.focus)
	}
	if clicked.taskIndex != 1 {
		t.Fatalf("ожидался выбор второй задачи, получено %d", clicked.taskIndex)
	}
}

func TestDashboardViewFitsWindowHeightWithTwoLineFooter(t *testing.T) {
	defer ShutdownLiveLogOverlay()

	model := newTestDashboardModel()
	model.tasks = []dashboardTask{
		{Snapshot: taskqueue.TaskSnapshot{ID: "t1", Title: "Первая", State: taskqueue.TaskQueued, StageText: "Ожидание"}},
	}

	view := model.View()
	if height := lipgloss.Height(view); height > model.height {
		t.Fatalf("view не должен выходить за высоту окна: %d > %d", height, model.height)
	}
}

func TestDashboardFooterShowsLiveLogHintWhenOverlayHidden(t *testing.T) {
	defer ShutdownLiveLogOverlay()

	globalLiveLogOverlay.mu.Lock()
	globalLiveLogOverlay.filePath = `C:\logs\goMH.log`
	globalLiveLogOverlay.unreadCount = 2
	globalLiveLogOverlay.visible = false
	globalLiveLogOverlay.mu.Unlock()

	model := newTestDashboardModel()
	footer := model.renderFooter()

	if !strings.Contains(footer, "Shift+Tab Просмотр лога [goMH.log - 2 новых строк]") {
		t.Fatalf("ожидалась подсказка о запущенном просмотрщике, получено: %s", footer)
	}
}

func TestDashboardShiftTabOpensGlobalLiveLogOverlay(t *testing.T) {
	defer ShutdownLiveLogOverlay()

	globalLiveLogOverlay.mu.Lock()
	globalLiveLogOverlay.filePath = `C:\logs\goMH.log`
	globalLiveLogOverlay.visible = false
	globalLiveLogOverlay.mu.Unlock()

	model := newTestDashboardModel()
	updatedModel, cmd := model.Update(tea.KeyMsg{Type: tea.KeyShiftTab})
	updated := updatedModel.(dashboardModel)

	if cmd != nil {
		t.Fatal("переключение overlay не должно запускать дополнительную команду")
	}
	if !liveLogOverlayVisible() {
		t.Fatal("overlay должен открываться по Shift+Tab")
	}
	if updated.moduleIndex != model.moduleIndex {
		t.Fatalf("overlay не должен менять выбор меню, получено %d", updated.moduleIndex)
	}
}

func TestDashboardShiftTabOpensTaskLogOverlayFromQueue(t *testing.T) {
	model := newTestDashboardModel()
	model.focus = 1
	model.tasks = []dashboardTask{
		{
			Snapshot: taskqueue.TaskSnapshot{ID: "task-1", Title: "Установка iikoFront", State: taskqueue.TaskRunning},
			Logs:     []string{"строка 1", "строка 2", "строка 3"},
		},
	}

	updatedModel, cmd := model.Update(tea.KeyMsg{Type: tea.KeyShiftTab})
	updated := updatedModel.(dashboardModel)

	if cmd != nil {
		t.Fatal("открытие task-log overlay не должно запускать дополнительную команду")
	}
	if !updated.taskLogOverlay.visible {
		t.Fatal("task-log overlay должен открываться по Shift+Tab из панели очереди")
	}
	if updated.taskLogOverlay.taskID != "task-1" {
		t.Fatalf("overlay должен быть привязан к выбранной задаче, получено %q", updated.taskLogOverlay.taskID)
	}
	if len(updated.taskLogOverlay.lines) != 3 {
		t.Fatalf("overlay должен содержать полный лог задачи, получено %d строк", len(updated.taskLogOverlay.lines))
	}
}

func TestDashboardRefreshSyncsTaskLogOverlayLines(t *testing.T) {
	model := newTestDashboardModel()
	model.focus = 1
	model.openTaskLogOverlay(dashboardTask{
		Snapshot: taskqueue.TaskSnapshot{ID: "task-1", Title: "Установка iikoFront", State: taskqueue.TaskRunning},
		Logs:     []string{"строка 1"},
	})

	updatedModel, _ := model.Update(refreshMsg{
		tasks: []dashboardTask{
			{
				Snapshot: taskqueue.TaskSnapshot{ID: "task-1", Title: "Установка iikoFront", State: taskqueue.TaskRunning},
				Logs:     []string{"строка 1", "строка 2", "строка 3"},
			},
		},
		queueStarted: true,
	})
	updated := updatedModel.(dashboardModel)

	if len(updated.taskLogOverlay.lines) != 3 {
		t.Fatalf("expected synced task log lines, got %d", len(updated.taskLogOverlay.lines))
	}
}

func newTestDashboardModel() dashboardModel {
	return dashboardModel{
		modules: []DashboardModule{
			{ID: "m1", Title: "Модуль 1"},
			{ID: "m2", Title: "Модуль 2"},
			{ID: "m3", Title: "Модуль 3"},
		},
		runInteractive: func(fn func() (DashboardActionResult, error)) tea.Cmd {
			return func() tea.Msg {
				result, err := fn()
				return actionDoneMsg{
					note:         result.Note,
					err:          err,
					selectTaskID: result.SelectTaskID,
				}
			}
		},
		width:  120,
		height: 30,
		panelStyle: lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			Padding(0, 1),
		focusStyle: lipgloss.NewStyle().Bold(true),
		mutedStyle: lipgloss.NewStyle(),
		keyStyle:   lipgloss.NewStyle(),
		errorStyle: lipgloss.NewStyle(),
		statusStyle: lipgloss.NewStyle().
			Padding(0, 1),
	}
}
