package distro

import (
	"goMH/config"
	"testing"
)

func TestInstallerTextIndicatesPendingReboot(t *testing.T) {
	t.Parallel()

	logText := `
[2560:04F0][2026-03-13T05:12:09]i052: Condition 'not RebootPending or WixBundleInstalled' evaluates to false.
[2560:04F0][2026-03-13T05:12:10]e000: Error 0x81f40001: Bundle condition evaluated to false: not RebootPending or WixBundleInstalled
[2560:04F0][2026-03-13T05:12:10]i410: Variable: RebootPending = 1
`

	if !installerTextIndicatesPendingReboot(logText) {
		t.Fatal("expected pending reboot marker to be detected")
	}
}

func TestInstallerTextDoesNotIndicatePendingReboot(t *testing.T) {
	t.Parallel()

	logText := `
[2560:04F0][2026-03-13T05:12:10]i199: Detect complete, result: 0x0
[2560:04F0][2026-03-13T05:12:10]i500: Shutting down, exit code: 0x0
`

	if installerTextIndicatesPendingReboot(logText) {
		t.Fatal("did not expect pending reboot marker in unrelated log")
	}
}

func TestRequiresPendingRebootResume(t *testing.T) {
	t.Parallel()

	cfg := &DistroInstallConfig{
		Action: ActionInstallComponent,
		Brand:  "iiko",
		Component: config.DistroComponent{
			ID: "iiko_front",
		},
	}

	if !requiresPendingRebootResume(cfg) {
		t.Fatal("expected iikoFront install to require pending reboot resume flow")
	}

	cfg.Component.ID = "iiko_rms_back"
	if requiresPendingRebootResume(cfg) {
		t.Fatal("did not expect non-front component to require pending reboot resume flow")
	}
}
