//go:build !darwin && !linux

package claudeagentsdk

import (
	"fmt"
	"os"
	"os/exec"
)

func applyCommandUser(_ *exec.Cmd, username string) error {
	if username == "" {
		return nil
	}
	return fmt.Errorf("Options.User is not supported on this platform")
}

func configureCommandProcessGroup(_ *exec.Cmd) {}

func killCommandProcess(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return os.ErrProcessDone
	}
	return cmd.Process.Kill()
}
