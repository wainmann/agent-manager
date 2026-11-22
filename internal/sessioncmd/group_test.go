package sessioncmd

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/YoanWai/agent-manager/internal/status"
	"github.com/YoanWai/agent-manager/internal/store"
)

func TestSessionGroupsListAndCreate(t *testing.T) {
	h := newSessionHarness(t)
	created, err := h.sessions.CreateGroup(h.caller.ID, "backend/payments", h.caller.Cwd)
	if err != nil {
		t.Fatalf("CreateGroup: %v", err)
	}
	if created.Path != "backend/payments" || !sameTerminalPath(created.Directory, h.caller.Cwd) {
		t.Fatalf("created group = %+v", created)
	}
	if _, err := h.sessions.CreateGroup(h.caller.ID, "backend/payments", ""); err == nil ||
		!strings.Contains(err.Error(), "already exists") {
		t.Fatalf("duplicate group error = %v", err)
	}
	if _, err := h.sessions.CreateGroup(h.caller.ID, "unknown/child", ""); err == nil ||
		!strings.Contains(err.Error(), "parent group") {
		t.Fatalf("orphan group error = %v", err)
	}
	if _, err := h.sessions.CreateGroup(h.caller.ID, "  ", ""); err == nil {
		t.Fatal("an empty group path should be refused")
	}

	spawned, err := h.sessions.Create(h.caller.ID, CreateSessionOptions{Name: "worker", Group: &created.Path})
	if err != nil {
		t.Fatalf("Create in new group: %v", err)
	}
	if spawned.Group != "backend/payments" {
		t.Fatalf("spawn group = %q", spawned.Group)
	}
	groups, err := h.sessions.Groups(h.caller.ID)
	if err != nil {
		t.Fatalf("Groups: %v", err)
	}
	counts := map[string]int{}
	for _, group := range groups {
		counts[group.Path] = group.Sessions
	}
	if counts["backend"] != 1 || counts["backend/payments"] != 1 {
		t.Fatalf("group counts = %+v", counts)
	}
}

func TestGroupsAreCreatedAndDeletedWithNoCallingSession(t *testing.T) {
	h := newSessionHarness(t)
	created, err := h.sessions.CreateGroup("", "remote", "")
	if err != nil {
		t.Fatalf("CreateGroup with no caller: %v", err)
	}
	if created.Path != "remote" {
		t.Fatalf("created group = %+v", created)
	}
	if _, err := h.sessions.CreateGroup("", "remote", ""); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("duplicate group with no caller = %v", err)
	}
	removal, err := h.sessions.DeleteGroup("", "remote")
	if err != nil {
		t.Fatalf("DeleteGroup with no caller: %v", err)
	}
	if len(removal.Removed) != 1 || removal.Removed[0] != "remote" {
		t.Fatalf("removal = %+v", removal)
	}
}

// A fleet that opened a group for its work has to be able to close it, and
// the sessions still filed there are not what it asked to remove.
func TestDeleteGroupMovesItsSessionsToTheRoot(t *testing.T) {
	h := newSessionHarness(t)
	if _, err := h.sessions.CreateGroup(h.caller.ID, "fleet", ""); err != nil {
		t.Fatalf("CreateGroup: %v", err)
	}
	group := "fleet"
	worker, err := h.sessions.Create(h.caller.ID, CreateSessionOptions{Tool: "resting", Name: "worker", Group: &group})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if worker.Group != "fleet" {
		t.Fatalf("session landed in %q, not the group it was given", worker.Group)
	}

	removal, err := h.sessions.DeleteGroup(h.caller.ID, "fleet")
	if err != nil {
		t.Fatalf("DeleteGroup: %v", err)
	}
	if len(removal.Removed) != 1 || removal.Removed[0] != "fleet" {
		t.Fatalf("removed = %v", removal.Removed)
	}
	if len(removal.Moved) != 1 || removal.Moved[0] != worker.ID {
		t.Fatalf("moved = %v, want the session that was filed there", removal.Moved)
	}

	// The session is the point: it keeps running, at the root.
	after, err := h.store.Get(worker.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if after.Group != "" {
		t.Fatalf("session sits in %q rather than the root", after.Group)
	}
	if !h.driver.Exists(worker.ID) {
		t.Fatal("deleting a group stopped the agent running in it")
	}
	if label := statusLeft(t, h, worker.ID); label != "worker" {
		t.Fatalf("status bar still names the deleted group: %q", label)
	}
	groups, err := h.sessions.Groups(h.caller.ID)
	if err != nil {
		t.Fatalf("Groups: %v", err)
	}
	for _, g := range groups {
		if g.Path == "fleet" {
			t.Fatal("the group survived its deletion")
		}
	}
	if _, err := h.sessions.DeleteGroup(h.caller.ID, "fleet"); err == nil {
		t.Fatal("deleting a group that does not exist was accepted")
	}
}

func TestDeleteGroupTakesItsSubtreeAndKeepsNesting(t *testing.T) {
	h := newStoreSessionHarness(t)
	if _, err := h.sessions.CreateGroup(h.caller.ID, "fleet", ""); err != nil {
		t.Fatalf("CreateGroup: %v", err)
	}
	if _, err := h.sessions.CreateGroup(h.caller.ID, "fleet/backend", ""); err != nil {
		t.Fatalf("CreateGroup nested: %v", err)
	}
	group := "fleet/backend"
	workerID := h.addSessionRow(t, "worker")
	if err := h.store.MoveSession(workerID, group); err != nil {
		t.Fatalf("move session row: %v", err)
	}
	shell := store.Session{
		ID: "aaaa1111", Name: "sh-worker", Tool: "resting",
		Group: "fleet/backend", Status: status.Idle, ParentID: workerID,
	}
	if err := h.store.CreateSession(shell); err != nil {
		t.Fatalf("nest shell: %v", err)
	}

	removal, err := h.sessions.DeleteGroup(h.caller.ID, "fleet")
	if err != nil {
		t.Fatalf("DeleteGroup: %v", err)
	}
	if len(removal.Removed) != 2 {
		t.Fatalf("removed = %v, want the group and its child", removal.Removed)
	}
	if len(removal.Moved) != 2 {
		t.Fatalf("moved = %v, want both sessions from the nested group", removal.Moved)
	}
	after, err := h.store.Get(shell.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if after.Group != "" {
		t.Fatalf("nested session sits in %q rather than the root", after.Group)
	}
	if after.ParentID != workerID {
		t.Fatalf("moving the subtree unhooked the terminal from its agent: parent %q", after.ParentID)
	}
}

func TestMoveFilesASessionAndItsTerminalsUnderAnExistingGroup(t *testing.T) {
	h := newStoreSessionHarness(t)
	if _, err := h.sessions.CreateGroup(h.caller.ID, "backend/plugin", ""); err != nil {
		t.Fatalf("CreateGroup: %v", err)
	}
	workerID := h.addSessionRow(t, "worker")
	if err := h.store.MoveSession(workerID, "backend"); err != nil {
		t.Fatalf("move session row: %v", err)
	}
	shell := store.Session{
		ID: "aaaa2222", Name: "sh-worker", Tool: "resting",
		Group: "backend", Status: status.Idle, ParentID: workerID,
	}
	if err := h.store.CreateSession(shell); err != nil {
		t.Fatalf("nest shell: %v", err)
	}

	moved, err := h.sessions.Move(h.caller.ID, workerID, "/backend/plugin/")
	if err != nil {
		t.Fatalf("Move: %v", err)
	}
	if moved.ID != workerID || moved.Group != "backend/plugin" {
		t.Fatalf("moved = %+v", moved)
	}
	after, err := h.store.Get(shell.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if after.Group != "backend/plugin" || after.ParentID != workerID {
		t.Fatalf("nested terminal sits in %q under %q", after.Group, after.ParentID)
	}

	if moved, err = h.sessions.Move("", workerID, ""); err != nil || moved.Group != "" {
		t.Fatalf("a move to the root with no caller = %+v, %v", moved, err)
	}
}

func TestMoveRefusesAnUnknownTargetAndChangesNothing(t *testing.T) {
	h := newStoreSessionHarness(t)
	workerID := h.addSessionRow(t, "worker")
	if err := h.store.MoveSession(workerID, "backend"); err != nil {
		t.Fatalf("move session row: %v", err)
	}
	if err := h.store.CreateGroup("backend/old", ""); err != nil {
		t.Fatalf("CreateGroup: %v", err)
	}
	if err := h.store.SetGroupArchived("backend/old", true); err != nil {
		t.Fatalf("SetGroupArchived: %v", err)
	}
	for _, tc := range []struct {
		target, group, want string
	}{
		{workerID, "nowhere", `group "nowhere" does not exist`},
		{workerID, "backend/old", `group "backend/old" is archived`},
		{"deadbeef", "backend", "session deadbeef does not exist"},
		{" ", "backend", "session_id is empty"},
	} {
		if _, err := h.sessions.Move(h.caller.ID, tc.target, tc.group); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("Move(%q, %q) = %v, want %q", tc.target, tc.group, err, tc.want)
		}
	}
	after, err := h.store.Get(workerID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if after.Group != "backend" {
		t.Fatalf("a refused move left the session in %q", after.Group)
	}
	groups, err := h.store.Groups()
	if err != nil {
		t.Fatalf("Groups: %v", err)
	}
	for _, g := range groups {
		if g.Name == "nowhere" {
			t.Fatal("a refused move created the group it named")
		}
	}
}

func TestMoveRelabelsTheStatusBarOfARunningSession(t *testing.T) {
	h := newSessionHarness(t)
	for _, path := range []string{"alpha", "repo-a"} {
		if _, err := h.sessions.CreateGroup(h.caller.ID, path, ""); err != nil {
			t.Fatalf("CreateGroup %s: %v", path, err)
		}
	}
	group := "alpha"
	worker, err := h.sessions.Create(h.caller.ID, CreateSessionOptions{Tool: "resting", Name: "worker", Group: &group})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := h.sessions.Move(h.caller.ID, worker.ID, "repo-a"); err != nil {
		t.Fatalf("Move: %v", err)
	}
	if label := statusLeft(t, h, worker.ID); label != "repo-a · worker" {
		t.Fatalf("status bar after the move = %q", label)
	}
}

func TestMoveIntoItsOwnGroupKeepsTheRowInPlace(t *testing.T) {
	h := newStoreSessionHarness(t)
	firstID := h.addSessionRow(t, "first")
	secondID := h.addSessionRow(t, "second")
	for _, id := range []string{firstID, secondID} {
		if err := h.store.MoveSession(id, "backend"); err != nil {
			t.Fatalf("move session row: %v", err)
		}
	}
	if _, err := h.sessions.Move(h.caller.ID, firstID, "backend"); err != nil {
		t.Fatalf("Move: %v", err)
	}
	sessions, err := h.store.ListSessions(false)
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	var order []string
	for _, sess := range sessions {
		if sess.ID == firstID || sess.ID == secondID {
			order = append(order, sess.ID)
		}
	}
	if len(order) != 2 || order[0] != firstID {
		t.Fatalf("backend order = %v, want %s still first", order, firstID)
	}
}

func statusLeft(t *testing.T, h *sessionHarness, id string) string {
	t.Helper()
	out, err := exec.Command("tmux", "-L", h.driver.SocketName(), "show-options", "-v", "-t", "am_"+id, "status-left").CombinedOutput()
	if err != nil {
		t.Fatalf("read status-left of %s: %v: %s", id, err, out)
	}
	return strings.TrimSpace(string(out))
}

// The user's delete key over SSH takes the group with what runs in it:
// subgroups, agents, the terminals under them and archived rows, and a clean
// worktree with its branch; a worktree with unsaved work stays and the
// notice says so. Rows filed elsewhere stay.
func TestDeleteGroupWithSessionsTakesTheSubtreeTheWayTheDeleteKeyDoes(t *testing.T) {
	h := newSessionHarness(t)
	for _, path := range []string{"fleet", "fleet/backend"} {
		if _, err := h.sessions.CreateGroup("", path, ""); err != nil {
			t.Fatalf("CreateGroup %s: %v", path, err)
		}
	}
	fleet, nested := "fleet", "fleet/backend"
	repo := newGitRepo(t)
	wanted := true
	clean, err := h.sessions.Create(h.caller.ID, CreateSessionOptions{Name: "clean-worker", Group: &fleet, Directory: repo, Worktree: &wanted})
	if err != nil {
		t.Fatalf("Create clean-worker: %v", err)
	}
	busy, err := h.sessions.Create(h.caller.ID, CreateSessionOptions{Name: "busy-worker", Group: &nested, Directory: repo, Worktree: &wanted})
	if err != nil {
		t.Fatalf("Create busy-worker: %v", err)
	}
	if err := os.WriteFile(filepath.Join(busy.Directory, "work.txt"), []byte("draft"), 0o644); err != nil {
		t.Fatal(err)
	}
	worker, err := h.sessions.Create(h.caller.ID, CreateSessionOptions{Name: "nested-worker", Group: &nested})
	if err != nil {
		t.Fatalf("Create nested-worker: %v", err)
	}
	terminal, err := h.terminals.Create(worker.ID, CreateTerminalOptions{})
	if err != nil {
		t.Fatalf("terminal Create: %v", err)
	}
	shelved, err := h.sessions.Create(h.caller.ID, CreateSessionOptions{Name: "shelved", Group: &fleet})
	if err != nil {
		t.Fatalf("Create shelved: %v", err)
	}
	if _, err := h.sessions.Archive("", shelved.ID, true); err != nil {
		t.Fatalf("Archive: %v", err)
	}

	confirmed := []string{shelved.ID, terminal.ID, clean.ID, busy.ID, worker.ID}
	deletion, err := h.sessions.DeleteGroupWithSessions("", "fleet", confirmed)
	if err != nil {
		t.Fatalf("DeleteGroupWithSessions: %v", err)
	}
	if want := "worktree kept (has work): " + busy.Directory; !slices.Equal(deletion.Removed, []string{"fleet", "fleet/backend"}) || deletion.Notice != want {
		t.Fatalf("deletion = %+v, want both groups and the notice %q", deletion, want)
	}
	if got := slices.Sorted(slices.Values(deletion.Deleted)); !slices.Equal(got, slices.Sorted(slices.Values(confirmed))) {
		t.Fatalf("deleted %q, want the confirmed %q", deletion.Deleted, confirmed)
	}
	for _, id := range confirmed {
		if _, err := h.store.Get(id); err == nil {
			t.Fatalf("%s still has a row", id)
		}
		if h.driver.Exists(id) {
			t.Fatalf("%s still has a pane", id)
		}
	}
	if _, err := os.Stat(clean.Directory); !os.IsNotExist(err) {
		t.Fatalf("clean worktree still on disk: %v", err)
	}
	branch := exec.Command("git", "rev-parse", "--verify", "--quiet", "refs/heads/"+clean.Branch)
	branch.Dir = repo
	if branch.Run() == nil {
		t.Fatalf("branch %s should be deleted", clean.Branch)
	}
	if _, err := os.Stat(filepath.Join(busy.Directory, "work.txt")); err != nil {
		t.Fatalf("the unsaved work must stay: %v", err)
	}
	if _, err := h.store.Get(h.caller.ID); err != nil || !h.driver.Exists(h.caller.ID) {
		t.Fatalf("a session in another group went with it: %v", err)
	}
	groups, err := h.sessions.Groups("")
	if err != nil {
		t.Fatal(err)
	}
	for _, group := range groups {
		if strings.HasPrefix(group.Path, "fleet") {
			t.Fatalf("group %s survived", group.Path)
		}
	}
}

// The ids are what the user saw in the question. A session that arrived,
// left, or was swapped for another since then refuses the whole delete, as
// the delete key does, and so does an id the host never had.
func TestDeleteGroupWithSessionsRefusesSessionsThatChanged(t *testing.T) {
	h := newSessionHarness(t)
	if _, err := h.sessions.CreateGroup("", "fleet", ""); err != nil {
		t.Fatal(err)
	}
	fleet := "fleet"
	gone, err := h.sessions.Create(h.caller.ID, CreateSessionOptions{Name: "gone-worker", Group: &fleet})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.sessions.Delete("", gone.ID); err != nil {
		t.Fatal(err)
	}
	late, err := h.sessions.Create(h.caller.ID, CreateSessionOptions{Name: "late-worker", Group: &fleet})
	if err != nil {
		t.Fatal(err)
	}
	for name, confirmed := range map[string][]string{
		"added":       nil,
		"removed":     {late.ID, gone.ID},
		"swapped":     {gone.ID},
		"unknown":     {"deadbeef"},
		"malformed":   {late.ID + "; rm -rf ~"},
		"empty":       {""},
		"twice":       {late.ID, late.ID},
		"wrong group": {h.caller.ID},
	} {
		if _, err := h.sessions.DeleteGroupWithSessions("", "fleet", confirmed); err == nil || err.Error() != "sessions in fleet changed since confirmation; review them and try again" {
			t.Fatalf("%s %q = %v, want the refusal", name, confirmed, err)
		}
	}
	if _, err := h.store.Get(late.ID); err != nil || !h.driver.Exists(late.ID) {
		t.Fatalf("a refused delete must keep the session running: %v", err)
	}
	if groups, err := h.sessions.Groups(""); err != nil || !slices.ContainsFunc(groups, func(group Group) bool { return group.Path == "fleet" }) {
		t.Fatalf("a refused delete must keep the group: %+v, %v", groups, err)
	}
	if _, err := h.sessions.DeleteGroupWithSessions("", "nowhere", nil); err == nil || !strings.Contains(err.Error(), `group "nowhere" does not exist`) {
		t.Fatalf("a missing group = %v", err)
	}
}

// An empty group the user confirmed as empty goes with nothing else.
func TestDeleteGroupWithSessionsDeletesAnEmptyGroup(t *testing.T) {
	h := newSessionHarness(t)
	if _, err := h.sessions.CreateGroup("", "idle", ""); err != nil {
		t.Fatal(err)
	}
	deletion, err := h.sessions.DeleteGroupWithSessions("", "idle", nil)
	if err != nil || !slices.Equal(deletion.Removed, []string{"idle"}) || len(deletion.Deleted) != 0 {
		t.Fatalf("deletion = %+v, %v", deletion, err)
	}
	if _, err := h.store.Get(h.caller.ID); err != nil || !h.driver.Exists(h.caller.ID) {
		t.Fatalf("a session outside the group went with it: %v", err)
	}
}

// An agent tidying up deletes the group alone; what runs in it is the user's.
func TestDeleteGroupWithSessionsRefusesACallingSession(t *testing.T) {
	h := newSessionHarness(t)
	if _, err := h.sessions.DeleteGroupWithSessions(h.caller.ID, "backend", []string{h.caller.ID}); err == nil || !strings.Contains(err.Error(), "delete-group without --with-sessions") {
		t.Fatalf("a session deleting its own group with it = %v, want a refusal", err)
	}
	if _, err := h.store.Get(h.caller.ID); err != nil || !h.driver.Exists(h.caller.ID) {
		t.Fatalf("a refused delete must keep the caller running: %v", err)
	}
}

// The user's rename key on another machine renames a group where it stands,
// and its subgroups and every session filed under it follow, as the local
// edit card's name change does.
func TestRenameGroupMovesItsSubtreeWithNoCaller(t *testing.T) {
	h := newSessionHarness(t)
	for _, path := range []string{"fleet", "fleet/web", "fleet/web/jobs"} {
		if _, err := h.sessions.CreateGroup("", path, ""); err != nil {
			t.Fatalf("CreateGroup %s: %v", path, err)
		}
	}
	group := "fleet/web/jobs"
	worker, err := h.sessions.Create("", CreateSessionOptions{Name: "worker", Group: &group})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	renamed, err := h.sessions.RenameGroup("", "fleet/web", " gateway ")
	if err != nil {
		t.Fatalf("RenameGroup: %v", err)
	}
	if renamed.Path != "fleet/gateway" {
		t.Fatalf("renamed = %+v", renamed)
	}
	groups, err := h.sessions.Groups("")
	if err != nil {
		t.Fatalf("Groups: %v", err)
	}
	var paths []string
	for _, g := range groups {
		paths = append(paths, g.Path)
	}
	if !slices.Contains(paths, "fleet/gateway/jobs") || slices.Contains(paths, "fleet/web") || slices.Contains(paths, "fleet/web/jobs") {
		t.Fatalf("groups after the rename = %v", paths)
	}
	if stored, err := h.store.Get(worker.ID); err != nil || stored.Group != "fleet/gateway/jobs" {
		t.Fatalf("worker after the rename = %+v (%v)", stored, err)
	}
	if got := statusLeft(t, h, worker.ID); !strings.Contains(got, "fleet/gateway/jobs") {
		t.Fatalf("status-left = %q, want the new group", got)
	}
}

func TestRenameGroupRefusesACallerAndABadName(t *testing.T) {
	h := newStoreSessionHarness(t)
	for _, path := range []string{"fleet", "other"} {
		if _, err := h.sessions.CreateGroup("", path, ""); err != nil {
			t.Fatalf("CreateGroup %s: %v", path, err)
		}
	}
	for _, tc := range []struct{ caller, path, name, want string }{
		{h.caller.ID, "fleet", "x", "agent-manager"},
		{"", "fleet", "  ", "group name cannot be empty"},
		{"", "fleet", "a/b", "group name cannot contain /"},
		{"", "nosuch", "x", `group "nosuch" does not exist`},
		{"", "fleet", "other", "group other already exists"},
	} {
		if _, err := h.sessions.RenameGroup(tc.caller, tc.path, tc.name); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("RenameGroup(%q, %q, %q) = %v, want %q", tc.caller, tc.path, tc.name, err, tc.want)
		}
	}
}
