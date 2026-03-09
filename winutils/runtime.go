package winutils

import (
	"fmt"
	"io"
	"os"
)

type Runtime struct {
	stdout io.Writer
	stderr io.Writer
}

func NewRuntime() *Runtime {
	return &Runtime{
		stdout: os.Stdout,
		stderr: os.Stderr,
	}
}

func (r *Runtime) WithConsoleOutput(stdout io.Writer, stderr io.Writer) *Runtime {
	if r == nil {
		r = NewRuntime()
	}

	cloned := *r
	if stdout != nil {
		cloned.stdout = stdout
	}
	if stderr != nil {
		cloned.stderr = stderr
	}
	return &cloned
}

func (r *Runtime) ConsoleWriter() io.Writer {
	return r.stdoutWriter()
}

func (r *Runtime) printf(format string, args ...interface{}) {
	fmt.Fprintf(r.stdoutWriter(), format, args...)
}

func (r *Runtime) stdoutWriter() io.Writer {
	if r == nil || r.stdout == nil {
		return io.Discard
	}
	return r.stdout
}

func (r *Runtime) stderrWriter() io.Writer {
	if r == nil || r.stderr == nil {
		return io.Discard
	}
	return r.stderr
}
