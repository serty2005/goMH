package distro

import (
	"fmt"
	"path/filepath"
	"strings"
)

type detachedProcessStarter interface {
	StartDetachedProcessInDir(name string, workingDir string, args ...string) error
}

func startConfiguredExecutable(starter detachedProcessStarter, executablePath string) error {
	executablePath = strings.TrimSpace(executablePath)
	if executablePath == "" {
		return nil
	}
	if starter == nil {
		return fmt.Errorf("starter is nil")
	}

	workingDir := filepath.Dir(executablePath)
	if workingDir == "." {
		workingDir = ""
	}

	if err := starter.StartDetachedProcessInDir(executablePath, workingDir); err != nil {
		return fmt.Errorf("не удалось запустить %s: %w", executablePath, err)
	}
	return nil
}
