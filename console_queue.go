package main

import (
	"context"
	"errors"
	"fmt"
	"goMH/assetmgr"
	"goMH/config"
	"goMH/core"
	"goMH/dependencies"
	"goMH/modules/distro"
	fiscaldrivers "goMH/modules/fiscal-drivers"
	"goMH/modules/frpc"
	"goMH/modules/regime"
	"goMH/modules/remoteaccess"
	"goMH/modules/serviceutils"
	"goMH/modules/utm"
	"goMH/modules/vcomcaster"
	"goMH/taskqueue"
	"goMH/tui"
	"io"
	"path/filepath"
	"strings"
	"sync"
)

type consoleModule struct {
	dashboard tui.DashboardModule
	enqueue   func(am core.AssetManager, wu core.WinUtils, queue *taskqueue.Queue) (tui.DashboardActionResult, error)
}

type taskCancelableControl interface {
	SetCancelable(cancel func())
	ClearCancelable()
}

func buildConsoleModules(cfgModules []config.ModuleDef, registered map[string]core.Installer) []consoleModule {
	modules := make([]consoleModule, 0, len(cfgModules))
	for _, modDef := range cfgModules {
		if modDef.ID == "iiko-plugins" {
			continue
		}
		installer, ok := registered[modDef.ID]
		if !ok {
			continue
		}

		module := consoleModule{
			dashboard: tui.DashboardModule{
				ID:    installer.ID(),
				Title: installer.MenuText(),
			},
		}

		switch typed := installer.(type) {
		case *distro.Module:
			module.enqueue = enqueueDistroTask(typed)
		case *fiscaldrivers.Module:
			module.enqueue = enqueueFiscalDriverTask(typed)
		case *frpc.Module:
			module.enqueue = enqueueFRPCTask(typed)
		case *regime.Module:
			module.enqueue = enqueueRegimeTask(typed)
		case *remoteaccess.Module:
			module.enqueue = enqueueRemoteAccessTask(typed)
		case *serviceutils.Module:
			module.enqueue = enqueueServiceUtilsTask(typed)
		case *vcomcaster.Module:
			module.enqueue = enqueueVComCasterTask(typed)
		case *utm.Module:
			module.enqueue = enqueueUTMTask(typed)
		}

		if module.enqueue != nil {
			modules = append(modules, module)
		}
	}
	return modules
}

func runConsoleDashboard(modules []consoleModule, queue *taskqueue.Queue, am core.AssetManager, wu core.WinUtils) error {
	queue.StartBackground(func(snapshot taskqueue.TaskSnapshot) core.TaskContext {
		return taskqueue.NewTaskContext(queue, snapshot.ID)
	})

	dashboardModules := make([]tui.DashboardModule, 0, len(modules))
	for _, module := range modules {
		dashboardModules = append(dashboardModules, module.dashboard)
	}

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
			found := findConsoleModule(modules, module.ID)
			if found == nil || found.enqueue == nil {
				return tui.DashboardActionResult{}, errors.New("для модуля недоступно добавление в очередь")
			}
			return found.enqueue(am, wu, queue)
		},
		OnRemoveTask: func(taskID string) (string, error) {
			if queue.RemoveQueued(taskID) {
				return "Задача удалена из очереди.", nil
			}
			if queue.Cancel(taskID) {
				return "Отмена скачивания запрошена.", nil
			}
			return "", errors.New("можно удалить ожидающую задачу или отменить активное скачивание")
		},
		OnClearFinished: func() (string, error) {
			removed := queue.ClearFinished()
			return fmt.Sprintf("Очищено завершенных задач: %d", removed), nil
		},
		IsQueueStarted: func() bool {
			return queue.Started()
		},
	}

	return tui.RunQueueDashboard(dashboardModules, controller)
}

func findConsoleModule(modules []consoleModule, id string) *consoleModule {
	for i := range modules {
		if modules[i].dashboard.ID == id {
			return &modules[i]
		}
	}
	return nil
}

func enqueueDistroTask(module *distro.Module) func(am core.AssetManager, wu core.WinUtils, queue *taskqueue.Queue) (tui.DashboardActionResult, error) {
	return func(am core.AssetManager, wu core.WinUtils, queue *taskqueue.Queue) (tui.DashboardActionResult, error) {
		cfg, err := module.Configure(tui.NewSilentContext(), am, wu)
		if err != nil || cfg == nil {
			return tui.DashboardActionResult{}, err
		}

		title, signature := describeDistroTask(cfg)
		snapshot, err := queue.Enqueue(taskqueue.TaskSpec{
			ModuleID:  module.ID(),
			Title:     title,
			Signature: signature,
			Exclusive: distroTaskExclusive(cfg),
			Run: func(taskCtx core.TaskContext) error {
				return executeQueuedTask(taskCtx, am, wu, func(taskAM core.AssetManager, taskWU core.WinUtils) error {
					return module.Execute(taskCtx, taskAM, taskWU, cfg)
				})
			},
		})
		if err != nil {
			return tui.DashboardActionResult{}, err
		}
		return enqueueResult("Задача добавлена в очередь.", snapshot.ID, false), nil
	}
}

func enqueueFiscalDriverTask(module *fiscaldrivers.Module) func(am core.AssetManager, wu core.WinUtils, queue *taskqueue.Queue) (tui.DashboardActionResult, error) {
	return func(am core.AssetManager, wu core.WinUtils, queue *taskqueue.Queue) (tui.DashboardActionResult, error) {
		cfg, err := module.Configure(tui.NewSilentContext(), am)
		if err != nil || cfg == nil {
			return tui.DashboardActionResult{}, err
		}

		title := "Установка драйвера: " + cfg.Driver.MenuText
		signature := "fiscal|" + cfg.Driver.ID
		snapshot, err := queue.Enqueue(taskqueue.TaskSpec{
			ModuleID:  module.ID(),
			Title:     title,
			Signature: signature,
			Exclusive: true,
			Run: func(taskCtx core.TaskContext) error {
				return executeQueuedTask(taskCtx, am, wu, func(taskAM core.AssetManager, taskWU core.WinUtils) error {
					return module.Execute(taskCtx, taskAM, taskWU, cfg)
				})
			},
		})
		if err != nil {
			return tui.DashboardActionResult{}, err
		}
		return enqueueResult("Задача добавлена в очередь.", snapshot.ID, false), nil
	}
}

func enqueueFRPCTask(module *frpc.Module) func(am core.AssetManager, wu core.WinUtils, queue *taskqueue.Queue) (tui.DashboardActionResult, error) {
	return func(am core.AssetManager, wu core.WinUtils, queue *taskqueue.Queue) (tui.DashboardActionResult, error) {
		module.Cfg = &am.Cfg().FrpcConfig
		cfg, err := module.Configure(tui.NewSilentContext(), am, wu)
		if err != nil || cfg == nil {
			return tui.DashboardActionResult{}, err
		}

		title, signature := describeFRPCTask(cfg)
		snapshot, err := queue.Enqueue(taskqueue.TaskSpec{
			ModuleID:  module.ID(),
			Title:     title,
			Signature: signature,
			Run: func(taskCtx core.TaskContext) error {
				return executeQueuedTask(taskCtx, am, wu, func(taskAM core.AssetManager, taskWU core.WinUtils) error {
					module.Cfg = &taskAM.Cfg().FrpcConfig
					return module.Execute(taskCtx, taskAM, taskWU, cfg)
				})
			},
		})
		if err != nil {
			return tui.DashboardActionResult{}, err
		}
		return enqueueResult("Задача добавлена в очередь.", snapshot.ID, false), nil
	}
}

func enqueueRemoteAccessTask(module *remoteaccess.Module) func(am core.AssetManager, wu core.WinUtils, queue *taskqueue.Queue) (tui.DashboardActionResult, error) {
	return func(am core.AssetManager, wu core.WinUtils, queue *taskqueue.Queue) (tui.DashboardActionResult, error) {
		cfg, err := module.Configure(tui.NewSilentContext(), am, wu)
		if err != nil || cfg == nil {
			return tui.DashboardActionResult{}, err
		}

		title, signature := describeRemoteAccessTask(cfg)
		snapshot, err := queue.Enqueue(taskqueue.TaskSpec{
			ModuleID:  module.ID(),
			Title:     title,
			Signature: signature,
			Exclusive: remoteAccessTaskExclusive(cfg),
			Run: func(taskCtx core.TaskContext) error {
				return executeQueuedTask(taskCtx, am, wu, func(taskAM core.AssetManager, taskWU core.WinUtils) error {
					return module.Execute(taskCtx, taskAM, taskWU, cfg)
				})
			},
		})
		if err != nil {
			return tui.DashboardActionResult{}, err
		}
		return enqueueResult("Задача добавлена в очередь.", snapshot.ID, false), nil
	}
}

func enqueueRegimeTask(module *regime.Module) func(am core.AssetManager, wu core.WinUtils, queue *taskqueue.Queue) (tui.DashboardActionResult, error) {
	return func(am core.AssetManager, wu core.WinUtils, queue *taskqueue.Queue) (tui.DashboardActionResult, error) {
		cfg, err := module.Configure(wu)
		if err != nil || cfg == nil {
			return tui.DashboardActionResult{}, err
		}

		title, signature := describeRegimeTask(cfg)
		snapshot, err := queue.Enqueue(taskqueue.TaskSpec{
			ModuleID:  module.ID(),
			Title:     title,
			Signature: signature,
			Exclusive: true,
			Run: func(taskCtx core.TaskContext) error {
				return executeQueuedTask(taskCtx, am, wu, func(taskAM core.AssetManager, taskWU core.WinUtils) error {
					return module.Execute(taskCtx, taskAM, taskWU, *cfg)
				})
			},
		})
		if err != nil {
			return tui.DashboardActionResult{}, err
		}
		return enqueueResult("Задача добавлена в очередь.", snapshot.ID, false), nil
	}
}

func enqueueServiceUtilsTask(module *serviceutils.Module) func(am core.AssetManager, wu core.WinUtils, queue *taskqueue.Queue) (tui.DashboardActionResult, error) {
	return func(am core.AssetManager, wu core.WinUtils, queue *taskqueue.Queue) (tui.DashboardActionResult, error) {
		cfg, err := module.Configure(tui.NewSilentContext(), am, wu)
		if err != nil || cfg == nil {
			return tui.DashboardActionResult{}, err
		}

		title, signature := describeServiceUtilsTask(cfg)
		snapshot, err := queue.Enqueue(taskqueue.TaskSpec{
			ModuleID:  module.ID(),
			Title:     title,
			Signature: signature,
			Run: func(taskCtx core.TaskContext) error {
				return executeQueuedTask(taskCtx, am, wu, func(taskAM core.AssetManager, taskWU core.WinUtils) error {
					return module.Execute(taskCtx, taskAM, taskWU, cfg)
				})
			},
		})
		if err != nil {
			return tui.DashboardActionResult{}, err
		}
		return enqueueResult("Задача добавлена в очередь.", snapshot.ID, cfg.Action == serviceutils.ActionViewLog), nil
	}
}

func enqueueVComCasterTask(module *vcomcaster.Module) func(am core.AssetManager, wu core.WinUtils, queue *taskqueue.Queue) (tui.DashboardActionResult, error) {
	return func(am core.AssetManager, wu core.WinUtils, queue *taskqueue.Queue) (tui.DashboardActionResult, error) {
		cfg, err := module.Configure(tui.NewSilentContext(), am, wu)
		if err != nil || cfg == nil {
			return tui.DashboardActionResult{}, err
		}

		title, signature := describeVComCasterTask(cfg)
		snapshot, err := queue.Enqueue(taskqueue.TaskSpec{
			ModuleID:  module.ID(),
			Title:     title,
			Signature: signature,
			Exclusive: vcomCasterTaskExclusive(cfg),
			Run: func(taskCtx core.TaskContext) error {
				return executeQueuedTask(taskCtx, am, wu, func(taskAM core.AssetManager, taskWU core.WinUtils) error {
					return module.Execute(taskCtx, taskAM, taskWU, cfg)
				})
			},
		})
		if err != nil {
			return tui.DashboardActionResult{}, err
		}
		return enqueueResult("Задача добавлена в очередь.", snapshot.ID, false), nil
	}
}

func enqueueUTMTask(module *utm.Module) func(am core.AssetManager, wu core.WinUtils, queue *taskqueue.Queue) (tui.DashboardActionResult, error) {
	return func(am core.AssetManager, wu core.WinUtils, queue *taskqueue.Queue) (tui.DashboardActionResult, error) {
		snapshot, err := queue.Enqueue(taskqueue.TaskSpec{
			ModuleID:  module.ID(),
			Title:     "Установка УТМ",
			Signature: "utm|install",
			Exclusive: true,
			Run: func(taskCtx core.TaskContext) error {
				return executeQueuedTask(taskCtx, am, wu, func(taskAM core.AssetManager, taskWU core.WinUtils) error {
					return module.Execute(taskCtx, taskAM, taskWU)
				})
			},
		})
		if err != nil {
			return tui.DashboardActionResult{}, err
		}
		return enqueueResult("Задача добавлена в очередь.", snapshot.ID, false), nil
	}
}

func executeQueuedTask(taskCtx core.TaskContext, am core.AssetManager, wu core.WinUtils, fn func(taskAM core.AssetManager, taskWU core.WinUtils) error) error {
	writer := newTaskLogWriter(taskCtx)
	restoreSevenZip := dependencies.SetConsoleOutput(writer)
	defer restoreSevenZip()
	defer writer.Flush()

	downloadCtx, cancelDownload := context.WithCancel(context.Background())
	defer cancelDownload()

	if control, ok := taskCtx.(taskCancelableControl); ok {
		defer control.ClearCancelable()
	}

	taskAM := withTaskAssetManager(am, writer, downloadCtx, cancelDownload, taskCtx)
	taskWU := withTaskWinUtils(wu, writer)

	return fn(taskAM, taskWU)
}

func enqueueResult(note string, taskID string, selectTask bool) tui.DashboardActionResult {
	result := tui.DashboardActionResult{Note: note}
	if selectTask {
		result.SelectTaskID = taskID
	}
	return result
}

func withTaskAssetManager(am core.AssetManager, writer io.Writer, downloadCtx context.Context, cancelDownload context.CancelFunc, taskCtx core.TaskContext) core.AssetManager {
	manager, ok := am.(*assetmgr.Manager)
	if !ok {
		return am
	}

	return manager.WithTaskRuntime(
		writer,
		io.Discard,
		func(text string) {
			taskCtx.SetStatus(text)
		},
		func(description string, percent int) {
			taskCtx.SetStatus("Скачивание " + description)
			taskCtx.SetProgress(percent)
		},
		downloadCtx,
		func(active bool, _ string) {
			control, ok := taskCtx.(taskCancelableControl)
			if !ok {
				return
			}
			if active {
				control.SetCancelable(cancelDownload)
				return
			}
			control.ClearCancelable()
		},
	)
}

func withTaskWinUtils(wu core.WinUtils, writer io.Writer) core.WinUtils {
	real, ok := wu.(*RealWinUtils)
	if !ok {
		return wu
	}
	return real.WithConsoleOutput(writer, writer)
}

func distroTaskExclusive(cfg *distro.DistroInstallConfig) bool {
	return cfg.Action == distro.ActionInstallComponent
}

func remoteAccessTaskExclusive(cfg *remoteaccess.RemoteAccessConfig) bool {
	switch cfg.Tool {
	case remoteaccess.ToolTeamViewer, remoteaccess.ToolLiteManager, remoteaccess.ToolRustDesk:
		return true
	default:
		return false
	}
}

func vcomCasterTaskExclusive(cfg *vcomcaster.VComCasterConfig) bool {
	return cfg.Action == vcomcaster.ActionInstall || cfg.Action == vcomcaster.ActionReinstall || cfg.Action == vcomcaster.ActionUninstall
}

type taskLogWriter struct {
	mu     sync.Mutex
	ctx    core.TaskContext
	buffer strings.Builder
}

func newTaskLogWriter(ctx core.TaskContext) *taskLogWriter {
	return &taskLogWriter{ctx: ctx}
}

func (w *taskLogWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	w.buffer.Write(p)
	w.flushLocked(false)
	return len(p), nil
}

func (w *taskLogWriter) Flush() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.flushLocked(true)
}

func (w *taskLogWriter) flushLocked(force bool) {
	for {
		text := w.buffer.String()
		index := strings.IndexByte(text, '\n')
		if index == -1 {
			if force && text != "" {
				w.ctx.Info(strings.TrimSpace(text))
				w.buffer.Reset()
			}
			return
		}

		line := strings.TrimSpace(text[:index])
		w.buffer.Reset()
		w.buffer.WriteString(text[index+1:])
		if line != "" {
			w.ctx.Info(line)
		}
	}
}

func describeDistroTask(cfg *distro.DistroInstallConfig) (string, string) {
	patchName := ""
	if cfg.Patch != nil {
		patchName = cfg.Patch.ShortName
	}

	switch cfg.Action {
	case distro.ActionInstallComponent:
		parts := []string{cfg.Brand, cfg.Component.MenuText}
		if cfg.Version != "" {
			parts = append(parts, cfg.Version)
		}
		if patchName != "" {
			parts = append(parts, "патч "+patchName)
		}
		return "Установка " + strings.Join(parts, " "), fmt.Sprintf("distro|component|%s|%s|%s|%s", cfg.Brand, cfg.Component.ID, cfg.Version, patchName)
	case distro.ActionInstallPortable:
		return fmt.Sprintf("Portable %s %s %s", cfg.Brand, cfg.Component.MenuText, cfg.Version), fmt.Sprintf("distro|portable|%s|%s|%s", cfg.Brand, cfg.Component.ID, cfg.Version)
	case distro.ActionManualPatch:
		return fmt.Sprintf("Ручной патч %s %s", cfg.Brand, patchName), fmt.Sprintf("distro|manual_patch|%s|%s|%s", cfg.Brand, cfg.Version, patchName)
	case distro.ActionPlugins:
		return fmt.Sprintf("Автообновление плагинов %s", cfg.Brand), fmt.Sprintf("distro|plugins|%s", cfg.Brand)
	default:
		return "Задача iiko / Syrve", "distro|generic"
	}
}

func describeFRPCTask(cfg *frpc.FrpcInstallConfig) (string, string) {
	switch cfg.Action {
	case frpc.ActionInstall:
		return fmt.Sprintf("FRPC: установка %s:%s -> %d", cfg.Alias, cfg.LocalPort, cfg.RemotePort), fmt.Sprintf("frpc|install|%s|%s|%d", cfg.Alias, cfg.LocalPort, cfg.RemotePort)
	case frpc.ActionAddPort:
		return fmt.Sprintf("FRPC: порт %s:%s -> %d", cfg.Alias, cfg.LocalPort, cfg.RemotePort), fmt.Sprintf("frpc|add|%s|%s|%d", cfg.Alias, cfg.LocalPort, cfg.RemotePort)
	case frpc.ActionReinstall:
		return fmt.Sprintf("FRPC: переустановка %s:%s -> %d", cfg.Alias, cfg.LocalPort, cfg.RemotePort), fmt.Sprintf("frpc|reinstall|%s|%s|%d", cfg.Alias, cfg.LocalPort, cfg.RemotePort)
	case frpc.ActionUninstall:
		return "FRPC: удаление", "frpc|uninstall"
	default:
		return "FRPC", "frpc|generic"
	}
}

func describeRemoteAccessTask(cfg *remoteaccess.RemoteAccessConfig) (string, string) {
	switch cfg.Tool {
	case remoteaccess.ToolTeamViewer:
		return "Установка TeamViewer", "remoteaccess|teamviewer"
	case remoteaccess.ToolLiteManager:
		return "Установка LiteManager", "remoteaccess|litemanager"
	case remoteaccess.ToolRustDesk:
		return "Установка RustDesk", "remoteaccess|rustdesk"
	case remoteaccess.ToolPOSRelayd:
		return "Установка POSRelayd Agent", "remoteaccess|posrelayd"
	default:
		return "Удаленный доступ", "remoteaccess|generic"
	}
}

func describeRegimeTask(cfg *regime.RegimeInstallConfig) (string, string) {
	if cfg.IsReinstall {
		return "Regime: переустановка", "regime|reinstall"
	}
	return "Regime: новая установка", "regime|install|" + cfg.Username
}

func describeServiceUtilsTask(cfg *serviceutils.ServiceUtilsConfig) (string, string) {
	switch cfg.Action {
	case serviceutils.ActionCleanTemp:
		return "Очистка временных файлов", "serviceutils|clean_temp"
	case serviceutils.ActionCollectLogs:
		return fmt.Sprintf("Сбор логов за %d дн.", cfg.LogDays), fmt.Sprintf("serviceutils|collect|%d|%s", cfg.LogDays, strings.Join(cfg.LogDirs, ";"))
	case serviceutils.ActionViewLog:
		return "Просмотр лога: " + filepath.Base(cfg.LogFileToView), "serviceutils|view|" + cfg.LogFileToView
	case serviceutils.ActionOrderCheck:
		return "OrderCheck: " + filepath.Base(cfg.TargetDatabasePath), "serviceutils|ordercheck|" + cfg.TargetDatabasePath
	case serviceutils.ActionFrontTools:
		return "FrontTools: " + strings.ToUpper(cfg.DatabaseType), "serviceutils|fronttools|" + cfg.DatabaseType
	default:
		return "Утилиты обслуживания", "serviceutils|generic"
	}
}

func describeVComCasterTask(cfg *vcomcaster.VComCasterConfig) (string, string) {
	switch cfg.Action {
	case vcomcaster.ActionInstall:
		return fmt.Sprintf("VComCaster: установка для %s", cfg.SelectedScanner.Caption), fmt.Sprintf("vcomcaster|install|%s", cfg.ScannerDeviceID)
	case vcomcaster.ActionReinstall:
		return fmt.Sprintf("VComCaster: переустановка для %s", cfg.SelectedScanner.Caption), fmt.Sprintf("vcomcaster|reinstall|%s", cfg.ScannerDeviceID)
	case vcomcaster.ActionUninstall:
		return "VComCaster: удаление", "vcomcaster|uninstall"
	default:
		return "VComCaster", "vcomcaster|generic"
	}
}
