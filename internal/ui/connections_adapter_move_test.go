package ui

import (
	"slices"
	"testing"

	"github.com/YoanWai/agent-manager/internal/remote"
	"github.com/YoanWai/agent-manager/internal/sessioncmd"
	uilink "github.com/YoanWai/agent-manager/internal/ui/connections/link"
	uidialogs "github.com/YoanWai/agent-manager/internal/ui/dialogs"
	uirail "github.com/YoanWai/agent-manager/internal/ui/rail"
	tea "github.com/charmbracelet/bubbletea"
)

// A move on a remote agent picks from that host's groups alone, which may
// take it, and the host files it there.
func TestRemoteMovePicksAmongTheHostsGroups(t *testing.T) {
	fake := &fakeSSH{}
	m := listedModel(t, fake)
	m.workspace.groups = []string{"local-only"}
	fake.snapshot.Groups = append(fake.snapshot.Groups, sessioncmd.Group{Path: "ops"}, sessioncmd.Group{Path: "ops/db"},
		sessioncmd.Group{Path: "shelf", Archived: true}, sessioncmd.Group{Path: "shelf/inner"})
	fake.snapshot.Sessions = append(fake.snapshot.Sessions,
		sessioncmd.Session{ID: "s4", Name: "stored", Tool: "claude", Group: "shelf/inner", Status: "dead", Archived: true})
	setHost(m, "box", uilink.Facts(remote.HostState{OK: true, Snapshot: fake.snapshot}))
	picked := func() (paths []string, cursor string) {
		card, ok := surfaceOf[*uidialogs.MoveSurface](m)
		if !ok {
			t.Fatalf("no move card, status %q", m.errBar.text)
		}
		for _, option := range card.Picker().Groups {
			paths = append(paths, option.Path)
		}
		return paths, card.Picker().GroupPath()
	}
	press := func(msg tea.KeyMsg) {
		_, cmd := m.Update(msg)
		m.runRemoteCmd(t, cmd)
	}

	// The host refuses an archived group, so the card offers none but the
	// agent's own.
	m.runRailIntent(uirail.Intent{Kind: uirail.MoveToGroup, Target: boxSession("s1")})
	if paths, cursor := picked(); !slices.Equal(paths, []string{"", "web", "ops", "ops/db"}) || cursor != "web" {
		t.Fatalf("targets %q on %q, want the host's open groups on web", paths, cursor)
	}
	press(tea.KeyMsg{Type: tea.KeyEnter})
	if calls := fake.taken(); moveOpen(m) || len(calls) != 0 {
		t.Fatalf("enter on its own group: open %v, calls %q", moveOpen(m), calls)
	}

	m.runRailIntent(uirail.Intent{Kind: uirail.MoveToGroup, Target: boxSession("s1")})
	press(tea.KeyMsg{Type: tea.KeyDown})
	press(tea.KeyMsg{Type: tea.KeyEnter})
	if calls := fake.taken(); moveOpen(m) || len(calls) == 0 || !slices.Equal(calls[0], []string{"move", "--json", "--", "s1", "ops"}) {
		t.Fatalf("calls = %q, want the move to ops first", calls)
	}
	if m.errBar.text != "moved api to ops on box" {
		t.Fatalf("status = %q", m.errBar.text)
	}

	// An archived agent in an archived group opens on its own group, and
	// enter leaves it there, as the local card does.
	fake.mu.Lock()
	fake.calls = nil
	fake.mu.Unlock()
	m.clearErr()
	m.runRailIntent(uirail.Intent{Kind: uirail.MoveToGroup, Target: uirail.Selection{Kind: uirail.SessionRow, SessionID: "s4", Group: "shelf/inner", Host: "box"}})
	if paths, cursor := picked(); !slices.Equal(paths, []string{"", "web", "ops", "ops/db", "shelf", "shelf/inner"}) || cursor != "shelf/inner" {
		t.Fatalf("targets %q on %q, want its own archived group kept and selected", paths, cursor)
	}
	press(tea.KeyMsg{Type: tea.KeyEnter})
	if calls := fake.taken(); moveOpen(m) || len(calls) != 0 || m.errBar.text != "" {
		t.Fatalf("enter on its own archived group: open %v, calls %q, status %q", moveOpen(m), calls, m.errBar.text)
	}
}

// A pick is weighed against the group the agent is in now, not the one it
// was in when the card opened.
func TestRemoteMoveComparesWithTheGroupTheAgentIsInNow(t *testing.T) {
	fake := &fakeSSH{}
	m := listedModel(t, fake)
	fake.snapshot.Groups = append(fake.snapshot.Groups, sessioncmd.Group{Path: "ops"})
	setHost(m, "box", uilink.Facts(remote.HostState{OK: true, Snapshot: fake.snapshot}))
	m.runRailIntent(uirail.Intent{Kind: uirail.MoveToGroup, Target: boxSession("s1")})

	moved := fake.snapshot
	moved.Sessions = slices.Clone(moved.Sessions)
	for i := range moved.Sessions {
		if moved.Sessions[i].ID == "s1" {
			moved.Sessions[i].Group = "ops"
		}
	}
	setHost(m, "box", uilink.Facts(remote.HostState{OK: true, Snapshot: moved}))

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m.runRemoteCmd(t, cmd)
	if calls := fake.taken(); len(calls) == 0 || !slices.Equal(calls[0], []string{"move", "--json", "--", "s1", "web"}) {
		t.Fatalf("calls = %q, want the move back to web", calls)
	}
}
