//go:build windows

package lifecyclehook

import (
	"os/exec"
	"strconv"
)

// killTreeOnCancel cancels the hook by killing it and every process under it.
func killTreeOnCancel(cmd *exec.Cmd) {
	cmd.Cancel = func() error {
		return exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(cmd.Process.Pid)).Run()
	}
}
