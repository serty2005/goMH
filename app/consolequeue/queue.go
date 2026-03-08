package consolequeue

import (
	"context"
	"errors"
	"fmt"
	"goMH/app/modruntime"
	"goMH/config"
	"goMH/core"
	moduleregistry "goMH/modules/registry"
	"goMH/taskqueue"
	"goMH/tui"
)

func Run(cfgModules []config.ModuleDef, registry *moduleregistry.Registry, am core.AssetManager, wu core.WinUtils) error {
	modules := buildConsoleModules(cfgModules, registry)
	if len(modules) == 0 {
		return errors.New("в конфигурации не определено ни одного доступного модуля")
	}

	queue := taskqueue.New()
	service := modruntime.Service{
		Resolver:  registry,
		Submitter: modruntime.NewTaskQueueSubmitter(queue),
		Services: core.ModuleServices{
			AssetManager: am,
			WinUtils:     wu,
		},
		ConfigureContext: core.NewSilentTaskContext(nil),
		ImmediateContext: tui.NewConsoleContext(),
	}

	return runDashboard(modules, queue, service)
}

func buildConsoleModules(cfgModules []config.ModuleDef, registry *moduleregistry.Registry) []tui.DashboardModule {
	registered := registry.Enabled(cfgModules)
	modules := make([]tui.DashboardModule, 0, len(registered))
	for _, module := range registered {
		modules = append(modules, tui.DashboardModule{
			ID:    module.ID(),
			Title: module.MenuText(),
		})
	}
	return modules
}

func runDashboard(modules []tui.DashboardModule, queue *taskqueue.Queue, service modruntime.Service) error {
	queue.StartBackground(func(snapshot taskqueue.TaskSnapshot, runtimeCtx context.Context) core.TaskContext {
		return taskqueue.NewTaskContext(queue, snapshot.ID, runtimeCtx)
	})

	controller := tui.DashboardController{
		LoadTasks: func() ([]taskqueue.TaskSnapshot, map[string][]string) {
			snapshots := queue.Snapshots()
			logs := make(map[string][]string, len(snapshots))
			for _, snapshot := range snapshots {
				logs[snapshot.ID] = queue.Logs(snapshot.ID)
			}
			return snapshots, logs
		},
		OnEnqueue: func(module tui.DashboardModule) (tui.DashboardActionResult, error) {
			result, err := service.EnqueueModule(module.ID)
			if err != nil {
				return tui.DashboardActionResult{}, err
			}
			return tui.DashboardActionResult{
				Note:         result.Note,
				SelectTaskID: taskIDFromResult(result),
			}, nil
		},
		OnRemoveTask: func(taskID string) (string, error) {
			if queue.RemoveQueued(taskID) {
				return "Задача удалена из очереди.", nil
			}
			if queue.Cancel(taskID) {
				return "Запрошена безопасная остановка скачивания.", nil
			}
			return "", errors.New("можно удалить ожидающую задачу или остановить только активное скачивание")
		},
		OnClearFinished: func() (string, error) {
			removed := queue.ClearFinished()
			return fmt.Sprintf("Очищено завершенных задач: %d", removed), nil
		},
		IsQueueStarted: func() bool {
			return queue.Started()
		},
	}

	return tui.RunQueueDashboard(modules, controller)
}

func taskIDFromResult(result core.ModuleActionResult) string {
	if !result.SelectTask {
		return ""
	}
	return result.TaskID
}
