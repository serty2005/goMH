package regime

import (
	"bufio"
	"fmt"
	"goMH/core"
	"goMH/tui"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type Module struct{}

// getCredentials запрашивает у пользователя логин и пароль для установки Regime
func getCredentials() (username, password string, err error) {
	scanner := bufio.NewScanner(os.Stdin)

	// Запрос логина
	fmt.Print("Введите логин администратора: ")
	if !scanner.Scan() {
		return "", "", fmt.Errorf("ошибка чтения логина")
	}
	username = strings.TrimSpace(scanner.Text())
	if username == "" {
		return "", "", fmt.Errorf("логин не может быть пустым")
	}

	// Запрос пароля
	fmt.Print("Введите пароль администратора: ")
	if !scanner.Scan() {
		return "", "", fmt.Errorf("ошибка чтения пароля")
	}
	password = strings.TrimSpace(scanner.Text())
	if password == "" {
		return "", "", fmt.Errorf("пароль не может быть пустым")
	}

	return username, password, nil
}

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
	}

	// Условное добавление флага переустановки
	if isReinstall {
		tui.Warn("Обнаружена существующая служба 'regime'. Будет выполнена переустановка с сохранением данных.")
		args = append(args, "REINSTALL_FLAG=1")
	} else {
		tui.Info("Новая установка 'regime'.")

		// 4. Запрашиваем учетные данные у пользователя только для новой установки
		tui.Info("-> Этап 3: Запрос учетных данных...")
		username, password, err := getCredentials()
		if err != nil {
			return fmt.Errorf("не удалось получить учетные данные: %w", err)
		}

		// Добавляем учетные данные к аргументам
		args = append(args,
			fmt.Sprintf("ADMINUSER=%s", username),
			fmt.Sprintf("ADMINPASSWORD=%s", password),
		)
	}

	// 5. Запуск установки с помощью msiexec
	tui.InfoF("-> Этап 4: Запуск установки %s...", filepath.Base(msiPath))
	tui.Info("Установка будет выполнена в тихом режиме. Это может занять несколько минут...")

	// Передаем слайс аргументов в RunCommand
	output, err := wu.RunCommand("msiexec.exe", args...)
	if err != nil {
		return fmt.Errorf("установщик msiexec завершился с ошибкой. Лог: %s. Вывод: %s. Ошибка: %w", logPath, output, err)
	}

	tui.SuccessF("Установка успешно завершена. Подробный лог сохранен в %s", logPath)
	return nil
}
