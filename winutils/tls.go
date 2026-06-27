package winutils

import (
	"fmt"
	"strings"

	"golang.org/x/sys/windows/registry"
)

// CollectTLSInfo собирает состояние TLS 1.1/1.2/1.3 и настроек .NET из реестра
// и возвращает готовый текстовый отчёт с пометками об отклонениях.
func (r *Runtime) CollectTLSInfo() string {
	var sb strings.Builder

	// ── SCHANNEL: TLS-протоколы ──────────────────────────────────────────────
	sb.WriteString("=== SCHANNEL: состояние TLS-протоколов ===\n\n")

	type protoCheck struct {
		name        string
		wantDDB     uint64
		wantEnabled uint64
		checkClient bool
		checkServer bool
	}

	// TLS 1.2: должен быть включён (DisabledByDefault=0, Enabled=1).
	// TLS 1.3 Server: должен быть явно отключён (DisabledByDefault=1, Enabled=0) для совместимости с iiko.
	// TLS 1.1: только отчёт, без проверки ожидаемых значений.
	protocols := []protoCheck{
		{"TLS 1.2", 0, 1, true, true},
		{"TLS 1.3", 1, 0, false, true},
		{"TLS 1.1", 0, 0, false, false},
	}

	const schannelBase = `SYSTEM\CurrentControlSet\Control\SecurityProviders\SCHANNEL\Protocols`

	fmtVal := func(v uint64, ok bool) string {
		if !ok {
			return "(нет)"
		}
		return fmt.Sprintf("%d", v)
	}

	for _, proto := range protocols {
		sb.WriteString(fmt.Sprintf("  %s:\n", proto.name))
		base := schannelBase + `\` + proto.name

		for _, side := range []struct {
			label   string
			doCheck bool
		}{
			{"Client", proto.checkClient},
			{"Server", proto.checkServer},
		} {
			path := base + `\` + side.label
			ddb, hasDDB := readRegDword(path, "DisabledByDefault")
			en, hasEn := readRegDword(path, "Enabled")

			if !hasDDB && !hasEn {
				sb.WriteString(fmt.Sprintf("    %s: ключи отсутствуют — используется системное умолчание\n", side.label))
				continue
			}

			line := fmt.Sprintf("    %s: DisabledByDefault=%s  Enabled=%s",
				side.label, fmtVal(ddb, hasDDB), fmtVal(en, hasEn))

			if side.doCheck {
				ddbOK := !hasDDB || ddb == proto.wantDDB
				enOK := !hasEn || en == proto.wantEnabled
				if ddbOK && enOK {
					line += "  [OK]"
				} else {
					line += fmt.Sprintf("  [!!! ОТКЛОНЕНИЕ: ожидается DisabledByDefault=%d Enabled=%d]",
						proto.wantDDB, proto.wantEnabled)
				}
			}

			sb.WriteString(line + "\n")
		}
	}

	// ── WinHTTP DefaultSecureProtocols ───────────────────────────────────────
	sb.WriteString("\n=== WinHTTP: DefaultSecureProtocols (ожидается 0xAA0 = TLS 1.0+1.1+1.2) ===\n")
	const expectedWinHTTP = uint64(0xAA0)
	for _, p := range []struct{ label, path string }{
		{"32-bit", `SOFTWARE\Microsoft\Windows\CurrentVersion\Internet Settings\WinHttp`},
		{"64-bit", `SOFTWARE\Wow6432Node\Microsoft\Windows\CurrentVersion\Internet Settings\WinHttp`},
	} {
		val, ok := readRegDword(p.path, "DefaultSecureProtocols")
		if !ok {
			sb.WriteString(fmt.Sprintf("  [%s] ключ отсутствует — системное умолчание\n", p.label))
		} else if val == expectedWinHTTP {
			sb.WriteString(fmt.Sprintf("  [%s] DefaultSecureProtocols=0x%X  [OK]\n", p.label, val))
		} else {
			sb.WriteString(fmt.Sprintf("  [%s] DefaultSecureProtocols=0x%X  [!!! ОТКЛОНЕНИЕ: ожидается 0x%X]\n",
				p.label, val, expectedWinHTTP))
		}
	}

	// ── .NET Framework SchUseStrongCrypto ────────────────────────────────────
	sb.WriteString("\n=== .NET Framework: SchUseStrongCrypto (ожидается 1) ===\n")
	for _, p := range []struct{ label, path string }{
		{"v2.0 (x64)", `SOFTWARE\Microsoft\.NETFramework\v2.0.50727`},
		{"v4.0 (x64)", `SOFTWARE\Microsoft\.NETFramework\v4.0.30319`},
		{"v2.0 (x86)", `SOFTWARE\Wow6432Node\Microsoft\.NETFramework\v2.0.50727`},
		{"v4.0 (x86)", `SOFTWARE\Wow6432Node\Microsoft\.NETFramework\v4.0.30319`},
	} {
		val, ok := readRegDword(p.path, "SchUseStrongCrypto")
		switch {
		case !ok:
			sb.WriteString(fmt.Sprintf("  [%s] SchUseStrongCrypto отсутствует  [!!! ОТКЛОНЕНИЕ: ожидается 1]\n", p.label))
		case val == 1:
			sb.WriteString(fmt.Sprintf("  [%s] SchUseStrongCrypto=1  [OK]\n", p.label))
		default:
			sb.WriteString(fmt.Sprintf("  [%s] SchUseStrongCrypto=%d  [!!! ОТКЛОНЕНИЕ: ожидается 1]\n", p.label, val))
		}
	}

	// ── .NET Framework SystemDefaultTlsVersions ──────────────────────────────
	sb.WriteString("\n=== .NET Framework: SystemDefaultTlsVersions (ожидается 1) ===\n")
	for _, p := range []struct{ label, path string }{
		{"v4.0 (x64)", `SOFTWARE\Microsoft\.NETFramework\v4.0.30319`},
		{"v4.0 (x86)", `SOFTWARE\Wow6432Node\Microsoft\.NETFramework\v4.0.30319`},
	} {
		val, ok := readRegDword(p.path, "SystemDefaultTlsVersions")
		switch {
		case !ok:
			sb.WriteString(fmt.Sprintf("  [%s] SystemDefaultTlsVersions отсутствует  [!!! ОТКЛОНЕНИЕ: ожидается 1]\n", p.label))
		case val == 1:
			sb.WriteString(fmt.Sprintf("  [%s] SystemDefaultTlsVersions=1  [OK]\n", p.label))
		default:
			sb.WriteString(fmt.Sprintf("  [%s] SystemDefaultTlsVersions=%d  [!!! ОТКЛОНЕНИЕ: ожидается 1]\n", p.label, val))
		}
	}

	return sb.String()
}

// readRegDword читает DWORD-значение из HKLM.
func readRegDword(path, name string) (uint64, bool) {
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
