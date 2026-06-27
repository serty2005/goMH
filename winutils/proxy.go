package winutils

import (
	"os"
	"strings"

	"golang.org/x/sys/windows/registry"
)

// CollectProxyInfo собирает настройки прокси из WinINET-реестра, WinHTTP и переменных среды.
func (r *Runtime) CollectProxyInfo() string {
	var sb strings.Builder

	sb.WriteString("=== WinINET (Internet Explorer / системный браузер) ===\n")
	k, err := registry.OpenKey(
		registry.CURRENT_USER,
		`Software\Microsoft\Windows\CurrentVersion\Internet Settings`,
		registry.QUERY_VALUE,
	)
	if err == nil {
		defer k.Close()
		if v, _, e := k.GetIntegerValue("ProxyEnable"); e == nil {
			if v == 1 {
				sb.WriteString("ProxyEnable: ВКЛ\n")
			} else {
				sb.WriteString("ProxyEnable: ВЫКЛ\n")
			}
		}
		if v, _, e := k.GetStringValue("ProxyServer"); e == nil && v != "" {
			sb.WriteString("ProxyServer: " + v + "\n")
		}
		if v, _, e := k.GetStringValue("ProxyOverride"); e == nil && v != "" {
			sb.WriteString("ProxyOverride (исключения): " + v + "\n")
		}
		if v, _, e := k.GetStringValue("AutoConfigURL"); e == nil && v != "" {
			sb.WriteString("PAC-файл (AutoConfigURL): " + v + "\n")
		}
	} else {
		sb.WriteString("Не удалось прочитать настройки реестра: " + err.Error() + "\n")
	}

	sb.WriteString("\n=== WinHTTP (системный прокси для служб и .NET) ===\n")
	if out, err := RunCommand("netsh", "winhttp", "show", "proxy"); err == nil {
		sb.WriteString(out)
	} else {
		sb.WriteString("Ошибка выполнения netsh winhttp: " + err.Error() + "\n")
	}

	sb.WriteString("\n=== Переменные среды ===\n")
	found := false
	for _, name := range []string{"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "NO_PROXY", "http_proxy", "https_proxy"} {
		if v := os.Getenv(name); v != "" {
			sb.WriteString(name + "=" + v + "\n")
			found = true
		}
	}
	if !found {
		sb.WriteString("Прокси через переменные среды не задан.\n")
	}

	return sb.String()
}
