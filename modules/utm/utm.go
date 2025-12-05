package utm

import (
	"errors"
	"fmt"
	"goMH/core"
	"goMH/tui"
	"strings"
)

type Module struct{}

func (m *Module) ID() string {
	return "UTM"
}

func (m *Module) MenuText() string {
	return "Установить УТМ (ЕГАИС)"
}

// Run - точка входа для консольного меню.
func (m *Module) Run(am core.AssetManager, wu core.WinUtils) error {
	// 1. Создаем контекст для консоли
	ctx := tui.NewConsoleContext()

	// 2. В данном модуле нет вопросов пользователю, сразу запускаем логику
	return m.Execute(ctx, am, wu)
}

// Execute - чистая бизнес-логика установки, не зависящая от конкретного TUI.
func (m *Module) Execute(ctx core.TaskContext, am core.AssetManager, wu core.WinUtils) error {
	cfg := am.Cfg().UTMConfig

	ctx.SetStatus(fmt.Sprintf("Начало установки: %s", m.MenuText()))

	if cfg.AssetID == "" {
		return errors.New("в секции 'utm_config' не указан asset_id")
	}

	ctx.Info("Получение установщика через AssetManager...")
	// Используем assetmgr для скачивания файла в кэш
	installerPath, err := am.DownloadToCache(cfg.AssetID)
	if err != nil {
		return fmt.Errorf("не удалось получить ассет '%s': %w", cfg.AssetID, err)
	}

	ctx.Info("Запуск установки...")
	ctx.Info(fmt.Sprintf("Аргументы: %s", cfg.InstallArgs))
	args := strings.Fields(cfg.InstallArgs)

	_, err = wu.RunCommand(installerPath, args...)
	if err != nil {
		return fmt.Errorf("ошибка при установке УТМ: %w", err)
	}

	ctx.Success("Установка УТМ завершена успешно.")
	return nil
}
