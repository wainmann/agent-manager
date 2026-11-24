//go:build !windows

package lifecyclehook

import (
	"errors"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/YoanWai/agent-manager/internal/status"
)

func TestDispatcherKillsTheHookTreeAtTheTimeout(t *testing.T) {
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "child.pid")
	hook := script(t, "sleep 30 &\necho $! > "+quote(pidFile)+"\nwait\n")
	writeHooks(t, dir, EventSessionState, []string{hook}, "timeout = \"1s\"\n")
	d, st := openDispatcher(t, dir)
	d.Emit(sessionEvent("s", status.Waiting))
	waitUntil(t, func() bool { return fileExists(pidFile) })
	pid, err := strconv.Atoi(strings.TrimSpace(readFile(t, pidFile)))
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for processRunning(t, pid) {
		if time.Now().After(deadline) {
			_ = syscall.Kill(pid, syscall.SIGKILL)
			t.Fatalf("child %d outlived the hook timeout", pid)
		}
		time.Sleep(20 * time.Millisecond)
	}
	waitUntil(t, func() bool {
		failures, err := st.HookFailures()
		return err == nil && len(failures) == 1 && strings.Contains(failures[0].Reason, "timed out")
	})
}

func processRunning(t *testing.T, pid int) bool {
	t.Helper()
	if err := syscall.Kill(pid, 0); errors.Is(err, syscall.ESRCH) {
		return false
	} else if err != nil {
		t.Fatalf("signal 0 to %d: %v", pid, err)
	}
	state, err := exec.Command("ps", "-o", "stat=", "-p", strconv.Itoa(pid)).Output()
	var exit *exec.ExitError
	if errors.As(err, &exit) && strings.TrimSpace(string(state)) == "" {
		return false
	}
	if err != nil {
		t.Fatalf("reading the state of %d: %v", pid, err)
	}
	return !strings.HasPrefix(strings.TrimSpace(string(state)), "Z")
}
