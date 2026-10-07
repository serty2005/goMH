package iikoplugins

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func TestReadLivePluginsCacheRejectsOutdatedCatalog(t *testing.T) {
	t.Parallel()

	for _, version := range []int{0, livePluginsCatalogVersion + 1} {
		t.Run(strconv.Itoa(version), func(t *testing.T) {
			t.Parallel()
			cache := map[string]any{
				"front_version": "9.4.8049.0",
				"plugins":       []Plugin{{Name: "Transport"}},
			}
			if version != 0 {
				cache["catalog_version"] = version
			}
			data, err := json.Marshal(cache)
			if err != nil {
				t.Fatal(err)
			}
			cachePath := filepath.Join(t.TempDir(), "plugins.json")
			if err := os.WriteFile(cachePath, data, 0o600); err != nil {
				t.Fatal(err)
			}

			if plugins, err := readLivePluginsCache(cachePath, "9.4.8049.0"); err == nil || len(plugins) != 0 {
				t.Fatalf("outdated cache returned plugins=%v, error=%v", plugins, err)
			}
		})
	}
}

func TestWriteLivePluginsCacheUsesCurrentCatalogVersion(t *testing.T) {
	t.Parallel()

	cachePath := filepath.Join(t.TempDir(), "plugins.json")
	if err := writeLivePluginsCache(cachePath, "9.4.8049.0", []Plugin{{Name: "Transport"}}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(cachePath)
	if err != nil {
		t.Fatal(err)
	}
	var cache livePluginsCache
	if err := json.Unmarshal(data, &cache); err != nil {
		t.Fatal(err)
	}
	if cache.CatalogVersion != livePluginsCatalogVersion {
		t.Fatalf("catalog version = %d, want %d", cache.CatalogVersion, livePluginsCatalogVersion)
	}
}

func TestLoadCachedLivePluginsWithProgressHonorsCancellation(t *testing.T) {
	t.Parallel()

	for _, cancelOnReport := range []bool{false, true} {
		name := "before cache read"
		if cancelOnReport {
			name = "after cache read"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			rootPath := t.TempDir()
			frontVersion := "9.4.8049.0"
			if err := writeLivePluginsCache(livePluginsCachePath(rootPath, frontVersion), frontVersion, []Plugin{{Name: "Transport"}}); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if !cancelOnReport {
				cancel()
			}
			reports := 0
			plugins, err := LoadCachedLivePluginsWithProgress(ctx, rootPath, frontVersion, func(state RapidScanProgress) {
				reports++
				if state.Source != livePluginsCacheSource {
					t.Fatalf("source = %q, want cache", state.Source)
				}
				cancel()
			})
			if !errors.Is(err, context.Canceled) || len(plugins) != 0 {
				t.Fatalf("canceled cache read returned plugins=%v, error=%v", plugins, err)
			}
			wantReports := 0
			if cancelOnReport {
				wantReports = 1
			}
			if reports != wantReports {
				t.Fatalf("reports = %d, want %d", reports, wantReports)
			}
		})
	}
}

func TestLoadLivePluginsWithProgressHonorsCanceledContext(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	plugins, err := LoadLivePluginsWithProgress(ctx, "9.4.8049.0", func(RapidScanProgress) {
		t.Fatal("canceled live load must not report a source scan")
	})
	if !errors.Is(err, context.Canceled) || len(plugins) != 0 {
		t.Fatalf("canceled live load returned plugins=%v, error=%v", plugins, err)
	}
}

func TestLoadFTPPluginsWithProgressHonorsCanceledContext(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	plugins, err := loadFTPPluginsWithProgress(ctx, "", nil)
	if !errors.Is(err, context.Canceled) || len(plugins) != 0 {
		t.Fatalf("canceled FTP load returned plugins=%v, error=%v", plugins, err)
	}
}

func TestResolveLivePluginSourcesCachesOnlyCompleteCatalog(t *testing.T) {
	t.Parallel()

	unavailable := errors.New("source unavailable")
	for _, test := range []struct {
		name         string
		rapid        []Plugin
		rapidErr     error
		ftp          []Plugin
		ftpErr       error
		wantCount    int
		wantComplete bool
		wantError    bool
	}{
		{
			name: "both sources complete", rapid: []Plugin{{Name: "Rapid", Source: PluginSourceRapid}},
			ftp: []Plugin{{Name: "FTP", Source: PluginSourceFTP}}, wantCount: 2, wantComplete: true,
		},
		{
			name: "Rapid fallback must not be cached", rapid: []Plugin{{Name: "Rapid", Source: PluginSourceRapid}},
			ftpErr: unavailable, wantCount: 1,
		},
		{
			name: "FTP fallback must not be cached", rapidErr: unavailable,
			ftp: []Plugin{{Name: "FTP", Source: PluginSourceFTP}}, wantCount: 1,
		},
		{name: "both sources failed", rapidErr: unavailable, ftpErr: unavailable, wantError: true},
		{name: "empty successful sources", wantError: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			plugins, complete, err := resolveLivePluginSources(test.rapid, test.rapidErr, test.ftp, test.ftpErr)
			if (err != nil) != test.wantError {
				t.Fatalf("error = %v, want error %t", err, test.wantError)
			}
			if len(plugins) != test.wantCount || complete != test.wantComplete {
				t.Fatalf("catalog count=%d complete=%t, want count=%d complete=%t", len(plugins), complete, test.wantCount, test.wantComplete)
			}
		})
	}
}

func TestMergePluginsKeepsCompatibleFTPWhenRapidAPINotSpecified(t *testing.T) {
	t.Parallel()
	rapid := Plugin{Name: "CustomerDisplay", PluginVersion: "1.0.1126.0", Source: PluginSourceRapid}
	ftp := Plugin{Name: rapid.Name, PluginVersion: rapid.PluginVersion, Source: PluginSourceFTP, FrontVersion: "9.4.8049.0"}
	merged := mergePluginsPreferPrimary([]Plugin{rapid}, []Plugin{ftp})
	if len(merged) != 2 {
		t.Fatalf("merged = %+v, want both source records", merged)
	}
	compatible := filterCompatiblePlugins(merged, []string{"V8"}, ftp.FrontVersion)
	if len(compatible) != 1 || compatible[0] != ftp {
		t.Fatalf("compatible = %+v, want FTP record %+v", compatible, ftp)
	}
}
