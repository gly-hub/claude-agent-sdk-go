package claudeagentsdk

import "fmt"

var (
	ErrNotConnected = fmt.Errorf("claude agent sdk: client not connected")
	ErrStreamClosed = fmt.Errorf("claude agent sdk: stream closed")
)

type ProcessError struct {
	ExitCode int
	Stderr   string
}

// CLIConnectionError reports a failure while creating or operating a CLI connection.
type CLIConnectionError struct{ Err error }

func (e *CLIConnectionError) Error() string { return e.Err.Error() }
func (e *CLIConnectionError) Unwrap() error { return e.Err }

// CLINotFoundError reports that the Claude CLI executable could not be resolved.
type CLINotFoundError struct {
	Message string
	CLIPath string
}

func (e *CLINotFoundError) Error() string {
	if e.CLIPath != "" {
		return fmt.Sprintf("%s: %s", e.Message, e.CLIPath)
	}
	return e.Message
}

// CLIJSONDecodeError retains the malformed stdout line that could not be decoded.
type CLIJSONDecodeError struct {
	Line string
	Err  error
}

func (e *CLIJSONDecodeError) Error() string {
	return fmt.Sprintf("failed to decode CLI JSON: %.100s", e.Line)
}
func (e *CLIJSONDecodeError) Unwrap() error { return e.Err }

// MessageParseError identifies a syntactically valid protocol message with an invalid shape.
type MessageParseError struct {
	Message string
	Data    map[string]any
}

func (e *MessageParseError) Error() string { return e.Message }

func (e *ProcessError) Error() string {
	if e.Stderr != "" {
		return fmt.Sprintf("claude process exited with code %d: %s", e.ExitCode, e.Stderr)
	}
	return fmt.Sprintf("claude process exited with code %d", e.ExitCode)
}
