package winutils

import (
	"fmt"
	"io"
	"os"
	"sync"
)

var consoleOutput = struct {
	mu     sync.Mutex
	stdout io.Writer
	stderr io.Writer
}{
	stdout: os.Stdout,
	stderr: os.Stderr,
}

func SetConsoleOutput(stdout io.Writer, stderr io.Writer) func() {
	consoleOutput.mu.Lock()
	prevStdout := consoleOutput.stdout
	prevStderr := consoleOutput.stderr
	if stdout != nil {
		consoleOutput.stdout = stdout
	}
	if stderr != nil {
		consoleOutput.stderr = stderr
	}
	consoleOutput.mu.Unlock()

	return func() {
		consoleOutput.mu.Lock()
		consoleOutput.stdout = prevStdout
		consoleOutput.stderr = prevStderr
		consoleOutput.mu.Unlock()
	}
}

func consolePrintf(format string, args ...interface{}) {
	consoleOutput.mu.Lock()
	writer := consoleOutput.stdout
	consoleOutput.mu.Unlock()
	fmt.Fprintf(writer, format, args...)
}

func consoleStdout() io.Writer {
	consoleOutput.mu.Lock()
	defer consoleOutput.mu.Unlock()
	return consoleOutput.stdout
}

func consoleStderr() io.Writer {
	consoleOutput.mu.Lock()
	defer consoleOutput.mu.Unlock()
	return consoleOutput.stderr
}
