package winutils

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func writeConfigFixture(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestFindFrontConfigNewest(t *testing.T) {
	root := t.TempDir()
	if _, err := findFrontConfig(root); err == nil {
		t.Fatal("missing file must fail")
	}
	iiko := filepath.Join(root, "iiko", "CashServer", "config.xml")
	syrve := filepath.Join(root, "Syrve", "CashServer", "config.xml")
	writeConfigFixture(t, iiko, []byte("iiko"))
	found, err := findFrontConfig(root)
	if err != nil || found.Path != iiko {
		t.Fatalf("single config: %+v, %v", found, err)
	}
	writeConfigFixture(t, syrve, []byte("syrve"))
	old, latest := time.Now().Add(-time.Hour), time.Now()
	if err := os.Chtimes(iiko, old, old); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(syrve, latest, latest); err != nil {
		t.Fatal(err)
	}
	found, err = findFrontConfig(root)
	if err != nil || found.Path != syrve || string(found.Data) != "syrve" {
		t.Fatalf("newest config: %+v, %v", found, err)
	}
	if err := os.Chtimes(iiko, latest.Add(time.Minute), latest.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	found, err = findFrontConfig(root)
	if err != nil || found.Path != iiko {
		t.Fatalf("iiko newer: %+v, %v", found, err)
	}
}

func TestFindFrontConfigEmptyAppData(t *testing.T) {
	t.Setenv("APPDATA", "")
	if _, err := FindFrontConfig(); err == nil {
		t.Fatal("empty APPDATA must not search relative paths")
	}
}

func TestSaveFileWithBackup(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.xml")
	original, updated := []byte("original\r\n"), []byte("updated\r\n")
	writeConfigFixture(t, path, original)
	backup, err := SaveFileWithBackup(path, original, updated)
	if err != nil {
		t.Fatal(err)
	}
	for file, want := range map[string][]byte{path: updated, backup: original} {
		got, err := os.ReadFile(file)
		if err != nil || !bytes.Equal(got, want) {
			t.Fatalf("unexpected data in %s: %v", file, err)
		}
	}
	if _, err := SaveFileWithBackup(path, original, []byte("stale")); err == nil {
		t.Fatal("stale edit must be rejected")
	}
	got, _ := os.ReadFile(path)
	if !bytes.Equal(got, updated) {
		t.Fatal("conflict overwrote config")
	}
	second, err := SaveFileWithBackup(path, updated, []byte("next"))
	if err != nil || backup == second {
		t.Fatal("backup must have a unique name")
	}
	got, _ = os.ReadFile(backup)
	if !bytes.Equal(got, original) {
		t.Fatal("previous backup overwritten")
	}
}

func TestSaveFileNoChanges(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "config.xml")
	writeConfigFixture(t, path, []byte("same"))
	backup, err := SaveFileWithBackup(path, []byte("same"), []byte("same"))
	files, _ := os.ReadDir(root)
	if err != nil || backup != "" || len(files) != 1 {
		t.Fatal("no-op created files")
	}
}

func TestSaveLockedFileKeepsOriginal(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "config.xml")
	original := []byte("original")
	writeConfigFixture(t, path, original)
	ptr, err := windows.UTF16PtrFromString(path)
	if err != nil {
		t.Fatal(err)
	}
	handle, err := windows.CreateFile(ptr, windows.GENERIC_READ, windows.FILE_SHARE_READ, nil, windows.OPEN_EXISTING, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(handle)
	backup, err := SaveFileWithBackup(path, original, []byte("new"))
	if err == nil {
		t.Fatal("expected locked file error")
	}
	got, _ := os.ReadFile(path)
	if !bytes.Equal(got, original) {
		t.Fatal("locked file was changed")
	}
	if backup == "" {
		t.Fatal("backup missing")
	}
	temps, err := filepath.Glob(filepath.Join(root, "*.tmp"))
	if err != nil || len(temps) != 0 {
		t.Fatal("temporary files were not cleaned up")
	}
}
