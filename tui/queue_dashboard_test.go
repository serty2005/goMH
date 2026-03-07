package tui

import (
	"goMH/taskqueue"
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
	model := newTestDashboardModel()
	model.tasks = []dashboardTask{
		{Snapshot: taskqueue.TaskSnapshot{ID: "t1", Title: "Первая", State: taskqueue.TaskQueued, StageText: "Ожидание"}},
	}

	view := model.View()
	if height := lipgloss.Height(view); height > model.height {
		t.Fatalf("view не должен выходить за высоту окна: %d > %d", height, model.height)
	}
}

func newTestDashboardModel() dashboardModel {
	return dashboardModel{
		modules: []DashboardModule{
			{ID: "m1", Title: "Модуль 1"},
			{ID: "m2", Title: "Модуль 2"},
			{ID: "m3", Title: "Модуль 3"},
		},
		runOutsideUI: func(fn func() error) error {
			return fn()
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
