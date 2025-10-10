package iiko

import (
	"bufio"
	"errors"
	"fmt"
	"goMH/config"
	"goMH/core"
	"goMH/modules/iiko-plugins"
	"goMH/tui"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// DiscoveredVersions хранит найденные на FTP версии и их компоненты
type DiscoveredVersions map[string][]config.IikoComponent

var errUserChoseExit = errors.New("пользователь выбрал выход в главное меню")

type Module struct {
	Cfg *config.IikoConfig
}

func (m *Module) ID() string       { return "iiko" }
func (m *Module) MenuText() string { return "iiko (Front, Back, Card)" }

func (m *Module) Run(am core.AssetManager, wu core.WinUtils) error {
	m.Cfg = &am.Cfg().IikoConfig

	// 1. Сканируем FTP на предмет доступных версий
	tui.Info("Сканирование FTP на наличие дистрибутивов iiko...")
	discovered, err := m.discoverVersions(am)
	if err != nil {
		return fmt.Errorf("не удалось просканировать FTP: %w", err)
	}
	if len(discovered) == 0 {
		return fmt.Errorf("на FTP не найдено ни одной корректной версии iiko")
	}

	// 2. Показываем меню выбора дистрибутива
	selectedComponent, err := m.showDistroMenu(discovered)
	if err != nil {
		if errors.Is(err, errUserChoseExit) {
			tui.Info("Возврат в главное меню.")
			return nil
		}
		return err
	}

	// --- ИЗМЕНЕНИЕ ЛОГИКИ ---
	// 3. Сразу после выбора дистрибутива, если это Front, предлагаем выбрать патч.
	var selectedPatch IikoPatch
	var patchWasSelected bool

	if selectedComponent.ID == "Front" {
		// Вызываем новую функцию, которая только находит и предлагает выбрать патч
		selectedPatch, patchWasSelected, err = FindAndSelectPatch(am, selectedComponent.Version)
		if err != nil {
			// В случае серьезной ошибки прерываем установку
			return fmt.Errorf("критическая ошибка при выборе патча: %w", err)
		}
	}
	// --- КОНЕЦ ИЗМЕНЕНИЯ ЛОГИКИ ---

	distroName := "iiko " + selectedComponent.Version + " " + selectedComponent.MenuText
	if selectedComponent.ID == "iikoCard" {
		distroName = selectedComponent.MenuText
	}
	tui.Title(fmt.Sprintf("\n--- Начало установки %s ---", distroName))

	targetDir := filepath.Join(am.Cfg().RootPath, selectedComponent.Version)
	if selectedComponent.ID == "iikoCard" {
		targetDir = filepath.Join(am.Cfg().RootPath, "iikoCardPOS")
	}
	_ = os.MkdirAll(targetDir, 0755)

	installerPath := filepath.Join(targetDir, selectedComponent.FileName)

	// 4. Скачиваем основной установщик
	tui.Info("Скачивание основного дистрибутива...")
	_, err = am.DownloadFTPWithProgress(selectedComponent.FTPPath, installerPath)
	if err != nil {
		return fmt.Errorf("не удалось скачать установщик %s: %w", distroName, err)
	}

	// 5. Запускаем установщик
	exitCode, err := m.runInstaller(wu, installerPath, selectedComponent.InstallArgs, am.Cfg().RootPath)
	if err != nil {
		return err
	}
	if exitCode != 0 {
		return fmt.Errorf("установщик завершился с кодом ошибки: %d", exitCode)
	}

	tui.Success(fmt.Sprintf("\nУстановка %s успешно завершена.", distroName))

	// Автоматическое обновление плагинов для iikoFront
	if selectedComponent.ID == "Front" {
		tui.Info("Выполняется автоматическое обновление плагинов iiko...")
		pluginsModule := &iikoplugins.Module{}
		if err := pluginsModule.AutoUpdatePlugins(am, wu); err != nil {
			tui.Warn(fmt.Sprintf("Ошибка при автоматическом обновлении плагинов: %v", err))
		} else {
			tui.Success("Автоматическое обновление плагинов завершено.")
		}
	}

	// --- ИЗМЕНЕНИЕ ЛОГИКИ ---
	// 6. Применяем предварительно выбранный патч
	if patchWasSelected {
		if selectedComponent.RunAfter != "" {
			installDir := filepath.Dir(selectedComponent.RunAfter)
			// Вызываем функцию, которая только применяет патч
			err = applyPatch(am, wu, selectedPatch, installDir, targetDir)
			if err != nil {
				// Применение патча - важный шаг, в случае ошибки сообщаем о ней
				tui.Error(fmt.Sprintf("Критическая ошибка при применении патча: %v", err))
				tui.Error("Основная программа была установлена, но патч - нет. Попробуйте установить патч через Утилиты обслуживания.")
			}
		} else {
			tui.Warn("Не удалось применить патч, так как не указан путь установки iiko (run_after).")
		}
	}
	// --- КОНЕЦ ИЗМЕНЕНИЯ ЛОГИКИ ---

	// 7. Запуск приложения после установки
	if selectedComponent.RunAfter != "" {
		if _, err := os.Stat(selectedComponent.RunAfter); err == nil {
			tui.InfoF("Запуск %s...", selectedComponent.RunAfter)
			exec.Command(selectedComponent.RunAfter).Start()
		}
	}

	return nil
}

// --- Функции-помощники ---

func (m *Module) discoverVersions(am core.AssetManager) (DiscoveredVersions, error) {
	entries, err := am.ListFTP(m.Cfg.BaseFTPPath)
	if err != nil {
		return nil, err
	}

	discovered := make(DiscoveredVersions)
	versionRegex := regexp.MustCompile(`^\d{3}$`)

	for _, entry := range entries {
		// Проверяем, что это директория
		if entry.Type != 1 || !versionRegex.MatchString(entry.Name) {
			continue
		}
		version := entry.Name
		versionPath := filepath.Join(m.Cfg.BaseFTPPath, version)
		versionPath = strings.ReplaceAll(versionPath, "\\", "/") // FTP пути используют /

		filesInVersion, err := am.ListFTP(versionPath)
		if err != nil {
			continue
		}

		filesMap := make(map[string]bool)
		for _, file := range filesInVersion {
			filesMap[file.Name] = true
		}

		var foundComponents []config.IikoComponent
		for _, compTmpl := range m.Cfg.ComponentsToFind {
			if filesMap[compTmpl.FileName] {
				comp := compTmpl // Копируем шаблон
				comp.Version = version
				comp.FTPPath = versionPath + "/" + comp.FileName
				foundComponents = append(foundComponents, comp)
			}
		}

		if len(foundComponents) > 0 {
			discovered[version] = foundComponents
		}
	}
	return discovered, nil
}

func (m *Module) showDistroMenu(versions DiscoveredVersions) (config.IikoComponent, error) {
	reader := bufio.NewReader(os.Stdin)
	var menuOptions []config.IikoComponent
	var versionsSorted []string
	for v := range versions {
		versionsSorted = append(versionsSorted, v)
	}
	sort.Sort(sort.Reverse(sort.StringSlice(versionsSorted))) // Сначала новые версии

	for {
		tui.Title("\n--- Выберите дистрибутив iiko для установки ---")
		menuOptions = nil

		// Опция 0 - iikoCard
		cardPosOption := m.Cfg.CardPOS
		cardPosOption.ID = "iikoCard"
		cardPosOption.Version = "Card"
		cardPosOption.FTPPath = m.Cfg.BaseFTPPath + "/" + cardPosOption.FileName
		menuOptions = append(menuOptions, cardPosOption)
		fmt.Printf(" %d. %s\n", 0, cardPosOption.MenuText)

		// Остальные опции
		for _, version := range versionsSorted {
			fmt.Printf("--- Версия iiko %s ---\n", version)
			for _, comp := range versions[version] {
				menuOptions = append(menuOptions, comp)
				fmt.Printf(" %d. %s %s\n", len(menuOptions)-1, version, comp.MenuText)
			}
		}

		fmt.Println("\n 00. Назад в главное меню")
		fmt.Print("Введите номер пункта: ")

		choiceStr, _ := reader.ReadString('\n')
		choiceStr = strings.TrimSpace(choiceStr)

		if choiceStr == "00" {
			return config.IikoComponent{}, errUserChoseExit
		}

		choice, err := strconv.Atoi(choiceStr)
		if err != nil || choice < 0 || choice >= len(menuOptions) {
			tui.Error("Некорректный выбор. Попробуйте снова. 2 секунды...")
			time.Sleep(2 * time.Second)
			continue // Показываем меню заново
		}
		return menuOptions[choice], nil
	}
}

func (m *Module) runInstaller(wu core.WinUtils, installerPath, args, rootPath string) (int, error) {
	// Создаем путь для временного лог-файла внутри root-каталога
	logFileName := fmt.Sprintf("installer_log_%d.txt", time.Now().Unix())
	tempLogPath := filepath.Join(rootPath, "temp", logFileName)

	// Формируем аргументы для установщика
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
			// Если ошибка не связана с кодом завершения (например, файл не найден),
			// возвращаем ее как критическую.
			return -1, fmt.Errorf("не удалось запустить установщик: %w", err)
		}
	} else {
		exitCode = 0
	}

	// Проверяем, был ли создан лог-файл
	if _, statErr := os.Stat(tempLogPath); statErr == nil {
		if exitCode != 0 {
			// Установка завершилась с ошибкой, ПЕРЕМЕЩАЕМ лог
			finalLogPath := filepath.Join(rootPath, logFileName)
			fmt.Printf("Установщик завершился с ошибкой. Сохраняем лог в: %s\n", finalLogPath)
			if renameErr := os.Rename(tempLogPath, finalLogPath); renameErr != nil {
				fmt.Printf("Предупреждение: не удалось переместить лог-файл: %v\n", renameErr)
				// Если переместить не удалось, пробуем хотя бы не удалять его из временной папки
			}
		} else {
			// Установка успешна, УДАЛЯЕМ временный лог
			os.Remove(tempLogPath)
		}
	}

	return exitCode, nil
}
