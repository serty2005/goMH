package distro

import (
	"os"
	"reflect"
	"testing"
)

func TestExtractSyrveVersions(t *testing.T) {
	names := []string{
		"9.4.6046.0",
		"/release_syrve/9.4.6046.0",
		"10.0.1.0",
		"9.10.1.0",
		"9.4.6046",
		"readme.txt",
		"/release_syrve/not-a-version",
	}

	got := extractSyrveVersions(names)
	want := []string{"10.0.1.0", "9.10.1.0", "9.4.6046.0"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("extractSyrveVersions() = %#v, want %#v", got, want)
	}
}

func TestFetchSyrveVersionsLive(t *testing.T) {
	if os.Getenv("GOMH_LIVE_FTPS") != "1" {
		t.Skip("set GOMH_LIVE_FTPS=1 to run live Syrve FTPS check")
	}

	versions, err := (&syrveHandler{}).fetchVersions()
	if err != nil {
		t.Fatalf("fetchVersions() failed: %v", err)
	}
	if len(versions) == 0 {
		t.Fatal("fetchVersions() returned no versions")
	}
	t.Logf("loaded %d Syrve versions, latest: %s", len(versions), versions[0])
}
