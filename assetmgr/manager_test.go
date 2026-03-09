package assetmgr

import (
	"bytes"
	"fmt"
	"goMH/config"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

func TestDownloadHTTPWithProgressResumesExistingPartialFile(t *testing.T) {
	rootDir := t.TempDir()
	manager := newTestManager(t, rootDir)

	payload := bytes.Repeat([]byte("resume-existing-"), 4096)
	localPath := filepath.Join(rootDir, "partial.bin")
	partialSize := len(payload) / 3
	if err := os.WriteFile(localPath, payload[:partialSize], 0644); err != nil {
		t.Fatalf("failed to prepare partial file: %v", err)
	}

	var receivedRange string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedRange = r.Header.Get("Range")
		if receivedRange == "" {
			t.Fatalf("expected Range request for existing partial file")
		}

		start := parseRangeStart(t, receivedRange)
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, len(payload)-1, len(payload)))
		w.Header().Set("Content-Length", strconv.Itoa(len(payload)-start))
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write(payload[start:])
	}))
	defer server.Close()

	alreadyDownloaded, err := manager.DownloadHTTPWithProgress(server.URL+"/partial.bin", localPath)
	if err != nil {
		t.Fatalf("DownloadHTTPWithProgress returned error: %v", err)
	}
	if alreadyDownloaded {
		t.Fatal("expected resumed download, not cache hit")
	}

	data, err := os.ReadFile(localPath)
	if err != nil {
		t.Fatalf("failed to read downloaded file: %v", err)
	}
	if !bytes.Equal(data, payload) {
		t.Fatal("downloaded file does not match payload after resume")
	}
	wantRange := fmt.Sprintf("bytes=%d-", partialSize)
	if receivedRange != wantRange {
		t.Fatalf("expected range %q, got %q", wantRange, receivedRange)
	}
}

func TestDownloadHTTPWithProgressRetriesAfterConnectionDrop(t *testing.T) {
	rootDir := t.TempDir()
	manager := newTestManager(t, rootDir)

	payload := bytes.Repeat([]byte("resume-after-drop-"), 8192)
	localPath := filepath.Join(rootDir, "asset.bin")
	initialChunk := len(payload) / 4

	var firstRequestDone atomic.Bool
	var rangeRequests []string
	var mu sync.Mutex

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rangeHeader := r.Header.Get("Range")
		if rangeHeader != "" {
			mu.Lock()
			rangeRequests = append(rangeRequests, rangeHeader)
			mu.Unlock()
		}

		if !firstRequestDone.Swap(true) {
			w.Header().Set("Content-Length", strconv.Itoa(len(payload)))
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(payload[:initialChunk])
			if flusher, ok := w.(http.Flusher); ok {
				flusher.Flush()
			}
			if hijacker, ok := w.(http.Hijacker); ok {
				conn, _, err := hijacker.Hijack()
				if err == nil {
					_ = conn.Close()
				}
			}
			return
		}

		if rangeHeader == "" {
			t.Fatalf("expected retry request to use Range header")
		}

		start := parseRangeStart(t, rangeHeader)
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, len(payload)-1, len(payload)))
		w.Header().Set("Content-Length", strconv.Itoa(len(payload)-start))
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write(payload[start:])
	}))
	defer server.Close()

	alreadyDownloaded, err := manager.DownloadHTTPWithProgress(server.URL+"/asset.bin", localPath)
	if err != nil {
		t.Fatalf("DownloadHTTPWithProgress returned error: %v", err)
	}
	if alreadyDownloaded {
		t.Fatal("expected retried download, not cache hit")
	}

	data, err := os.ReadFile(localPath)
	if err != nil {
		t.Fatalf("failed to read downloaded file: %v", err)
	}
	if !bytes.Equal(data, payload) {
		t.Fatal("downloaded file does not match payload after retry")
	}

	mu.Lock()
	defer mu.Unlock()
	if len(rangeRequests) == 0 {
		t.Fatal("expected at least one Range retry after connection drop")
	}
}

func newTestManager(t *testing.T, rootDir string) *Manager {
	t.Helper()

	manager, err := New(&config.Config{
		RootPath:        rootDir,
		AssetsCachePath: filepath.Join(rootDir, "cache"),
	})
	if err != nil {
		t.Fatalf("failed to create manager: %v", err)
	}
	return manager
}

func parseRangeStart(t *testing.T, header string) int {
	t.Helper()

	if !strings.HasPrefix(header, "bytes=") || !strings.HasSuffix(header, "-") {
		t.Fatalf("unexpected Range header: %q", header)
	}
	value := strings.TrimSuffix(strings.TrimPrefix(header, "bytes="), "-")
	start, err := strconv.Atoi(value)
	if err != nil {
		t.Fatalf("failed to parse range start from %q: %v", header, err)
	}
	return start
}
