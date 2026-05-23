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

func (e *ProcessError) Error() string {
	if e.Stderr != "" {
		return fmt.Sprintf("claude process exited with code %d: %s", e.ExitCode, e.Stderr)
	}
	return fmt.Sprintf("claude process exited with code %d", e.ExitCode)
}
