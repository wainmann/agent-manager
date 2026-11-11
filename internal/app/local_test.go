//go:build !windows

package app

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/YoanWai/agent-manager/internal/lifecyclehook"
	"github.com/YoanWai/agent-manager/internal/store"
	"github.com/YoanWai/agent-manager/internal/tmux"
	"github.com/google/uuid"
)

func TestStopHooksKillsARunningHookAndKeepsTheStoreOpen(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	driver, err := tmux.NewWithSocket("amhooktest-" + uuid.NewString()[:8])
	if err != nil {
		t.Fatalf("tmux driver: %v", err)
	}
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "pid")
	command := []string{"sh", "-c", "echo $$ > " + pidFile + "; exec sleep 30"}
	hooks := "schema_version = 1\n\n[[hooks.session_start]]\ncommand = [\"sh\", \"-c\", \"echo $$ > " + pidFile + "; exec sleep 30\"]\ntimeout = \"30s\"\n"
	if err := os.WriteFile(filepath.Join(dir, lifecyclehook.HooksFile), []byte(hooks), 0o600); err != nil {
		t.Fatal(err)
	}
	local, err := OpenLocal(dir, "test", driver)
	if err != nil {
		t.Fatalf("OpenLocal: %v", err)
	}
	defer local.Close()
	if _, err := local.Runtime.Store.ClaimHookShown(lifecyclehook.Fingerprint(lifecyclehook.EventSessionStart, command)); err != nil {
		t.Fatal(err)
	}
	local.Runtime.Events.Emit(lifecyclehook.Event{Name: lifecyclehook.EventSessionStart, Session: store.Session{ID: "s1", Tool: "claude"}})
	var pid int
	deadline := time.Now().Add(10 * time.Second)
	for pid == 0 {
		if time.Now().After(deadline) {
			t.Fatal("the hook never started")
		}
		if raw, err := os.ReadFile(pidFile); err == nil {
			pid, _ = strconv.Atoi(strings.TrimSpace(string(raw)))
		}
		time.Sleep(20 * time.Millisecond)
	}
	started := time.Now()
	local.StopHooks()
	if took := time.Since(started); took > 5*time.Second {
		t.Fatalf("StopHooks waited %s for a 30s hook", took)
	}
	if err := syscall.Kill(pid, 0); err == nil {
		t.Fatalf("hook %d still runs after StopHooks", pid)
	}
	if _, err := local.Runtime.Store.Setting("notifications"); err != nil {
		t.Fatalf("StopHooks closed the store: %v", err)
	}
}
