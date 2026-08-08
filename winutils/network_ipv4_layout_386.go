//go:build 386

package winutils

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

const expectedMibUnicastIPAddressRow386 = 76

var (
	_ [expectedMibUnicastIPAddressRow386 - unsafe.Sizeof(windows.MibUnicastIpAddressRow{})]byte
	_ [unsafe.Sizeof(windows.MibUnicastIpAddressRow{}) - expectedMibUnicastIPAddressRow386]byte
	_ [28 - unsafe.Sizeof(windows.RawSockaddrInet{})]byte
	_ [unsafe.Sizeof(windows.RawSockaddrInet{}) - 28]byte
)
