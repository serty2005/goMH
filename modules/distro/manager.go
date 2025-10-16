// modules/distro/manager.go
package distro

import (
	"errors"
	"fmt"
	"goMH/config"
	"goMH/core"
	"goMH/dependencies"
	iikoplugins "goMH/modules/iiko-plugins"
	"goMH/tui"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/manifoldco/promptui"
)

// Module реализует интерфейс core.Installer.
type Module struct{}

func (m *Module) ID() string       { return "iiko" } // Оставляем старый ID для совместимости с config.json
func (m *Module) MenuText() string { return "iiko / Syrve (Front, Back, Card)" }

// DistroManager управляет всем процессом установки.
type DistroManager struct {
	AM core.AssetManager
	WU core.WinUtils
}

// Run - основная точка входа в модуль.
func (m *Module) Run(am core.AssetManager, wu core.WinUtils) error {
	dm := &DistroManager{AM: am, WU: wu}
	return dm.selectBrand()
}

// selectBrand - первый шаг, выбор между iiko и Syrve.
func (dm *DistroManager) selectBrand() error {
	prompt := promptui.Select{
		Label: "Выберите продукт для установки",
		Items: []string{"iiko", "Syrve", "Назад"},
	}
	_, result, err := prompt.Run()
	if err != nil {
		return nil // Пользователь отменил выбор (Ctrl+C)
	}

	switch result {
	case "iiko":
		handler := &iikoHandler{dm: dm}
		return handler.Run()
	case "Syrve":
		handler := &syrveHandler{dm: dm}
		return handler.Run()
	default:
		return nil // Выбрали "Назад"
	}
}

// brandHandler определяет интерфейс для специфичной логики бренда.
type brandHandler interface {
	Run() error
	selectComponentMenu() (config.DistroComponent, error)
	getAvailableVersions() ([]string, error)
	getAvailablePortableVersions(component config.DistroComponent) ([]string, error)
	installComponent(component config.DistroComponent, version string) error
	installPortable(component config.DistroComponent, version string) error
}

// runWorkflow - общий сценарий установки.
func (dm *DistroManager) runWorkflow(h brandHandler) error {
	component, err := h.selectComponentMenu()
	if err != nil {
		// Проверяем, является ли ошибка выходом в главное меню
		if err == tui.ErrExitToMainMenu {
			return err
		}
		return nil // Отмена
	}

	// Для компонентов без версий (iikoCard, Syrve Loyalty) сразу запускаем установку.
	isVersioned := component.URLTemplate == "" || strings.Contains(component.URLTemplate, "{{VERSION}}")
	if !isVersioned {
		return h.installComponent(component, "")
	}

	// Проверка существующей установки для версионных компонентов
	installedVer, err := dm.WU.GetFileVersion(component.RunAfter)
	if err == nil && component.PortableArchiveKey != "" {
		tui.Warn(fmt.Sprintf("Обнаружена установленная версия: %s", installedVer))
		choice, err := dm.promptUpdateOrPortable(installedVer)
		if err != nil || choice == "cancel" {
			return nil
		}

		if choice == "portable" {
			versions, err := h.getAvailablePortableVersions(component)
			if err != nil {
				return err
			}
			if len(versions) == 0 {
				return fmt.Errorf("не найдено доступных портативных версий для %s", component.MenuText)
			}
			version, err := dm.selectVersionMenu(versions, "Выберите версию для портативной установки")
			if err != nil {
				// Проверяем, является ли ошибка выходом в главное меню
				if err.Error() == "exit_to_main_menu" {
					return tui.ErrExitToMainMenu
				}
				return err
			}
			return h.installPortable(component, version)
		}
	}

	// Специальная логика ТОЛЬКО для iikoFront
	if component.ID == "iiko_front" {
		return dm.runIikoFrontInstall(h, component)
	}

	// Стандартная установка для всех остальных версионных компонентов
	versions, err := h.getAvailableVersions()
	if err != nil {
		return err
	}
	if len(versions) == 0 {
		return fmt.Errorf("не найдено доступных версий для %s", component.MenuText)
	}
	version, err := dm.selectVersionMenu(versions, "Выберите версию для установки")
	if err != nil {
		// Проверяем, является ли ошибка выходом в главное меню
		if err.Error() == "exit_to_main_menu" {
			return tui.ErrExitToMainMenu
		}
		return err
	}
	return h.installComponent(component, version)
}

// runIikoFrontInstall - специальная логика ТОЛЬКО для iikoFront.
func (dm *DistroManager) runIikoFrontInstall(h brandHandler, component config.DistroComponent) error {
	versions, err := h.getAvailableVersions()
	if err != nil {
		return err
	}
	version, err := dm.selectVersionMenu(versions, "Выберите версию iikoFront для установки")
	if err != nil {
		// Проверяем, является ли ошибка выходом в главное меню
		if err.Error() == "exit_to_main_menu" {
			return tui.ErrExitToMainMenu
		}
		return err
	}

	selectedPatch, patchSelected, err := FindAndSelectPatch(dm.AM, version)
	if err != nil {
		tui.Warn(fmt.Sprintf("Ошибка при выборе патча: %v. Установка продолжится без него.", err))
	}

	if err := h.installComponent(component, version); err != nil {
		return err
	}

	if patchSelected {
		installDir := filepath.Dir(component.RunAfter)
		backupDir := filepath.Join(dm.AM.Cfg().RootPath, "patches_backup", version)
		if err := ApplyPatch(dm.AM, dm.WU, selectedPatch, installDir, backupDir); err != nil {
			tui.Error(fmt.Sprintf("Критическая ошибка при применении патча: %v", err))
		}
	}

	tui.Info("Запуск автоматического обновления плагинов iiko...")
	pluginsModule := &iikoplugins.Module{}
	if err := pluginsModule.AutoUpdatePlugins(dm.AM, dm.WU); err != nil {
		tui.Warn(fmt.Sprintf("Ошибка при автообновлении плагинов: %v", err))
	} else {
		tui.Success("Автообновление плагинов завершено.")
	}

	return nil
}

// promptUpdateOrPortable - диалог выбора действия.
func (dm *DistroManager) promptUpdateOrPortable(installedVer string) (string, error) {
	prompt := promptui.Select{
		Label: fmt.Sprintf("Уже установлена версия %s. Что вы хотите сделать?", installedVer),
		Items: []string{"Обновить (полная установка)", "Установить портативную версию 'рядом'", "Отмена"},
	}
	_, result, err := prompt.Run()
	if err != nil {
		return "cancel", err
	}
	switch result {
	case "Обновить (полная установка)":
		return "update", nil
	case "Установить портативную версию 'рядом'":
		return "portable", nil
	default:
		return "cancel", nil
	}
}

// selectVersionMenu - общий UI для выбора версии.
func (dm *DistroManager) selectVersionMenu(versions []string, label string) (string, error) {
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
		// Проверяем, была ли нажата комбинация для выхода в главное меню
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
	tui.InfoF("Запуск установщика: %s с аргументами %v", installerPath, finalArgs)
	tui.Info("... ИДЕТ УСТАНОВКА, ПОЖАЛУЙСТА, ОЖИДАЙТЕ ...")

	cmd := exec.Command(installerPath, finalArgs...)
	output, err := cmd.CombinedOutput()

	var exitCode int
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			return fmt.Errorf("не удалось запустить установщик: %w, вывод: %s", err, string(output))
		}
	}

	if exitCode != 0 {
		finalLogPath := filepath.Join(dm.AM.Cfg().RootPath, logFileName)
		_ = os.Rename(tempLogPath, finalLogPath)
		return fmt.Errorf("установщик завершился с кодом %d. Лог сохранен в: %s", exitCode, finalLogPath)
	}

	_ = os.Remove(tempLogPath)
	return nil
}

func (dm *DistroManager) installPortable(h brandHandler, brandName string, component config.DistroComponent, version string) error {
	tui.Title(fmt.Sprintf("\n--- Начало установки портативной версии: %s %s ---", component.MenuText, version))

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

	// Создаем папку для версии портативного дистрибутива
	versionDir := filepath.Join(dm.AM.Cfg().RootPath, "portable", brandName, version)
	if err := os.MkdirAll(versionDir, 0755); err != nil {
		return fmt.Errorf("не удалось создать папку для портативной версии %s: %w", version, err)
	}

	cachePath := filepath.Join(versionDir, filepath.Base(archiveName))

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

		// Попытка скачать по FTP
		_, err := dm.AM.DownloadFTPWithProgress(ftpCfg, ftpPath, cachePath)
		if err != nil {
			// Если FTP не сработал и HTTP источник включен, используем его как fallback
			if portableSrc.HttpSource.Enabled {
				tui.Warn(fmt.Sprintf("FTP-сервер недоступен, используем HTTP как резервный источник: %v", err))
				_, err := dm.AM.DownloadHTTPWithProgress(downloadURL, cachePath)
				if err != nil {
					return fmt.Errorf("ошибка загрузки по FTP: %w, ошибка загрузки по HTTP: %v", err, err)
				}
				tui.Success("Файл успешно загружен по HTTP как резервный источник")
			} else {
				return fmt.Errorf("ошибка загрузки по FTP: %w", err)
			}
		}
	}

	redaction := strings.ToUpper(strings.Split(strings.TrimPrefix(component.ID, brandName+"_"), "_")[0])
	destDir := filepath.Join(dm.AM.Cfg().RootPath, fmt.Sprintf("p_%s%s_%s", brandName, redaction, version))

	tui.InfoF("Распаковка в: %s", destDir)

	// Используем 7-Zip напрямую вместо встроенной распаковки
	sevenZipClient, err := dependencies.NewClient(dm.AM, dm.WU)
	if err != nil {
		return fmt.Errorf("не удалось создать клиент 7-Zip: %w", err)
	}

	// Распаковываем архив с сохранением структуры папок
	if err := sevenZipClient.Extract(cachePath, destDir, true); err != nil {
		return fmt.Errorf("ошибка распаковки архива: %w", err)
	}

	tui.Success("Портативная версия успешно установлена.")
	return nil
}
