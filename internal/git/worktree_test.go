package git

import (
	"strings"
	"testing"
)

func TestWorktreeUnsavedNamesWhatRemovalWouldKeep(t *testing.T) {
	driver, dir := testRepo(t)
	write(t, dir, "a.txt", "x")
	commit(t, dir, "seed")
	withRemote(t, dir)
	path, branch, err := driver.AddWorktree(dir, "check", "")
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	unsaved := func() string {
		t.Helper()
		got, err := driver.WorktreeUnsaved(dir, path, branch, "")
		if err != nil {
			t.Fatalf("unsaved: %v", err)
		}
		return got
	}
	if got := unsaved(); got != "" {
		t.Fatalf("a fresh worktree loses nothing, got %q", got)
	}
	write(t, path, "b.txt", "work")
	if got := unsaved(); !strings.Contains(got, "uncommitted or untracked") {
		t.Fatalf("an untracked file is unsaved work, got %q", got)
	}
	commit(t, path, "work")
	if got := unsaved(); !strings.Contains(got, "commits on "+branch) {
		t.Fatalf("an unpushed commit is unsaved work, got %q", got)
	}
	if _, err := driver.run(path, "rev-parse", "HEAD"); err != nil {
		t.Fatalf("the check must leave the worktree in place: %v", err)
	}
}
