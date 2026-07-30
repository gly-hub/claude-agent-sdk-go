//go:build darwin || linux

package claudeagentsdk

import (
	"bufio"
	"errors"
	"os/exec"
	"strconv"
	"syscall"
	"testing"
	"time"
)

func TestKillCommandProcessKillsUnixProcessGroup(t *testing.T) {
	cmd := exec.Command("sh", "-c", "sleep 30 & printf '%s\\n' $!; wait")
	configureCommandProcessGroup(cmd)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	scanner := bufio.NewScanner(stdout)
	if !scanner.Scan() {
		t.Fatalf("expected child pid: %v", scanner.Err())
	}
	childPID, err := strconv.Atoi(scanner.Text())
	if err != nil {
		t.Fatal(err)
	}
	if err := killCommandProcess(cmd); err != nil {
		t.Fatal(err)
	}
	_ = cmd.Wait()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		err := syscall.Kill(childPID, 0)
		if errors.Is(err, syscall.ESRCH) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("child process %d remained after process-group termination", childPID)
}
