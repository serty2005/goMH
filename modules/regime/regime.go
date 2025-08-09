package regime

import (
	"fmt"
	"goMH/core"
	"goMH/tui"
	"os"
	"path/filepath"
	"time"
)

type Module struct{}

func (m *Module) ID() string {
	return "Regime"
}

func (m *Module) MenuText() string {
	return "Regime (Локальный модуль ЧестныйЗнак)"
}

func (m *Module) Run(am core.AssetManager, wu core.WinUtils) error {
	tui.Title("\n--- Запуск установки/обновления Regime ---")

	// 1. Получаем ресурс (MSI-установщик) через assetmgr
	tui.Info("-> Этап 1: Получение установщика...")
	msiPath, err := am.DownloadToCache("Regime_Installer")
	if err != nil {
		return fmt.Errorf("не удалось получить ресурс 'Regime_Installer': %w", err)
	}

	// 2. Проверяем, установлена ли служба "regime"
	tui.Info("-> Этап 2: Проверка существующей установки...")
	const serviceName = "regime"
	isReinstall, err := wu.ServiceExists(serviceName)
	if err != nil {
		// Если сама проверка не удалась, это критическая ошибка.
		return fmt.Errorf("не удалось проверить наличие службы '%s': %w", serviceName, err)
	}

	// 3. Формируем аргументы для msiexec
	logDir := filepath.Join(am.Cfg().RootPath, "logs")
	_ = os.MkdirAll(logDir, 0755)
	logPath := filepath.Join(logDir, fmt.Sprintf("regime_install_%d.log", time.Now().Unix()))

	// Базовый набор аргументов
	args := []string{
		"/i", msiPath,
		"/qn", // Тихий режим без интерфейса
		"/norestart",
		"/L*v", logPath,
		"ADMINUSER=MH",
		"ADMINPASSWORD=mhrcadmin994525",
	}

	// Условное добавление флага переустановки
	if isReinstall {
		tui.Warn("Обнаружена существующая служба 'regime'. Будет выполнена переустановка с сохранением данных.")
		args = append(args, "REINSTALL_FLAG=1")
	} else {
		tui.Info("Новая установка 'regime'.")
	}

	// 4. Запуск установки с помощью msiexec
	tui.InfoF("-> Этап 3: Запуск установки %s...", filepath.Base(msiPath))
	tui.Info("Установка будет выполнена в тихом режиме. Это может занять несколько минут...")

	// Передаем слайс аргументов в RunCommand
	output, err := wu.RunCommand("msiexec.exe", args...)
	if err != nil {
		return fmt.Errorf("установщик msiexec завершился с ошибкой. Лог: %s. Вывод: %s. Ошибка: %w", logPath, output, err)
	}

	tui.SuccessF("Установка успешно завершена. Подробный лог сохранен в %s", logPath)
	return nil
}
