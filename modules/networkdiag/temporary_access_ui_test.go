package networkdiag

import (
	"goMH/core"
	"net/netip"
	"strings"
	"testing"
	"time"
)

func TestTemporarySessionMenuContainsOnlyHTTPBrowserAction(t *testing.T) {
	items := temporarySessionMenuItems()
	if len(items) != 4 || items[0].Title != "Открыть HTTP" {
		t.Fatalf("unexpected session menu: %#v", items)
	}
	for _, item := range items {
		if strings.Contains(strings.ToUpper(item.Title), "HTTPS") {
			t.Fatalf("HTTPS action must not be shown: %q", item.Title)
		}
	}
}

func TestTemporarySessionSubtitleShowsExactExpiryInsteadOfFrozenCountdown(t *testing.T) {
	expiresAt := time.Date(2026, time.August, 9, 1, 27, 52, 0, time.Local)
	entry := &core.TemporaryIPv4Info{DADState: core.IPAddressDADPreferred}
	subtitle := temporarySessionSubtitle(&temporaryAccessTransaction{
		TargetIP:       netip.MustParseAddr("192.168.100.123"),
		TemporaryIP:    netip.MustParseAddr("192.168.100.124"),
		InterfaceAlias: "Ethernet",
		ExpiresAt:      expiresAt,
		Entry:          entry,
	}, ReachabilityResult{OpenTCPPorts: []int{80}})
	if !strings.Contains(subtitle, "09.08.2026 01:27:52") {
		t.Fatalf("subtitle does not show exact expiry: %q", subtitle)
	}
	if strings.Contains(subtitle, "осталось") {
		t.Fatalf("subtitle still contains frozen countdown: %q", subtitle)
	}
}
