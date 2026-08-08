//go:build amd64

package winutils

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

const expectedMibUnicastIPAddressRowAMD64 = 80

var (
	_ [expectedMibUnicastIPAddressRowAMD64 - unsafe.Sizeof(windows.MibUnicastIpAddressRow{})]byte
	_ [unsafe.Sizeof(windows.MibUnicastIpAddressRow{}) - expectedMibUnicastIPAddressRowAMD64]byte
	_ [28 - unsafe.Sizeof(windows.RawSockaddrInet{})]byte
	_ [unsafe.Sizeof(windows.RawSockaddrInet{}) - 28]byte
)
