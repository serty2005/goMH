package automation

import "fmt"

type runError struct {
	exitCode int
	code     string
	message  string
	cause    error
}

func (e *runError) Error() string {
	if e == nil {
		return ""
	}
	if e.cause == nil {
		return e.message
	}
	return fmt.Sprintf("%s: %v", e.message, e.cause)
}

func (e *runError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
}

func newRunError(exitCode int, code string, message string, cause error) error {
	return &runError{
		exitCode: exitCode,
		code:     code,
		message:  message,
		cause:    cause,
	}
}

func classifyError(err error) (int, string, string) {
	if err == nil {
		return ExitSuccess, "", ""
	}
	if runErr, ok := err.(*runError); ok {
		return runErr.exitCode, runErr.code, runErr.message
	}
	return ExitInternalError, "internal_error", err.Error()
}
