package assetmgr

import (
	"fmt"
	"io"
	"os"
	"sync"
)

var consoleOutput = struct {
	mu        sync.Mutex
	stdout    io.Writer
	progress  io.Writer
	statusFn  func(string)
	percentFn func(string, int)
}{
	stdout:   os.Stdout,
	progress: os.Stderr,
}

func SetConsoleOutput(stdout io.Writer, progress io.Writer) func() {
	consoleOutput.mu.Lock()
	prevStdout := consoleOutput.stdout
	prevProgress := consoleOutput.progress
	if stdout != nil {
		consoleOutput.stdout = stdout
	}
	if progress != nil {
		consoleOutput.progress = progress
	}
	consoleOutput.mu.Unlock()

	return func() {
		consoleOutput.mu.Lock()
		consoleOutput.stdout = prevStdout
		consoleOutput.progress = prevProgress
		consoleOutput.mu.Unlock()
	}
}

func SetProgressCallbacks(statusFn func(string), percentFn func(string, int)) func() {
	consoleOutput.mu.Lock()
	prevStatusFn := consoleOutput.statusFn
	prevPercentFn := consoleOutput.percentFn
	consoleOutput.statusFn = statusFn
	consoleOutput.percentFn = percentFn
	consoleOutput.mu.Unlock()

	return func() {
		consoleOutput.mu.Lock()
		consoleOutput.statusFn = prevStatusFn
		consoleOutput.percentFn = prevPercentFn
		consoleOutput.mu.Unlock()
	}
}

func consolePrintf(format string, args ...interface{}) {
	consoleOutput.mu.Lock()
	writer := consoleOutput.stdout
	consoleOutput.mu.Unlock()
	fmt.Fprintf(writer, format, args...)
}

func consoleFprintProgress(text string) {
	consoleOutput.mu.Lock()
	writer := consoleOutput.progress
	consoleOutput.mu.Unlock()
	fmt.Fprint(writer, text)
}

func progressWriter() io.Writer {
	consoleOutput.mu.Lock()
	defer consoleOutput.mu.Unlock()
	return consoleOutput.progress
}

func reportStatus(text string) {
	consoleOutput.mu.Lock()
	fn := consoleOutput.statusFn
	consoleOutput.mu.Unlock()
	if fn != nil {
		fn(text)
	}
}

func reportProgress(description string, percent int) {
	consoleOutput.mu.Lock()
	fn := consoleOutput.percentFn
	consoleOutput.mu.Unlock()
	if fn != nil {
		fn(description, percent)
	}
}
