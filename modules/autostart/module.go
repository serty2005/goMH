package autostart

import (
	"fmt"
	"goMH/core"
	"goMH/tui"
	"log/slog"
)

type Module struct{}

type Config struct {
	Changes     []core.AutostartChange
	Creates     []core.AutostartCreateRequest
	TaskCreates []core.AutostartCreateRequest // создаются как задачи планировщика (ONLOGON + HighestAvailable)
	Edits       []core.AutostartEdit
}

type autostartEditWinUtils interface {
	AddRegistryAutostartEntry(req core.AutostartCreateRequest) error
	DeleteRegistryAutostartValue(scope core.AutostartScope, key core.AutostartRegistryKey, valueName string) error
}

func (m *Module) ID() string { return "autostart" }

func (m *Module) MenuText() string { return "Управление автозапуском" }

func (m *Module) Run(am core.AssetManager, wu core.WinUtils) error {
	cfg, err := m.ConfigureTask(tui.NewConsoleContext(), core.ModuleServices{AssetManager: am, WinUtils: wu})
	if err != nil || cfg == nil {
		return err
	}
	_, err = m.ExecuteImmediate(tui.NewConsoleContext(), core.ModuleServices{AssetManager: am, WinUtils: wu}, cfg)
	return err
}

func (m *Module) ConfigureTask(ctx core.TaskContext, services core.ModuleServices) (any, error) {
	return runUI(services.WinUtils.ListAutostartEntriesWithProgress, services.WinUtils.RequiresAdminElevation)
}

func (m *Module) BuildTask(config any) (core.ModuleTaskPlan, error) {
	cfg, err := m.config(config)
	if err != nil || cfg == nil {
		return core.ModuleTaskPlan{}, err
	}
	return core.ModuleTaskPlan{
		Mode:             core.ModuleRunModeImmediate,
		SkipConfirmation: true,
		Result: core.ModuleActionResult{
			Note: "Изменения автозапуска подготовлены.",
		},
	}, nil
}

func (m *Module) ExecuteTask(ctx core.TaskContext, services core.ModuleServices, config any) error {
	_, err := m.ExecuteImmediate(ctx, services, config)
	return err
}

func (m *Module) ExecuteImmediate(ctx core.TaskContext, services core.ModuleServices, config any) (core.ModuleActionResult, error) {
	cfg, err := m.config(config)
	if err != nil || cfg == nil {
		return core.ModuleActionResult{}, err
	}
	for _, create := range cfg.Creates {
		slog.Debug("Автозапуск: добавление записи реестра", "name", create.Name, "path", create.Path, "scope", create.Scope, "key", create.RegistryKey)
		if err := services.WinUtils.AddRegistryAutostartEntry(create); err != nil {
			slog.Info("Автозапуск: не удалось добавить запись реестра", "name", create.Name, "path", create.Path, "error", err)
			return core.ModuleActionResult{}, err
		}
	}
	for _, create := range cfg.TaskCreates {
		slog.Debug("Автозапуск: создание задачи планировщика", "name", create.Name, "path", create.Path, "args", create.Arguments)
		if err := services.WinUtils.AddScheduledAutostartTask(create.Name, create.Path, create.Arguments); err != nil {
			wrapped := fmt.Errorf("не удалось создать задачу планировщика %q: %w", create.Name, err)
			slog.Info("Автозапуск: ошибка создания задачи планировщика", "name", create.Name, "path", create.Path, "error", err)
			return core.ModuleActionResult{}, wrapped
		}
	}
	for _, edit := range cfg.Edits {
		slog.Debug("Автозапуск: редактирование записи", "original_name", edit.Original.Name, "new_name", edit.Name, "new_path", edit.Path, "new_args", edit.Arguments)
		if err := applyAutostartEdit(services.WinUtils, edit); err != nil {
			slog.Info("Автозапуск: не удалось применить редактирование", "original_name", edit.Original.Name, "error", err)
			return core.ModuleActionResult{}, err
		}
	}
	for _, change := range cfg.Changes {
		slog.Debug("Автозапуск: изменение состояния записи", "name", change.Entry.Name, "source", change.Entry.Source, "enabled", change.Enabled)
	}
	if err := services.WinUtils.ApplyAutostartChanges(cfg.Changes); err != nil {
		slog.Info("Автозапуск: не удалось применить изменения состояния", "count", len(cfg.Changes), "error", err)
		return core.ModuleActionResult{}, err
	}
	total := len(cfg.Creates) + len(cfg.TaskCreates) + len(cfg.Edits) + len(cfg.Changes)
	slog.Debug("Автозапуск: все изменения применены", "total", total)
	ctx.Success(fmt.Sprintf("Изменения автозапуска применены: %d", total))
	return core.ModuleActionResult{Note: fmt.Sprintf("Изменения автозапуска применены: %d", total)}, nil
}

func applyAutostartEdit(wu autostartEditWinUtils, edit core.AutostartEdit) error {
	original := edit.Original
	if original.Source != core.AutostartSourceRegistryRun && original.Source != core.AutostartSourceRegistryRunOnce {
		return nil
	}
	name := edit.Name
	if name == "" {
		name = original.Name
	}
	if err := wu.AddRegistryAutostartEntry(core.AutostartCreateRequest{
		Name:             name,
		RegistryKey:      original.RegistryKey,
		Scope:            original.Scope,
		Path:             edit.Path,
		Arguments:        edit.Arguments,
		WorkingDirectory: edit.WorkingDirectory,
	}); err != nil {
		return err
	}
	oldName := original.RegistryValue
	if oldName == "" {
		oldName = original.Name
	}
	if oldName != "" && oldName != name {
		return wu.DeleteRegistryAutostartValue(original.Scope, original.RegistryKey, oldName)
	}
	return nil
}

func (m *Module) config(config any) (*Config, error) {
	cfg, ok := config.(*Config)
	if !ok {
		return nil, fmt.Errorf("неверная конфигурация модуля автозапуска")
	}
	return cfg, nil
}
