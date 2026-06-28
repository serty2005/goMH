package iikoplugins

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestRapidScanPercentClampsRange(t *testing.T) {
	tests := []struct {
		name      string
		processed int
		total     int
		want      int
	}{
		{name: "zero total", processed: 1, total: 0, want: 0},
		{name: "mid range", processed: 5, total: 20, want: 25},
		{name: "complete", processed: 20, total: 20, want: 100},
		{name: "overflow", processed: 25, total: 20, want: 100},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := rapidScanPercent(tt.processed, tt.total); got != tt.want {
				t.Fatalf("rapidScanPercent(%d, %d) = %d, want %d", tt.processed, tt.total, got, tt.want)
			}
		})
	}
}

func TestScanPluginZipFilesWithProgressHonorsCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := scanPluginZipFilesWithProgress(ctx, RapidPluginsBaseURL, maxScanDepth, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}

func TestParsePluginFromFTPArchive(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		remotePath   string
		wantName     string
		wantAPI      string
		wantVersion  string
		wantFrontVer string
	}{
		{
			name:         "rapid style archive on ftp",
			remotePath:   "/release_iiko/9.4.8049.0/Plugins/Front/Resto.Front.Api.Transport.V9Preview7-9.7.20-2026.02.26.zip",
			wantName:     "Transport",
			wantAPI:      "V9Preview7",
			wantVersion:  "9.7.20",
			wantFrontVer: "9.4.8049.0",
		},
		{
			name:         "vendor archive with api in name",
			remotePath:   "/release_iiko/9.4.8049.0/Plugins/Front/Arbus.AutoPackages.V8-1.5.1-2025.10.13.zip",
			wantName:     "Arbus.AutoPackages",
			wantAPI:      "V8",
			wantVersion:  "1.5.1",
			wantFrontVer: "9.4.8049.0",
		},
		{
			name:         "archive without api in name",
			remotePath:   "/release_iiko/9.4.8049.0/Plugins/Front/CustomerDisplay-1.0.1127.0.zip",
			wantName:     "CustomerDisplay",
			wantAPI:      "",
			wantVersion:  "1.0.1127.0",
			wantFrontVer: "9.4.8049.0",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parsePluginFromFTPArchive("9.4.8049.0", tt.remotePath)
			if got.Name != tt.wantName {
				t.Fatalf("Name = %q, want %q", got.Name, tt.wantName)
			}
			if got.ApiVersion != tt.wantAPI {
				t.Fatalf("ApiVersion = %q, want %q", got.ApiVersion, tt.wantAPI)
			}
			if got.PluginVersion != tt.wantVersion {
				t.Fatalf("PluginVersion = %q, want %q", got.PluginVersion, tt.wantVersion)
			}
			if got.Source != PluginSourceFTP {
				t.Fatalf("Source = %q, want %q", got.Source, PluginSourceFTP)
			}
			if got.FrontVersion != tt.wantFrontVer {
				t.Fatalf("FrontVersion = %q, want %q", got.FrontVersion, tt.wantFrontVer)
			}
		})
	}
}

func TestMergePluginsPreferPrimary(t *testing.T) {
	t.Parallel()

	rapid := []Plugin{
		{
			Name:          "Transport",
			ApiVersion:    "V9Preview7",
			PluginVersion: "9.7.20",
			DownloadUrl:   "https://rapid.iiko.ru/plugins/Resto.Front.Api.Transport.V9Preview7-9.7.20.zip",
			Source:        PluginSourceRapid,
		},
	}
	ftp := []Plugin{
		{
			Name:          "Transport",
			ApiVersion:    "V9Preview7",
			PluginVersion: "9.7.20",
			DownloadUrl:   "ftp://ftp.iiko.ru/release_iiko/9.4.8049.0/Plugins/Front/Resto.Front.Api.Transport.V9Preview7-9.7.20.zip",
			Source:        PluginSourceFTP,
			FrontVersion:  "9.4.8049.0",
		},
		{
			Name:          "BoiteNoire",
			ApiVersion:    "V9",
			PluginVersion: "",
			DownloadUrl:   "ftpdir://ftp.iiko.ru/release_iiko/9.4.8049.0/Plugins/Front/Resto.Front.Api.BoiteNoire",
			Source:        PluginSourceFTP,
			FrontVersion:  "9.4.8049.0",
		},
	}

	merged := mergePluginsPreferPrimary(rapid, ftp)
	if len(merged) != 2 {
		t.Fatalf("len(merged) = %d, want 2", len(merged))
	}
	if merged[0].Source != PluginSourceRapid {
		t.Fatalf("merged[0].Source = %q, want %q", merged[0].Source, PluginSourceRapid)
	}
	if merged[1].Name != "BoiteNoire" {
		t.Fatalf("merged[1].Name = %q, want %q", merged[1].Name, "BoiteNoire")
	}
}

func TestFilterCompatiblePluginsIncludesFTPByFrontVersion(t *testing.T) {
	t.Parallel()

	plugins := []Plugin{
		{
			Name:          "RapidOnly",
			ApiVersion:    "V8",
			PluginVersion: "1.0.0",
			Source:        PluginSourceRapid,
		},
		{
			Name:          "FtpExact",
			ApiVersion:    "",
			PluginVersion: "1.0.0",
			Source:        PluginSourceFTP,
			FrontVersion:  "9.4.8049.0",
		},
		{
			Name:          "FtpOther",
			ApiVersion:    "",
			PluginVersion: "1.0.0",
			Source:        PluginSourceFTP,
			FrontVersion:  "9.4.7039.0",
		},
	}

	filtered := filterCompatiblePlugins(plugins, []string{"V8"}, "9.4.8049.0")
	if len(filtered) != 2 {
		t.Fatalf("len(filtered) = %d, want 2", len(filtered))
	}
	if filtered[0].Name != "RapidOnly" {
		t.Fatalf("filtered[0].Name = %q, want %q", filtered[0].Name, "RapidOnly")
	}
	if filtered[1].Name != "FtpExact" {
		t.Fatalf("filtered[1].Name = %q, want %q", filtered[1].Name, "FtpExact")
	}
}

func TestLivePluginsCacheRoundTrip(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	cachePath := livePluginsCachePath(tempDir, "9.4.8049.0")
	plugins := []Plugin{
		{
			Name:          "Transport",
			ApiVersion:    "V9Preview7",
			PluginVersion: "9.7.20",
			Source:        PluginSourceRapid,
			DownloadUrl:   "https://rapid.iiko.ru/plugins/test.zip",
		},
		{
			Name:         "BoiteNoire",
			ApiVersion:   "V9",
			Source:       PluginSourceFTP,
			FrontVersion: "9.4.8049.0",
			DownloadUrl:  "ftpdir://ftp.iiko.ru/release_iiko/9.4.8049.0/Plugins/Front/Resto.Front.Api.BoiteNoire",
		},
	}

	if err := writeLivePluginsCache(cachePath, "9.4.8049.0", plugins); err != nil {
		t.Fatalf("writeLivePluginsCache() error = %v", err)
	}

	got, err := readLivePluginsCache(cachePath, "9.4.8049.0")
	if err != nil {
		t.Fatalf("readLivePluginsCache() error = %v", err)
	}
	if len(got) != len(plugins) {
		t.Fatalf("len(got) = %d, want %d", len(got), len(plugins))
	}
	if got[0].Name != plugins[0].Name {
		t.Fatalf("got[0].Name = %q, want %q", got[0].Name, plugins[0].Name)
	}
	if got[1].Source != PluginSourceFTP {
		t.Fatalf("got[1].Source = %q, want %q", got[1].Source, PluginSourceFTP)
	}
}

func TestReadLivePluginsCacheRejectsWrongVersion(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	cachePath := livePluginsCachePath(tempDir, "9.4.8049.0")
	if err := writeLivePluginsCache(cachePath, "9.4.8049.0", []Plugin{{Name: "Transport"}}); err != nil {
		t.Fatalf("writeLivePluginsCache() error = %v", err)
	}

	if _, err := readLivePluginsCache(cachePath, "9.4.7039.0"); err == nil {
		t.Fatal("expected version mismatch error")
	}
}

func TestLivePluginsCachePathUsesTempFolder(t *testing.T) {
	t.Parallel()

	rootPath := filepath.Join("C:\\MH")
	cachePath := livePluginsCachePath(rootPath, "9.4.8049.0")
	wantDir := filepath.Join(rootPath, "temp")
	if filepath.Dir(cachePath) != wantDir {
		t.Fatalf("filepath.Dir(cachePath) = %q, want %q", filepath.Dir(cachePath), wantDir)
	}
	if filepath.Ext(cachePath) != ".json" {
		t.Fatalf("filepath.Ext(cachePath) = %q, want .json", filepath.Ext(cachePath))
	}
}

func TestReadLivePluginsCacheMissingFile(t *testing.T) {
	t.Parallel()

	cachePath := filepath.Join(t.TempDir(), "missing.json")
	_, err := readLivePluginsCache(cachePath, "9.4.8049.0")
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected os.ErrNotExist, got %v", err)
	}
}

func TestIsIikoFrontExecutableName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		want bool
	}{
		{name: "iikoFront.Net.exe", want: true},
		{name: "iikoFront.exe", want: true},
		{name: "New.iikoFront.Launcher.EXE", want: true},
		{name: "Resto.Front.Main.exe", want: true},
		{name: "BackOffice.exe", want: false},
		{name: "iikoFront.exe.config", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isIikoFrontExecutableName(tt.name); got != tt.want {
				t.Fatalf("isIikoFrontExecutableName(%q) = %v, want %v", tt.name, got, tt.want)
			}
		})
	}
}

func TestIikoFrontExecutableNameForVersion(t *testing.T) {
	t.Parallel()

	tests := []struct {
		version string
		want    string
	}{
		{version: "9.4.8049.0", want: "iikoFront.Net.exe"},
		{version: "9.5.0.0", want: "Resto.Front.Main.exe"},
		{version: "9.5.1234.0", want: "Resto.Front.Main.exe"},
	}

	for _, tt := range tests {
		t.Run(tt.version, func(t *testing.T) {
			if got := IikoFrontExecutableNameForVersion(tt.version); got != tt.want {
				t.Fatalf("IikoFrontExecutableNameForVersion(%q) = %q, want %q", tt.version, got, tt.want)
			}
		})
	}
}

func TestFindIikoFrontExecutableInDirFindsShallowExe(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	nested := filepath.Join(root, "nested")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatalf("mkdir nested: %v", err)
	}
	nestedExe := filepath.Join(nested, "iikoFront.Worker.exe")
	if err := os.WriteFile(nestedExe, []byte("exe"), 0o644); err != nil {
		t.Fatalf("write nested exe: %v", err)
	}
	shallowExe := filepath.Join(root, "New.iikoFront.Launcher.exe")
	if err := os.WriteFile(shallowExe, []byte("exe"), 0o644); err != nil {
		t.Fatalf("write shallow exe: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "iikoFront.exe.config"), []byte("config"), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	got, err := FindIikoFrontExecutableInDir(root)
	if err != nil {
		t.Fatalf("FindIikoFrontExecutableInDir() error = %v", err)
	}
	if got != shallowExe {
		t.Fatalf("FindIikoFrontExecutableInDir() = %q, want %q", got, shallowExe)
	}
}

func TestFindIikoFrontExecutableInDirFindsModernExe(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	modernExe := filepath.Join(root, "Resto.Front.Main.exe")
	if err := os.WriteFile(modernExe, []byte("exe"), 0o644); err != nil {
		t.Fatalf("write modern exe: %v", err)
	}

	got, err := FindIikoFrontExecutableInDir(root)
	if err != nil {
		t.Fatalf("FindIikoFrontExecutableInDir() error = %v", err)
	}
	if got != modernExe {
		t.Fatalf("FindIikoFrontExecutableInDir() = %q, want %q", got, modernExe)
	}
}

func TestResolvePluginInstallDirUsesTopLevelPluginFolder(t *testing.T) {
	t.Parallel()

	extractDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(extractDir, "Manifest.xml"), []byte(`<?xml version="1.0"?><Manifest><FileName>Resto.Front.Api.CustomerScreen.dll</FileName></Manifest>`), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	if err := os.WriteFile(filepath.Join(extractDir, "Resto.Front.Api.CustomerScreen.dll"), []byte("dll"), 0o644); err != nil {
		t.Fatalf("write dll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(extractDir, "helper-installer.exe"), []byte("exe"), 0o644); err != nil {
		t.Fatalf("write helper: %v", err)
	}

	dir, rawName, err := resolvePluginInstallDir(extractDir, filepath.Join(extractDir, "CustomerScreenBundle.zip"), &Plugin{Name: "CustomerScreen"})
	if err != nil {
		t.Fatalf("resolvePluginInstallDir() error = %v", err)
	}
	if dir != extractDir {
		t.Fatalf("dir = %q, want %q", dir, extractDir)
	}
	if rawName != "CustomerScreenBundle" {
		t.Fatalf("rawName = %q, want %q", rawName, "CustomerScreenBundle")
	}
}

func TestResolvePluginInstallDirFindsNestedPluginFolder(t *testing.T) {
	t.Parallel()

	extractDir := t.TempDir()
	wrapperDir := filepath.Join(extractDir, "Bundle")
	pluginDir := filepath.Join(wrapperDir, "Plugin.Front.Api.CustomerScreen")
	if err := os.MkdirAll(pluginDir, 0o755); err != nil {
		t.Fatalf("mkdir plugin dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(wrapperDir, "helper-installer.exe"), []byte("exe"), 0o644); err != nil {
		t.Fatalf("write helper: %v", err)
	}
	if err := os.WriteFile(filepath.Join(pluginDir, "manifest.xml"), []byte(`<?xml version="1.0"?><Manifest><FileName>Resto.Front.Api.CustomerScreen.dll</FileName></Manifest>`), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	if err := os.WriteFile(filepath.Join(pluginDir, "Resto.Front.Api.CustomerScreen.dll"), []byte("dll"), 0o644); err != nil {
		t.Fatalf("write dll: %v", err)
	}

	dir, rawName, err := resolvePluginInstallDir(extractDir, filepath.Join(extractDir, "CustomerScreenBundle.zip"), &Plugin{
		Name:       "CustomerScreen",
		InstallDir: "Plugin.Front.Api.CustomerScreen",
	})
	if err != nil {
		t.Fatalf("resolvePluginInstallDir() error = %v", err)
	}
	if dir != pluginDir {
		t.Fatalf("dir = %q, want %q", dir, pluginDir)
	}
	if rawName != "Plugin.Front.Api.CustomerScreen" {
		t.Fatalf("rawName = %q, want %q", rawName, "Plugin.Front.Api.CustomerScreen")
	}
}
