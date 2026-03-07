package dependencies

import (
	"fmt"
	"io"
	"os"
	"sync"
)

var consoleOutput = struct {
	mu     sync.Mutex
	writer io.Writer
}{
	writer: os.Stdout,
}

func SetConsoleOutput(writer io.Writer) func() {
	consoleOutput.mu.Lock()
	prev := consoleOutput.writer
	if writer != nil {
		consoleOutput.writer = writer
	}
	consoleOutput.mu.Unlock()

	return func() {
		consoleOutput.mu.Lock()
		consoleOutput.writer = prev
		consoleOutput.mu.Unlock()
	}
}

func consolePrintf(format string, args ...interface{}) {
	consoleOutput.mu.Lock()
	writer := consoleOutput.writer
	consoleOutput.mu.Unlock()
	fmt.Fprintf(writer, format, args...)
}

func consolePrintln(args ...interface{}) {
	consoleOutput.mu.Lock()
	writer := consoleOutput.writer
	consoleOutput.mu.Unlock()
	fmt.Fprintln(writer, args...)
}
