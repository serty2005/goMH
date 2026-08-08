package winutils

import (
	"strings"
	"testing"
	"time"
)

func TestBuildOneShotTaskXMLUsesSystemTimeTriggerAndEscapesArguments(t *testing.T) {
	runAt := time.Date(2026, time.August, 9, 12, 34, 56, 0, time.Local)
	xml := buildOneShotTaskXML(
		`C:\MH & tools\goMH.exe`,
		[]string{"-internal-network-temp-cleanup", `C:\MH state\tx & one.json`},
		`C:\MH & tools`,
		runAt,
	)
	for _, expected := range []string{
		"<TimeTrigger>",
		"<StartBoundary>2026-08-09T12:34:56</StartBoundary>",
		"<UserId>S-1-5-18</UserId>",
		"<StartWhenAvailable>true</StartWhenAvailable>",
		`-internal-network-temp-cleanup &#34;C:\MH state\tx &amp; one.json&#34;`,
		`C:\MH &amp; tools\goMH.exe`,
	} {
		if !strings.Contains(xml, expected) {
			t.Errorf("task XML does not contain %q\n%s", expected, xml)
		}
	}
	if strings.Contains(xml, "<LogonType>") {
		t.Fatalf("SYSTEM principal must not use XML LogonType: %s", xml)
	}
}

func TestOpenURLRejectsNonHTTPProtocols(t *testing.T) {
	if err := OpenURL("file:///C:/Windows/win.ini"); err == nil {
		t.Fatal("expected non-HTTP URL rejection")
	}
}
