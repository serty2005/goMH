package iikoplugins

import (
	"reflect"
	"testing"
)

func TestParseRapidArchiveMetadata(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name        string
		archiveURL  string
		wantName    string
		wantAPI     string
		wantVersion string
	}{
		{
			name:       "preview API and hyphenated version",
			archiveURL: "https://rapid.iiko.ru/plugins/Resto.Front.Api.Transport/Resto.Front.Api.Transport.V9Preview7-9.7.20.zip",
			wantName:   "Transport", wantAPI: "V9Preview7", wantVersion: "9.7.20",
		},
		{
			name:       "stable Resto API and dotted version",
			archiveURL: "https://rapid.iiko.ru/plugins/Smart%20Sberbank/Resto.Front.Api.SberbankPlugin.V9.1.2.57.zip",
			wantName:   "SberbankPlugin", wantAPI: "V9", wantVersion: "1.2.57",
		},
		{
			name:       "vendor archive without Resto prefix",
			archiveURL: "https://rapid.iiko.ru/plugins/Guestme.Plugin/GuestMe.Plugin.V8.8.0.0.zip",
			wantName:   "GuestMe.Plugin", wantAPI: "V8", wantVersion: "8.0.0",
		},
		{
			name:       "embedded API text belongs to vendor name",
			archiveURL: "https://rapid.iiko.ru/plugins/Custom%20YarusTerminal/YarusTerminalPluginV5.V5.1.23.12.zip",
			wantName:   "YarusTerminalPluginV5", wantAPI: "V5", wantVersion: "1.23.12",
		},
		{
			name:       "versioned archive without API",
			archiveURL: "https://rapid.iiko.ru/plugins/Arrivals/arrivals-3.1.28.zip",
			wantName:   "arrivals", wantVersion: "3.1.28",
		},
		{
			name:       "uppercase ZIP extension",
			archiveURL: "https://rapid.iiko.ru/plugins/Guestme.Plugin/GuestMe.Plugin.V8.8.0.0.ZIP",
			wantName:   "GuestMe.Plugin", wantAPI: "V8", wantVersion: "8.0.0",
		},
		{
			name:       "API archive build hash",
			archiveURL: "https://rapid.iiko.ru/plugins/Smart%20Sberbank/Resto.Front.Api.SberbankPlugin.V9Preview4.1.2.56-g218025e686.zip",
			wantName:   "SberbankPlugin", wantAPI: "V9Preview4", wantVersion: "1.2.56",
		},
		{
			name:       "beta suffix remains visible",
			archiveURL: "https://rapid.iiko.ru/plugins/Smart%20Joinleader/BeOpen.JoinLeader.V7.1.0.27-beta.zip",
			wantName:   "BeOpen.JoinLeader", wantAPI: "V7", wantVersion: "1.0.27-beta",
		},
		{
			name:       "API archive build date",
			archiveURL: "https://rapid.iiko.ru/plugins/Resto.Front.Api.Transport/Resto.Front.Api.Transport.V9Preview7-9.7.20-2026.02.26.zip",
			wantName:   "Transport", wantAPI: "V9Preview7", wantVersion: "9.7.20",
		},
		{
			name:       "legacy API qualifier is separate from plugin version",
			archiveURL: "https://rapid.iiko.ru/plugins/PlaziusCheckin/V6.Legacy.6.2/Resto.Front.Api.PlaziusCheckinPlugin.V6.Legacy.6.2-1.0.0.137-2020.08.05.zip",
			wantName:   "PlaziusCheckinPlugin", wantAPI: "V6.Legacy.6.2", wantVersion: "1.0.0.137",
		},
		{
			name:       "legacy API specified by directory",
			archiveURL: "https://rapid.iiko.ru/plugins/Paidit.Kiosk_Integration_Plugin/V6/Paidid.Resto.Front.Api.PaiditPlugin-1.0.0.7-2020.11.02.zip",
			wantName:   "Paidid.Resto.Front.Api.PaiditPlugin", wantAPI: "V6", wantVersion: "1.0.0.7",
		},
		{
			name:       "legacy lowercase API directory",
			archiveURL: "https://rapid.iiko.ru/plugins/LoyaltyPlantPlugin/release/v6-api/Resto.Front.Api.LoyaltyPlantPlugin.1.0.1002-gd34ed8e35a.zip",
			wantName:   "LoyaltyPlantPlugin", wantAPI: "V6", wantVersion: "1.0.1002",
		},
		{
			name:       "nonexact API directory does not specify compatibility",
			archiveURL: "https://rapid.iiko.ru/plugins/example/V8%20docs/CustomerDisplay-1.0.1126.0.zip",
			wantName:   "CustomerDisplay", wantVersion: "1.0.1126.0",
		},
		{
			name:       "beta build hash preserves prerelease qualifier",
			archiveURL: "https://rapid.iiko.ru/plugins/Smart%20EqwaPayment/V8/Resto.Front.Api.EqwaPaymentPlugin.V8.1.0.7-beta-g2c972284b8.zip",
			wantName:   "EqwaPaymentPlugin", wantAPI: "V8", wantVersion: "1.0.7-beta",
		},
		{
			name:       "filename API takes priority over directory",
			archiveURL: "https://rapid.iiko.ru/plugins/Custom%20PrinterECR/V6/Resto.Front.Api.PrinterCustom.V7.1.2.4-alpha.zip",
			wantName:   "PrinterCustom", wantAPI: "V7", wantVersion: "1.2.4-alpha",
		},
		{
			name:       "archive build date without API",
			archiveURL: "https://rapid.iiko.ru/plugins/Resto.Front.Api.PaymentSystem.DualConnector/Front.Api.PaymentSystem.DualConnector-1.0.0.18-2021.07.21.zip",
			wantName:   "Front.Api.PaymentSystem.DualConnector", wantVersion: "1.0.0.18",
		},
		{
			name:       "Resto archive without API",
			archiveURL: "https://rapid.iiko.ru/plugins/legacy/Resto.Front.Api.MotionView-1.0.1126.0.zip",
			wantName:   "MotionView", wantVersion: "1.0.1126.0",
		},
		{
			name:       "numeric Front version directory does not specify API",
			archiveURL: "https://rapid.iiko.ru/plugins/example/9.1.5/CustomerDisplay-1.0.1126.0.zip",
			wantName:   "CustomerDisplay", wantVersion: "1.0.1126.0",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			plugin, ok := parsePluginFromZipURL(test.archiveURL)
			if !ok {
				t.Fatalf("archive metadata was not parsed: %s", test.archiveURL)
			}
			want := Plugin{
				Name: test.wantName, ApiVersion: test.wantAPI, PluginVersion: test.wantVersion,
				Source: PluginSourceRapid, DownloadUrl: test.archiveURL,
			}
			if !reflect.DeepEqual(plugin, want) {
				t.Fatalf("plugin = %+v, want %+v", plugin, want)
			}
		})
	}
}

func TestParseRapidArchiveRejectsNonArchiveURLs(t *testing.T) {
	t.Parallel()

	for _, archiveURL := range []string{
		"https://rapid.iiko.ru/plugins/Arrivals/arrivals-3.1.28.txt",
		"https://rapid.iiko.ru/plugins/Arrivals/",
		"https://rapid.iiko.ru/plugins/Arrivals/arrivals-3.1.28.zip/",
		"https://rapid.iiko.ru/plugins/%broken.zip",
		"https://rapid.iiko.ru/plugins/pburuninTest/resgen/TranslationPortalExchange.zip",
	} {
		t.Run(archiveURL, func(t *testing.T) {
			t.Parallel()
			if plugin, ok := parsePluginFromZipURL(archiveURL); ok {
				t.Fatalf("non-archive URL parsed as %+v", plugin)
			}
		})
	}
}

func TestRapidArchiveWithoutAPIIsCataloguedButNotAssumedCompatible(t *testing.T) {
	t.Parallel()

	unknownAPI, ok := parsePluginFromZipURL("https://rapid.iiko.ru/plugins/Arrivals/arrivals-3.1.28.zip")
	if !ok {
		t.Fatal("archive without API was omitted from catalog")
	}
	knownAPI, ok := parsePluginFromZipURL("https://rapid.iiko.ru/plugins/Guestme.Plugin/GuestMe.Plugin.V8.8.0.0.zip")
	if !ok {
		t.Fatal("archive with API was omitted from catalog")
	}

	filtered := filterCompatiblePlugins([]Plugin{unknownAPI, knownAPI}, []string{"V8", "V9"}, "9.4.8049.0")
	if !reflect.DeepEqual(filtered, []Plugin{knownAPI}) {
		t.Fatalf("compatible plugins = %+v, want only known API archive %+v", filtered, knownAPI)
	}
}
