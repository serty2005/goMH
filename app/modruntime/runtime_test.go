package modruntime

import (
	"context"
	"goMH/assetmgr"
	"goMH/config"
	"goMH/core"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExecuteWithTaskRuntimeRoutesAssetManagerOutputAndProgress(t *testing.T) {
	rootDir := t.TempDir()
	cfg := &config.Config{
		RootPath:        rootDir,
		AssetsCachePath: filepath.Join(rootDir, "cache"),
	}

	manager, err := assetmgr.New(cfg)
	if err != nil {
		t.Fatalf("не удалось создать asset manager: %v", err)
	}

	payload := []byte("cached-asset")
	localPath := filepath.Join(rootDir, "asset.exe")
	if err := os.WriteFile(localPath, payload, 0666); err != nil {
		t.Fatalf("не удалось подготовить локальный файл: %v", err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "12")
		_, _ = w.Write(payload)
	}))
	defer server.Close()

	taskCtx := &captureTaskContext{}
	err = ExecuteWithTaskRuntime(taskCtx, core.ModuleServices{AssetManager: manager}, func(taskServices core.ModuleServices) error {
		_, err := taskServices.AssetManager.DownloadHTTPWithProgress(server.URL+"/asset.exe", localPath)
		return err
	})
	if err != nil {
		t.Fatalf("ExecuteWithTaskRuntime вернул ошибку: %v", err)
	}

	if !containsLine(taskCtx.logs, "Файл 'asset.exe' уже существует и размер совпадает. Пропускаем.") {
		t.Fatalf("ожидалась запись о переиспользовании локального файла, логи: %#v", taskCtx.logs)
	}
	if !containsLine(taskCtx.statuses, "Скачивание asset.exe") {
		t.Fatalf("ожидался статус скачивания, статусы: %#v", taskCtx.statuses)
	}
	if len(taskCtx.progress) == 0 || taskCtx.progress[len(taskCtx.progress)-1] != 100 {
		t.Fatalf("ожидался прогресс 100, получено: %#v", taskCtx.progress)
	}
}

type captureTaskContext struct {
	logs     []string
	statuses []string
	progress []int
}

func (c *captureTaskContext) Context() context.Context { return context.Background() }
func (c *captureTaskContext) Info(msg string)          { c.logs = append(c.logs, msg) }
func (c *captureTaskContext) Warn(msg string)          { c.logs = append(c.logs, msg) }
func (c *captureTaskContext) Error(msg string)         { c.logs = append(c.logs, msg) }
func (c *captureTaskContext) Success(msg string)       { c.logs = append(c.logs, msg) }

func (c *captureTaskContext) SetStatus(text string) {
	c.statuses = append(c.statuses, text)
}

func (c *captureTaskContext) SetProgress(percent int) {
	c.progress = append(c.progress, percent)
}

func containsLine(lines []string, needle string) bool {
	for _, line := range lines {
		if strings.Contains(line, needle) {
			return true
		}
	}
	return false
}
