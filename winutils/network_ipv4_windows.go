package winutils

import (
	"errors"
	"fmt"
	"goMH/core"
	"math"
	"net"
	"net/netip"
	"os"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	iphlpapi                            = windows.NewLazySystemDLL("iphlpapi.dll")
	initializeUnicastIPAddressEntryProc = iphlpapi.NewProc("InitializeUnicastIpAddressEntry")
	createUnicastIPAddressEntryProc     = iphlpapi.NewProc("CreateUnicastIpAddressEntry")
	deleteUnicastIPAddressEntryProc     = iphlpapi.NewProc("DeleteUnicastIpAddressEntry")
	setUnicastIPAddressEntryProc        = iphlpapi.NewProc("SetUnicastIpAddressEntry")
	getBestRoute2Proc                   = iphlpapi.NewProc("GetBestRoute2")
)

const ipAdapterDHCPEnabled = 0x00000004

func ListNetworkInterfaces() ([]core.NetworkInterfaceInfo, error) {
	const flags = windows.GAA_FLAG_INCLUDE_PREFIX | windows.GAA_FLAG_INCLUDE_GATEWAYS

	var size uint32
	err := windows.GetAdaptersAddresses(windows.AF_INET, flags, 0, nil, &size)
	if !errors.Is(err, windows.ERROR_BUFFER_OVERFLOW) {
		return nil, fmt.Errorf("GetAdaptersAddresses(size): %w", err)
	}

	buffer := make([]byte, size)
	first := (*windows.IpAdapterAddresses)(unsafe.Pointer(&buffer[0]))
	if err := windows.GetAdaptersAddresses(windows.AF_INET, flags, 0, first, &size); err != nil {
		return nil, fmt.Errorf("GetAdaptersAddresses: %w", err)
	}

	interfaces := make([]core.NetworkInterfaceInfo, 0)
	for adapter := first; adapter != nil; adapter = adapter.Next {
		info := core.NetworkInterfaceInfo{
			LUID:        adapter.Luid,
			GUID:        adapter.NetworkGuid.String(),
			Index:       adapter.IfIndex,
			Alias:       windows.UTF16PtrToString(adapter.FriendlyName),
			Description: windows.UTF16PtrToString(adapter.Description),
			Type:        adapter.IfType,
			Up:          adapter.OperStatus == windows.IfOperStatusUp,
			DHCPEnabled: adapter.Flags&ipAdapterDHCPEnabled != 0,
		}
		if adapter.PhysicalAddressLength > 0 {
			info.MAC = net.HardwareAddr(adapter.PhysicalAddress[:adapter.PhysicalAddressLength]).String()
		}
		info.Virtual = isLikelyVirtualInterface(info)
		for address := adapter.FirstUnicastAddress; address != nil; address = address.Next {
			ip, ok := socketAddressToAddr(address.Address)
			if !ok || !ip.Is4() {
				continue
			}
			info.IPv4Addresses = append(info.IPv4Addresses, core.NetworkIPv4Address{
				Address:           ip,
				PrefixLength:      address.OnLinkPrefixLength,
				PrefixOrigin:      uint32(address.PrefixOrigin),
				SuffixOrigin:      uint32(address.SuffixOrigin),
				DADState:          core.IPAddressDADState(address.DadState),
				ValidLifetime:     secondsDuration(address.ValidLifetime),
				PreferredLifetime: secondsDuration(address.PreferredLifetime),
			})
		}
		for gateway := adapter.FirstGatewayAddress; gateway != nil; gateway = gateway.Next {
			if ip, ok := socketAddressToAddr(gateway.Address); ok && ip.Is4() {
				info.DefaultGateways = append(info.DefaultGateways, ip)
			}
		}
		for dns := adapter.FirstDnsServerAddress; dns != nil; dns = dns.Next {
			if ip, ok := socketAddressToAddr(dns.Address); ok && ip.Is4() {
				info.DNSServers = append(info.DNSServers, ip)
			}
		}
		interfaces = append(interfaces, info)
	}
	return interfaces, nil
}

func CreateTemporaryIPv4(req core.TemporaryIPv4Request) (core.TemporaryIPv4Info, error) {
	if !req.Address.Is4() || req.PrefixLength > 32 {
		return core.TemporaryIPv4Info{}, fmt.Errorf("некорректный IPv4/prefix: %s/%d", req.Address, req.PrefixLength)
	}
	row := windows.MibUnicastIpAddressRow{}
	initializeUnicastIPAddressEntryProc.Call(uintptr(unsafe.Pointer(&row)))
	setRowAddress(&row, req.Address)
	row.InterfaceLuid = req.InterfaceLUID
	row.InterfaceIndex = req.InterfaceIndex
	row.OnLinkPrefixLength = req.PrefixLength
	row.SkipAsSource = 0
	row.ValidLifetime = durationSeconds(req.ValidLifetime)
	row.PreferredLifetime = durationSeconds(req.PreferredLifetime)
	// DadState намеренно остаётся Invalid: Windows выполняет обычный, не optimistic DAD.
	if err := callNetIO(createUnicastIPAddressEntryProc, &row); err != nil {
		return core.TemporaryIPv4Info{}, fmt.Errorf("CreateUnicastIpAddressEntry(%s, LUID=%d, windows_code=%d): %w", req.Address, req.InterfaceLUID, windowsErrorCode(err), err)
	}
	return GetTemporaryIPv4(req.InterfaceLUID, req.InterfaceIndex, req.Address)
}

func GetTemporaryIPv4(interfaceLUID uint64, interfaceIndex uint32, address netip.Addr) (core.TemporaryIPv4Info, error) {
	if !address.Is4() {
		return core.TemporaryIPv4Info{}, fmt.Errorf("ожидался IPv4: %s", address)
	}
	row := windows.MibUnicastIpAddressRow{InterfaceLuid: interfaceLUID, InterfaceIndex: interfaceIndex}
	setRowAddress(&row, address)
	if err := windows.GetUnicastIpAddressEntry(&row); err != nil {
		if errors.Is(err, windows.ERROR_NOT_FOUND) {
			return core.TemporaryIPv4Info{}, fmt.Errorf("GetUnicastIpAddressEntry(%s): %w", address, os.ErrNotExist)
		}
		return core.TemporaryIPv4Info{}, fmt.Errorf("GetUnicastIpAddressEntry(%s, windows_code=%d): %w", address, windowsErrorCode(err), err)
	}
	return rowToTemporaryInfo(&row), nil
}

func SetTemporaryIPv4Lifetimes(info core.TemporaryIPv4Info, valid, preferred time.Duration) (core.TemporaryIPv4Info, error) {
	row := windows.MibUnicastIpAddressRow{InterfaceLuid: info.InterfaceLUID, InterfaceIndex: info.InterfaceIndex}
	setRowAddress(&row, info.Address)
	if err := windows.GetUnicastIpAddressEntry(&row); err != nil {
		return core.TemporaryIPv4Info{}, fmt.Errorf("GetUnicastIpAddressEntry перед Set: %w", err)
	}
	if info.CreationTimestamp != 0 && filetimeValue(row.CreationTimeStamp) != info.CreationTimestamp {
		return core.TemporaryIPv4Info{}, errors.New("NetIO-запись больше не принадлежит ожидаемой transaction")
	}
	row.ValidLifetime = durationSeconds(valid)
	row.PreferredLifetime = durationSeconds(preferred)
	if err := callNetIO(setUnicastIPAddressEntryProc, &row); err != nil {
		return core.TemporaryIPv4Info{}, fmt.Errorf("SetUnicastIpAddressEntry(%s, windows_code=%d): %w", info.Address, windowsErrorCode(err), err)
	}
	return GetTemporaryIPv4(info.InterfaceLUID, info.InterfaceIndex, info.Address)
}

func DeleteTemporaryIPv4(info core.TemporaryIPv4Info) error {
	current, err := GetTemporaryIPv4(info.InterfaceLUID, info.InterfaceIndex, info.Address)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if current.PrefixLength != info.PrefixLength ||
		(info.CreationTimestamp != 0 && current.CreationTimestamp != info.CreationTimestamp) {
		return fmt.Errorf("отказ удаления %s: текущая NetIO-запись не совпадает с transaction", info.Address)
	}
	row := windows.MibUnicastIpAddressRow{InterfaceLuid: current.InterfaceLUID, InterfaceIndex: current.InterfaceIndex}
	setRowAddress(&row, current.Address)
	if err := callNetIO(deleteUnicastIPAddressEntryProc, &row); err != nil && !errors.Is(err, windows.ERROR_NOT_FOUND) {
		return fmt.Errorf("DeleteUnicastIpAddressEntry(%s, LUID=%d, windows_code=%d): %w", info.Address, info.InterfaceLUID, windowsErrorCode(err), err)
	}
	return nil
}

func GetBestRouteIPv4(interfaceLUID uint64, interfaceIndex uint32, source, destination netip.Addr) (core.IPv4RouteInfo, error) {
	if !source.Is4() || !destination.Is4() {
		return core.IPv4RouteInfo{}, errors.New("GetBestRoute2 поддерживает здесь только IPv4")
	}
	sourceSockaddr := rawSockaddr(source)
	destinationSockaddr := rawSockaddr(destination)
	var route windows.MibIpForwardRow2
	var bestSource windows.RawSockaddrInet
	var luidPtr unsafe.Pointer
	if interfaceLUID != 0 {
		luidPtr = unsafe.Pointer(&interfaceLUID)
	}
	r1, _, _ := getBestRoute2Proc.Call(
		uintptr(luidPtr),
		uintptr(interfaceIndex),
		uintptr(unsafe.Pointer(&sourceSockaddr)),
		uintptr(unsafe.Pointer(&destinationSockaddr)),
		0,
		uintptr(unsafe.Pointer(&route)),
		uintptr(unsafe.Pointer(&bestSource)),
	)
	if r1 != 0 {
		err := syscall.Errno(r1)
		return core.IPv4RouteInfo{}, fmt.Errorf("GetBestRoute2(%s, windows_code=%d): %w", destination, windowsErrorCode(err), err)
	}
	return core.IPv4RouteInfo{
		InterfaceLUID:  route.InterfaceLuid,
		InterfaceIndex: route.InterfaceIndex,
		SourceAddress:  addrFromRawSockaddr(bestSource),
		NextHop:        addrFromRawSockaddr(route.NextHop),
		Metric:         route.Metric,
	}, nil
}

func callNetIO(proc *windows.LazyProc, row *windows.MibUnicastIpAddressRow) error {
	r1, _, _ := proc.Call(uintptr(unsafe.Pointer(row)))
	if r1 == 0 {
		return nil
	}
	return syscall.Errno(r1)
}

func windowsErrorCode(err error) uint32 {
	var errno syscall.Errno
	if errors.As(err, &errno) {
		return uint32(errno)
	}
	return 0
}

func setRowAddress(row *windows.MibUnicastIpAddressRow, address netip.Addr) {
	raw := (*windows.RawSockaddrInet4)(unsafe.Pointer(&row.Address))
	raw.Family = windows.AF_INET
	raw.Addr = address.As4()
}

func rawSockaddr(address netip.Addr) windows.RawSockaddrInet {
	var raw windows.RawSockaddrInet
	inet4 := (*windows.RawSockaddrInet4)(unsafe.Pointer(&raw))
	inet4.Family = windows.AF_INET
	inet4.Addr = address.As4()
	return raw
}

func addrFromRawSockaddr(raw windows.RawSockaddrInet) netip.Addr {
	inet4 := (*windows.RawSockaddrInet4)(unsafe.Pointer(&raw))
	if inet4.Family != windows.AF_INET {
		return netip.Addr{}
	}
	return netip.AddrFrom4(inet4.Addr)
}

func socketAddressToAddr(address windows.SocketAddress) (netip.Addr, bool) {
	ip := address.IP()
	if ip == nil {
		return netip.Addr{}, false
	}
	return netip.AddrFromSlice(ip)
}

func rowToTemporaryInfo(row *windows.MibUnicastIpAddressRow) core.TemporaryIPv4Info {
	return core.TemporaryIPv4Info{
		InterfaceLUID:     row.InterfaceLuid,
		InterfaceIndex:    row.InterfaceIndex,
		Address:           addrFromRow(row),
		PrefixLength:      row.OnLinkPrefixLength,
		DADState:          core.IPAddressDADState(row.DadState),
		ValidLifetime:     secondsDuration(row.ValidLifetime),
		PreferredLifetime: secondsDuration(row.PreferredLifetime),
		CreationTimestamp: filetimeValue(row.CreationTimeStamp),
	}
}

func addrFromRow(row *windows.MibUnicastIpAddressRow) netip.Addr {
	inet4 := (*windows.RawSockaddrInet4)(unsafe.Pointer(&row.Address))
	if inet4.Family != windows.AF_INET {
		return netip.Addr{}
	}
	return netip.AddrFrom4(inet4.Addr)
}

func durationSeconds(duration time.Duration) uint32 {
	seconds := duration / time.Second
	if seconds <= 0 {
		return 1
	}
	return uint32(min(seconds, time.Duration(math.MaxUint32-1)))
}

func secondsDuration(seconds uint32) time.Duration {
	if seconds == math.MaxUint32 {
		return time.Duration(math.MaxInt64)
	}
	return time.Duration(seconds) * time.Second
}

func filetimeValue(filetime windows.Filetime) int64 {
	return int64(uint64(filetime.HighDateTime)<<32 | uint64(filetime.LowDateTime))
}

func isLikelyVirtualInterface(info core.NetworkInterfaceInfo) bool {
	if info.Type == windows.IF_TYPE_TUNNEL || info.Type == windows.IF_TYPE_PPP || info.Type == windows.IF_TYPE_SOFTWARE_LOOPBACK {
		return true
	}
	text := strings.ToLower(info.Alias + " " + info.Description)
	for _, marker := range []string{"vpn", "virtual", "hyper-v", "vmware", "tap", "tunnel", "loopback"} {
		if strings.Contains(text, marker) {
			return true
		}
	}
	return false
}
