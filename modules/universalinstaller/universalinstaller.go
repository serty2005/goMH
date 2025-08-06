package universalinstaller

import (
	"archive/zip"
	"bufio"
	"errors"
	"fmt"
	"goMH/config"
	"goMH/core"
	"goMH/tui"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

var errUserChoseExit = errors.New("пользователь выбрал выход в главное меню")

type Module struct{}

func (m *Module) ID() string {
	return "UniversalInstaller"
}

func (m *Module) MenuText() string {
	return "Универсальный установщик ПО (из config.json)"
}

func (m *Module) Run(am core.AssetManager, wu core.WinUtils) error {
	cfg := am.Cfg()
	if len(cfg.UniversalInstalls) == 0 {
		tui.Warn("В файле конфигурации не найдена секция 'UniversalInstalls' или она пуста.")
		tui.Info("Для использования этого модуля, добавьте описание программ в config.json.")
		return nil
	}

	for {
		item, err := m.showInstallMenu(cfg.UniversalInstalls)
		if err != nil {
			if errors.Is(err, errUserChoseExit) {
				tui.Info("Возврат в главное меню.")
				return nil
			}
			return err // Другая ошибка
		}

		tui.Title(fmt.Sprintf("\n--- Начало установки: %s ---", item.MenuText))

		// Скачивание ресурса в кэш
		localCachePath, err := m.downloadResource(am, item.URL)
		if err != nil {
			return fmt.Errorf("ошибка при скачивании ресурса: %w", err)
		}
		tui.Success("Ресурс успешно скачан в кэш.")

		// Обработка в зависимости от типа
		switch strings.ToLower(item.Type) {
		case "installer":
			err = m.handleInstaller(wu, localCachePath, item.InstallArgs)
		case "archive":
			err = m.handleArchive(wu, localCachePath, item.DestinationPath, item.RunAfterUnpack)
		default:
			err = fmt.Errorf("неизвестный тип установки '%s' в конфигурации", item.Type)
		}

		if err != nil {
			return err // Возвращаем ошибку наверх, чтобы main.go ее красиво отобразил
		}

		tui.Success(fmt.Sprintf("\n--- Установка '%s' завершена. ---", item.MenuText))
		fmt.Println("\nНажмите Enter, чтобы вернуться в меню универсального установщика...")
		fmt.Scanln()
	}
}

// showInstallMenu показывает внутреннее меню этого модуля
func (m *Module) showInstallMenu(items []config.UniversalInstallItem) (config.UniversalInstallItem, error) {
	reader := bufio.NewReader(os.Stdin)

	for {
		tui.Title("\n--- Меню универсального установщика ---")
		for i, item := range items {
			fmt.Printf(" %d. Установить %s\n", i+1, item.MenuText)
		}
		fmt.Println("\n 0. Назад в главное меню")
		fmt.Print("Выберите пункт для установки или возврата: ")

		choiceStr, _ := reader.ReadString('\n')
		choiceStr = strings.TrimSpace(choiceStr)

		if choiceStr == "0" {
			return config.UniversalInstallItem{}, errUserChoseExit
		}

		choice, err := strconv.Atoi(choiceStr)
		if err != nil || choice < 1 || choice > len(items) {
			tui.Error("Некорректный выбор. Попробуйте снова.")
			time.Sleep(2 * time.Second)
			continue
		}
		return items[choice-1], nil
	}
}

// downloadResource скачивает файл по URL в кэш и возвращает путь к нему.
func (m *Module) downloadResource(am core.AssetManager, url string) (string, error) {
	fileName := filepath.Base(url)
	localCachePath := filepath.Join(am.Cfg().AssetsCachePath, fileName)

	// Используем DownloadHTTPWithProgress, т.к. он универсален для HTTP/HTTPS
	_, err := am.DownloadHTTPWithProgress(url, localCachePath)
	if err != nil {
		return "", err
	}
	return localCachePath, nil
}

// handleInstaller запускает исполняемый файл с аргументами.
func (m *Module) handleInstaller(wu core.WinUtils, installerPath, installArgs string) error {
	tui.InfoF("Запуск установщика: %s", installerPath)
	tui.InfoF("Аргументы: %s", installArgs)
	tui.Info("... ИДЕТ УСТАНОВКА, ПОЖАЛУЙСТА, ОЖИДАЙТЕ ...")

	// strings.Fields правильно обрабатывает пробелы и кавычки
	args := strings.Fields(installArgs)
	_, err := wu.RunCommand(installerPath, args...)
	return err
}

// handleArchive распаковывает архив и опционально запускает файл.
func (m *Module) handleArchive(wu core.WinUtils, archivePath, destPath, runAfter string) error {
	tui.InfoF("Распаковка архива '%s' в '%s'...", filepath.Base(archivePath), destPath)
	if err := os.MkdirAll(destPath, 0755); err != nil {
		return fmt.Errorf("не удалось создать директорию назначения %s: %w", destPath, err)
	}

	if err := unzip(archivePath, destPath); err != nil {
		return fmt.Errorf("ошибка распаковки: %w", err)
	}
	tui.Success("Архив успешно распакован.")

	if runAfter != "" {
		tui.InfoF("Запуск команды после распаковки: %s", runAfter)
		parts := strings.Fields(runAfter)
		if len(parts) == 0 {
			return fmt.Errorf("некорректная команда в 'run_after_unpack'")
		}
		executable := parts[0]
		args := parts[1:]
		_, err := wu.RunCommand(executable, args...)
		return err
	}

	return nil
}

// unzip - это локальная копия функции из assetmgr для распаковки архивов,
// так как она не является частью публичного интерфейса core.AssetManager.
func unzip(src, dest string) error {
	r, err := zip.OpenReader(src)
	if err != nil {
		return err
	}
	defer r.Close()

	for _, f := range r.File {
		fpath := filepath.Join(dest, f.Name)
		if !strings.HasPrefix(fpath, filepath.Clean(dest)+string(os.PathSeparator)) {
			return fmt.Errorf("небезопасный путь в архиве: %s", fpath)
		}

		if f.FileInfo().IsDir() {
			os.MkdirAll(fpath, os.ModePerm)
			continue
		}

		if err := os.MkdirAll(filepath.Dir(fpath), os.ModePerm); err != nil {
			return err
		}

		outFile, err := os.OpenFile(fpath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, f.Mode())
		if err != nil {
			return err
		}

		rc, err := f.Open()
		if err != nil {
			outFile.Close()
			return err
		}

		_, err = io.Copy(outFile, rc)
		outFile.Close()
		rc.Close()

		if err != nil {
			return err
		}
	}
	return nil
}
