package winutils

import (
	"net/netip"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

func TestIPv4SockaddrRoundTripPreservesNetworkByteOrder(t *testing.T) {
	want := netip.MustParseAddr("192.168.17.231")
	raw := rawSockaddr(want)
	if got := addrFromRawSockaddr(raw); got != want {
		t.Fatalf("sockaddr round trip = %s, want %s", got, want)
	}

	row := windows.MibUnicastIpAddressRow{}
	setRowAddress(&row, want)
	if got := addrFromRow(&row); got != want {
		t.Fatalf("row address = %s, want %s", got, want)
	}
}

func TestNetIOStructLayoutAMD64(t *testing.T) {
	if unsafe.Sizeof(uintptr(0)) != 8 {
		t.Skip("amd64 layout assertion")
	}
	if got := unsafe.Sizeof(windows.RawSockaddrInet{}); got != 28 {
		t.Fatalf("SOCKADDR_INET size = %d, want 28", got)
	}
	if got := unsafe.Sizeof(windows.MibUnicastIpAddressRow{}); got != 80 {
		t.Fatalf("MIB_UNICASTIPADDRESS_ROW size = %d, want 80", got)
	}
}

func TestDurationSecondsIsFinite(t *testing.T) {
	if got := durationSeconds(30 * time.Minute); got != 1800 {
		t.Fatalf("durationSeconds = %d, want 1800", got)
	}
	if got := durationSeconds(0); got != 1 {
		t.Fatalf("zero duration = %d, want finite minimum 1", got)
	}
}
