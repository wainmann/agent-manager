//go:build windows

package lifecyclehook

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/YoanWai/agent-manager/internal/pwsh"
	"github.com/YoanWai/agent-manager/internal/status"
	"github.com/YoanWai/agent-manager/internal/store"
)

const windowsHookWait = 60 * time.Second

func TestDispatcherRunsAPowerShellHook(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "out")
	pwned := filepath.Join(dir, "pwned")
	payload := "$(New-Item " + pwned + ")"
	hook := powerShellHook(t, "param([string]$Argument)\n"+
		"$line = [Console]::In.ReadToEnd()\n"+
		"[IO.File]::WriteAllText("+pwsh.Quote(out)+", \"$env:AM_EVENT|$env:AM_SESSION_ID|$env:AM_SESSION_NAME|$Argument|$line\")\n")
	writeHooks(t, dir, EventSessionStart, append(hook, payload), "timeout = \"30s\"\n")
	d, st := openDispatcher(t, dir)
	d.Emit(Event{
		Name: EventSessionStart, Reason: ReasonSpawn,
		Session: store.Session{ID: "s1", Name: "deploy", Tool: "claude", Cwd: dir, Status: status.Starting},
	})
	closeDispatcher(d, windowsHookWait)
	got := readFile(t, out)
	prefix := "session_start|s1|deploy|" + payload + "|"
	line, found := strings.CutPrefix(got, prefix)
	if !found || !strings.Contains(line, `"event":"session_start"`) || strings.Count(line, "\n") != 1 {
		t.Fatalf("hook wrote %q, want prefix %q and one JSON line\n%s", got, prefix, hookFailureReport(t, st, dir))
	}
	if fileExists(pwned) {
		t.Fatal("the argument was executed")
	}
}

func TestDispatcherRunsACmdHookAndRecordsItsExit(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "out")
	hook := filepath.Join(t.TempDir(), "hook.cmd")
	body := "@echo off\r\necho %AM_EVENT% %AM_SESSION_ID%> \"" + out + "\"\r\nexit /b 7\r\n"
	if err := os.WriteFile(hook, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
	writeHooks(t, dir, EventSessionState, []string{hook}, "timeout = \"30s\"\n")
	d, st := openDispatcher(t, dir)
	d.Emit(sessionEvent("s", status.Errored))
	closeDispatcher(d, windowsHookWait)
	if got := strings.TrimSpace(readFile(t, out)); got != "session_state s" {
		t.Fatalf("hook wrote %q\n%s", got, hookFailureReport(t, st, dir))
	}
	failures, err := st.HookFailures()
	if err != nil {
		t.Fatal(err)
	}
	if len(failures) != 1 || failures[0].Command != hook || failures[0].Reason != "exit status 7" {
		t.Fatalf("failures = %+v", failures)
	}
}

func TestDispatcherKillsTheHookTreeAtTheTimeout(t *testing.T) {
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "child.pid")
	hook := powerShellHook(t, "$child = Start-Process -PassThru -WindowStyle Hidden ping -ArgumentList '-n','120','127.0.0.1'\n"+
		"[IO.File]::WriteAllText("+pwsh.Quote(pidFile)+", [string]$child.Id)\n"+
		"Wait-Process -Id $child.Id\n")
	writeHooks(t, dir, EventSessionState, hook, "timeout = \"10s\"\n")
	d, st := openDispatcher(t, dir)
	d.Emit(sessionEvent("s", status.Waiting))
	closeDispatcher(d, windowsHookWait)
	pid, err := strconv.Atoi(strings.TrimSpace(readFile(t, pidFile)))
	if err != nil {
		t.Fatalf("the child never started: %v\n%s", err, hookFailureReport(t, st, dir))
	}
	failures, err := st.HookFailures()
	if err != nil {
		t.Fatal(err)
	}
	if len(failures) != 1 || failures[0].Reason != "timed out after 10s" {
		t.Fatalf("failures = %+v", failures)
	}
	child, err := os.FindProcess(pid)
	if err != nil {
		return
	}
	exited := make(chan struct{})
	go func() {
		child.Wait()
		close(exited)
	}()
	select {
	case <-exited:
	case <-time.After(10 * time.Second):
		child.Kill()
		t.Fatalf("child %d outlived the hook timeout", pid)
	}
}

func powerShellHook(t *testing.T, body string) []string {
	t.Helper()
	shell, err := pwsh.Path()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "hook.ps1")
	if err := os.WriteFile(path, pwsh.ScriptFile(body), 0o700); err != nil {
		t.Fatal(err)
	}
	return []string{shell, "-NoProfile", "-NonInteractive", "-File", path}
}

func hookFailureReport(t *testing.T, st *store.Store, dir string) string {
	t.Helper()
	failures, err := st.HookFailures()
	return fmt.Sprintf("failures = %+v (%v)\nhooks.log = %q", failures, err, readFile(t, filepath.Join(dir, LogFile)))
}
