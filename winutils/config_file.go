package winutils

import (
	"bytes"
	"errors"
	"fmt"
	"goMH/core"
	"os"
	"path/filepath"
	"strings"
)

func FindFrontConfig() (core.ConfigFileSnapshot, error) {
	appData := os.Getenv("APPDATA")
	if strings.TrimSpace(appData) == "" {
		return core.ConfigFileSnapshot{}, errors.New("переменная APPDATA не задана")
	}
	return findFrontConfig(appData)
}

func findFrontConfig(appData string) (core.ConfigFileSnapshot, error) {
	var latest core.ConfigFileSnapshot
	for _, brand := range []string{"iiko", "Syrve"} {
		path := filepath.Join(appData, brand, "CashServer", "config.xml")
		info, err := os.Stat(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return latest, fmt.Errorf("проверка %s: %w", path, err)
		}
		if !info.Mode().IsRegular() {
			return latest, fmt.Errorf("%s не является обычным файлом", path)
		}
		if latest.Path == "" || info.ModTime().After(latest.ModTime) {
			latest = core.ConfigFileSnapshot{Path: path, Brand: brand, ModTime: info.ModTime()}
		}
	}
	if latest.Path == "" {
		return latest, fmt.Errorf("config.xml не найден в %s; проверены iiko\\CashServer и Syrve\\CashServer. Запустите goMH под пользователем фронта", appData)
	}
	data, err := os.ReadFile(latest.Path)
	if err != nil {
		return latest, fmt.Errorf("чтение %s: %w", latest.Path, err)
	}
	latest.Data = data
	return latest, nil
}

// SaveFileWithBackup не обрезает исходный файл: новая версия сначала полностью
// записывается рядом, затем заменяет исходную. Копия сохраняет исходные байты.
func SaveFileWithBackup(path string, original, updated []byte) (string, error) {
	checkOriginal := func() error {
		current, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("проверка исходного файла: %w", err)
		}
		if !bytes.Equal(current, original) {
			return errors.New("config.xml изменён другой программой; откройте редактор заново")
		}
		return nil
	}
	if err := checkOriginal(); err != nil {
		return "", err
	}
	if bytes.Equal(original, updated) {
		return "", nil
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	writeTemp := func(pattern string, data []byte) (string, error) {
		f, err := os.CreateTemp(filepath.Dir(path), pattern)
		if err != nil {
			return "", err
		}
		name := f.Name()
		ok := false
		defer func() {
			f.Close()
			if !ok {
				os.Remove(name)
			}
		}()
		if err := f.Chmod(info.Mode().Perm()); err != nil {
			return "", err
		}
		if _, err := f.Write(data); err != nil {
			return "", err
		}
		if err := f.Sync(); err != nil {
			return "", err
		}
		if err := f.Close(); err != nil {
			return "", err
		}
		ok = true
		return name, nil
	}
	backup, err := writeTemp(filepath.Base(path)+".gomh-*.bak", original)
	if err != nil {
		return "", fmt.Errorf("создание резервной копии: %w", err)
	}
	temp, err := writeTemp(".gomh-config-*.tmp", updated)
	if err != nil {
		return backup, fmt.Errorf("подготовка нового файла (копия: %s): %w", backup, err)
	}
	defer os.Remove(temp)
	if err := checkOriginal(); err != nil {
		return backup, err
	}
	if err := os.Rename(temp, path); err != nil {
		return backup, fmt.Errorf("сохранение config.xml (копия: %s): %w", backup, err)
	}
	return backup, nil
}
