//go:build !windows

package tmux

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// attachedClient attaches a real client to session id from a pane of a
// second tmux server, the way a terminal (or ssh -t) holds one, and
// returns a way to type keys into it and whether it is still attached.
func attachedClient(t *testing.T, driver *Driver, id string) (press func(key string), attached func() bool) {
	t.Helper()
	outer := testSocket + "outer"
	run := func(args ...string) *exec.Cmd {
		return exec.Command(Binary, SocketArgs(outer, args...)...)
	}
	t.Cleanup(func() { run("kill-server").Run() })
	attach := ShellQuote(driver.bin) + " -L " + testSocket + " attach-session -t " + sessionName(id)
	if out, err := run("new-session", "-d", "-s", "term", "-x", "100", "-y", "30", "env -u TMUX "+attach+"; sleep 300").CombinedOutput(); err != nil {
		t.Fatalf("outer terminal: %v: %s", err, out)
	}
	attached = func() bool {
		out, err := tmuxCmd("list-clients", "-t", sessionName(id)).Output()
		if err != nil {
			t.Fatalf("list-clients: %v", err)
		}
		return strings.TrimSpace(string(out)) != ""
	}
	waitFor(t, "the client to attach", attached)
	return func(key string) { run("send-keys", "-t", "term", key).Run() }, attached
}

func waitFor(t *testing.T, what string, done func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !done() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// promptHeadStub stands in for the binary's prompt-head check: it records
// the session name tmux passed and answers with the status in answer. Its
// path carries a quote and a #, which the binding must hand on unchanged.
func promptHeadStub(t *testing.T) (check, answer, asked string) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "it's #here")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	answer, asked = filepath.Join(dir, "answer"), filepath.Join(dir, "asked")
	script := "#!/bin/sh\nprintf '%s' \"$2\" > " + ShellQuote(asked) + "\nexit \"$(cat " + ShellQuote(answer) + ")\"\n"
	stub := filepath.Join(dir, "check")
	if err := os.WriteFile(stub, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return ShellQuote(stub) + " prompt-head", answer, asked
}

func footer(t *testing.T, id string) string {
	t.Helper()
	right, err := tmuxCmd("display-message", "-p", "-t", sessionName(id), "#{T:status-right}").CombinedOutput()
	if err != nil {
		t.Fatalf("status-right: %v: %s", err, right)
	}
	return string(right)
}

// Issue 850: with the check set, ← in an attached session detaches at the
// head of the agent's prompt and reaches the agent anywhere else. A pane
// the CLI split off (a Claude Code teammate) keeps its ← and is never
// asked about. Without the check nothing binds ←, and the footer of a
// session that was already running stops naming it.
func TestLeftAtThePromptHeadDetachesAnAttachedClient(t *testing.T) {
	driver := requireTmux(t)
	check, answer, asked := promptHeadStub(t)
	driver.SetPromptHeadCheck(check)
	t.Cleanup(func() {
		driver.SetPromptHeadCheck("")
		driver.EnsureBindings()
	})
	id := "lefthead" + strings.ReplaceAll(time.Now().Format("150405.000000"), ".", "")
	if err := driver.Create(id, paneDir(), "cat -v", nil, 100, 30); err != nil {
		t.Fatalf("Create: %v", err)
	}
	t.Cleanup(func() { driver.Kill(id) })
	if !strings.Contains(footer(t, id), "← prompt start = back") {
		t.Fatalf("the footer does not name ←: %q", footer(t, id))
	}
	if out, err := tmuxCmd("split-window", "-t", sessionName(id), "--", "cat", "-v").CombinedOutput(); err != nil {
		t.Fatalf("split-window: %v: %s", err, out)
	}
	gotLeft := func(target string) func() bool {
		return func() bool {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			out, err := driver.capturePlainContext(ctx, target)
			if err != nil {
				t.Fatalf("capture %s: %v", target, err)
			}
			return strings.Contains(out, "^[[D") || strings.Contains(out, "^[OD")
		}
	}
	answerWith := func(status string) {
		t.Helper()
		if err := os.WriteFile(answer, []byte(status), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	press, attached := attachedClient(t, driver, id)

	answerWith("0")
	press("Left")
	waitFor(t, "← to reach the teammate's pane", gotLeft(windowTarget(id)+".1"))
	if got, err := os.ReadFile(asked); err == nil {
		t.Fatalf("← in a teammate's pane asked about the agent's prompt in %q", got)
	}

	if out, err := tmuxCmd("select-pane", "-t", PaneTarget(id)).CombinedOutput(); err != nil {
		t.Fatalf("select-pane: %v: %s", err, out)
	}
	answerWith("1")
	press("Left")
	waitFor(t, "← to reach the agent", gotLeft(PaneTarget(id)))
	if !attached() {
		t.Fatal("← left the session while the check said the caret was inside the prompt")
	}
	if got, _ := os.ReadFile(asked); string(got) != sessionName(id) {
		t.Fatalf("the check was asked about %q, want %q", got, sessionName(id))
	}

	answerWith("0")
	press("Left")
	waitFor(t, "← at the prompt head to detach", func() bool { return !attached() })

	driver.SetPromptHeadCheck("")
	if err := driver.EnsureBindings(); err != nil {
		t.Fatalf("EnsureBindings: %v", err)
	}
	if line, bound := ownedRootLines(t, tmuxCmd)["Left"]; bound {
		t.Fatalf("no check left ← bound: %q", line)
	}
	if strings.Contains(footer(t, id), "←") {
		t.Fatalf("the footer names ← without the check: %q", footer(t, id))
	}
}
