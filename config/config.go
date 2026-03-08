// config/config.go

package config

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
)

type SelfUpdateConfig struct {
	Enabled         bool   `json:"enabled"`
	ReferenceURL    string `json:"reference_url"`     // Default (x64)
	HashURL         string `json:"hash_url"`          // Default (x64)
	ReferenceURLX86 string `json:"reference_url_x86"` // Specific for 32-bit
	HashURLX86      string `json:"hash_url_x86"`      // Specific for 32-bit
	TempDir         string `json:"temp_dir"`
}

// DistroComponent описывает компонент для установки (Front, BackOffice и т.д.).
type DistroComponent struct {
	ID                 string `json:"id"`
	MenuText           string `json:"menu_text"`
	InstallArgs        string `json:"install_args"`
	RunAfter           string `json:"run_after"`
	URLTemplate        string `json:"url_template,omitempty"` // Для полных дистрибутивов
	PortableArchiveKey string `json:"portable_archive_key"`   // Ключ для поиска имени архива в источниках
}

// BrandConfig содержит специфичные настройки для бренда (iiko или Syrve).
type BrandConfig struct {
	Components     []DistroComponent `json:"components"`
	PatchesBaseURL string            `json:"patches_base_url,omitempty"` // Для iiko
}

// BrandPortableSource описывает источники портативных версий для одного бренда.
type BrandPortableSource struct {
	HttpSource struct {
		Enabled      bool              `json:"enabled"`
		URL          string            `json:"url"`
		ArchiveNames map[string]string `json:"archive_names"` // Ключ -> Шаблон имени архива
	} `json:"http_source"`
	FtpSource struct {
		Enabled      bool              `json:"enabled"`
		Directory    string            `json:"directory"`
		ArchiveNames map[string]string `json:"archive_names"` // Ключ -> Шаблон имени архива
	} `json:"ftp_source"`
}

// DistroConfig содержит все настройки, связанные с установкой дистрибутивов iiko и Syrve.
type DistroConfig struct {
	Iiko              BrandConfig         `json:"iiko"`
	Syrve             BrandConfig         `json:"syrve"`
	IikoPortable      BrandPortableSource `json:"iiko_portable_sources"`
	SyrvePortable     BrandPortableSource `json:"syrve_portable_sources"`
	ExcludedPlugins   []string            `json:"excluded_plugins"`
	AutoUpdatePlugins []string            `json:"auto_update_plugins"`
}

type FrpcConfig struct {
	InstallPath     string           `json:"install_path"`
	ServiceName     string           `json:"service_name"`
	PortRange       string           `json:"port_range"`
	FrpcDownloadURL string           `json:"frpc_download_url"`
	NssmDownloadURL string           `json:"nssm_download_url"`
	ServerConfig    FrpcServerConfig `json:"server_config"`
}

type FrpcServerConfig struct {
	Host       string `json:"host"`
	APIPort    int    `json:"api_port"`
	TunnelPort int    `json:"tunnel_port"`
	User       string `json:"user"`
	Pass       string `json:"pass"`
}

type TeamViewerConfig struct {
	ShortURL string `json:"ShortURL"`
	ApiURL   string `json:"ApiURL"`
}

type MaintenanceConfig struct {
	TempPaths         []string `json:"TempPaths"`
	LogCollectorPaths []string `json:"LogCollectorPaths"`
	SevenZipAssetID   string   `json:"7zipAssetID"`
}

type FiscalDriver struct {
	ID          string `json:"id"`
	MenuText    string `json:"menu_text"`
	AssetID     string `json:"asset_id"`
	InstallArgs string `json:"install_args"`
}

type UTMConfig struct {
	MenuText    string `json:"menu_text"`
	AssetID     string `json:"asset_id"`
	InstallArgs string `json:"install_args"`
}

type LoggingConfig struct {
	Level       string `json:"level"`
	FileEnabled bool   `json:"file_enabled"`
}

type Config struct {
	RootPath            string               `json:"root_path"`
	SelfUpdateConfig    SelfUpdateConfig     `json:"self_update_config"`
	AssetsCachePath     string               `json:"assets_cache_path"`
	LogLevel            string               `json:"log_level"`
	Logging             LoggingConfig        `json:"logging"`
	FTP                 []FTPConfig          `json:"ftp_config"`
	Modules             []ModuleDef          `json:"modules"`
	FrpcConfig          FrpcConfig           `json:"frpc_config"`
	DistroConfig        DistroConfig         `json:"distro_config"`
	AssetCatalog        map[string]AssetInfo `json:"asset_catalog"`
	TeamViewerConfig    TeamViewerConfig     `json:"TeamViewerConfig"`
	MaintenanceConfig   MaintenanceConfig    `json:"MaintenanceConfig"`
	FiscalDriversConfig []FiscalDriver       `json:"fiscal_drivers_config"`
	UTMConfig           UTMConfig            `json:"utm_config"`
}

type FTPConfig struct {
	Host string `json:"host"`
	User string `json:"user"`
	Pass string `json:"pass"`
	Port int    `json:"port,omitempty"`
}

type ModuleDef struct {
	ID string `json:"id"`
}

type AssetInfo struct {
	URL            string `json:"url"`
	Type           string `json:"type"`
	Destination    string `json:"destination"`
	DownloadMethod string `json:"download_method"`
}

type rawLoggingConfig struct {
	Level       string `json:"level"`
	FileEnabled *bool  `json:"file_enabled"`
}

type configAlias Config

type rawConfig struct {
	configAlias
	Logging *rawLoggingConfig `json:"logging"`
}

func LoadConfig(pathOrURL string) (*Config, error) {
	var data []byte
	var err error

	if strings.HasPrefix(pathOrURL, "http://") || strings.HasPrefix(pathOrURL, "https://") {
		fmt.Printf("Загрузка конфигурации с URL: %s\n", pathOrURL)
		resp, errHttp := http.Get(pathOrURL)
		if errHttp != nil {
			return nil, fmt.Errorf("ошибка при загрузке конфигурации по HTTP: %w", errHttp)
		}
		defer resp.Body.Close()
		data, err = io.ReadAll(resp.Body)
	} else {
		fmt.Printf("Чтение локального файла конфигурации: %s\n", pathOrURL)
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

func (cfg *Config) applyLoggingDefaults(raw *rawLoggingConfig) {
	level := strings.TrimSpace(cfg.LogLevel)
	if raw != nil && strings.TrimSpace(raw.Level) != "" {
		level = strings.TrimSpace(raw.Level)
	}
	if level == "" {
		level = "INFO"
	}

	fileEnabled := true
	if raw != nil && raw.FileEnabled != nil {
		fileEnabled = *raw.FileEnabled
	}

	cfg.Logging = LoggingConfig{
		Level:       level,
		FileEnabled: fileEnabled,
	}
	cfg.LogLevel = cfg.Logging.Level
}
