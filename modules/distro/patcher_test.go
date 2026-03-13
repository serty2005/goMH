package distro

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestGetPatchesPathPrefersBaseVersionDir(t *testing.T) {
	t.Parallel()

	got := getPatchesPath("https://example.com/patches/", "9.4.8049.0")
	want := "https://example.com/patches/9.4.8049.0"
	if got != want {
		t.Fatalf("unexpected primary path: got %q want %q", got, want)
	}

	fallback := getPatchesFallbackPath("https://example.com/patches/", "9.4.8049.0")
	wantFallback := "https://example.com/patches/9.4.8049.0/Patches"
	if fallback != wantFallback {
		t.Fatalf("unexpected fallback path: got %q want %q", fallback, wantFallback)
	}
}

func TestFindPatchesUsesPrimaryDirWhenArchivesExist(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/patches/9.4.8049.0":
			_, _ = fmt.Fprint(w, `<a href="front-build%20101).7z">front-build 101).7z</a>`)
		case "/patches/9.4.8049.0/front-build 101).txt":
			_, _ = fmt.Fprint(w, "Front.dll\n\n0123456789012345678901234567890123456789 RMS-101 fixed: test")
		case "/patches/9.4.8049.0/Patches":
			t.Fatalf("fallback path should not be requested when primary has archives")
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	patches, err := FindPatches(server.URL+"/patches", "9.4.8049.0")
	if err != nil {
		t.Fatalf("FindPatches returned error: %v", err)
	}
	if len(patches) != 1 {
		t.Fatalf("expected 1 patch, got %d", len(patches))
	}
	if patches[0].BuildNumber != 101 {
		t.Fatalf("unexpected build number: got %d", patches[0].BuildNumber)
	}
	if !strings.Contains(patches[0].ChangeNote, "RMS-101 test") {
		t.Fatalf("expected parsed changenote, got %q", patches[0].ChangeNote)
	}
}

func TestFindPatchesFallsBackToPatchesDirWhenPrimaryContainsOnlyFolder(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/patches/9.4.8049.0":
			_, _ = fmt.Fprint(w, `<a href="Patches/">Patches/</a>`)
		case "/patches/9.4.8049.0/Patches":
			_, _ = fmt.Fprint(w, `<a href="front-build%20202).zip">front-build 202).zip</a>`)
		case "/patches/9.4.8049.0/Patches/front-build 202).txt":
			_, _ = fmt.Fprint(w, "Front.dll\n\n0123456789012345678901234567890123456789 RMS-202 fixed: fallback")
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	patches, err := FindPatches(server.URL+"/patches", "9.4.8049.0")
	if err != nil {
		t.Fatalf("FindPatches returned error: %v", err)
	}
	if len(patches) != 1 {
		t.Fatalf("expected 1 patch from fallback, got %d", len(patches))
	}
	if patches[0].BuildNumber != 202 {
		t.Fatalf("unexpected build number from fallback: got %d", patches[0].BuildNumber)
	}
	if !strings.Contains(patches[0].ChangeNote, "RMS-202 fallback") {
		t.Fatalf("expected fallback changenote, got %q", patches[0].ChangeNote)
	}
}

func TestFormatPatchChangeNote(t *testing.T) {
	t.Parallel()

	raw := []byte("Agent.dll\r\nResto.CashServer.dll\r\n\r\nf43c9a38da02e9dd9e75e3533f4a57fdd0ee1c33 RMS-59660 fixed: Fix one\r\n" +
		"dbd547612eb2cf794c8a807cc801268fce9025d1 Revert RMS-58768 fixed: Fix two\r\n")

	got := formatPatchChangeNote(raw)
	wantParts := []string{
		"Файлы:",
		"Agent.dll",
		"Resto.CashServer.dll",
		"Исправления:",
		"RMS-59660 Fix one",
		"RMS-58768 Fix two",
	}
	for _, part := range wantParts {
		if !strings.Contains(got, part) {
			t.Fatalf("expected changenote to contain %q, got %q", part, got)
		}
	}
}
