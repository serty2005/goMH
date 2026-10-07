package iikoplugins

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"path"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/html"
)

const (
	rapidScanWorkers     = 4
	rapidRequestInterval = 500 * time.Millisecond
	rapidRequestAttempts = 6
)

type rapidDirectory struct {
	url   string
	depth int
}

type rapidDirectoryResult struct {
	directory rapidDirectory
	dirs      []string
	zips      []string
	err       error
}

func scanPluginZipFiles(ctx context.Context, startURL string, depthLimit int, report func(RapidScanProgress)) ([]string, error) {
	return scanPluginZipFilesWithProgress(ctx, startURL, depthLimit, report)
}

func scanPluginZipFilesWithProgress(ctx context.Context, startURL string, depthLimit int, report func(RapidScanProgress)) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if depthLimit < 0 {
		return nil, fmt.Errorf("глубина парсинга плагинов не может быть отрицательной")
	}
	root, err := canonicalRapidURL(startURL)
	if err != nil {
		return nil, err
	}
	root.Path = strings.TrimRight(root.Path, "/") + "/"
	startURL = root.String()

	ctx, cancel := context.WithCancel(ctx)
	client := &http.Client{
		Timeout: 15 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return fmt.Errorf("слишком много перенаправлений каталога плагинов")
			}
			initial, err := canonicalRapidURL(via[0].URL.String())
			if err != nil {
				return err
			}
			redirect, err := canonicalRapidURL(req.URL.String())
			if err != nil || !sameRapidOrigin(initial, redirect) || initial.Path != redirect.Path {
				return fmt.Errorf("перенаправление за пределы каталога плагинов: %s", req.URL)
			}
			return nil
		},
	}
	if transport, ok := http.DefaultTransport.(*http.Transport); ok {
		transport = transport.Clone()
		transport.MaxConnsPerHost = rapidScanWorkers
		transport.MaxIdleConnsPerHost = rapidScanWorkers
		client.Transport = transport
	}
	defer client.CloseIdleConnections()

	jobs := make(chan rapidDirectory)
	results := make(chan rapidDirectoryResult, rapidScanWorkers)
	gate := &rapidRequestGate{interval: rapidRequestInterval}
	var workers sync.WaitGroup
	for range rapidScanWorkers {
		workers.Go(func() {
			for {
				select {
				case <-ctx.Done():
					return
				case directory := <-jobs:
					dirs, zips, err := readRapidDirectory(ctx, client, gate, directory.url)
					select {
					case results <- rapidDirectoryResult{directory, dirs, zips, err}:
					case <-ctx.Done():
						return
					}
				}
			}
		})
	}
	defer func() {
		cancel()
		workers.Wait()
	}()

	queue := []rapidDirectory{{url: startURL}}
	discovered := map[string]struct{}{startURL: {}}
	zipSet := map[string]struct{}{}
	active, processed := 0, 0
	for len(queue) > 0 || active > 0 {
		var next rapidDirectory
		var dispatch chan rapidDirectory
		if len(queue) > 0 && active < rapidScanWorkers {
			next = queue[0]
			dispatch = jobs
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case dispatch <- next:
			queue = queue[1:]
			active++
		case result := <-results:
			active--
			processed++
			if result.err != nil {
				slog.Warn("Не удалось прочитать каталог плагинов", "url", result.directory.url, "error", result.err)
				reportRapidScanProgress(report, processed, len(discovered), result.directory.url)
				return nil, fmt.Errorf("не удалось полностью прочитать каталог плагинов, %s: %w", result.directory.url, result.err)
			}
			for _, zipURL := range result.zips {
				zipSet[zipURL] = struct{}{}
			}
			if result.directory.depth < depthLimit {
				for _, dirURL := range result.dirs {
					if _, exists := discovered[dirURL]; exists {
						continue
					}
					discovered[dirURL] = struct{}{}
					queue = append(queue, rapidDirectory{dirURL, result.directory.depth + 1})
				}
			}
			reportRapidScanProgress(report, processed, len(discovered), result.directory.url)
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(zipSet) == 0 {
		return nil, fmt.Errorf("не найдено ни одного zip-плагина по адресу %s", startURL)
	}
	zips := make([]string, 0, len(zipSet))
	for zipURL := range zipSet {
		zips = append(zips, zipURL)
	}
	sort.Strings(zips)
	return zips, nil
}

// Общий лимит запросов и пауза при 429 не дают параллельным загрузкам перегружать Rapid.
type rapidRequestGate struct {
	mu       sync.Mutex
	next     time.Time
	cooldown time.Time
	interval time.Duration
}

func (g *rapidRequestGate) wait(ctx context.Context) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		g.mu.Lock()
		delay := time.Until(g.next)
		if delay <= 0 {
			g.next = time.Now().Add(g.interval)
			g.mu.Unlock()
			return nil
		}
		g.mu.Unlock()
		if err := waitRapidRequest(ctx, delay); err != nil {
			return err
		}
	}
}

func (g *rapidRequestGate) pause(delay time.Duration) {
	g.mu.Lock()
	defer g.mu.Unlock()
	now := time.Now()
	if !now.Before(g.cooldown) {
		g.interval = min(g.interval*2, 3*time.Second)
	}
	if until := now.Add(delay); until.After(g.cooldown) {
		g.cooldown = until
	}
	if g.cooldown.After(g.next) {
		g.next = g.cooldown
	}
}

func waitRapidRequest(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func readRapidDirectory(ctx context.Context, client *http.Client, gate *rapidRequestGate, pageURL string) ([]string, []string, error) {
	for attempt := range rapidRequestAttempts {
		if err := gate.wait(ctx); err != nil {
			return nil, nil, err
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, pageURL, nil)
		if err != nil {
			return nil, nil, err
		}
		req.Header.Set("User-Agent", "goMH/1.0")
		resp, err := client.Do(req)
		if err != nil {
			return nil, nil, err
		}
		if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == http.StatusServiceUnavailable {
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
			if attempt+1 == rapidRequestAttempts {
				return nil, nil, fmt.Errorf("HTTP %s после %d попыток", resp.Status, rapidRequestAttempts)
			}
			delay := rapidRetryDelay(resp.Header.Get("Retry-After"), attempt)
			slog.Warn("Источник Rapid временно недоступен, повторяем запрос каталога", "url", pageURL, "status", resp.StatusCode, "attempt", attempt+1, "delay", delay)
			gate.pause(delay)
			continue
		}
		if resp.StatusCode != http.StatusOK {
			_ = resp.Body.Close()
			return nil, nil, fmt.Errorf("HTTP %s", resp.Status)
		}
		dirs, zips, err := parseRapidDirectoryListing(pageURL, resp.Body)
		_ = resp.Body.Close()
		return dirs, zips, err
	}
	return nil, nil, fmt.Errorf("исчерпаны попытки чтения каталога %s", pageURL)
}

func rapidRetryDelay(value string, attempt int) time.Duration {
	if seconds, err := strconv.Atoi(value); err == nil && seconds >= 0 {
		return time.Duration(min(seconds, 30)) * time.Second
	}
	if until, err := http.ParseTime(value); err == nil {
		return min(max(time.Until(until), 0), 30*time.Second)
	}
	return min(5*time.Second<<attempt, 30*time.Second)
}

func canonicalRapidURL(value string) (*url.URL, error) {
	u, err := url.Parse(value)
	if err != nil {
		return nil, err
	}
	if (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.User != nil {
		return nil, fmt.Errorf("неверный URL каталога плагинов: %s", value)
	}
	if strings.Contains(u.Path, "\\") {
		return nil, fmt.Errorf("неверный путь каталога плагинов: %s", value)
	}
	trailingSlash := strings.HasSuffix(u.Path, "/")
	u.Path = path.Clean("/" + strings.TrimLeft(u.Path, "/"))
	if trailingSlash && u.Path != "/" {
		u.Path += "/"
	}
	u.Scheme = strings.ToLower(u.Scheme)
	u.Host = strings.ToLower(u.Host)
	u.RawPath, u.RawQuery, u.Fragment = "", "", ""
	u.ForceQuery, u.RawFragment = false, ""
	return u, nil
}

func sameRapidOrigin(a, b *url.URL) bool {
	return a.Scheme == b.Scheme && a.Host == b.Host
}

func parseRapidDirectoryListing(pageURL string, body io.Reader) ([]string, []string, error) {
	base, err := canonicalRapidURL(pageURL)
	if err != nil {
		return nil, nil, err
	}
	base.Path = strings.TrimRight(base.Path, "/") + "/"
	dirSet, zipSet := map[string]struct{}{}, map[string]struct{}{}
	tokenizer := html.NewTokenizer(body)
	for {
		switch tokenizer.Next() {
		case html.ErrorToken:
			if err := tokenizer.Err(); err != io.EOF {
				return nil, nil, err
			}
			dirs, zips := make([]string, 0, len(dirSet)), make([]string, 0, len(zipSet))
			for dir := range dirSet {
				dirs = append(dirs, dir)
			}
			for zip := range zipSet {
				zips = append(zips, zip)
			}
			sort.Strings(dirs)
			sort.Strings(zips)
			return dirs, zips, nil
		case html.StartTagToken, html.SelfClosingTagToken:
			token := tokenizer.Token()
			if token.Data != "a" {
				continue
			}
			for _, attr := range token.Attr {
				if attr.Key != "href" {
					continue
				}
				href, err := url.Parse(strings.TrimSpace(attr.Val))
				if err != nil || href.Path == "" || href.RawQuery != "" || href.ForceQuery || href.Fragment != "" {
					break
				}
				resolved, err := canonicalRapidURL(base.ResolveReference(href).String())
				if err != nil || !sameRapidOrigin(base, resolved) || !strings.HasPrefix(resolved.Path, base.Path) {
					break
				}
				child := strings.TrimPrefix(resolved.Path, base.Path)
				name := strings.TrimSuffix(child, "/")
				// Только непосредственные дети: ../, корень и навигация не являются содержимым каталога.
				if name == "" || strings.Contains(name, "/") {
					break
				}
				if strings.HasSuffix(child, "/") {
					dirSet[resolved.String()] = struct{}{}
				} else if strings.EqualFold(path.Ext(name), ".zip") {
					zipSet[resolved.String()] = struct{}{}
				}
				break
			}
		}
	}
}
