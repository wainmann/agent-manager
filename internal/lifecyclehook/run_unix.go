//go:build !windows

package lifecyclehook

import (
	"os/exec"
	"syscall"
)

// killTreeOnCancel runs the hook in a session of its own and cancels it by
// killing that whole group, so a child the hook started does not outlive
// the timeout.
func killTreeOnCancel(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
}
