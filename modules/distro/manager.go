package distro

import (
	"errors"
	"fmt"
	"goMH/config"
	"goMH/core"
	"goMH/dependencies"
	iikoplugins "goMH/modules/iiko-plugins"
	"goMH/tui"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/manifoldco/promptui"
)

// Module реализует интерфейс core.Installer.
type Module struct{}

func (m *Module) ID() string { return "iiko" }
func (m *Module) MenuText() string {
	return "iiko / Syrve (Дистрибутивы и плагины)"
}

// DistroManager управляет всем процессом установки.
type DistroManager struct {
	AM core.AssetManager
	WU core.WinUtils
}

// Run - основная точка входа в модуль.
func (m *Module) Run(am core.AssetManager, wu core.WinUtils) error {
	slog.Info("Запуск модуля Distro")
	dm := &DistroManager{AM: am, WU: wu}
	return dm.selectBrand()
}

// selectBrand - первый шаг, выбор между iiko и Syrve (цифровое меню с мгновенным вводом).
func (dm *DistroManager) selectBrand() error {
	slog.Debug("Отображение меню выбора бренда (iiko/Syrve)")

	for {
		tui.ClearScreen()
		tui.Title("\n--- Выберите продукт ---")
		fmt.Println(" 1. iiko")
		fmt.Println(" 2. Syrve")
		fmt.Println("\n 0. Назад в главное меню")
		fmt.Print("Ваш выбор: ")

		// Используем ReadKey для мгновенного ввода
		key, err := tui.ReadKey()
		if err != nil {
			// Если прерывание (Ctrl+C), выходим
			return nil
		}

		switch key {
		case "1":
			slog.Info("Выбран бренд iiko")
			handler := &iikoHandler{dm: dm}
			return handler.Run()
		case "2":
			slog.Info("Выбран бренд Syrve")
			handler := &syrveHandler{dm: dm}
			return handler.Run()
		case "0":
			slog.Info("Выбор бренда отменен пользователем")
			return nil
		default:
			// Игнорируем или мигаем ошибкой
			// Для мгновенного меню лучше просто игнорировать неверные клавиши или быстро показать сообщение
		}
	}
}

// brandHandler определяет интерфейс для специфичной логики бренда.
type brandHandler interface {
	Run() error
	getAvailableVersions() ([]string, error)
	getAvailablePortableVersions(component config.DistroComponent) ([]string, error)
	installComponent(component config.DistroComponent, version string) error
	installPortable(component config.DistroComponent, version string) error
}

// StartInstallFlow - общий сценарий установки для УЖЕ выбранного компонента.
func (dm *DistroManager) StartInstallFlow(h brandHandler, component config.DistroComponent) error {
	slog.Info("Запуск потока установки", "component", component.ID)

	// Определяем установленную версию (если компонент версионный)
	// Делаем это ВСЕГДА, если указан путь RunAfter
	var installedVer string
	if component.RunAfter != "" {
		ver, err := dm.WU.GetFileVersion(component.RunAfter)
		if err == nil {
			installedVer = ver
			slog.Info("Обнаружена установленная версия", "component", component.ID, "version", installedVer)
		} else {
			slog.Debug("Не удалось определить версию (возможно не установлено)", "path", component.RunAfter, "error", err)
		}
	}

	// Для компонентов без версий сразу запускаем установку
	isVersioned := component.URLTemplate == "" || strings.Contains(component.URLTemplate, "{{VERSION}}")
	if !isVersioned {
		return h.installComponent(component, "")
	}

	// Меню выбора режима установки (Обычная / Портативная)
	mode := "install"
	if component.PortableArchiveKey != "" {
		items := []string{"Стандартная установка (в Program Files)", "Портативная версия (распаковка)", "Назад"}
		label := fmt.Sprintf("Выберите режим установки для %s", component.MenuText)

		if installedVer != "" {
			label = fmt.Sprintf("%s (Установлено: %s)", label, installedVer)
		}

		tui.DisableConsoleBeep()
		prompt := promptui.Select{
			Label: label,
			Items: items,
		}
		_, result, err := prompt.Run()
		tui.RestoreConsoleBeep()
		if err != nil || result == "Назад" {
			return nil
		}

		if result == "Портативная версия (распаковка)" {
			mode = "portable"
		}
	}

	if mode == "portable" {
		slog.Info("Выбор портативной установки")
		versions, err := h.getAvailablePortableVersions(component)
		if err != nil {
			slog.Error("Ошибка получения версий portable", "error", err)
			return err
		}
		if len(versions) == 0 {
			return fmt.Errorf("не найдено доступных портативных версий для %s", component.MenuText)
		}
		version, err := dm.selectVersionMenu(versions, "Выберите версию для портативной установки")
		if err != nil {
			if err.Error() == "exit_to_main_menu" {
				return tui.ErrExitToMainMenu
			}
			return err
		}
		return h.installPortable(component, version)
	}

	// --- СТАНДАРТНАЯ УСТАНОВКА ---

	versions, err := h.getAvailableVersions()
	if err != nil {
		slog.Error("Ошибка получения версий", "error", err)
		return err
	}
	if len(versions) == 0 {
		return fmt.Errorf("не найдено доступных версий для %s", component.MenuText)
	}

	// Добавляем метку (current) в меню выбора версий, если версия совпадает с установленной
	versionPrompt := "Выберите версию для установки"
	if installedVer != "" {
		versionPrompt = fmt.Sprintf("Выберите версию для установки (Текущая: %s)", installedVer)
	}

	version, err := dm.selectVersionMenu(versions, versionPrompt)
	if err != nil {
		if err.Error() == "exit_to_main_menu" {
			return tui.ErrExitToMainMenu
		}
		slog.Error("Выбор версии отменен", "error", err)
		return err
	}

	// --- ПРОВЕРКА НА ПОНИЖЕНИЕ ВЕРСИИ ---
	if installedVer != "" {
		// Используем compareSemanticVersions из iiko.go (он в том же пакете distro, так что доступен)
		if compareSemanticVersions(version, installedVer) < 0 {
			tui.Warn(fmt.Sprintf("\nВНИМАНИЕ: Вы пытаетесь установить версию %s, которая ниже установленной %s.", version, installedVer))
			tui.Warn("Для корректной работы необходимо удалить текущую версию.")

			prompt := promptui.Prompt{
				Label:     "Вы хотите автоматически удалить текущую версию перед установкой? (Y/N)",
				IsConfirm: true,
			}
			_, err := prompt.Run()
			if err != nil {
				slog.Info("Пользователь отказался от даунгрейда")
				return nil // Отмена
			}

			// Запуск удаления старой версии
			if err := dm.uninstallOldVersion(h, component, installedVer); err != nil {
				slog.Error("Ошибка при удалении старой версии", "error", err)
				tui.Error(fmt.Sprintf("Не удалось удалить старую версию: %v", err))
				tui.Info("Попробуйте удалить её вручную через Панель управления.")
				return err
			}
		}
	}

	// Специальная логика для iikoFront (патчи, плагины)
	if component.ID == "iiko_front" {
		slog.Info("Выбран компонент iikoFront, запуск спец. логики")
		return dm.runIikoFrontInstall(h, component, version) // Передаем версию явно
	}

	// Обычная установка
	return h.installComponent(component, version)
}

// uninstallOldVersion скачивает установщик старой версии и запускает его удаление
func (dm *DistroManager) uninstallOldVersion(h brandHandler, component config.DistroComponent, version string) error {
	tui.InfoF("Подготовка к удалению версии %s...", version)
	slog.Info("Начало процедуры удаления старой версии", "version", version)

	// --- ПОПЫТКА 1: Системное удаление через реестр ---
	// Нам нужно сопоставить ID компонента с именем в "Установке и удалении программ"
	var systemAppName string
	switch component.ID {
	case "iiko_front":
		systemAppName = "iikoRMS Front"
	case "iiko_rms_back":
		systemAppName = "iikoRMS BackOffice"
	case "iiko_chain_back":
		systemAppName = "iikoChain"
	case "syrve_front":
		systemAppName = "SyrveRMS Front"
	case "syrve_rms_back":
		systemAppName = "SyrveRMS BackOffice"
	case "syrve_chain_back":
		systemAppName = "SyrveChain"
	}

	if systemAppName != "" {
		tui.InfoF("Попытка системного удаления через реестр для '%s'...", systemAppName)
		err := dm.WU.UninstallSystemApp(systemAppName)
		if err == nil {
			tui.Success("Системное удаление успешно инициировано.")
			return nil
		}
		slog.Warn("Системное удаление не удалось или приложение не найдено", "app", systemAppName, "error", err)
		tui.Warn(fmt.Sprintf("Системное удаление не удалось (%v). Переходим к варианту с деинсталлятором.", err))
	} else {
		slog.Debug("Системное имя не определено для компонента, пропуск шага реестра", "id", component.ID)
	}

	// --- ПОПЫТКА 2: Использование установщика (с проверкой кэша) ---

	var downloadURL string
	if component.URLTemplate != "" {
		downloadURL = strings.Replace(component.URLTemplate, "{{VERSION}}", version, 1)
	}

	fileName := filepath.Base(downloadURL)

	// Путь, где должен лежать дистрибутив этой версии
	// C:\MH\iiko_9.X.X\Setup.Front.exe
	versionDir := filepath.Join(dm.AM.Cfg().RootPath, fmt.Sprintf("%s_%s", strings.Split(component.ID, "_")[0], version)) // iiko_9.2.0 или syrve_9.2.0 - грубая эвристика, лучше использовать brandName, но он тут недоступен напрямую, используем префикс ID

	// Уточним префикс папки. iikoHandler создает "iiko_%s", syrveHandler создает "syrve_%s"
	if strings.HasPrefix(component.ID, "iiko") {
		versionDir = filepath.Join(dm.AM.Cfg().RootPath, fmt.Sprintf("iiko_%s", version))
	} else if strings.HasPrefix(component.ID, "syrve") {
		versionDir = filepath.Join(dm.AM.Cfg().RootPath, fmt.Sprintf("syrve_%s", version))
	}

	localInstallerPath := filepath.Join(versionDir, fileName)

	// Проверяем, есть ли файл локально
	if _, err := os.Stat(localInstallerPath); err == nil {
		tui.InfoF("Найден локальный дистрибутив: %s", localInstallerPath)
		slog.Info("Используем локальный дистрибутив для удаления", "path", localInstallerPath)
	} else {
		tui.Info("Локальный дистрибутив не найден. Скачивание...")

		// Создаем директорию, если её нет (например, если удаляем версию, которую ставили не через MH)
		_ = os.MkdirAll(versionDir, 0755)

		if strings.HasPrefix(downloadURL, "http") {
			if _, err := dm.AM.DownloadHTTPWithProgress(downloadURL, localInstallerPath); err != nil {
				return fmt.Errorf("не удалось скачать деинсталлятор: %w", err)
			}
		} else {
			ftpCfg := dm.AM.Cfg().FTP[0]
			if _, err := dm.AM.DownloadFTPWithProgress(ftpCfg, downloadURL, localInstallerPath); err != nil {
				return fmt.Errorf("не удалось скачать деинсталлятор по FTP: %w", err)
			}
		}
	}

	tui.Info("Запуск удаления через установщик...")

	// Формируем аргументы удаления.
	uninstallArgs := strings.ReplaceAll(component.InstallArgs, "/install", "/uninstall")
	if !strings.Contains(uninstallArgs, "/uninstall") {
		uninstallArgs = "/uninstall " + uninstallArgs
	}

	slog.Info("Запуск деинсталлятора", "path", localInstallerPath, "args", uninstallArgs)

	if err := dm.runInstaller(localInstallerPath, uninstallArgs); err != nil {
		return fmt.Errorf("процесс удаления завершился с ошибкой: %w", err)
	}

	tui.Success("Старая версия успешно удалена.")

	// Файл установщика НЕ удаляем, он теперь лежит в правильном месте (в папке версии) и может пригодиться.

	return nil
}

// runIikoFrontInstall обновлен для приема версии аргументом
func (dm *DistroManager) runIikoFrontInstall(h brandHandler, component config.DistroComponent, version string) error {
	// Версия уже выбрана в runWorkflow, используем её
	slog.Info("Установка iikoFront", "version", version)

	selectedPatch, patchSelected, err := FindAndSelectPatch(dm.AM, version)
	if err != nil {
		tui.Warn(fmt.Sprintf("Ошибка при выборе патча: %v. Установка продолжится без него.", err))
		slog.Error("Ошибка выбора патча", "error", err)
	}

	if err := h.installComponent(component, version); err != nil {
		slog.Error("Ошибка установки компонента", "error", err)
		return err
	}

	if patchSelected {
		installDir := filepath.Dir(component.RunAfter)
		// Бэкап рядом с версией: C:\MH\iiko_9.2.8035.0\backup_...
		versionDir := filepath.Join(dm.AM.Cfg().RootPath, fmt.Sprintf("iiko_%s", version))

		slog.Info("Применение патча", "installDir", installDir, "backupDir", versionDir)

		if err := ApplyPatch(dm.AM, dm.WU, selectedPatch, installDir, versionDir); err != nil {
			slog.Error("Критическая ошибка патча", "error", err)
			tui.Error(fmt.Sprintf("Критическая ошибка при применении патча: %v", err))
		}
	}

	tui.Info("Запуск автоматического обновления плагинов iiko...")
	pluginsModule := &iikoplugins.Module{}
	if err := pluginsModule.AutoUpdatePlugins(dm.AM, dm.WU); err != nil {
		slog.Error("Ошибка автообновления плагинов", "error", err)
		tui.Warn(fmt.Sprintf("Ошибка при автообновлении плагинов: %v", err))
	} else {
		slog.Info("Автообновление плагинов завершено")
		tui.Success("Автообновление плагинов завершено.")
	}

	return nil
}

// selectVersionMenu - общий UI для выбора версии.
func (dm *DistroManager) selectVersionMenu(versions []string, label string) (string, error) {
	tui.DisableConsoleBeep()
	defer tui.RestoreConsoleBeep()

	searcher := func(input string, index int) bool {
		return strings.HasPrefix(strings.ReplaceAll(versions[index], ".", ""), input)
	}
	prompt := promptui.Select{
		Label:             label + " (Ctrl+C для выхода в главное меню)",
		Items:             versions,
		StartInSearchMode: true,
		Searcher:          searcher,
	}
	_, result, err := prompt.Run()
	if err != nil {
		if strings.Contains(err.Error(), "interrupt") {
			return "", fmt.Errorf("exit_to_main_menu")
		}
		return "", errors.New("выбор версии отменен")
	}
	return result, nil
}

// runInstaller - корректный запуск MSI/EXE с логгированием и проверкой кода выхода.
func (dm *DistroManager) runInstaller(installerPath, args string) error {
	logFileName := fmt.Sprintf("installer_log_%d.txt", time.Now().Unix())
	tempLogPath := filepath.Join(dm.AM.Cfg().RootPath, "temp", logFileName)
	_ = os.MkdirAll(filepath.Dir(tempLogPath), 0755)

	finalArgs := append(strings.Fields(args), "/log", tempLogPath)
	slog.Info("Запуск установщика", "path", installerPath, "args", finalArgs)
	tui.InfoF("Запуск установщика: %s с аргументами %v", installerPath, finalArgs)
	tui.Info("... ИДЕТ УСТАНОВКА, ПОЖАЛУЙСТА, ОЖИДАЙТЕ ...")

	cmd := exec.Command(installerPath, finalArgs...)
	output, err := cmd.CombinedOutput()

	var exitCode int
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
			slog.Debug("Установщик завершился с кодом", "code", exitCode)
		} else {
			slog.Error("Не удалось запустить установщик", "error", err)
			return fmt.Errorf("не удалось запустить установщик: %w, вывод: %s", err, string(output))
		}
	}

	if exitCode != 0 {
		finalLogPath := filepath.Join(dm.AM.Cfg().RootPath, logFileName)
		_ = os.Rename(tempLogPath, finalLogPath)

		slog.Error("Установщик завершился с кодом", "code", exitCode, "log", finalLogPath)
		return fmt.Errorf("установщик завершился с кодом %d. Лог сохранен в: %s", exitCode, finalLogPath)
	}

	_ = os.Remove(tempLogPath)
	return nil
}

func (dm *DistroManager) installPortable(h brandHandler, brandName string, component config.DistroComponent, version string) error {
	tui.Title(fmt.Sprintf("\n--- Начало установки портативной версии: %s %s ---", component.MenuText, version))
	slog.Info("Начало установки портативной версии", "brand", brandName, "component", component.MenuText, "version", version)

	if component.PortableArchiveKey == "" {
		return fmt.Errorf("для компонента %s не задан ключ 'portable_archive_key'", component.MenuText)
	}

	var portableSrc config.BrandPortableSource
	if brandName == "iiko" {
		portableSrc = dm.AM.Cfg().DistroConfig.IikoPortable
	} else {
		portableSrc = dm.AM.Cfg().DistroConfig.SyrvePortable
	}

	var archiveName, downloadURL, ftpPath string
	var useHttp bool

	if portableSrc.HttpSource.Enabled {
		useHttp = true
		tpl := portableSrc.HttpSource.ArchiveNames[component.PortableArchiveKey]
		archiveName = strings.Replace(tpl, "{{version}}", version, 1)
		downloadURL = fmt.Sprintf("%s/%s", strings.TrimSuffix(portableSrc.HttpSource.URL, "/"), archiveName)
	} else if portableSrc.FtpSource.Enabled {
		useHttp = false
		tpl := portableSrc.FtpSource.ArchiveNames[component.PortableArchiveKey]
		archiveName = strings.Replace(tpl, "{{version}}", version, 1)
		ftpPath = fmt.Sprintf("%s/%s", strings.TrimSuffix(portableSrc.FtpSource.Directory, "/"), archiveName)
	} else {
		return errors.New("не включен ни один источник для портативных версий")
	}

	// Временная директория для скачивания (чтобы не мусорить)
	tempDir := filepath.Join(dm.AM.Cfg().RootPath, "temp", fmt.Sprintf("portable_dl_%d", time.Now().Unix()))
	if err := os.MkdirAll(tempDir, 0755); err != nil {
		return err
	}
	// Очистка в конце работы функции
	defer os.RemoveAll(tempDir)

	cachePath := filepath.Join(tempDir, filepath.Base(archiveName))

	if useHttp {
		_, err := dm.AM.DownloadHTTPWithProgress(downloadURL, cachePath)
		if err != nil {
			return err
		}
	} else {
		var ftpCfg config.FTPConfig
		if brandName == "syrve" {
			var err error
			ftpCfg, err = dm.AM.GetFastestFTP(dm.AM.Cfg().FTP[1:], "/speedtest.txt")
			if err != nil {
				return err
			}
		} else {
			ftpCfg = dm.AM.Cfg().FTP[0]
		}

		_, err := dm.AM.DownloadFTPWithProgress(ftpCfg, ftpPath, cachePath)
		if err != nil {
			if portableSrc.HttpSource.Enabled {
				tui.Warn(fmt.Sprintf("FTP недоступен, попытка HTTP: %v", err))
				_, err := dm.AM.DownloadHTTPWithProgress(downloadURL, cachePath)
				if err != nil {
					return fmt.Errorf("ошибка загрузки по FTP и HTTP: %v", err)
				}
			} else {
				return fmt.Errorf("ошибка загрузки по FTP: %w", err)
			}
		}
	}

	redaction := strings.ToUpper(strings.Split(strings.TrimPrefix(component.ID, brandName+"_"), "_")[0])
	destDir := filepath.Join(dm.AM.Cfg().RootPath, fmt.Sprintf("p_%s%s_%s", brandName, redaction, version))

	slog.Info("Распаковка портативной версии", "cachePath", cachePath, "destDir", destDir)
	tui.InfoF("Распаковка в: %s", destDir)

	// Используем 7-Zip для умной распаковки (flattening)
	sevenZipClient, err := dependencies.NewClient(dm.AM, dm.WU)
	if err != nil {
		return fmt.Errorf("не удалось создать клиент 7-Zip: %w", err)
	}

	// 1. Распаковываем во временную папку для анализа
	extractTempDir := filepath.Join(tempDir, "extracted")
	if err := sevenZipClient.Extract(cachePath, extractTempDir, true); err != nil {
		return fmt.Errorf("ошибка первичной распаковки архива: %w", err)
	}

	// 2. Анализируем содержимое
	entries, err := os.ReadDir(extractTempDir)
	if err != nil {
		return err
	}

	sourceDir := extractTempDir
	// Если внутри только одна папка, спускаемся в неё
	if len(entries) == 1 && entries[0].IsDir() {
		sourceDir = filepath.Join(extractTempDir, entries[0].Name())
		slog.Info("Обнаружена вложенная папка, извлекаем содержимое из неё", "subfolder", entries[0].Name())
	}

	// 3. Копируем из sourceDir в целевую destDir
	if err := os.MkdirAll(destDir, 0755); err != nil {
		return err
	}

	if err := dm.WU.CopyDir(sourceDir, destDir); err != nil {
		return fmt.Errorf("ошибка копирования файлов портативной версии: %w", err)
	}

	// --- СОЗДАНИЕ ЯРЛЫКА ---
	tui.Info("Создание ярлыка на рабочем столе...")

	// 1. Определяем имя исполняемого файла.
	// Берем его из RunAfter конфигурации компонента (например, "BackOffice.exe" из "C:\...\BackOffice.exe")
	exeName := filepath.Base(component.RunAfter)
	if exeName == "." || exeName == "" {
		// Фолбэк на стандартные имена, если конфиг пуст (маловероятно)
		if strings.Contains(strings.ToLower(component.ID), "front") {
			exeName = "iikoFront.Net.exe"
		} else {
			exeName = "BackOffice.exe"
		}
	}

	// 2. Ищем этот файл в целевой директории
	targetExePath := filepath.Join(destDir, exeName)
	if _, err := os.Stat(targetExePath); os.IsNotExist(err) {
		// Если файла нет в корне, ищем рекурсивно (на случай, если структура папок сложнее)
		foundPath, err := dm.WU.FindFileRecursive(destDir, exeName)
		if err == nil {
			targetExePath = foundPath
		} else {
			slog.Warn("Исполняемый файл для ярлыка не найден", "name", exeName, "dir", destDir)
			tui.Warn(fmt.Sprintf("Внимание: файл %s не найден, ярлык может не работать.", exeName))
			// Все равно пытаемся создать ярлык на предполагаемый путь
		}
	}

	// 3. Получаем путь к рабочему столу
	desktopDir, err := dm.WU.GetDesktopDir()
	if err != nil {
		slog.Error("Не удалось получить путь к рабочему столу", "error", err)
		// Не критичная ошибка, продолжаем
	} else {
		// 4. Формируем параметры ярлыка
		// Название: "iikoRMS BackOffice 9.2.0 (Portable).lnk"
		shortcutName := fmt.Sprintf("%s %s (Portable).lnk", component.MenuText, version)
		// Убираем недопустимые символы из имени файла
		shortcutName = strings.Map(func(r rune) rune {
			if strings.ContainsRune(`<>:"/\|?*`, r) {
				return -1
			}
			return r
		}, shortcutName)

		shortcutPath := filepath.Join(desktopDir, shortcutName)

		// Аргументы: /AdditionalTmpFolder="{brand}_{version}"
		// Например: /AdditionalTmpFolder="iiko_9.2.0"
		args := fmt.Sprintf(`/AdditionalTmpFolder="%s_%s"`, brandName, version)

		// 5. Создаем ярлык
		if err := dm.WU.CreateShortcut(targetExePath, shortcutPath, args); err != nil {
			slog.Error("Ошибка создания ярлыка", "error", err)
			tui.Warn(fmt.Sprintf("Не удалось создать ярлык: %v", err))
		} else {
			tui.SuccessF("Ярлык создан: %s", shortcutName)
		}
	}

	tui.Success("Портативная версия успешно установлена.")
	slog.Info("Портативная версия успешно установлена", "destDir", destDir)
	return nil
}
