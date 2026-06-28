package distro

import (
	"errors"
	"goMH/config"
	"testing"
)

func TestStartConfiguredExecutableUsesExecutableDirectory(t *testing.T) {
	starter := &captureDetachedProcessStarter{}

	err := startConfiguredExecutable(starter, `C:\Program Files\iiko\iikoRMS\Front.Net\iikoFront.Net.exe`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if starter.name != `C:\Program Files\iiko\iikoRMS\Front.Net\iikoFront.Net.exe` {
		t.Fatalf("unexpected executable path: %s", starter.name)
	}
	if starter.workingDir != `C:\Program Files\iiko\iikoRMS\Front.Net` {
		t.Fatalf("unexpected working directory: %s", starter.workingDir)
	}
}

func TestStartConfiguredExecutableReturnsStarterError(t *testing.T) {
	starter := &captureDetachedProcessStarter{err: errors.New("boom")}

	err := startConfiguredExecutable(starter, `C:\Program Files\iiko\iikoRMS\Front.Net\iikoFront.Net.exe`)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestEffectiveIikoFrontRunAfterUsesLegacyExecutableBefore95(t *testing.T) {
	cfg := &DistroInstallConfig{
		Brand:   "iiko",
		Version: "9.4.8049.0",
		Component: config.DistroComponent{
			ID:       "iiko_front",
			RunAfter: `C:\Program Files\iiko\iikoRMS\Front.Net\iikoFront.Net.exe`,
		},
	}

	got := effectiveIikoFrontRunAfter(cfg)
	want := `C:\Program Files\iiko\iikoRMS\Front.Net\iikoFront.Net.exe`
	if got != want {
		t.Fatalf("effectiveIikoFrontRunAfter() = %q, want %q", got, want)
	}
}

func TestEffectiveIikoFrontRunAfterUsesModernExecutableFrom95(t *testing.T) {
	cfg := &DistroInstallConfig{
		Brand:   "iiko",
		Version: "9.5.0.0",
		Component: config.DistroComponent{
			ID:       "iiko_front",
			RunAfter: `C:\Program Files\iiko\iikoRMS\Front.Net\iikoFront.Net.exe`,
		},
	}

	got := effectiveIikoFrontRunAfter(cfg)
	want := `C:\Program Files\iiko\iikoRMS\Front.Net\Resto.Front.Main.exe`
	if got != want {
		t.Fatalf("effectiveIikoFrontRunAfter() = %q, want %q", got, want)
	}
}

func TestEffectiveIikoFrontRunAfterLeavesNonIikoFrontPath(t *testing.T) {
	cfg := &DistroInstallConfig{
		Brand:   "syrve",
		Version: "9.5.0.0",
		Component: config.DistroComponent{
			ID:       "syrve_front",
			RunAfter: `C:\Program Files\Syrve\Front.Net\iikoFront.Net.exe`,
		},
	}

	got := effectiveIikoFrontRunAfter(cfg)
	want := cfg.Component.RunAfter
	if got != want {
		t.Fatalf("effectiveIikoFrontRunAfter() = %q, want %q", got, want)
	}
}

type captureDetachedProcessStarter struct {
	name       string
	workingDir string
	err        error
}

func (c *captureDetachedProcessStarter) StartDetachedProcessInDir(name string, workingDir string, args ...string) error {
	c.name = name
	c.workingDir = workingDir
	return c.err
}
