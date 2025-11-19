package selfupdate

import (
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"goMH/config"
	"goMH/core"
	"goMH/tui"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type Updater struct {
	Cfg config.SelfUpdateConfig
	WU  core.WinUtils
}

func New(cfg config.SelfUpdateConfig, wu core.WinUtils) *Updater {
	return &Updater{
		Cfg: cfg,
		WU:  wu,
	}
}

// CheckAndPerformUpdate проверяет наличие обновлений и применяет их.
// Возвращает true, если обновление было успешно применено и запущен новый процесс.
func (u *Updater) CheckAndPerformUpdate() (bool, error) {
	tui.Info("Проверка наличия новой версии...")

	// 1. Получаем MD5 хэш удаленного файла
	remoteHash, err := u.fetchRemoteHash()
	if err != nil {
		return false, fmt.Errorf("не удалось получить удаленный хэш: %w", err)
	}

	// 2. Получаем путь к текущему исполняемому файлу
	exePath, err := os.Executable()
	if err != nil {
		return false, fmt.Errorf("не удалось определить путь к exe: %w", err)
	}

	// 3. Считаем хэш текущего файла
	localHash, err := u.calculateFileHash(exePath)
	if err != nil {
		return false, fmt.Errorf("не удалось посчитать хэш текущего файла: %w", err)
	}

	slog.Debug("Сравнение хэшей", "local", localHash, "remote", remoteHash)
	if strings.EqualFold(localHash, remoteHash) {
		tui.Success("У вас установлена актуальная версия.")
		return false, nil
	}

	tui.Warn("Обнаружена новая версия! Начинаем обновление...")

	// 4. Скачиваем новый файл во временную директорию
	tempDir := u.Cfg.TempDir
	if tempDir == "" {
		tempDir = filepath.Join(filepath.Dir(exePath), "temp")
	}
	_ = os.MkdirAll(tempDir, 0755)

	newExePath := filepath.Join(tempDir, "goMH_new.exe")
	if err := u.downloadFile(u.Cfg.ReferenceURL, newExePath); err != nil {
		return false, fmt.Errorf("ошибка скачивания обновления: %w", err)
	}

	// 5. Проверяем хэш скачанного файла
	downloadedHash, err := u.calculateFileHash(newExePath)
	if err != nil || !strings.EqualFold(downloadedHash, remoteHash) {
		return false, fmt.Errorf("хэш скачанного файла не совпадает (скачан: %s, ожидался: %s)", downloadedHash, remoteHash)
	}

	// 6. Процесс подмены (Rename trick)
	// Windows не дает перезаписать запущенный exe, но дает его переименовать.
	oldExePath := exePath + ".old"
	// Если остался старый файл с прошлого раза (и по какой-то причине не удалился при старте), удаляем его сейчас
	if _, err := os.Stat(oldExePath); err == nil {
		if err := os.Remove(oldExePath); err != nil {
			// Если не смогли удалить старый .old, обновление не получится (rename упадет)
			return false, fmt.Errorf("не удалось удалить предыдущий .old файл: %w", err)
		}
	}

	if err := os.Rename(exePath, oldExePath); err != nil {
		return false, fmt.Errorf("не удалось переименовать текущий exe: %w", err)
	}

	if err := u.WU.MoveFile(newExePath, exePath); err != nil {
		// Попытка отката
		os.Rename(oldExePath, exePath)
		return false, fmt.Errorf("не удалось переместить новый exe на место старого: %w", err)
	}

	tui.Success("Файлы обновлены. Перезапуск приложения...")

	// 7. Перезапуск процесса
	// Передаем те же аргументы, с которыми был запущен текущий процесс (кроме имени программы в os.Args[0])
	cmd := exec.Command(exePath, os.Args[1:]...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		// Если не удалось запустить новый процесс, возвращаем ошибку,
		// но файл уже обновлен, так что пользователь может запустить его вручную.
		return true, fmt.Errorf("не удалось перезапустить приложение автоматически: %w. Пожалуйста, запустите его вручную", err)
	}

	return true, nil
}

func (u *Updater) fetchRemoteHash() (string, error) {
	resp, err := http.Get(u.Cfg.HashURL)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("bad status: %s", resp.Status)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	// Предполагаем, что файл содержит только хэш
	return strings.TrimSpace(string(body)), nil
}

func (u *Updater) downloadFile(url, dest string) error {
	out, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer out.Close()

	resp, err := http.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("bad status: %s", resp.Status)
	}

	_, err = io.Copy(out, resp.Body)
	return err
}

func (u *Updater) calculateFileHash(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := md5.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
