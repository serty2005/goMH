package networkdiag

import (
	"errors"
	"fmt"
	"net/netip"
	"slices"
)

const temporaryPrefixLength = 24

func parseDeviceIPv4(raw string) (netip.Addr, error) {
	address, err := netip.ParseAddr(raw)
	if err != nil || !address.Is4() {
		return netip.Addr{}, fmt.Errorf("некорректный IPv4 устройства: %q", raw)
	}
	if address.IsUnspecified() || address.IsLoopback() || address.IsMulticast() || address.IsLinkLocalUnicast() {
		return netip.Addr{}, fmt.Errorf("адрес %s нельзя использовать как адрес локального устройства", address)
	}
	octets := address.As4()
	if octets[3] == 0 || octets[3] == 255 {
		return netip.Addr{}, fmt.Errorf("адрес %s является адресом сети или broadcast для /24", address)
	}
	return address, nil
}

func deviceSubnet(address netip.Addr) netip.Prefix {
	return netip.PrefixFrom(address, temporaryPrefixLength).Masked()
}

func candidateAddresses(target netip.Addr, preferred netip.Addr, excluded []netip.Addr) ([]netip.Addr, error) {
	if _, err := parseDeviceIPv4(target.String()); err != nil {
		return nil, err
	}
	subnet := deviceSubnet(target)
	if preferred.IsValid() && (!preferred.Is4() || !subnet.Contains(preferred) || preferred == target) {
		return nil, errors.New("временный адрес должен находиться в /24 устройства и отличаться от адреса устройства")
	}

	excludedSet := make(map[netip.Addr]struct{}, len(excluded)+1)
	excludedSet[target] = struct{}{}
	for _, address := range excluded {
		excludedSet[address] = struct{}{}
	}

	result := make([]netip.Addr, 0, 253)
	appendCandidate := func(address netip.Addr) {
		if _, excluded := excludedSet[address]; excluded || slices.Contains(result, address) {
			return
		}
		result = append(result, address)
	}
	if preferred.IsValid() {
		appendCandidate(preferred)
	}

	octets := target.As4()
	for host := int(octets[3]) + 1; host <= 254; host++ {
		candidateOctets := octets
		candidateOctets[3] = byte(host)
		appendCandidate(netip.AddrFrom4(candidateOctets))
	}
	for host := 1; host < int(octets[3]); host++ {
		candidateOctets := octets
		candidateOctets[3] = byte(host)
		appendCandidate(netip.AddrFrom4(candidateOctets))
	}
	if len(result) == 0 {
		return nil, errors.New("в подсети устройства нет доступных кандидатов временного IPv4")
	}
	return result, nil
}

func defaultTemporaryAddress(target netip.Addr, excluded []netip.Addr) (netip.Addr, error) {
	candidates, err := candidateAddresses(target, netip.Addr{}, excluded)
	if err != nil {
		return netip.Addr{}, err
	}
	return candidates[0], nil
}
