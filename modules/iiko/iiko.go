package iiko

import (
	"bufio"
	"errors"
	"fmt"
	"goMH/config"
	"goMH/core"
	iikoplugins "goMH/modules/iiko-plugins"
	"goMH/tui"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jlaffaye/ftp"
	"github.com/manifoldco/promptui"
)

const (
	minVersion   = "8.7.6032.0"
	releasesPath = "/release_iiko" // Путь к релизам на FTP
)

var (
	ftpHosts = []string{"ftp.iiko.ru:21", "ftp.iiko2.ru:21"}
	ftpUser  = "partners"
	ftpPass  = "partners#iiko"
)

type Module struct {
	Cfg *config.IikoConfig
}

func (m *Module) ID() string       { return "iiko" }
func (m *Module) MenuText() string { return "iiko (Front, Back, Chain, Card) - Установка" }

func (m *Module) Run(am core.AssetManager, wu core.WinUtils) error {
	m.Cfg = &am.Cfg().IikoConfig
	if len(m.Cfg.DistroURLTemplates) == 0 || m.Cfg.CardPOS.ID == "" {
		return errors.New("конфигурация для модуля iiko (distro_url_templates или card_pos) не заполнена")
	}

	// 1. Выбор дистрибутива из общего списка
	selectedComponent, err := m.selectComponentMenu()
	if err != nil {
		tui.Info("Выбор дистрибутива отменен. Возврат в главное меню.")
		return nil
	}

	// 2. Если выбран iikoCard, запускаем его установку и выходим
	if selectedComponent.ID == "iikoCard" {
		return m.installIikoCard(am, wu, selectedComponent)
	}

	// 3. Для остальных дистрибутивов запускаем воркфлоу с выбором версии
	return m.runVersionedInstallWorkflow(am, wu, selectedComponent)
}

// runVersionedInstallWorkflow выполняет установку для продуктов с версиями (Front, RMS, Chain)
func (m *Module) runVersionedInstallWorkflow(am core.AssetManager, wu core.WinUtils, component config.DistroInfo) error {
	// 1. Получаем список версий с FTP
	tui.Info("Получение списка доступных версий с официального FTP iiko...")
	versions, err := m.discoverOfficialVersions()
	if err != nil {
		return fmt.Errorf("не удалось получить список версий: %w", err)
	}
	if len(versions) == 0 {
		return fmt.Errorf("не найдено ни одной версии iiko, удовлетворяющей условию (>= %s)", minVersion)
	}
	tui.Success(fmt.Sprintf("Найдено %d подходящих версий.", len(versions)))

	// 2. Показываем интерактивное меню выбора версии
	selectedVersion, err := m.selectVersionMenu(versions)
	if err != nil {
		if errors.Is(err, promptui.ErrInterrupt) {
			tui.Info("Выбор версии отменен.")
			return nil
		}
		return fmt.Errorf("ошибка выбора версии: %w", err)
	}
	tui.SuccessF("Выбрана версия: %s", selectedVersion)

	// 3. Для Front: предлагаем выбрать патч
	var selectedPatch IikoPatch
	var patchSelected bool
	if component.ID == "front" {
		selectedPatch, patchSelected, err = FindAndSelectPatch(am, selectedVersion)
		if err != nil {
			// Не критичная ошибка, просто предупреждаем
			tui.Warn(fmt.Sprintf("Произошла ошибка при выборе патча: %v", err))
		}
	}

	// 4. Запускаем установку
	return m.installComponent(am, wu, component, selectedVersion, selectedPatch, patchSelected)
}

// installComponent скачивает по HTTP и устанавливает версионный компонент
func (m *Module) installComponent(am core.AssetManager, wu core.WinUtils, component config.DistroInfo, version string, patch IikoPatch, patchSelected bool) error {
	// Формируем URL и имя
	downloadURL := strings.Replace(component.URLTemplate, "{{VERSION}}", version, 1)
	fileName := filepath.Base(downloadURL)
	distroName := fmt.Sprintf("iiko %s (%s)", version, component.MenuText)

	tui.Title(fmt.Sprintf("\n--- Начало установки %s ---", distroName))

	// Скачиваем установщик
	installerPath := filepath.Join(am.Cfg().AssetsCachePath, fileName)
	tui.Info("Скачивание дистрибутива...")
	_, err := am.DownloadHTTPWithProgress(downloadURL, installerPath)
	if err != nil {
		return fmt.Errorf("не удалось скачать установщик %s: %w", distroName, err)
	}

	// Запускаем установку
	exitCode, err := m.runInstaller(wu, installerPath, component.InstallArgs, am.Cfg().RootPath)
	if err != nil {
		return err
	}
	if exitCode != 0 {
		return fmt.Errorf("установщик завершился с кодом ошибки: %d", exitCode)
	}
	tui.Success(fmt.Sprintf("\nУстановка %s успешно завершена.", distroName))

	// Применяем патч и обновляем плагины для Front
	if component.ID == "front" {
		tui.Info("Выполняется автоматическое обновление плагинов iiko...")
		pluginsModule := &iikoplugins.Module{}
		if err := pluginsModule.AutoUpdatePlugins(am, wu); err != nil {
			tui.Warn(fmt.Sprintf("Ошибка при автообновлении плагинов: %v", err))
		} else {
			tui.Success("Автоматическое обновление плагинов завершено.")
		}

		if patchSelected {
			if component.RunAfter != "" {
				installDir := filepath.Dir(component.RunAfter)
				backupDir := filepath.Join(am.Cfg().RootPath, version) // Директория для бэкапа
				err := applyPatch(am, wu, patch, installDir, backupDir)
				if err != nil {
					tui.Error(fmt.Sprintf("Критическая ошибка при применении патча: %v", err))
					tui.Error("Основная программа установлена, но патч - нет. Попробуйте установить его через Утилиты обслуживания.")
				}
			} else {
				tui.Warn("Не удалось применить патч: не указан путь установки iiko (run_after).")
			}
		}
	}

	// Запуск приложения после установки
	if component.RunAfter != "" {
		if _, err := os.Stat(component.RunAfter); err == nil {
			tui.InfoF("Запуск %s...", component.RunAfter)
			exec.Command(component.RunAfter).Start()
		}
	}

	return nil
}

// installIikoCard скачивает с FTP и устанавливает iikoCard
func (m *Module) installIikoCard(am core.AssetManager, wu core.WinUtils, component config.DistroInfo) error {
	distroName := component.MenuText
	tui.Title(fmt.Sprintf("\n--- Начало установки %s ---", distroName))

	// Путь для iikoCard - из конфига BaseFTPPath
	ftpPath := filepath.Join(am.Cfg().IikoConfig.BaseFTPPath, component.FileName)
	ftpPath = strings.ReplaceAll(ftpPath, "\\", "/") // Для FTP нужны прямые слеши

	// Скачиваем установщик
	installerPath := filepath.Join(am.Cfg().AssetsCachePath, component.FileName)
	tui.Info("Скачивание дистрибутива с FTP...")
	_, err := am.DownloadFTPWithProgress(ftpPath, installerPath)
	if err != nil {
		return fmt.Errorf("не удалось скачать установщик %s: %w", distroName, err)
	}

	// Запускаем установку
	exitCode, err := m.runInstaller(wu, installerPath, component.InstallArgs, am.Cfg().RootPath)
	if err != nil {
		return err
	}
	if exitCode != 0 {
		return fmt.Errorf("установщик завершился с кодом ошибки: %d", exitCode)
	}
	tui.Success(fmt.Sprintf("\nУстановка %s успешно завершена.", distroName))

	return nil
}

// selectComponentMenu показывает меню выбора компонента.
func (m *Module) selectComponentMenu() (config.DistroInfo, error) {
	reader := bufio.NewReader(os.Stdin)
	var components []config.DistroInfo
	components = append(components, m.Cfg.DistroURLTemplates...)
	components = append(components, m.Cfg.CardPOS)

	for {
		tui.Title("\n--- Выберите дистрибутив для установки ---")
		for i, comp := range components {
			fmt.Printf(" %d. %s\n", i+1, comp.MenuText)
		}
		fmt.Println("\n 0. Назад")
		fmt.Print("Введите номер пункта: ")

		choiceStr, _ := reader.ReadString('\n')
		choice, err := strconv.Atoi(strings.TrimSpace(choiceStr))

		if choice == 0 {
			return config.DistroInfo{}, errors.New("выбор отменен")
		}

		if err != nil || choice < 1 || choice > len(components) {
			tui.Error("Неверный выбор. Попробуйте снова.")
			time.Sleep(1 * time.Second)
			continue
		}
		return components[choice-1], nil
	}
}

// selectVersionMenu отображает интерактивное меню выбора версии с поиском.
func (m *Module) selectVersionMenu(versions []string) (string, error) {
	// Функция нормализации убирает точки для сравнения (9.2.8... -> 928...)
	normalize := func(s string) string {
		return strings.ReplaceAll(s, ".", "")
	}

	searcher := func(input string, index int) bool {
		normalizedInput := normalize(input)
		normalizedItem := normalize(versions[index])
		return strings.HasPrefix(normalizedItem, normalizedInput)
	}

	prompt := promptui.Select{
		Label:             "Выберите версию (введите часть версии, например, 928)",
		Items:             versions,
		StartInSearchMode: true,
		Searcher:          searcher,
	}

	_, result, err := prompt.Run()
	return result, err
}

// --- Остальные функции (discoverOfficialVersions, compareSemanticVersions, runInstaller) ---
// Они уже были в предыдущем коде и остаются без изменений, просто для полноты файла.

func (m *Module) discoverOfficialVersions() ([]string, error) {
	var allErrors []string
	for _, host := range ftpHosts {
		tui.InfoF("Попытка подключения к %s...", host)
		c, err := ftp.Dial(host, ftp.DialWithTimeout(15*time.Second))
		if err != nil {
			errMsg := fmt.Sprintf("ошибка подключения к %s: %v", host, err)
			tui.Warn(errMsg)
			allErrors = append(allErrors, errMsg)
			continue
		}

		err = c.Login(ftpUser, ftpPass)
		if err != nil {
			c.Quit()
			errMsg := fmt.Sprintf("ошибка входа на %s: %v", host, err)
			tui.Warn(errMsg)
			allErrors = append(allErrors, errMsg)
			continue
		}

		entries, err := c.List(releasesPath)
		c.Quit()
		if err != nil {
			errMsg := fmt.Sprintf("ошибка получения списка каталогов с %s: %v", host, err)
			tui.Warn(errMsg)
			allErrors = append(allErrors, errMsg)
			continue
		}

		tui.SuccessF("Успешно подключились и получили список с %s", host)
		versionRegex := regexp.MustCompile(`^\d+\.\d+\.\d+\.\d+$`)
		var versions []string

		for _, entry := range entries {
			if entry.Type == ftp.EntryTypeFolder && versionRegex.MatchString(entry.Name) {
				if compareSemanticVersions(entry.Name, minVersion) >= 0 {
					versions = append(versions, entry.Name)
				}
			}
		}

		sort.Slice(versions, func(i, j int) bool {
			return compareSemanticVersions(versions[i], versions[j]) > 0
		})

		return versions, nil
	}

	return nil, fmt.Errorf("не удалось получить данные ни с одного из FTP-серверов: %s", strings.Join(allErrors, "; "))
}

func compareSemanticVersions(v1, v2 string) int {
	parts1 := strings.Split(v1, ".")
	parts2 := strings.Split(v2, ".")
	len1, len2 := len(parts1), len(parts2)
	maxLen := len1
	if len2 > maxLen {
		maxLen = len2
	}

	for i := 0; i < maxLen; i++ {
		var num1, num2 int
		if i < len1 {
			num1, _ = strconv.Atoi(parts1[i])
		}
		if i < len2 {
			num2, _ = strconv.Atoi(parts2[i])
		}
		if num1 > num2 {
			return 1
		}
		if num1 < num2 {
			return -1
		}
	}
	return 0
}

func (m *Module) runInstaller(wu core.WinUtils, installerPath, args, rootPath string) (int, error) {
	logFileName := fmt.Sprintf("installer_log_%d.txt", time.Now().Unix())
	tempLogPath := filepath.Join(rootPath, "temp", logFileName)
	_ = os.MkdirAll(filepath.Dir(tempLogPath), 0755)

	baseArgs := strings.Fields(args)
	finalArgs := append(baseArgs, "/log", tempLogPath)

	fmt.Printf("\nЗапуск установщика: %s с аргументами %v\n", installerPath, finalArgs)
	fmt.Println("... ИДЕТ УСТАНОВКА, ПОЖАЛУЙСТА, ОЖИДАЙТЕ ...")

	_, err := wu.RunCommand(installerPath, finalArgs...)
	var exitCode int
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			return -1, fmt.Errorf("не удалось запустить установщик: %w", err)
		}
	} else {
		exitCode = 0
	}

	if _, statErr := os.Stat(tempLogPath); statErr == nil {
		if exitCode != 0 {
			finalLogPath := filepath.Join(rootPath, logFileName)
			fmt.Printf("Установщик завершился с ошибкой. Сохраняем лог в: %s\n", finalLogPath)
			if renameErr := os.Rename(tempLogPath, finalLogPath); renameErr != nil {
				fmt.Printf("Предупреждение: не удалось переместить лог-файл: %v\n", renameErr)
			}
		} else {
			os.Remove(tempLogPath)
		}
	}

	return exitCode, nil
}
