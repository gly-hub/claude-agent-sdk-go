//go:build darwin

package claudeagentsdk

import "syscall"

func configureParentDeathSignal(_ *syscall.SysProcAttr) {}
