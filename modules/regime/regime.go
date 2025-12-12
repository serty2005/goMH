package regime

import (
	"bufio"
	"encoding/json"
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
	IsReinstall bool   `json:"is_reinstall"`
	Username    string `json:"username"`
	Password    string `json:"password"`
}

const resumeTaskName = "goMH_Regime_Resume"

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

// installDotNet48 скачивает и устанавливает .NET Framework 4.8 Runtime через AssetManager
func installDotNet48(ctx core.TaskContext, am core.AssetManager, wu core.WinUtils) error {
	const assetID = "DotNet48_Installer"

	ctx.Info("-> Скачивание .NET Framework 4.8 Runtime...")

	// Скачиваем установщик в кэш через AssetManager
	installerPath, err := am.DownloadToCache(assetID)
	if err != nil {
		return fmt.Errorf("не удалось скачать установщик .NET Framework 4.8: %w", err)
	}

	ctx.Success("Файл успешно получен")

	// Установка в тихом режиме
	ctx.Info("-> Установка .NET Framework 4.8...")

	// Используем стандартные флаги тихого установщика Microsoft: /q /norestart
	output, err := wu.RunCommand(installerPath, "/q", "/norestart")
	if err != nil {
		// ОБНОВЛЕНО: Проверяем код 3010 (ERROR_SUCCESS_REBOOT_REQUIRED)
		if strings.Contains(err.Error(), "exit status 3010") {
			ctx.Info("Установщик вернул код 3010 (требуется перезагрузка). Это нормальное поведение.")
			return nil
		}
		return fmt.Errorf("ошибка установки .NET Framework 4.8. Вывод: %s. Ошибка: %w", output, err)
	}

	ctx.Success(".NET Framework 4.8 успешно установлен")
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

	// 1. Проверяем, установлена ли служба "regime"
	const serviceName = "regime"
	isReinstall, err := wu.ServiceExists(serviceName)
	if err != nil {
		return fmt.Errorf("не удалось проверить наличие службы '%s': %w", serviceName, err)
	}

	// 2. Собираем конфиг
	installCfg := RegimeInstallConfig{
		IsReinstall: isReinstall,
	}

	if !isReinstall {
		tui.Info("Новая установка 'regime'.")
		username, password, err := getCredentials()
		if err != nil {
			return fmt.Errorf("не удалось получить учетные данные: %w", err)
		}
		installCfg.Username = username
		installCfg.Password = password
	} else {
		tui.Warn("Обнаружена существующая служба 'regime'. Будет выполнена переустановка с сохранением данных.")
	}

	// 3. Передаем управление логике
	return m.Execute(ctx, am, wu, installCfg)
}

// Resume - точка входа для автоматического продолжения после перезагрузки
func (m *Module) Resume(am core.AssetManager, wu core.WinUtils, configPath string) error {
	ctx := tui.NewConsoleContext()
	ctx.SetStatus("Возобновление установки Regime после перезагрузки")

	// 1. Загружаем конфиг
	data, err := os.ReadFile(configPath)
	if err != nil {
		return fmt.Errorf("не удалось прочитать файл возобновления: %w", err)
	}
	var cfg RegimeInstallConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return fmt.Errorf("ошибка парсинга конфига возобновления: %w", err)
	}

	// 2. Выполняем установку (теперь .NET должен быть установлен)
	if err := m.Execute(ctx, am, wu, cfg); err != nil {
		return err
	}

	// 3. Очистка
	ctx.Info("Очистка временных файлов возобновления...")
	_ = os.Remove(configPath)
	_ = wu.DeleteScheduledTaskByName(resumeTaskName)

	return nil
}

// Execute - логика установки
func (m *Module) Execute(ctx core.TaskContext, am core.AssetManager, wu core.WinUtils, cfg RegimeInstallConfig) error {
	// 1. Проверка .NET Framework 4.8
	ctx.SetStatus("Проверка окружения")
	ctx.Info("-> Проверка .NET Framework 4.8...")
	isDotNetInstalled, err := checkDotNet48()
	if err != nil {
		ctx.Warn(fmt.Sprintf("Ошибка проверки .NET: %v. Попытка продолжить...", err))
	}

	if !isDotNetInstalled {
		ctx.Warn(".NET Framework 4.8 не обнаружен.")

		// Установка .NET
		if err := installDotNet48(ctx, am, wu); err != nil {
			return fmt.Errorf("не удалось установить .NET Framework 4.8: %w", err)
		}

		// Для Regime требуется перезагрузка после установки .NET
		ctx.Info("Для завершения установки .NET Framework требуется перезагрузка.")
		ctx.Info("Подготовка к автоматическому возобновлению после перезагрузки...")

		// 1. Сохраняем конфиг во временный файл
		tempDir := filepath.Join(am.Cfg().RootPath, "temp")
		_ = os.MkdirAll(tempDir, 0755)
		resumeConfigPath := filepath.Join(tempDir, "regime_resume.json")

		data, _ := json.Marshal(cfg)
		if err := os.WriteFile(resumeConfigPath, data, 0600); err != nil {
			return fmt.Errorf("не удалось сохранить конфиг для возобновления: %w", err)
		}

		// 2. Создаем задачу в планировщике
		exePath, _ := os.Executable()
		args := fmt.Sprintf("-module Regime -resume \"%s\"", resumeConfigPath)

		if err := wu.CreateScheduledTask(resumeTaskName, exePath, args, filepath.Dir(exePath)); err != nil {
			return fmt.Errorf("не удалось создать задачу автозапуска: %w", err)
		}

		ctx.Success("Задача автозапуска создана. Перезагрузка через 5 секунд...")
		time.Sleep(2 * time.Second)

		if err := wu.Reboot(); err != nil {
			return fmt.Errorf("не удалось инициировать перезагрузку: %w", err)
		}

		// Завершаем программу, чтобы не идти дальше
		os.Exit(0)
	} else {
		ctx.Success(".NET Framework 4.8 установлен.")
	}

	// 2. Установка Regime MSI
	ctx.SetStatus("Установка Regime")
	ctx.Info("-> Скачивание установщика Regime...")
	msiPath, err := am.DownloadToCache("Regime_Installer")
	if err != nil {
		return fmt.Errorf("не удалось получить ресурс 'Regime_Installer': %w", err)
	}

	logDir := filepath.Join(am.Cfg().RootPath, "logs")
	_ = os.MkdirAll(logDir, 0755)
	logPath := filepath.Join(logDir, fmt.Sprintf("regime_install_%d.log", time.Now().Unix()))

	args := []string{
		"/i", msiPath,
		"/qn",
		"/norestart",
		"/L*v", logPath,
	}

	if cfg.IsReinstall {
		args = append(args, "REINSTALL_FLAG=1")
	} else {
		args = append(args,
			fmt.Sprintf("ADMINUSER=%s", cfg.Username),
			fmt.Sprintf("ADMINPASSWORD=%s", cfg.Password),
		)
	}

	ctx.Info("-> Запуск установки MSI...")
	output, err := wu.RunCommand("msiexec.exe", args...)
	if err != nil {
		return fmt.Errorf("ошибка msiexec. Лог: %s. Вывод: %s. Ошибка: %w", logPath, output, err)
	}

	ctx.Success(fmt.Sprintf("Установка Regime успешно завершена. Лог: %s", logPath))

	// 3. Пост-установочная настройка службы
	ctx.Info("Настройка службы regime...")
	const nssmPath = `C:\Program Files\Regime\bin\nssm.exe`
	const serviceName = "regime"

	// Установка типа запуска "Автоматически"
	// Сначала пробуем через родной nssm, если он есть
	if _, err := os.Stat(nssmPath); err == nil {
		ctx.Info("Конфигурация через NSSM: Start=SERVICE_AUTO_START")
		if _, err := wu.RunCommand(nssmPath, "set", serviceName, "Start", "SERVICE_AUTO_START"); err != nil {
			ctx.Warn(fmt.Sprintf("Не удалось настроить автозапуск через NSSM: %v", err))
		}
	} else {
		// Fallback на стандартный sc.exe
		ctx.Info("NSSM не найден, используем SC config...")
		if _, err := wu.RunCommand("sc", "config", serviceName, "start=", "auto"); err != nil {
			ctx.Warn(fmt.Sprintf("Не удалось настроить автозапуск через SC: %v", err))
		}
	}

	// Принудительный запуск службы
	ctx.Info("Запуск службы...")
	if _, err := wu.RunCommand("sc", "start", serviceName); err != nil {
		// Если служба уже запущена, sc вернет ошибку, это нормально. Проверим статус.
		status, _ := wu.GetServiceStatus(serviceName)
		if status != "RUNNING" && status != "START_PENDING" {
			ctx.Warn(fmt.Sprintf("Не удалось отправить команду start: %v", err))
		}
	}

	// Финальная проверка
	time.Sleep(3 * time.Second)
	status, err := wu.GetServiceStatus(serviceName)
	if err == nil && status == "RUNNING" {
		ctx.Success("Служба regime запущена и работает корректно.")
	} else {
		ctx.Warn(fmt.Sprintf("Внимание: Статус службы: %s. Рекомендуется проверить вручную.", status))
	}

	return nil
}
