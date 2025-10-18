package utm

import (
	"errors"
	"fmt"
	"goMH/core"
	"goMH/tui"
	"strings"
)

// Module представляет модуль для установки УТМ
type Module struct{}

// ID возвращает идентификатор модуля
func (m *Module) ID() string {
	return "UTM"
}

// MenuText возвращает текст для отображения в меню
func (m *Module) MenuText() string {
	return "Установить УТМ (ЕГАИС)"
}

// Run запускает процесс установки УТМ
// Возвращает ошибку для совместимости с интерфейсом Installer
func (m *Module) Run(am core.AssetManager, wu core.WinUtils) error {
	// Выполняем установку синхронно для совместимости с интерфейсом
	return InstallUTMCore(am, wu)
}

// InstallUTMCore содержит основную логику установки УТМ
// Выделена в отдельную функцию для удобства тестирования и повторного использования
func InstallUTMCore(am core.AssetManager, wu core.WinUtils) error {
	cfg := am.Cfg().UTMConfig

	tui.Title(fmt.Sprintf("\n--- Начало установки: %s ---", "Установить УТМ (ЕГАИС)"))

	if cfg.AssetID == "" {
		return errors.New("в секции 'utm_config' не указан asset_id")
	}

	tui.Info("Получение установщика через AssetManager...")
	// Используем assetmgr для скачивания файла в кэш, он сам выберет метод (HTTP/FTP)
	installerPath, err := am.DownloadToCache(cfg.AssetID)
	if err != nil {
		return fmt.Errorf("не удалось получить ассет '%s': %w", cfg.AssetID, err)
	}

	tui.Info("Запуск установки...")
	tui.InfoF("Аргументы: %s", cfg.InstallArgs)
	args := strings.Fields(cfg.InstallArgs)

	_, err = wu.RunCommand(installerPath, args...)
	if err != nil {
		return fmt.Errorf("ошибка при установке УТМ: %w", err)
	}

	return nil
}
