package lifecycle

import (
	"testing"
	"time"

	"github.com/YoanWai/agent-manager/internal/store"
)

func session(id string) store.Session { return store.Session{ID: id, Name: id} }

// A listing captured before a launch carries the row over, so a poll cannot
// blink a session this run just spawned off screen.
func TestPendingLaunchSurvivesListingCapturedBefore(t *testing.T) {
	t.Parallel()
	state := State{}
	launchedAt := time.Now()
	state.RecordLaunch("new", launchedAt)

	got := state.KeepPendingLaunches(nil, []store.Session{session("new")}, launchedAt.Add(-time.Second))
	if len(got) != 1 || got[0].ID != "new" {
		t.Fatalf("kept %+v, want the launched row", got)
	}
}

// The first listing to read the store after a launch is the authority on it,
// whether it reports the row or its absence.
func TestListingAfterLaunchRetiresThePendingRow(t *testing.T) {
	t.Parallel()
	state := State{}
	launchedAt := time.Now()
	state.RecordLaunch("new", launchedAt)

	got := state.KeepPendingLaunches(nil, []store.Session{session("new")}, launchedAt.Add(time.Second))
	if len(got) != 0 {
		t.Fatalf("kept %+v, want none", got)
	}
	if _, still := state.Launched["new"]; still {
		t.Fatal("a listing that postdates the launch must retire it")
	}
}

// A stale copy of a deleted row is dropped and a stale copy of an archive has
// its flag corrected, so the row cannot blink back for a frame.
func TestStaleListingIsReconciledAgainstThisRun(t *testing.T) {
	t.Parallel()
	state := State{}
	state.MarkSession("gone", GoneMark{Deleted: true})
	state.MarkSession("archived", GoneMark{Archived: true})

	got := state.DropRecentlyRemoved([]store.Session{session("gone"), session("archived")}, time.Now().Add(-time.Minute))
	if len(got) != 1 || got[0].ID != "archived" || !got[0].Archived {
		t.Fatalf("kept %+v, want the archive with its flag", got)
	}
}

// A listing that postdates a change retires the marker instead.
func TestFreshListingRetiresTheMarkers(t *testing.T) {
	t.Parallel()
	state := State{}
	state.MarkSession("archived", GoneMark{Archived: true})
	listedAt := time.Now().Add(time.Minute)

	got := state.DropRecentlyRemoved([]store.Session{session("archived")}, listedAt)
	if len(got) != 1 {
		t.Fatalf("kept %+v", got)
	}
	if len(state.Gone) != 0 {
		t.Fatalf("markers outlived a listing that postdates them: %v", state.Gone)
	}
}

// A deleted group's header cannot hang back onto the tree, and its metadata
// goes with it.
func TestDeletedGroupLeavesTheListing(t *testing.T) {
	t.Parallel()
	state := State{}
	state.MarkGroup("grp", GoneMark{Deleted: true})
	listing := Listing{
		Groups:         []string{"grp", "other"},
		GroupPaths:     map[string]string{"grp": "/tmp/grp", "other": "/tmp/other"},
		GroupWorktrees: map[string]string{"grp": "on"},
		GroupBases:     map[string]string{"grp": "main"},
		ArchivedGroups: map[string]bool{"grp": true},
	}

	listing.ListedAt = time.Now().Add(-time.Minute)
	got := state.ReconcileListing(listing)
	if len(got.Groups) != 1 || got.Groups[0] != "other" {
		t.Fatalf("groups = %v", got.Groups)
	}
	if _, ok := got.GroupPaths["grp"]; ok {
		t.Fatal("deleted group kept its path")
	}
	if _, ok := got.GroupBases["grp"]; ok {
		t.Fatal("deleted group kept its base")
	}
}

// A queued action marks every row it targets until its run completes.
func TestPendingCoversTheTargetUntilCleared(t *testing.T) {
	t.Parallel()
	state := State{}
	target := Target{Action: Archive, Sessions: []store.Session{session("a"), session("b")}}
	state.MarkPending(target)

	if got := PendingWord(state.Pending["b"]); got != "archiving" {
		t.Fatalf("pending word = %q, want archiving", got)
	}
	if !state.Overlaps(Target{Action: Kill, Sessions: []store.Session{session("c"), session("a")}}) {
		t.Fatal("a set holding a pending row must read as pending")
	}
	state.ClearPending(target)
	if state.Overlaps(target) {
		t.Fatalf("cleared target still pending: %v", state.Pending)
	}
}

func TestPendingWordNamesEachAction(t *testing.T) {
	for action, want := range map[Action]string{
		Archive: "archiving", Restore: "restoring", Remove: "deleting",
		Kill: "killing", Restart: "restarting", Revive: "reviving", None: "",
	} {
		if got := PendingWord(action); got != want {
			t.Errorf("PendingWord(%v) = %q, want %q", action, got, want)
		}
	}
}

// A run reserves the rows it waits on and an empty group's path, both ways.
func TestPendingReservesMembersAndGroupPaths(t *testing.T) {
	t.Parallel()
	state := State{}
	kill := Target{Action: Kill, Sessions: []store.Session{session("agent")},
		Selection: Selection{Kind: SelectSession, RootID: "agent", Members: []store.Session{session("agent"), session("shell")}}}
	state.MarkPending(kill)
	if !state.Overlaps(Target{Action: Revive, Sessions: []store.Session{session("shell")}}) {
		t.Fatal("a revive on a terminal the kill skipped was admitted")
	}
	if state.Overlaps(Target{Action: Revive, Sessions: []store.Session{session("other")}}) {
		t.Fatal("an unrelated row was refused")
	}
	state.ClearPending(kill)
	if state.Overlaps(Target{Action: Revive, Sessions: []store.Session{session("shell")}}) {
		t.Fatal("the cleared kill still reserves its terminal")
	}

	web := Target{Action: Remove, Path: "web"}
	state.MarkPending(web)
	for path, want := range map[string]bool{"web": true, "web/api": true, "website": false} {
		if got := state.Overlaps(Target{Action: Remove, Path: path}); got != want {
			t.Fatalf("delete of %s overlaps = %t, want %t", path, got, want)
		}
	}
	state.ClearPending(web)
	if state.Overlaps(Target{Action: Remove, Path: "web/api"}) {
		t.Fatal("the cleared group delete still reserves its path")
	}
}

func TestGroupArchiveAndRestoreReserveTheirPath(t *testing.T) {
	t.Parallel()
	for _, action := range []Action{Archive, Restore} {
		state := State{}
		state.MarkPending(Target{Action: action, Path: "web"})
		if !state.Overlaps(Target{Action: action, Path: "web"}) {
			t.Fatalf("%v: a repeat on the same empty group was admitted", action)
		}
		if state.Overlaps(Target{Action: action, Path: "website"}) {
			t.Fatalf("%v: an unrelated group was refused", action)
		}
	}
}
