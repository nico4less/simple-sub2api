//go:build windows

package tunnel

import (
	"os/exec"
	"syscall"
)

const createNewProcessGroup = 0x00000200

func configureProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: createNewProcessGroup}
	cmd.Cancel = func() error { return terminateProcess(cmd) }
	cmd.WaitDelay = 5
}

func terminateProcess(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	return cmd.Process.Kill()
}
