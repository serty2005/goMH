package iikoplugins

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type rapidCatalogRequests struct {
	sync.Mutex
	counts map[string]int
}

func (r *rapidCatalogRequests) record(requestURI string) {
	r.Lock()
	defer r.Unlock()
	if r.counts == nil {
		r.counts = make(map[string]int)
	}
	r.counts[requestURI]++
}

func (r *rapidCatalogRequests) snapshot() map[string]int {
	r.Lock()
	defer r.Unlock()
	counts := make(map[string]int, len(r.counts))
	for uri, count := range r.counts {
		counts[uri] = count
	}
	return counts
}

func rapidCatalogURL(serverURL, decodedPath string) string {
	parsed, _ := url.Parse(serverURL)
	parsed.Path = decodedPath
	return parsed.String()
}

func TestRapidCatalogTraversesOnlyCanonicalDirectoryChildren(t *testing.T) {
	var requests, externalRequests rapidCatalogRequests
	external := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		externalRequests.record(r.RequestURI)
		io.WriteString(w, `<a href="external.zip">external.zip</a>`)
	}))
	defer external.Close()

	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.record(r.RequestURI)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		switch r.URL.Path {
		case "/plugins/":
			fmt.Fprintf(w, `<!doctype html><html><body><table id="list"><tbody>
<tr><td><a href="../"><span>Parent directory/</span></a></td></tr>
<tr><td><a href="?C=N;O=D">Name</a><a href="?C=M;O=A">Modified</a></td></tr>
<tr><td><a href="/">Site root/</a><a href="/other/">Other/</a></td></tr>
<tr><td><a href="%s/plugins/">External/</a></td></tr>
<tr><td><a href="%s/plugins/absolute/">Absolute/</a></td></tr>
<tr><td><a href="old/">Old/</a><a href="./old/">Old again/</a><a href="%%6Fld/">Encoded old/</a></td></tr>
<tr><td><a href="legacy/">Legacy/</a><a href="%%D0%%A1%%D0%%BB%%D0%%BE%%D0%%B2%%D0%%BE%%20%%D0%%B8%%D0%%BC%%D1%%8F/">Слово имя/</a></td></tr>
<tr><td><a href="Alpha.zip">Alpha.zip</a><a href="./Alpha.zip">Alpha.zip</a><a href="%%41lpha.zip">Alpha.zip</a></td></tr>
<tr><td><a href="missing/?sort=name">Sorting/</a><a href="missing/#part">Fragment/</a></td></tr>
<tr><td><a href="query.zip?download=1">Query archive</a><a href="fragment.zip#part">Fragment archive</a></td></tr>
<tr><td><a href="/plugins/skipped/deep/">Deep shortcut/</a><a href="/plugins/skipped/deep.zip">Deep archive shortcut</a></td></tr>
<tr><td><a href="%%2e%%2e/">Encoded parent/</a><a href="%%2e%%2e/outside.zip">Encoded outside</a></td></tr>
<tr><td><a href="readme.txt">Readme</a><a href="javascript:foo/">Script/</a><a href="mailto:foo/">Mail/</a></td></tr>
</tbody></table></body></html>`, external.URL, server.URL)
		case "/plugins/old/":
			io.WriteString(w, `<a href="../">Parent directory/</a><a href="nested/">Nested/</a><a href="Old.ZIP">Old.ZIP</a>`)
		case "/plugins/old/nested/":
			io.WriteString(w, `<a href="../../">Parent directory/</a><a href="Archive.zip">Archive.zip</a><a href="/plugins/legacy/">Sibling/</a>`)
		case "/plugins/legacy/":
			io.WriteString(w, `<a href="../">Parent directory/</a><a href="older/">Older/</a>`)
		case "/plugins/legacy/older/":
			io.WriteString(w, `<a href="Legacy.zip">Legacy.zip</a>`)
		case "/plugins/absolute/":
			io.WriteString(w, `<a href="/plugins/absolute/Absolute.zip">Absolute.zip</a>`)
		case "/plugins/Слово имя/":
			io.WriteString(w, `<a href="Плагин%20V8.zip">Плагин V8.zip</a>`)
		default:
			http.Error(w, "unexpected request", http.StatusNotFound)
		}
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var progress []RapidScanProgress
	zips, err := scanPluginZipFilesWithProgress(ctx, server.URL+"/plugins", 8, func(state RapidScanProgress) {
		progress = append(progress, state)
	})
	if err != nil {
		t.Fatal(err)
	}
	wantPaths := []string{
		"/plugins/Alpha.zip",
		"/plugins/absolute/Absolute.zip",
		"/plugins/legacy/older/Legacy.zip",
		"/plugins/old/Old.ZIP",
		"/plugins/old/nested/Archive.zip",
		"/plugins/Слово имя/Плагин V8.zip",
	}
	want := make([]string, len(wantPaths))
	for i, archivePath := range wantPaths {
		want[i] = rapidCatalogURL(server.URL, archivePath)
	}
	sort.Strings(want)
	if !reflect.DeepEqual(zips, want) {
		t.Fatalf("archives = %v, want %v", zips, want)
	}
	wantRequests := map[string]int{
		"/plugins/":              1,
		"/plugins/absolute/":     1,
		"/plugins/old/":          1,
		"/plugins/old/nested/":   1,
		"/plugins/legacy/":       1,
		"/plugins/legacy/older/": 1,
		"/plugins/%D0%A1%D0%BB%D0%BE%D0%B2%D0%BE%20%D0%B8%D0%BC%D1%8F/": 1,
	}
	if got := requests.snapshot(); !reflect.DeepEqual(got, wantRequests) {
		t.Fatalf("requests = %v, want only %v", got, wantRequests)
	}
	if got := externalRequests.snapshot(); len(got) != 0 {
		t.Fatalf("external site was requested: %v", got)
	}
	assertRapidCatalogProgress(t, progress, len(wantRequests))
}

func TestRapidCatalogDepthLimitCompletesProgress(t *testing.T) {
	for _, depth := range []int{0, 1} {
		t.Run(fmt.Sprintf("depth%d", depth), func(t *testing.T) {
			var requests rapidCatalogRequests
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.record(r.RequestURI)
				io.WriteString(w, `<a href="plugin.zip">plugin.zip</a><a href="child/">Child/</a>`)
			}))
			defer server.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			var progress []RapidScanProgress
			zips, err := scanPluginZipFilesWithProgress(ctx, server.URL+"/plugins/", depth, func(state RapidScanProgress) {
				progress = append(progress, state)
			})
			if err != nil {
				t.Fatal(err)
			}
			if len(zips) != depth+1 {
				t.Fatalf("archives = %v, want %d", zips, depth+1)
			}
			wantRequests := map[string]int{"/plugins/": 1}
			if depth == 1 {
				wantRequests["/plugins/child/"] = 1
			}
			if got := requests.snapshot(); !reflect.DeepEqual(got, wantRequests) {
				t.Fatalf("requests = %v, want %v", got, wantRequests)
			}
			assertRapidCatalogProgress(t, progress, depth+1)
		})
	}
}

func assertRapidCatalogProgress(t *testing.T, progress []RapidScanProgress, total int) {
	t.Helper()
	if len(progress) == 0 {
		t.Fatal("missing progress")
	}
	previousProcessed, previousTotal := 0, 0
	for _, state := range progress {
		if state.ProcessedPages < previousProcessed || state.TotalPages < previousTotal || state.ProcessedPages > state.TotalPages {
			t.Fatalf("invalid progress sequence: %v", progress)
		}
		previousProcessed, previousTotal = state.ProcessedPages, state.TotalPages
	}
	last := progress[len(progress)-1]
	if last.ProcessedPages != total || last.TotalPages != total {
		t.Fatalf("final progress = %+v, want %d/%d", last, total, total)
	}
}

func TestRapidCatalogDoesNotReturnPartialSuccess(t *testing.T) {
	for _, status := range []int{http.StatusForbidden, http.StatusNotFound, http.StatusInternalServerError} {
		t.Run(fmt.Sprintf("HTTP%d", status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/plugins/" {
					io.WriteString(w, `<a href="root.zip">root.zip</a><a href="broken/">Broken/</a>`)
					return
				}
				http.Error(w, "listing unavailable", status)
			}))
			defer server.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			zips, err := scanPluginZipFilesWithProgress(ctx, server.URL+"/plugins/", 2, nil)
			if err == nil || !strings.Contains(err.Error(), fmt.Sprint(status)) {
				t.Fatalf("error = %v, want HTTP %d", err, status)
			}
			if len(zips) != 0 {
				t.Fatalf("partial archives returned with listing failure: %v", zips)
			}
		})
	}
}

func TestRapidCatalogRejectsEmptyCatalog(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `<a href="../">Parent directory/</a><a href="readme.txt">Readme</a>`)
	}))
	defer server.Close()
	zips, err := scanPluginZipFilesWithProgress(context.Background(), server.URL+"/plugins/", 2, nil)
	if err == nil || len(zips) != 0 {
		t.Fatalf("empty catalog returned archives %v, error %v", zips, err)
	}
}

func TestRapidCatalogDoesNotFollowEscapingRedirect(t *testing.T) {
	var outsideRequests atomic.Int32
	outside := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		outsideRequests.Add(1)
		io.WriteString(w, `<a href="wrong.zip">wrong.zip</a>`)
	}))
	defer outside.Close()
	for _, target := range []string{"/outside/", outside.URL + "/plugins/"} {
		t.Run(target, func(t *testing.T) {
			var requests rapidCatalogRequests
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.record(r.RequestURI)
				if r.URL.Path == "/plugins/" {
					io.WriteString(w, `<a href="redirect/">redirect/</a><a href="root.zip">root.zip</a>`)
					return
				}
				if r.URL.Path == "/plugins/redirect/" {
					http.Redirect(w, r, target, http.StatusMovedPermanently)
					return
				}
				io.WriteString(w, `<a href="wrong.zip">wrong.zip</a>`)
			}))
			defer server.Close()
			zips, err := scanPluginZipFilesWithProgress(context.Background(), server.URL+"/plugins/", 2, nil)
			if err == nil || len(zips) != 0 {
				t.Fatalf("escaping redirect returned archives %v, error %v", zips, err)
			}
			if got := requests.snapshot(); !reflect.DeepEqual(got, map[string]int{"/plugins/": 1, "/plugins/redirect/": 1}) {
				t.Fatalf("followed escaping redirect: %v", got)
			}
		})
	}
	if outsideRequests.Load() != 0 {
		t.Fatal("redirect was followed to external server")
	}
}

func TestRapidCatalogCancellationInterruptsInFlightRequest(t *testing.T) {
	for _, flushHeaders := range []bool{false, true} {
		t.Run(fmt.Sprintf("headersFlushed=%t", flushHeaders), func(t *testing.T) {
			entered := make(chan struct{})
			release := make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if flushHeaders {
					w.Header().Set("Content-Type", "text/html")
					w.WriteHeader(http.StatusOK)
					w.(http.Flusher).Flush()
				}
				close(entered)
				select {
				case <-r.Context().Done():
				case <-release:
				}
			}))
			defer server.Close()
			defer close(release)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan error, 1)
			go func() {
				_, err := scanPluginZipFilesWithProgress(ctx, server.URL+"/plugins/", 2, nil)
				done <- err
			}()
			select {
			case <-entered:
			case <-time.After(3 * time.Second):
				t.Fatal("request did not start")
			}
			cancel()
			select {
			case err := <-done:
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("error = %v, want context.Canceled", err)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("canceled scan is still waiting for HTTP response")
			}
		})
	}
}

func TestRapidCatalogUsesBoundedConcurrencyAndSerializedProgress(t *testing.T) {
	entered := make(chan struct{}, 12)
	release := make(chan struct{})
	var releaseOnce sync.Once
	var activeRequests, maxRequests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/plugins/" {
			for i := range 12 {
				fmt.Fprintf(w, `<a href="plugin%d/">Plugin%d/</a>`, i, i)
			}
			return
		}
		active := activeRequests.Add(1)
		defer activeRequests.Add(-1)
		for current := maxRequests.Load(); active > current; current = maxRequests.Load() {
			if maxRequests.CompareAndSwap(current, active) {
				break
			}
		}
		entered <- struct{}{}
		select {
		case <-release:
			io.WriteString(w, `<a href="archive.zip">archive.zip</a>`)
		case <-r.Context().Done():
		}
	}))
	defer server.Close()
	defer releaseOnce.Do(func() { close(release) })
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	var activeReports atomic.Int32
	var overlappingReports atomic.Bool
	var progressMu sync.Mutex
	var progress []RapidScanProgress
	done := make(chan error, 1)
	go func() {
		zips, err := scanPluginZipFilesWithProgress(ctx, server.URL+"/plugins/", 2, func(state RapidScanProgress) {
			if activeReports.Add(1) > 1 {
				overlappingReports.Store(true)
			}
			defer activeReports.Add(-1)
			for range 16 {
				runtime.Gosched()
			}
			progressMu.Lock()
			progress = append(progress, state)
			progressMu.Unlock()
		})
		if err == nil && len(zips) != 12 {
			err = fmt.Errorf("got %d archives, want 12", len(zips))
		}
		done <- err
	}()
	for range 4 {
		select {
		case <-entered:
		case <-ctx.Done():
			t.Fatalf("four directory requests did not run concurrently: %v", ctx.Err())
		}
	}
	releaseOnce.Do(func() { close(release) })
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if max := maxRequests.Load(); max != 4 {
		t.Fatalf("maximum concurrent requests = %d, want 4", max)
	}
	if overlappingReports.Load() {
		t.Fatal("progress callbacks ran concurrently")
	}
	progressMu.Lock()
	defer progressMu.Unlock()
	assertRapidCatalogProgress(t, progress, 13)
}

func TestRapidCatalogRetriesTransientStatus(t *testing.T) {
	for _, status := range []int{http.StatusTooManyRequests, http.StatusServiceUnavailable} {
		t.Run(fmt.Sprintf("HTTP%d", status), func(t *testing.T) {
			var attempts atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if attempts.Add(1) == 1 {
					w.Header().Set("Retry-After", "0")
					http.Error(w, "try later", status)
					return
				}
				io.WriteString(w, `<a href="archive.zip">archive.zip</a>`)
			}))
			defer server.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			zips, err := scanPluginZipFilesWithProgress(ctx, server.URL+"/plugins/", 2, nil)
			if err != nil || len(zips) != 1 || attempts.Load() != 2 {
				t.Fatalf("archives = %v, error = %v, attempts = %d; want one archive after two attempts", zips, err, attempts.Load())
			}
		})
	}
}

func TestRapidCatalogRetriesAreBounded(t *testing.T) {
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		w.Header().Set("Retry-After", "0")
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	zips, err := scanPluginZipFilesWithProgress(ctx, server.URL+"/plugins/", 2, nil)
	if err == nil || len(zips) != 0 || errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("archives = %v, error = %v; want bounded HTTP failure", zips, err)
	}
	if count := attempts.Load(); count < 2 || count > 6 {
		t.Fatalf("attempts = %d, want 2..6", count)
	}
}

func TestRapidCatalogRespectsRetryAfter(t *testing.T) {
	var attempts atomic.Int32
	firstAttempt := make(chan time.Time, 1)
	delay := make(chan time.Duration, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if attempts.Add(1) == 1 {
			firstAttempt <- time.Now()
			w.Header().Set("Retry-After", "1")
			http.Error(w, "retry later", http.StatusTooManyRequests)
			return
		}
		delay <- time.Since(<-firstAttempt)
		io.WriteString(w, `<a href="archive.zip">archive.zip</a>`)
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	zips, err := scanPluginZipFilesWithProgress(ctx, server.URL+"/plugins/", 2, nil)
	if err != nil || len(zips) != 1 || attempts.Load() != 2 {
		t.Fatalf("archives = %v, error = %v, attempts = %d; want one archive after two attempts", zips, err, attempts.Load())
	}
	if elapsed := <-delay; elapsed < 950*time.Millisecond {
		t.Fatalf("retried after %v, before Retry-After: 1", elapsed)
	}
}

func TestRapidCatalogCancellationInterruptsRetryAfter(t *testing.T) {
	entered := make(chan struct{}, 1)
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		w.Header().Set("Retry-After", "30")
		http.Error(w, "retry later", http.StatusTooManyRequests)
		entered <- struct{}{}
	}))
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := scanPluginZipFilesWithProgress(ctx, server.URL+"/plugins/", 2, nil)
		done <- err
	}()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("request did not start")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("error = %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Retry-After delay ignored cancellation")
	}
	if count := attempts.Load(); count != 1 {
		t.Fatalf("attempts = %d, want no request before Retry-After", count)
	}
}
