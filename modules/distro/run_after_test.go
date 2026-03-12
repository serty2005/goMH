package distro

import (
	"errors"
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
