package autostart

import (
	"fmt"
	"goMH/core"
	"goMH/tui"
)

type Module struct{}

type Config struct {
	Changes []core.AutostartChange
	Creates []core.AutostartCreateRequest
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
	return runUI(services.WinUtils.ListAutostartEntriesWithProgress)
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
		if err := services.WinUtils.AddRegistryAutostartEntry(create); err != nil {
			return core.ModuleActionResult{}, err
		}
	}
	if err := services.WinUtils.ApplyAutostartChanges(cfg.Changes); err != nil {
		return core.ModuleActionResult{}, err
	}
	total := len(cfg.Creates) + len(cfg.Changes)
	ctx.Success(fmt.Sprintf("Изменения автозапуска применены: %d", total))
	return core.ModuleActionResult{Note: fmt.Sprintf("Изменения автозапуска применены: %d", total)}, nil
}

func (m *Module) config(config any) (*Config, error) {
	cfg, ok := config.(*Config)
	if !ok {
		return nil, fmt.Errorf("неверная конфигурация модуля автозапуска")
	}
	return cfg, nil
}
