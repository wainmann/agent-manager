//go:build darwin || linux

package tmux

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"time"
)

func configureBoundedCommand(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return os.ErrProcessDone
		}
		return killProcessGroup(cmd.Process.Pid, syscall.Kill)
	}
}

func killProcessGroup(pid int, kill func(int, syscall.Signal) error) error {
	group := -pid
	err := kill(group, syscall.SIGKILL)
	if errors.Is(err, syscall.ESRCH) {
		return os.ErrProcessDone
	}
	if err != nil {
		return err
	}
	deadline := time.Now().Add(100 * time.Millisecond)
	for {
		err := kill(group, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return nil
		}
		if errors.Is(err, syscall.EPERM) {
			// EPERM is not an exit fence while the leader still exists.
			if errors.Is(kill(pid, 0), syscall.ESRCH) {
				return nil
			}
		} else if err != nil {
			return err
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("process group %d still has members 100ms after SIGKILL", -group)
		}
		time.Sleep(time.Millisecond)
	}
}
