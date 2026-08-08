package networkdiag

import (
	"goMH/core"
	"net/netip"
	"reflect"
	"testing"
)

func TestParseDeviceIPv4(t *testing.T) {
	for _, valid := range []string{"192.168.0.1", "10.20.30.254", "172.16.1.100"} {
		if _, err := parseDeviceIPv4(valid); err != nil {
			t.Errorf("parseDeviceIPv4(%q): %v", valid, err)
		}
	}
	for _, invalid := range []string{"", "not-an-ip", "::1", "0.0.0.0", "127.0.0.1", "224.0.0.1", "169.254.1.2", "192.168.1.0", "192.168.1.255"} {
		if _, err := parseDeviceIPv4(invalid); err == nil {
			t.Errorf("parseDeviceIPv4(%q) unexpectedly succeeded", invalid)
		}
	}
}

func TestCandidateAddressesFollowDevicePlusOneThenWrap(t *testing.T) {
	target := netip.MustParseAddr("192.168.0.252")
	candidates, err := candidateAddresses(target, netip.Addr{}, []netip.Addr{
		netip.MustParseAddr("192.168.0.253"),
		netip.MustParseAddr("192.168.0.1"),
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []netip.Addr{
		netip.MustParseAddr("192.168.0.254"),
		netip.MustParseAddr("192.168.0.2"),
		netip.MustParseAddr("192.168.0.3"),
	}
	if !reflect.DeepEqual(candidates[:3], want) {
		t.Fatalf("first candidates = %v, want %v", candidates[:3], want)
	}
}

func TestCandidateAddressesPutEditablePreferredFirst(t *testing.T) {
	target := netip.MustParseAddr("192.168.10.100")
	preferred := netip.MustParseAddr("192.168.10.40")
	candidates, err := candidateAddresses(target, preferred, nil)
	if err != nil {
		t.Fatal(err)
	}
	if candidates[0] != preferred || candidates[1] != netip.MustParseAddr("192.168.10.101") {
		t.Fatalf("candidate order = %v", candidates[:2])
	}
}

func TestCandidateAddressesRejectPreferredOutsideDevice24(t *testing.T) {
	_, err := candidateAddresses(
		netip.MustParseAddr("192.168.0.100"),
		netip.MustParseAddr("192.168.1.101"),
		nil,
	)
	if err == nil {
		t.Fatal("expected preferred address validation error")
	}
}

func TestDeviceSubnetAlwaysUses24(t *testing.T) {
	prefix := deviceSubnet(netip.MustParseAddr("192.168.77.240"))
	if got, want := prefix.String(), "192.168.77.0/24"; got != want {
		t.Fatalf("deviceSubnet = %s, want %s", got, want)
	}
}

func TestHasExistingTargetSubnetUsesActualLocalPrefix(t *testing.T) {
	interfaces := []core.NetworkInterfaceInfo{{
		Up: true,
		IPv4Addresses: []core.NetworkIPv4Address{{
			Address:      netip.MustParseAddr("192.168.0.25"),
			PrefixLength: 24,
		}},
	}}
	if !hasExistingTargetSubnet(interfaces, netip.MustParseAddr("192.168.0.100")) {
		t.Fatal("expected existing /24 to contain target")
	}
	if hasExistingTargetSubnet(interfaces, netip.MustParseAddr("192.168.1.100")) {
		t.Fatal("unexpected target subnet match")
	}
}
