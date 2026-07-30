//go:build linux

package claudeagentsdk

import "syscall"

func configureParentDeathSignal(attr *syscall.SysProcAttr) {
	// If the host process disappears abruptly, do not leave the CLI running.
	attr.Pdeathsig = syscall.SIGKILL
}
