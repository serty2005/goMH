package dto

import (
	"errors"
	"fmt"
	"goMH/core"
	"goMH/tui"
	"strings"
)

type Module struct{}

func (m *Module) ID() string {
	return "DTO"
}

func (m *Module) MenuText() string {
	return "Установить ДТО"
}

func (m *Module) Run(am core.AssetManager, wu core.WinUtils) error {
	cfg := am.Cfg().DTOConfig

	tui.Title(fmt.Sprintf("\n--- Начало установки: %s ---", m.MenuText()))

	if cfg.AssetID == "" {
		return errors.New("в секции 'dto_config' не указан asset_id")
	}

	tui.Info("Получение установщика через AssetManager...")
	// Используем assetmgr для скачивания файла в кэш, он сам выберет метод (HTTP/FTP)
	installerPath, err := am.DownloadToCache(cfg.AssetID)
	if err != nil {
		return fmt.Errorf("не удалось получить ассет '%s': %w", cfg.AssetID, err)
	}

	tui.Info("Запуск установки в тихом режиме...")
	tui.InfoF("Аргументы: %s", cfg.InstallArgs)
	args := strings.Fields(cfg.InstallArgs)

	_, err = wu.RunCommand(installerPath, args...)
	if err != nil {
		return fmt.Errorf("ошибка при установке ДТО: %w", err)
	}

	return nil
}
