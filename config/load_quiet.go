package config

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
)

func LoadConfigQuiet(pathOrURL string) (*Config, error) {
	return loadConfigQuiet(pathOrURL, nil)
}

func loadConfigQuiet(pathOrURL string, progress io.Writer) (*Config, error) {
	var data []byte
	var err error

	if strings.HasPrefix(pathOrURL, "http://") || strings.HasPrefix(pathOrURL, "https://") {
		writeLoadConfigProgress(progress, "Загрузка конфигурации с URL: %s\n", pathOrURL)
		resp, errHTTP := http.Get(pathOrURL)
		if errHTTP != nil {
			return nil, fmt.Errorf("ошибка при загрузке конфигурации по HTTP: %w", errHTTP)
		}
		defer resp.Body.Close()
		data, err = io.ReadAll(resp.Body)
	} else {
		writeLoadConfigProgress(progress, "Чтение локального файла конфигурации: %s\n", pathOrURL)
		data, err = os.ReadFile(pathOrURL)
	}

	if err != nil {
		return nil, fmt.Errorf("не удалось получить данные конфигурации: %w", err)
	}

	var raw rawConfig
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("ошибка парсинга JSON конфигурации: %w", err)
	}

	cfg := Config(raw.configAlias)
	cfg.applyLoggingDefaults(raw.Logging)

	for i := range cfg.FTP {
		if cfg.FTP[i].Port == 0 {
			cfg.FTP[i].Port = 21
		}
	}

	return &cfg, nil
}

func writeLoadConfigProgress(w io.Writer, format string, args ...any) {
	if w == nil {
		return
	}
	fmt.Fprintf(w, format, args...)
}
