package sessioncmd

import (
	"os/exec"
	"strings"
	"testing"
)

// The user's rename key on another machine names an agent or a terminal on
// this one at any time, and a managed branch follows the name as it does
// for a local rename.
func TestRenameSessionNamesAnAgentOrATerminalWithNoCaller(t *testing.T) {
	h := newSessionHarness(t)
	repo := newGitRepo(t)
	wanted := true
	created, err := h.sessions.Create("", CreateSessionOptions{Name: "first-name", Directory: repo, Worktree: &wanted})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	renamed, err := h.sessions.RenameSession("", created.ID, "  second name  ")
	if err != nil {
		t.Fatalf("RenameSession: %v", err)
	}
	if renamed.ID != created.ID || renamed.Name != "second name" || renamed.Branch != "am/second-name" {
		t.Fatalf("renamed = %+v", renamed)
	}
	stored, err := h.store.Get(created.ID)
	if err != nil || stored.Name != "second name" || stored.WorktreeBranch != "am/second-name" {
		t.Fatalf("stored row = %+v (%v)", stored, err)
	}
	if out, err := exec.Command("git", "-C", repo, "branch", "--list", "am/second-name").CombinedOutput(); err != nil || !strings.Contains(string(out), "am/second-name") {
		t.Fatalf("git branch after the rename: %q (%v)", out, err)
	}
	if got := statusLeft(t, h, created.ID); !strings.Contains(got, "second name") {
		t.Fatalf("status-left = %q, want the new name", got)
	}

	terminal, err := h.terminals.Create("", CreateTerminalOptions{})
	if err != nil {
		t.Fatalf("create terminal: %v", err)
	}
	if renamed, err := h.sessions.RenameSession("", terminal.ID, "logs"); err != nil || renamed.Name != "logs" {
		t.Fatalf("rename a terminal = %+v (%v)", renamed, err)
	}
}

// A session names itself with rename, once, on a placeholder; renaming
// another one is the user's.
func TestRenameSessionRefusesACallerAndABadTarget(t *testing.T) {
	h := newSessionHarness(t)
	worker := h.addSessionRow(t, "worker")
	if _, err := h.sessions.RenameSession(h.caller.ID, worker, "taken"); err == nil || !strings.Contains(err.Error(), "agent-manager rename") {
		t.Fatalf("RenameSession from a session = %v, want it pointed at rename", err)
	}
	if _, err := h.sessions.RenameSession("", worker, "   "); err == nil || err.Error() != "name cannot be empty" {
		t.Fatalf("an empty name = %v", err)
	}
	if _, err := h.sessions.RenameSession("", "nosuch01", "x"); err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("a missing session = %v", err)
	}
	if stored, err := h.store.Get(worker); err != nil || stored.Name != "worker" {
		t.Fatalf("worker after the refusals = %+v (%v)", stored, err)
	}
}
