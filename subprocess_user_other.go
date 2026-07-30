//go:build !darwin && !linux

package claudeagentsdk

import (
	"fmt"
	"os/exec"
)

func applyCommandUser(_ *exec.Cmd, username string) error {
	if username == "" {
		return nil
	}
	return fmt.Errorf("Options.User is not supported on this platform")
}
