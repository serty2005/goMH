package distro

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
)

const iikoPatchesListing = `<table id="list"><thead><tr><th><a href="?C=N&amp;O=A">File Name</a></th></tr></thead>
<tbody><tr><td class="link"><a href="../">Parent directory/</a></td></tr>
<tr><td class="link"><a href="aggregated-patch-9.5.7018.0%28build%2022716%29.iiko.txt">patch note</a></td></tr>
<tr><td class="link"><a href="aggregated-patch-9.5.7018.0%28build%2022716%29.iiko.zip">patch archive</a></td></tr>
<tr><td class="link"><a href="aggregated-patch-9.5.7018.0%28build%2022721%29.iiko.zip">patch archive</a></td></tr>
<tr><td class="link"><a href="aggregated-patch-9.5.7018.0%28build%2022751%29.iiko.zip">patch archive</a></td></tr>
<tr><td class="link"><a href="aggregated-patch-9.5.7018.0%28build%2022783%29.iiko.zip">patch archive</a></td></tr>
<tr><td class="link"><a href="aggregated-patch-9.5.7018.0%28build%2022791%29.iiko.zip">patch archive</a></td></tr>
<tr><td class="link"><a href="aggregated-patch-9.5.7018.0%28build%2022838%29.iiko.zip">patch archive</a></td></tr>
<tr><td class="link"><a href="aggregated-patch-9.5.7018.0%28build%2022857%29.iiko.zip">patch archive</a></td></tr>
<tr><td class="link"><a href="aggregated-patch-9.5.7018.0%28build%2022874%29.iiko.txt">patch note</a></td></tr>
<tr><td class="link"><a href="aggregated-patch-9.5.7018.0%28build%2022874%29.iiko.zip">patch archive</a></td></tr>
</tbody></table>`

func TestParsePatchesHTMLIikoFancyIndex(t *testing.T) {
	t.Parallel()

	baseURL := "https://rapid.iiko.ru/versionPatches/9.5.7018.0/"
	patches, folderOnly := parsePatchesHTML(baseURL, iikoPatchesListing)
	wantBuilds := []int{22874, 22857, 22838, 22791, 22783, 22751, 22721, 22716}
	if len(patches) != len(wantBuilds) {
		t.Fatalf("got %d patches, want %d", len(patches), len(wantBuilds))
	}
	if folderOnly {
		t.Fatal("listing with archives reported as folder-only")
	}
	for i, build := range wantBuilds {
		patch := patches[i]
		if patch.BuildNumber != build {
			t.Errorf("patch %d: got build %d, want %d", i, patch.BuildNumber, build)
		}
		wantName := fmt.Sprintf("aggregated-patch-9.5.7018.0(build %d).iiko.zip", build)
		if patch.Description != wantName {
			t.Errorf("patch %d: got description %q, want %q", i, patch.Description, wantName)
		}
		wantShortName := fmt.Sprintf("aggregated-%d", build)
		if patch.ShortName != wantShortName {
			t.Errorf("patch %d: got short name %q, want %q", i, patch.ShortName, wantShortName)
		}
		wantURL := baseURL + fmt.Sprintf("aggregated-patch-9.5.7018.0%%28build%%20%d%%29.iiko.zip", build)
		if patch.FullURL != wantURL {
			t.Errorf("patch %d: got URL %q, want %q", i, patch.FullURL, wantURL)
		}
	}
}

func TestFindPatchesIikoFancyIndexRedirect(t *testing.T) {
	t.Parallel()
	for _, version := range []string{"9.5.7018.0", "9.5.7018"} {
		t.Run(version, func(t *testing.T) {
			t.Parallel()
			var fallbackRequests, noteRequests, archiveRequests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.URL.Path == "/patches/9.5.7018.0/":
					http.Redirect(w, r, "/versionPatches/9.5.7018.0/", http.StatusMovedPermanently)
				case r.URL.Path == "/versionPatches/9.5.7018.0/":
					_, _ = fmt.Fprint(w, iikoPatchesListing)
				case strings.HasSuffix(strings.TrimRight(r.URL.Path, "/"), "/Patches"):
					fallbackRequests.Add(1)
					http.NotFound(w, r)
				case strings.HasSuffix(r.URL.Path, ".zip"):
					archiveRequests.Add(1)
					http.NotFound(w, r)
				default:
					for _, build := range []int{22874, 22857, 22838, 22791} {
						wantPath := fmt.Sprintf("/versionPatches/9.5.7018.0/aggregated-patch-9.5.7018.0(build %d).iiko.txt", build)
						if r.URL.Path == wantPath {
							noteRequests.Add(1)
							_, _ = fmt.Fprintf(w, "Resto.CashServer.dll\n\nRMS-%d fixed: fix-%d\n", build, build)
							return
						}
					}
					t.Errorf("unexpected request: %s", r.URL.Path)
					http.NotFound(w, r)
				}
			}))
			defer server.Close()

			patches, err := FindPatches(server.URL+"/patches", version)
			if err != nil {
				t.Fatalf("FindPatches: %v", err)
			}
			wantBuilds := []int{22874, 22857, 22838, 22791}
			if len(patches) != len(wantBuilds) {
				t.Fatalf("got %d patches, want %d", len(patches), len(wantBuilds))
			}
			for i, build := range wantBuilds {
				if patches[i].BuildNumber != build {
					t.Errorf("patch %d: got build %d, want %d", i, patches[i].BuildNumber, build)
				}
				if !strings.Contains(patches[i].ChangeNote, fmt.Sprintf("RMS-%d fix-%d", build, build)) {
					t.Errorf("patch %d: unexpected change note %q", i, patches[i].ChangeNote)
				}
				if !strings.HasPrefix(patches[i].FullURL, server.URL+"/versionPatches/9.5.7018.0/") {
					t.Errorf("patch %d: URL did not use redirect target: %q", i, patches[i].FullURL)
				}
			}
			if got := fallbackRequests.Load(); got != 0 {
				t.Errorf("got %d fallback requests, want 0", got)
			}
			if got := noteRequests.Load(); got != 4 {
				t.Errorf("got %d note requests, want 4", got)
			}
			if got := archiveRequests.Load(); got != 0 {
				t.Errorf("got %d archive requests, want 0", got)
			}
		})
	}
}

func TestParsePatchesHTMLLinkFormats(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, body, filename, wantPath string
		build                          int
	}{
		{
			name:     "legacy literal closing parenthesis",
			body:     `<a href="front-build%20101).7z">archive</a>`,
			filename: "front-build 101).7z", wantPath: "/versions/9.5.7018.0/front-build 101).7z", build: 101,
		},
		{
			name:     "attributes before single quoted href and uppercase anchor",
			body:     `<A class='link' title='archive' HREF='front%28build%20102%29.zip'>archive</A>`,
			filename: "front(build 102).zip", wantPath: "/versions/9.5.7018.0/front(build 102).zip", build: 102,
		},
		{
			name:     "unquoted href",
			body:     `<a href=front%28build%20103%29.zip>archive</a>`,
			filename: "front(build 103).zip", wantPath: "/versions/9.5.7018.0/front(build 103).zip", build: 103,
		},
		{
			name:     "literal plus in path",
			body:     `<a href="front+extra-build%20104).zip">archive</a>`,
			filename: "front+extra-build 104).zip", wantPath: "/versions/9.5.7018.0/front+extra-build 104).zip", build: 104,
		},
		{
			name:     "root relative href",
			body:     `<a href="/downloads/front%28build%20105%29.zip">archive</a>`,
			filename: "front(build 105).zip", wantPath: "/downloads/front(build 105).zip", build: 105,
		},
		{
			name:     "absolute href",
			body:     `<a href="https://example.com/downloads/front%28build%20106%29.zip">archive</a>`,
			filename: "front(build 106).zip", wantPath: "/downloads/front(build 106).zip", build: 106,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			patches, folderOnly := parsePatchesHTML("https://example.com/versions/9.5.7018.0", tc.body)
			if len(patches) != 1 || folderOnly {
				t.Fatalf("got %d patches, folder-only=%v; want one archive", len(patches), folderOnly)
			}
			patch := patches[0]
			if patch.BuildNumber != tc.build || patch.Description != tc.filename {
				t.Errorf("got build %d, description %q; want %d, %q", patch.BuildNumber, patch.Description, tc.build, tc.filename)
			}
			parsedURL, err := url.Parse(patch.FullURL)
			if err != nil {
				t.Fatalf("parse patch URL %q: %v", patch.FullURL, err)
			}
			if parsedURL.Scheme != "https" || parsedURL.Host != "example.com" || parsedURL.Path != tc.wantPath {
				t.Errorf("unexpected patch URL %q, want path %q", patch.FullURL, tc.wantPath)
			}
		})
	}
}

func TestParsePatchesHTMLIgnoresInvalidArchives(t *testing.T) {
	t.Parallel()
	body := `<a href="../">parent</a>
<a href="?C=N&amp;O=A">sort</a>
<a href="front-build%20101).txt">note</a>
<a href="front.zip">no build</a>
<a href="front-build%20101).zip/">directory with archive name</a>
<a href="front-build%20101%ZZ).zip">invalid URL escape</a>
<a href="front-build%20999999999999999999999999999999999999).zip">overflow build</a>
<a href="Patches/">nested patches</a>`
	patches, folderOnly := parsePatchesHTML("https://example.com/versions/9.5.7018.0/", body)
	if len(patches) != 0 {
		t.Fatalf("got %d patches from invalid archive links, want 0", len(patches))
	}
	if !folderOnly {
		t.Fatal("listing with only a Patches folder should request fallback")
	}
}

func TestParsePatchesHTMLArchiveSuppressesFolderFallback(t *testing.T) {
	t.Parallel()
	body := `<a class="folder" href='Patches/'>nested patches</a>
<a title="archive" href='front%28build%20101%29.zip'>archive</a>`
	patches, folderOnly := parsePatchesHTML("https://example.com/versions/9.5.7018.0/", body)
	if len(patches) != 1 || folderOnly {
		t.Fatalf("got %d patches, folder-only=%v; want one archive without fallback", len(patches), folderOnly)
	}
}
