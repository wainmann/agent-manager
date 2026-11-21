package connections

import (
	"slices"
	"testing"

	remote "github.com/YoanWai/agent-manager/internal/remote/value"
)

func TestRowFindsAgentsAndTerminals(t *testing.T) {
	var c Connections
	c.SetList([]remote.Connection{box})
	c.Refreshed("box", c.Gen(), Host{
		OK:        true,
		Sessions:  []Session{{ID: "s1", Name: "api", Group: "web", Status: "idle"}},
		Terminals: []Session{{ID: "t1", Name: "sh", Group: "web", Archived: true}, {ID: "t2", Name: "api-sh", Group: "web", ParentID: "s1"}, {ID: "t3", Name: "api-log", Group: "web", ParentID: "s1", Archived: true}},
	})
	agent, ok := c.Row(Target{Kind: SessionRow, Host: "box", ID: "s1"})
	if !ok || agent.Terminal || agent.Ref != (remote.Ref{Host: "box", ID: "s1"}) || agent.Name != "api" || agent.Group != "web" || agent.Terminals != 2 {
		t.Fatalf("agent row = %+v, %v", agent, ok)
	}
	term, ok := c.Row(Target{Kind: SessionRow, Host: "box", ID: "t1"})
	if !ok || !term.Terminal || !term.Archived {
		t.Fatalf("terminal row = %+v, %v", term, ok)
	}
	if _, ok := c.Row(Target{Kind: SessionRow, Host: "box", ID: "gone"}); ok {
		t.Fatal("a row the snapshot does not list was found")
	}
	if group, ok := c.Row(Target{Kind: GroupRow, Host: "box", Group: "web"}); !ok || group.Kind != GroupRow || group.Group != "web" {
		t.Fatalf("group row = %+v, %v", group, ok)
	}
}

// A group delete on the host takes every row filed under the group or a
// group nested in it, archived ones and terminals too, so the question
// counts them all and the call names them, from the host's last snapshot.
func TestSubtreeCountsWhatAGroupDeleteTakes(t *testing.T) {
	var c Connections
	c.SetList([]remote.Connection{box})
	c.Refreshed("box", c.Gen(), Host{
		OK:     true,
		Groups: []Group{{Path: "web"}, {Path: "web/ui"}, {Path: "web/ui/kit"}, {Path: "webby"}, {Path: "ops"}},
		Sessions: []Session{
			{ID: "s1", Group: "web"}, {ID: "s2", Group: "web/ui/kit", Archived: true},
			{ID: "s3", Group: "webby"}, {ID: "s4", Group: "ops"},
		},
		Terminals: []Session{{ID: "t1", Group: "web/ui", ParentID: "s9"}, {ID: "t2", Group: "ops"}},
	})
	if subgroups, sessions := c.Subtree("box", "web"); subgroups != 2 || !slices.Equal(sessions, []string{"s1", "s2", "t1"}) {
		t.Fatalf("web holds %d subgroups and sessions %q, want 2 and s1, s2, t1", subgroups, sessions)
	}
	if subgroups, sessions := c.Subtree("box", "ops"); subgroups != 0 || !slices.Equal(sessions, []string{"s4", "t2"}) {
		t.Fatalf("ops holds %d subgroups and sessions %q, want 0 and s4, t2", subgroups, sessions)
	}
}

func TestTroubleLabels(t *testing.T) {
	for trouble, want := range map[remote.Trouble]string{
		remote.TroubleUnreachable: "unreachable",
		remote.TroubleMissing:     "agent-manager missing",
		remote.TroubleOutdated:    "agent-manager too old",
		remote.TroubleNewer:       "agent-manager newer",
		remote.TroubleFailed:      "error",
	} {
		if got := TroubleLabel(trouble); got != want {
			t.Errorf("TroubleLabel(%d) = %q, want %q", trouble, got, want)
		}
	}
}
