package regime

import (
	"bufio"
	"fmt"
	"goMH/core"
	"goMH/tui"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type Module struct{}

// RegimeInstallConfig содержит параметры для установки
type RegimeInstallConfig struct {
	IsReinstall bool
	Username    string
	Password    string
}

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

// checkDotNet48 проверяет установленную версию .NET Framework 4.8
func checkDotNet48() (bool, error) {
	// PowerShell команда для получения версии .NET Framework
	cmd := exec.Command("powershell", "-Command",
		"Get-ItemProperty 'HKLM:SOFTWARE\\Microsoft\\NET Framework Setup\\NDP\\v4\\Full\\' -Name Release -ErrorAction SilentlyContinue | Select-Object -ExpandProperty Release")

	output, err := cmd.Output()
	if err != nil {
		return false, fmt.Errorf("не удалось выполнить проверку .NET Framework: %w", err)
	}

	releaseStr := strings.TrimSpace(string(output))
	if releaseStr == "" {
		return false, fmt.Errorf(".NET Framework 4.8 не установлен")
	}

	release, err := strconv.Atoi(releaseStr)
	if err != nil {
		return false, fmt.Errorf("не удалось преобразовать версию .NET Framework: %w", err)
	}

	// .NET Framework 4.8 соответствует Release >= 528040
	isInstalled := release >= 528040
	return isInstalled, nil
}

// installDotNet48 скачивает и устанавливает .NET Framework 4.8 Developer Pack
func installDotNet48(ctx core.TaskContext, rootPath string) error {
	dotNetUrl := "https://go.microsoft.com/fwlink/?linkid=2088517"

	ctx.Info("-> Скачивание .NET Framework 4.8 Developer Pack...")
	ctx.Info(fmt.Sprintf("URL: %s", dotNetUrl))

	// Создаем директорию _assets в корне проекта для скачивания
	assetsDir := filepath.Join(rootPath, "_assets")
	if err := os.MkdirAll(assetsDir, 0755); err != nil {
		return fmt.Errorf("не удалось создать директорию _assets: %w", err)
	}

	installerPath := filepath.Join(assetsDir, "ndp48-devpack-enu.exe")

	// PowerShell команда для скачивания файла
	downloadCmd := fmt.Sprintf(
		"Invoke-WebRequest -Uri '%s' -OutFile '%s' -UseBasicParsing",
		dotNetUrl, installerPath)

	cmd := exec.Command("powershell", "-Command", downloadCmd)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("не удалось скачать .NET Framework 4.8: %s. Ошибка: %w", string(output), err)
	}

	ctx.Success("Файл успешно скачан")

	// Установка в тихом режиме
	ctx.Info("-> Установка .NET Framework 4.8 Developer Pack...")
	installCmd := exec.Command(installerPath, "/quiet", "/norestart")

	installOutput, err := installCmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("не удалось установить .NET Framework 4.8: %s. Ошибка: %w", string(installOutput), err)
	}

	ctx.Success(".NET Framework 4.8 Developer Pack успешно установлен")

	// Удаляем установщик после установки
	if err := os.Remove(installerPath); err != nil {
		ctx.Warn(fmt.Sprintf("Не удалось удалить временный файл установщика: %v", err))
	}

	return nil
}

func (m *Module) ID() string {
	return "Regime"
}

func (m *Module) MenuText() string {
	return "Regime (Локальный модуль ЧестныйЗнак)"
}

// Run - точка входа (UI слой)
func (m *Module) Run(am core.AssetManager, wu core.WinUtils) error {
	ctx := tui.NewConsoleContext()
	ctx.SetStatus("Подготовка к установке Regime")

	// 1. Проверяем, установлена ли служба "regime" (нужно для решения о переустановке)
	const serviceName = "regime"
	isReinstall, err := wu.ServiceExists(serviceName)
	if err != nil {
		return fmt.Errorf("не удалось проверить наличие службы '%s': %w", serviceName, err)
	}

	// 2. Собираем конфиг (взаимодействие с пользователем)
	installCfg := RegimeInstallConfig{
		IsReinstall: isReinstall,
	}

	if !isReinstall {
		tui.Info("Новая установка 'regime'.")
		// Запрашиваем учетные данные у пользователя только для новой установки
		username, password, err := getCredentials()
		if err != nil {
			return fmt.Errorf("не удалось получить учетные данные: %w", err)
		}
		installCfg.Username = username
		installCfg.Password = password
	} else {
		tui.Warn("Обнаружена существующая служба 'regime'. Будет выполнена переустановка с сохранением данных.")
	}

	// 3. Передаем управление логике (фоновый процесс)
	return m.Execute(ctx, am, wu, installCfg)
}

// Execute - логика установки
func (m *Module) Execute(ctx core.TaskContext, am core.AssetManager, wu core.WinUtils, cfg RegimeInstallConfig) error {
	ctx.SetStatus("Запуск установки/обновления Regime")

	// 1. Получаем ресурс (MSI-установщик) через assetmgr
	ctx.Info("-> Этап 1: Получение установщика...")
	msiPath, err := am.DownloadToCache("Regime_Installer")
	if err != nil {
		return fmt.Errorf("не удалось получить ресурс 'Regime_Installer': %w", err)
	}

	// 2. Проверяем наличие .NET Framework 4.8
	ctx.Info("-> Этап 2: Проверка .NET Framework 4.8...")
	isDotNetInstalled, err := checkDotNet48()
	if err != nil {
		ctx.Warn(fmt.Sprintf("Не удалось проверить установку .NET Framework 4.8: %v", err))
		ctx.Info("Продолжаем установку без проверки .NET Framework...")
	} else if !isDotNetInstalled {
		ctx.Warn(".NET Framework 4.8 не обнаружен. Начинаем установку...")

		// Устанавливаем .NET Framework 4.8
		if err := installDotNet48(ctx, am.Cfg().RootPath); err != nil {
			return fmt.Errorf("не удалось установить .NET Framework 4.8: %w", err)
		}

		// Проверяем установку еще раз после установки
		ctx.Info("-> Повторная проверка .NET Framework 4.8...")
		isDotNetInstalled, err = checkDotNet48()
		if err != nil || !isDotNetInstalled {
			return fmt.Errorf("не удалось подтвердить установку .NET Framework 4.8: %w", err)
		}

		ctx.Success(".NET Framework 4.8 успешно установлен и проверен")
	} else {
		ctx.Success(".NET Framework 4.8 уже установлен")
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
	if cfg.IsReinstall {
		args = append(args, "REINSTALL_FLAG=1")
	} else {
		// Добавляем учетные данные к аргументам
		args = append(args,
			fmt.Sprintf("ADMINUSER=%s", cfg.Username),
			fmt.Sprintf("ADMINPASSWORD=%s", cfg.Password),
		)
	}

	// 5. Запуск установки с помощью msiexec
	ctx.Info(fmt.Sprintf("-> Этап 3: Запуск установки %s...", filepath.Base(msiPath)))
	ctx.Info("Установка будет выполнена в тихом режиме. Это может занять несколько минут...")

	// Передаем слайс аргументов в RunCommand
	output, err := wu.RunCommand("msiexec.exe", args...)
	if err != nil {
		return fmt.Errorf("установщик msiexec завершился с ошибкой. Лог: %s. Вывод: %s. Ошибка: %w", logPath, output, err)
	}

	ctx.Success(fmt.Sprintf("Установка успешно завершена. Подробный лог сохранен в %s", logPath))
	return nil
}
