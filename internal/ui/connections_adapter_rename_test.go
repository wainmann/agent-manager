package ui

import (
	"slices"
	"strings"
	"testing"
	"time"

	uirail "github.com/YoanWai/agent-manager/internal/ui/rail"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

// r on a remote agent or terminal types the new name into its row, as it
// does here, and the host renames it; nothing reaches the host before enter.
func TestRemoteRenameTypesTheNameIntoTheRowAndSendsIt(t *testing.T) {
	for _, tc := range []struct{ id, old, name string }{{"s1", "api", "gateway"}, {"t1", "sh", "logs"}} {
		fake := &fakeSSH{}
		m := connectedModel(t, fake)
		m.layout.width, m.layout.height = 120, 34
		m.rail.Focus(boxSession(tc.id))
		m.runRailIntent(uirail.Intent{Kind: uirail.RenameAction, Target: boxSession(tc.id)})
		if !renameOpen(m) || openRename(m).Name() != tc.old {
			t.Fatalf("%s: rename open %v, status %q", tc.id, renameOpen(m), m.errBar.text)
		}
		if hints := openRename(m).Footer(); slices.ContainsFunc(hints, func(h [2]string) bool { return h[0] == "tab" }) {
			t.Fatalf("%s: the remote field offers a tool: %q", tc.id, hints)
		}
		openRename(m).SetName(tc.name)
		if view := ansi.Strip(preparedView(m)); !strings.Contains(view, tc.name) {
			t.Fatalf("%s: the field is not drawn on the remote row:\n%s", tc.id, view)
		}
		if calls := fake.taken(); len(calls) != 0 {
			t.Fatalf("%s: calls before enter = %q", tc.id, calls)
		}
		_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
		m.runRemoteCmd(t, cmd)
		if want := []string{"rename-session", "--json", "--", tc.id, tc.name}; !slices.ContainsFunc(fake.taken(), func(call []string) bool { return slices.Equal(call, want) }) {
			t.Fatalf("%s: calls = %q, want %q", tc.id, fake.taken(), want)
		}
		if want := "renamed " + tc.old + " to " + tc.name + " on box"; m.errBar.text != want {
			t.Fatalf("%s: status = %q, want %q", tc.id, m.errBar.text, want)
		}
		if renameOpen(m) {
			t.Fatalf("%s: the field stayed open after the host renamed it", tc.id)
		}
		if len(m.workspace.sessions) != 0 {
			t.Fatalf("%s: a remote rename reached the local list: %+v", tc.id, m.workspace.sessions)
		}
	}
}

// A name the host refuses keeps the field open on it, as a local rename
// git refuses does.
func TestRemoteRenameTheHostRefusesKeepsTheField(t *testing.T) {
	fake := &fakeSSH{}
	m := connectedModel(t, fake)
	m.runRailIntent(uirail.Intent{Kind: uirail.RenameAction, Target: boxSession("s1")})
	openRename(m).SetName("gateway")
	fake.mu.Lock()
	fake.stderr, fake.code = "agent-manager: worktree rename: branch am/gateway already exists\n", 1
	fake.mu.Unlock()
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m.runRemoteCmd(t, cmd)
	if m.errBar.text != "box: worktree rename: branch am/gateway already exists" {
		t.Fatalf("status = %q", m.errBar.text)
	}
	if !renameOpen(m) || openRename(m).Name() != "gateway" {
		t.Fatalf("rename open %v after the refusal", renameOpen(m))
	}
}

// r on a remote group types its last path part into the row, says its
// subtree moves with it, and the host renames the group in place.
func TestRemoteGroupRenameKeepsItsParentAndSaysTheSubtreeMoves(t *testing.T) {
	fake := &fakeSSH{}
	m := connectedModel(t, fake)
	m.layout.width, m.layout.height = 120, 34
	m.rail.Focus(boxGroup)
	m.runRailIntent(uirail.Intent{Kind: uirail.RenameAction, Target: boxGroup})
	if !renameOpen(m) || openRename(m).Name() != "web" {
		t.Fatalf("rename open %v, status %q", renameOpen(m), m.errBar.text)
	}
	if hints := openRename(m).Footer(); !slices.Contains(hints, [2]string{"↵", "save; its subgroups and sessions move with it"}) {
		t.Fatalf("footer = %q, want the subtree named", hints)
	}
	openRename(m).SetName("frontend")
	if view := ansi.Strip(preparedView(m)); !strings.Contains(view, "frontend") {
		t.Fatalf("the field is not drawn on the remote group:\n%s", view)
	}
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m.runRemoteCmd(t, cmd)
	if want := []string{"rename-group", "--json", "--", "web", "frontend"}; !slices.ContainsFunc(fake.taken(), func(call []string) bool { return slices.Equal(call, want) }) {
		t.Fatalf("calls = %q, want %q", fake.taken(), want)
	}
	if m.errBar.text != "renamed group web to frontend on box" || renameOpen(m) {
		t.Fatalf("status %q, rename open %v", m.errBar.text, renameOpen(m))
	}
}

func TestRemoteRenameRefusesAnOfflineHost(t *testing.T) {
	fake := &fakeSSH{}
	m := connectedModel(t, fake)
	state := hostState(m, "box")
	state.OK, state.Failures, state.Attempted, state.Err = false, 1, time.Now(), "box: unreachable over SSH: connection refused"
	setHost(m, "box", state)
	for _, target := range []uirail.Selection{boxSession("s1"), boxGroup} {
		m.clearErr()
		m.runRailIntent(uirail.Intent{Kind: uirail.RenameAction, Target: target})
		if renameOpen(m) || m.errBar.text != "box: unreachable over SSH: connection refused" {
			t.Fatalf("r on %+v of an offline host: open %v, status %q", target, renameOpen(m), m.errBar.text)
		}
	}
	if calls := fake.taken(); len(calls) != 0 {
		t.Fatalf("calls = %q, want none", calls)
	}
}

// A second enter while a rename is on its way sends nothing: a group's
// second call would name the path the first one changed. A session's field
// keeps a name typed after the sent one; a group's closes, its path gone.
func TestRemoteRenameSendsOneCallPerEnterOnItsWay(t *testing.T) {
	for _, tc := range []struct {
		target   uirail.Selection
		op       string
		stayOpen bool
	}{{boxSession("s1"), "rename-session", true}, {boxGroup, "rename-group", false}} {
		fake := &fakeSSH{}
		m := connectedModel(t, fake)
		m.runRailIntent(uirail.Intent{Kind: uirail.RenameAction, Target: tc.target})
		openRename(m).SetName("a")
		_, first := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
		openRename(m).SetName("ab")
		_, second := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
		if m.errBar.text != "this rename is already on its way" {
			t.Fatalf("%s: second enter status = %q", tc.op, m.errBar.text)
		}
		m.runRemoteCmd(t, first)
		m.runRemoteCmd(t, second)
		var renames [][]string
		for _, call := range fake.taken() {
			if call[0] == tc.op {
				renames = append(renames, call)
			}
		}
		if len(renames) != 1 || renames[0][len(renames[0])-1] != "a" {
			t.Fatalf("%s: calls = %q, want one rename to a", tc.op, renames)
		}
		if renameOpen(m) != tc.stayOpen || tc.stayOpen && openRename(m).Name() != "ab" {
			t.Fatalf("%s: rename open %v after the host took a, want %v holding ab", tc.op, renameOpen(m), tc.stayOpen)
		}
	}
}

// A group's folds here follow it to the path the host gave it, so the old
// path does not come back folded on a later group of that name.
func TestRemoteGroupRenameCarriesItsFolds(t *testing.T) {
	fake := &fakeSSH{}
	m := connectedModel(t, fake)
	m.rail.RestoreHostFolds([]uirail.HostFold{{Host: "box", Path: "web"}})
	m.runRailIntent(uirail.Intent{Kind: uirail.RenameAction, Target: boxGroup})
	openRename(m).SetName("frontend")
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m.runRemoteCmd(t, cmd)
	if want := []uirail.HostFold{{Host: "box", Path: "frontend"}}; !slices.Equal(m.rail.HostCollapsed(), want) {
		t.Fatalf("folds = %+v, want %+v", m.rail.HostCollapsed(), want)
	}
}
