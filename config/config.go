package config

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
)

type IikoConfig struct {
	BaseFTPPath      string          `json:"base_ftp_path"`
	PatchRouteFile   string          `json:"patch_route_file"`
	ComponentsToFind []IikoComponent `json:"components_to_find"`
	CardPOS          IikoComponent   `json:"card_pos"`
}

type IikoComponent struct {
	ID          string `json:"id"`
	MenuText    string `json:"menu_text"`
	FileName    string `json:"file_name"`
	InstallArgs string `json:"install_args"`
	RunAfter    string `json:"run_after"`
	// Поля ниже не из JSON, а будут заполняться в рантайме
	Version string `json:"-"`
	FTPPath string `json:"-"`
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
}

type Config struct {
	RootPath          string               `json:"root_path"`
	AssetsCachePath   string               `json:"assets_cache_path"`
	FTP               FTPConfig            `json:"ftp_config"`
	Modules           []ModuleDef          `json:"modules"`
	FrpcConfig        FrpcConfig           `json:"frpc_config"`
	IikoConfig        IikoConfig           `json:"iiko_config"`
	AssetCatalog      map[string]AssetInfo `json:"asset_catalog"`
	TeamViewerConfig  TeamViewerConfig     `json:"TeamViewerConfig"`
	MaintenanceConfig MaintenanceConfig    `json:"MaintenanceConfig"`
}

type FTPConfig struct {
	Host string `json:"host"`
	User string `json:"user"`
	Pass string `json:"pass"`
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

// LoadConfig загружает конфигурацию из файла или по URL
func LoadConfig(pathOrURL string) (*Config, error) {
	var data []byte
	var err error

	// Проверяем, является ли строка URL-адресом
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

	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("ошибка парсинга JSON конфигурации: %w", err)
	}

	return &cfg, nil
}
