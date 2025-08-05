package iiko

import (
	"archive/zip"
	"bufio"
	"encoding/csv"
	"fmt"
	"goMH/config"
	"goMH/core"
	"io"
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

type IikoPatch struct {
	Path        string
	Name        string
	Description string
	Version     string
	LocalPath   string
	Downloaded  bool
}

type Module struct {
	Cfg *config.IikoConfig
}

func (m *Module) ID() string       { return "iiko" }
func (m *Module) MenuText() string { return "iiko (Front, Back, Card)" }

func (m *Module) Run(am core.AssetManager, wu core.WinUtils) error {
	m.Cfg = &am.Cfg().IikoConfig

	// 1. Сканируем FTP на предмет доступных версий
	fmt.Println("Сканирование FTP на наличие дистрибутивов iiko...")
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
		return err // Пользователь вышел
	}

	distroName := "iiko " + selectedComponent.Version + " " + selectedComponent.MenuText
	if selectedComponent.ID == "iikoCard" {
		distroName = selectedComponent.MenuText
	}
	fmt.Printf("\n--- Начало установки %s ---\n", distroName)

	targetDir := filepath.Join(am.Cfg().RootPath, selectedComponent.Version)
	if selectedComponent.ID == "iikoCard" {
		targetDir = filepath.Join(am.Cfg().RootPath, "iikoCardPOS")
	}
	_ = os.MkdirAll(targetDir, 0755)

	installerPath := filepath.Join(targetDir, selectedComponent.FileName)

	// 3. Скачиваем основной установщик
	_, err = am.DownloadFTPWithProgress(selectedComponent.FTPPath, installerPath)
	if err != nil {
		return fmt.Errorf("не удалось скачать установщик %s: %w", distroName, err)
	}

	// 4. Обрабатываем патчи (только для Front)
	var patchesToInstall []IikoPatch
	if selectedComponent.ID == "Front" {
		patchesToInstall, err = m.handlePatches(am, selectedComponent.Version, targetDir)
		if err != nil {
			fmt.Printf("Предупреждение: не удалось обработать патчи: %v. Установка продолжится без них.\n", err)
		}
	}

	// 5. Запускаем установщик
	exitCode, err := m.runInstaller(wu, installerPath, selectedComponent.InstallArgs, am.Cfg().RootPath)
	if err != nil {
		return err
	}
	if exitCode != 0 {
		return fmt.Errorf("установщик завершился с кодом ошибки: %d", exitCode)
	}

	fmt.Printf("\nУстановка %s успешно завершена.\n", distroName)

	// 6. Применяем патчи
	if len(patchesToInstall) > 0 {
		if selectedComponent.RunAfter != "" {
			installDir := filepath.Dir(selectedComponent.RunAfter)
			for _, patch := range patchesToInstall {
				if patch.Downloaded {
					fmt.Printf("\n--- Применение патча: %s ---\n", patch.Name)
					if err := m.applyIikoPatch(installDir, patch.LocalPath, patch.Name); err != nil {
						fmt.Printf("ОШИБКА при применении патча %s: %v\n", patch.Name, err)
					}
				}
			}
		} else {
			fmt.Printf("Предупреждение: не удалось применить патчи, так как не указана команда запуска после установки.\n")
		}
	}

	// 7. Запуск приложения после установки
	if selectedComponent.RunAfter != "" {
		if _, err := os.Stat(selectedComponent.RunAfter); err == nil {
			fmt.Printf("Запуск %s...\n", selectedComponent.RunAfter)
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

	fmt.Println("\nВыберите дистрибутив для установки:")
	// Добавляем iikoCard как опцию 0
	cardPosOption := m.Cfg.CardPOS
	cardPosOption.ID = "iikoCard"
	cardPosOption.Version = "Card"
	cardPosOption.FTPPath = m.Cfg.BaseFTPPath + "/" + cardPosOption.FileName
	menuOptions = append(menuOptions, cardPosOption)
	fmt.Printf(" 0) %s\n", cardPosOption.MenuText)

	// Добавляем найденные версии
	for _, version := range versionsSorted {
		fmt.Printf("--- Версия iiko %s ---\n", version)
		for _, comp := range versions[version] {
			menuOptions = append(menuOptions, comp)
			fmt.Printf(" %d) %s %s\n", len(menuOptions)-1, version, comp.MenuText)
		}
	}

	for {
		fmt.Print("Введите номер пункта: ")
		choiceStr, _ := reader.ReadString('\n')
		choice, err := strconv.Atoi(strings.TrimSpace(choiceStr))
		if err != nil || choice < 0 || choice >= len(menuOptions) {
			fmt.Println("Некорректный выбор. Попробуйте снова.")
			continue
		}
		return menuOptions[choice], nil
	}
}

func (m *Module) handlePatches(am core.AssetManager, version, targetDir string) ([]IikoPatch, error) {
	routeFTPPath := m.Cfg.BaseFTPPath + m.Cfg.PatchRouteFile
	tempRouteFile := filepath.Join(os.TempDir(), "patcher_route.txt")

	_, err := am.DownloadFTPWithProgress(routeFTPPath, tempRouteFile)
	if err != nil {
		return nil, fmt.Errorf("не удалось скачать файл с патчами: %w", err)
	}
	defer os.Remove(tempRouteFile)

	file, err := os.Open(tempRouteFile)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	r := csv.NewReader(file)
	r.FieldsPerRecord = 4
	records, err := r.ReadAll()
	if err != nil {
		return nil, err
	}

	var availablePatches []IikoPatch
	for _, rec := range records {
		if strings.TrimSpace(rec[3]) == version {
			availablePatches = append(availablePatches, IikoPatch{
				Path:        rec[0],
				Name:        rec[1],
				Description: rec[2],
				Version:     rec[3],
			})
		}
	}

	if len(availablePatches) == 0 {
		fmt.Println("Актуальные патчи для этой версии не найдены.")
		return nil, nil
	}

	// Показываем меню выбора патчей
	reader := bufio.NewReader(os.Stdin)
	fmt.Println("\nНайдены следующие патчи. Выберите, какие установить:")
	for i, p := range availablePatches {
		fmt.Printf(" %d) %s - %s\n", i+1, p.Name, p.Description)
	}
	fmt.Print("Введите номера через запятую (напр., 1,3) или Enter для пропуска: ")
	choiceStr, _ := reader.ReadString('\n')
	choiceStr = strings.TrimSpace(choiceStr)
	if choiceStr == "" {
		return nil, nil
	}

	var selectedPatches []IikoPatch
	for _, idxStr := range strings.Split(choiceStr, ",") {
		idx, err := strconv.Atoi(strings.TrimSpace(idxStr))
		if err == nil && idx >= 1 && idx <= len(availablePatches) {
			patch := availablePatches[idx-1]
			patch.LocalPath = filepath.Join(targetDir, filepath.Base(patch.Path))
			patch.Downloaded = false
			selectedPatches = append(selectedPatches, patch)
		}
	}

	// Скачиваем выбранные патчи
	for i := range selectedPatches {
		patch := &selectedPatches[i] // Берем указатель, чтобы изменять поле Downloaded
		ftpURL := m.Cfg.BaseFTPPath + patch.Path
		_, err := am.DownloadFTPWithProgress(ftpURL, patch.LocalPath)
		if err != nil {
			fmt.Printf("ОШИБКА скачивания патча %s: %v\n", patch.Name, err)
		} else {
			patch.Downloaded = true
		}
	}

	return selectedPatches, nil
}

func (m *Module) runInstaller(wu core.WinUtils, installerPath, args, rootPath string) (int, error) {
	// Создаем путь для временного лог-файла
	logFileName := fmt.Sprintf("installer_log_%d.txt", time.Now().Unix())
	tempLogPath := filepath.Join(os.TempDir(), logFileName)

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

func (m *Module) applyIikoPatch(installDir, patchZipPath, patchName string) error {
	backupDir := filepath.Join(filepath.Dir(patchZipPath), fmt.Sprintf("backup_%s_%d", patchName, time.Now().Unix()))
	if err := os.MkdirAll(backupDir, 0755); err != nil {
		return fmt.Errorf("не удалось создать папку для бэкапа: %w", err)
	}
	fmt.Printf("Создана папка для бэкапа: %s\n", backupDir)

	r, err := zip.OpenReader(patchZipPath)
	if err != nil {
		return err
	}
	defer r.Close()

	for _, f := range r.File {
		destPath := filepath.Join(installDir, f.Name)
		if f.FileInfo().IsDir() {
			os.MkdirAll(destPath, f.Mode())
			continue
		}

		// Бэкап существующего файла
		if _, err := os.Stat(destPath); err == nil {
			backupPath := filepath.Join(backupDir, f.Name)
			os.MkdirAll(filepath.Dir(backupPath), 0755)
			if err := os.Rename(destPath, backupPath); err != nil {
				fmt.Printf("Предупреждение: не удалось сделать бэкап файла %s: %v\n", destPath, err)
			}
		}

		// Распаковка нового файла
		os.MkdirAll(filepath.Dir(destPath), 0755)
		destFile, err := os.OpenFile(destPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, f.Mode())
		if err != nil {
			return err
		}

		srcFile, err := f.Open()
		if err != nil {
			destFile.Close()
			return err
		}

		_, err = io.Copy(destFile, srcFile)
		destFile.Close()
		srcFile.Close()
		if err != nil {
			return err
		}
	}
	fmt.Printf("Патч '%s' успешно применен.\n", patchName)
	return nil
}
