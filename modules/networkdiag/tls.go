package networkdiag

import (
	"fmt"
	"strings"

	"golang.org/x/sys/windows/registry"
)

// TLSItem описывает один параметр TLS с текущим состоянием.
type TLSItem struct {
	Key         string
	Label       string
	StatusText  string
	NeedsChange bool
}

// tlsFixLabel хранит читаемые имена ключей для экрана подтверждения.
var tlsFixLabel = map[string]string{
	"tls12":                "TLS 1.2 (Client + Server)",
	"tls13_server_disable": "TLS 1.3 Server (отключить для совместимости с iiko)",
	"winhttp":              "WinHTTP DefaultSecureProtocols = 0xA00 (TLS 1.1 + 1.2)",
	"dotnet":               ".NET SystemDefaultTlsVersions v4.x",
}

// regWrite описывает одну запись реестра, которую нужно установить.
type regWrite struct {
	path  string
	name  string
	value uint32
}

var tlsFixes = map[string][]regWrite{
	// TLS 1.2: явно включаем Client и Server
	"tls12": {
		{`SYSTEM\CurrentControlSet\Control\SecurityProviders\SCHANNEL\Protocols\TLS 1.2\Client`, "DisabledByDefault", 0},
		{`SYSTEM\CurrentControlSet\Control\SecurityProviders\SCHANNEL\Protocols\TLS 1.2\Client`, "Enabled", 1},
		{`SYSTEM\CurrentControlSet\Control\SecurityProviders\SCHANNEL\Protocols\TLS 1.2\Server`, "DisabledByDefault", 0},
		{`SYSTEM\CurrentControlSet\Control\SecurityProviders\SCHANNEL\Protocols\TLS 1.2\Server`, "Enabled", 1},
	},
	// TLS 1.3 Server: отключаем для совместимости с iiko
	"tls13_server_disable": {
		{`SYSTEM\CurrentControlSet\Control\SecurityProviders\SCHANNEL\Protocols\TLS 1.3\Server`, "DisabledByDefault", 1},
		{`SYSTEM\CurrentControlSet\Control\SecurityProviders\SCHANNEL\Protocols\TLS 1.3\Server`, "Enabled", 0},
	},
	// WinHTTP: 0xA00 = TLS 1.1 + TLS 1.2 (без SSL 3.0/TLS 1.0).
	// Не конфликтует с TLS 1.3 — Windows 10 1903+ управляет им отдельным стеком,
	// независимо от DefaultSecureProtocols.
	"winhttp": {
		{`SOFTWARE\Microsoft\Windows\CurrentVersion\Internet Settings\WinHttp`, "DefaultSecureProtocols", 0x00000A00},
		{`SOFTWARE\Wow6432Node\Microsoft\Windows\CurrentVersion\Internet Settings\WinHttp`, "DefaultSecureProtocols", 0x00000A00},
	},
	// .NET v4.x: SystemDefaultTlsVersions=1 — позволяет .NET использовать TLS-версию по умолчанию ОС
	"dotnet": {
		{`SOFTWARE\Microsoft\.NETFramework\v4.0.30319`, "SystemDefaultTlsVersions", 1},
		{`SOFTWARE\Wow6432Node\Microsoft\.NETFramework\v4.0.30319`, "SystemDefaultTlsVersions", 1},
	},
}

// ReadTLSState считывает текущее состояние TLS из реестра.
// Для каждого параметра всегда показывает все поля; — означает «не задано».
func ReadTLSState() []TLSItem {
	return []TLSItem{
		checkTLS12(),
		checkSCHANNEL("tls13_server_disable", "TLS 1.3 Server (откл. для iiko)", "TLS 1.3", "Server", 1, 0),
		checkWinHTTP(),
		checkDotNet(),
	}
}

// checkTLS12 проверяет TLS 1.2 Client и Server одновременно.
func checkTLS12() TLSItem {
	const base = `SYSTEM\CurrentControlSet\Control\SecurityProviders\SCHANNEL\Protocols\TLS 1.2`

	cDDB, hasCDDB := tlsReadDword(base+`\Client`, "DisabledByDefault")
	cEn, hasCEn := tlsReadDword(base+`\Client`, "Enabled")
	sDDB, hasSDB := tlsReadDword(base+`\Server`, "DisabledByDefault")
	sEn, hasSEn := tlsReadDword(base+`\Server`, "Enabled")

	clientOK := hasCDDB && uint32(cDDB) == 0 && hasCEn && uint32(cEn) == 1
	serverOK := hasSDB && uint32(sDDB) == 0 && hasSEn && uint32(sEn) == 1
	needsChange := !clientOK || !serverOK

	clientStr := fmt.Sprintf("Client: DDB=%-3s  En=%-3s", dwordStr(cDDB, hasCDDB), dwordStr(cEn, hasCEn))
	if clientOK {
		clientStr += " [OK]"
	} else {
		clientStr += " → DDB=0 En=1"
	}

	serverStr := fmt.Sprintf("Server: DDB=%-3s  En=%-3s", dwordStr(sDDB, hasSDB), dwordStr(sEn, hasSEn))
	if serverOK {
		serverStr += " [OK]"
	} else {
		serverStr += " → DDB=0 En=1"
	}

	return TLSItem{
		Key:         "tls12",
		Label:       "TLS 1.2 (Client + Server)",
		StatusText:  clientStr + "   " + serverStr,
		NeedsChange: needsChange,
	}
}

func checkSCHANNEL(key, label, proto, side string, wantDDB, wantEnabled uint32) TLSItem {
	const base = `SYSTEM\CurrentControlSet\Control\SecurityProviders\SCHANNEL\Protocols`
	path := base + `\` + proto + `\` + side

	ddb, hasDDB := tlsReadDword(path, "DisabledByDefault")
	en, hasEn := tlsReadDword(path, "Enabled")

	ddbOK := hasDDB && uint32(ddb) == wantDDB
	enOK := hasEn && uint32(en) == wantEnabled
	needsChange := !ddbOK || !enOK

	status := fmt.Sprintf("DisabledByDefault: %-5s  Enabled: %-5s",
		dwordStr(ddb, hasDDB), dwordStr(en, hasEn))
	if needsChange {
		status += fmt.Sprintf("  →  DDB=%d En=%d", wantDDB, wantEnabled)
	} else {
		status += "  [OK]"
	}
	return TLSItem{Key: key, Label: label, StatusText: status, NeedsChange: needsChange}
}

// checkWinHTTP проверяет DefaultSecureProtocols.
// Цель: 0xA00 = TLS 1.1 + TLS 1.2 (без SSL 3.0/TLS 1.0).
func checkWinHTTP() TLSItem {
	const expected = uint64(0xA00)
	entries := []struct{ label, path string }{
		{"32-bit", `SOFTWARE\Microsoft\Windows\CurrentVersion\Internet Settings\WinHttp`},
		{"64-bit", `SOFTWARE\Wow6432Node\Microsoft\Windows\CurrentVersion\Internet Settings\WinHttp`},
	}

	var parts []string
	needsChange := false
	for _, e := range entries {
		val, ok := tlsReadDword(e.path, "DefaultSecureProtocols")
		if !ok {
			parts = append(parts, fmt.Sprintf("%s: —", e.label))
			needsChange = true
		} else if val == expected {
			parts = append(parts, fmt.Sprintf("%s: 0x%X [OK]", e.label, val))
		} else {
			parts = append(parts, fmt.Sprintf("%s: 0x%X  →  0xA00", e.label, val))
			needsChange = true
		}
	}
	return TLSItem{
		Key:         "winhttp",
		Label:       "WinHTTP DefaultSecureProtocols (TLS 1.1 + 1.2)",
		StatusText:  strings.Join(parts, "   "),
		NeedsChange: needsChange,
	}
}

// checkDotNet проверяет SystemDefaultTlsVersions для .NET 4.x.
// Позволяет .NET-приложениям использовать TLS-версию по умолчанию ОС.
func checkDotNet() TLSItem {
	entries := []struct{ label, path string }{
		{"v4 x64", `SOFTWARE\Microsoft\.NETFramework\v4.0.30319`},
		{"v4 x86", `SOFTWARE\Wow6432Node\Microsoft\.NETFramework\v4.0.30319`},
	}

	var parts []string
	needsChange := false
	for _, e := range entries {
		val, ok := tlsReadDword(e.path, "SystemDefaultTlsVersions")
		if !ok {
			parts = append(parts, fmt.Sprintf("%s: —", e.label))
			needsChange = true
		} else if val == 1 {
			parts = append(parts, fmt.Sprintf("%s: 1 [OK]", e.label))
		} else {
			parts = append(parts, fmt.Sprintf("%s: %d  →  1", e.label, val))
			needsChange = true
		}
	}
	return TLSItem{
		Key:         "dotnet",
		Label:       ".NET SystemDefaultTlsVersions (v4.x)",
		StatusText:  strings.Join(parts, "   "),
		NeedsChange: needsChange,
	}
}

// ApplyTLSFixes записывает выбранные исправления TLS в реестр HKLM.
func ApplyTLSFixes(keys []string) error {
	for _, key := range keys {
		writes, ok := tlsFixes[key]
		if !ok {
			continue
		}
		for _, w := range writes {
			k, _, err := registry.CreateKey(registry.LOCAL_MACHINE, w.path, registry.SET_VALUE|registry.CREATE_SUB_KEY)
			if err != nil {
				return fmt.Errorf("CreateKey %s: %w", w.path, err)
			}
			if err := k.SetDWordValue(w.name, w.value); err != nil {
				k.Close()
				return fmt.Errorf("SetDWord %s\\%s: %w", w.path, w.name, err)
			}
			k.Close()
		}
	}
	return nil
}

// dwordStr форматирует DWORD-значение или возвращает "—" если ключ отсутствует.
func dwordStr(val uint64, ok bool) string {
	if !ok {
		return "—"
	}
	return fmt.Sprintf("%d", val)
}

// tlsReadDword читает DWORD-значение из HKLM.
func tlsReadDword(path, name string) (uint64, bool) {
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, path, registry.QUERY_VALUE)
	if err != nil {
		return 0, false
	}
	defer k.Close()
	v, _, err := k.GetIntegerValue(name)
	if err != nil {
		return 0, false
	}
	return v, true
}
