package dependencies

import (
	"errors"
	"fmt"
	"goMH/core"
	"os"
	"os/exec"
	"strings"
	"time"
)

// SevenZipClient предоставляет удобный интерфейс для работы с архиватором 7-Zip.
type SevenZipClient struct {
	exePath string
	wu      core.WinUtils
}

// NewClient создает и инициализирует новый клиент для работы с 7-Zip.
// Он автоматически находит или устанавливает 7z.exe при первом вызове.
func NewClient(am core.AssetManager, wu core.WinUtils) (*SevenZipClient, error) {
	exePath, err := findAndInstall(am, wu)
	if err != nil {
		return nil, err
	}
	return &SevenZipClient{
		exePath: exePath,
		wu:      wu,
	}, nil
}

// Extract распаковывает указанный архив в целевую директорию.
// fullPaths: true использует флаг 'x' (сохранение структуры папок), false - флаг 'e' (плоская распаковка).
func (c *SevenZipClient) Extract(sourceArchive, destinationDir string, fullPaths bool) error {
	command := "e" // Плоская распаковка по умолчанию
	if fullPaths {
		command = "x" // Распаковка с сохранением путей
	}

	// -y отвечает "Да" на все запросы (например, перезапись файлов)
	_, err := c.wu.RunCommand(c.exePath, command, sourceArchive, fmt.Sprintf("-o%s", destinationDir), "-y")
	if err != nil {
		return fmt.Errorf("7-Zip завершился с ошибкой при распаковке '%s': %w", sourceArchive, err)
	}
	return nil
}

// List возвращает список путей файлов внутри архива.
func (c *SevenZipClient) List(sourceArchive string) ([]string, error) {
	// l - команда листинга
	// -slt - технический вывод для машинного парсинга
	output, err := c.wu.RunCommand(c.exePath, "l", "-slt", sourceArchive)
	if err != nil {
		return nil, fmt.Errorf("не удалось получить список файлов из архива '%s': %w", sourceArchive, err)
	}

	files := parseFileList(output)
	if len(files) == 0 {
		return nil, errors.New("не удалось найти файлы для установки внутри архива")
	}
	return files, nil
}

// findAndInstall ищет исполняемый файл 7z.exe в стандартных местах и устанавливает его при необходимости.
// Эта функция теперь является неэкспортируемой и используется только внутри этого пакета.
func findAndInstall(am core.AssetManager, wu core.WinUtils) (string, error) {
	// 1. Проверяем стандартные пути установки
	potentialPaths := []string{
		`C:\Program Files\7-Zip\7z.exe`,
		`C:\Program Files (x86)\7-Zip\7z.exe`,
	}
	for _, path := range potentialPaths {
		if _, err := os.Stat(path); err == nil {
			return path, nil
		}
	}

	// 2. Ищем в системной переменной PATH
	path, err := exec.LookPath("7z.exe")
	if err == nil {
		return path, nil
	}

	// 3. Если 7z не найден, пытаемся скачать и установить
	fmt.Println("7z.exe не найден в системе. Попытка автоматической установки...")

	cfg := am.Cfg()
	if cfg == nil || cfg.MaintenanceConfig.SevenZipAssetID == "" {
		return "", errors.New("в конфигурации не указан '7zipAssetID' для автоматической установки 7-Zip")
	}

	fmt.Printf("Попытка скачивания 7-Zip через AssetManager с ID: %s...\n", cfg.MaintenanceConfig.SevenZipAssetID)

	cachePath, err := am.DownloadToCache(cfg.MaintenanceConfig.SevenZipAssetID)
	if err != nil {
		return "", fmt.Errorf("не удалось скачать ассет 7-Zip: %w", err)
	}
	defer os.Remove(cachePath)

	fmt.Println("Установка 7-Zip...")
	_, err = wu.RunCommand(cachePath, "/S")
	if err != nil {
		return "", fmt.Errorf("не удалось установить 7-Zip: %w", err)
	}

	time.Sleep(3 * time.Second)

	installPath := `C:\Program Files\7-Zip\7z.exe`
	if _, err := os.Stat(installPath); err == nil {
		fmt.Println("7-Zip успешно установлен.")
		return installPath, nil
	}

	return "", errors.New("не удалось найти 7z.exe после установки. Проверьте права администратора")
}

// parseFileList парсит вывод команды `7z l -slt` и возвращает список путей файлов.
// Эта функция перенесена из iiko/patcher.go.
func parseFileList(output string) []string {
	var files []string
	var currentPath string
	lines := strings.Split(output, "\n")

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "Path = ") {
			currentPath = strings.TrimPrefix(line, "Path = ")
		} else if strings.HasPrefix(line, "Size = ") && currentPath != "" {
			// Это запись о файле, а не о папке
			files = append(files, currentPath)
			currentPath = "" // Сбрасываем, чтобы не добавить папку
		} else if line == "" {
			currentPath = "" // Сбрасываем на пустой строке
		}
	}
	return files
}
