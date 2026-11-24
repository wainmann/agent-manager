package rail_test

import (
	"slices"
	"testing"

	"github.com/YoanWai/agent-manager/internal/keybind"
	"github.com/YoanWai/agent-manager/internal/ui/rail"
)

func TestRowLegendReadsTheRowRootPasses(t *testing.T) {
	model := rail.New([]string{"work"})
	ctx := rail.LegendContext{ListKeys: keybind.DefaultList(), EnterFocuses: true}
	if legend := model.RowLegend(ctx); legend.Title != "" || legend.Pairs != nil {
		t.Fatalf("no row should give no tier, got %+v", legend)
	}

	ctx.RowOK, ctx.Row = true, rail.Selection{Kind: rail.GroupRow, Group: "work"}
	group := model.RowLegend(ctx)
	if group.Title != "Group" || !slices.Contains(group.Pairs, [2]string{"↵", "unfold"}) {
		t.Fatalf("a folded group should offer unfolding, got %+v", group)
	}

	ctx.Row = rail.Selection{Kind: rail.SessionRow, SessionID: "sh"}
	ctx.Session = rail.Session{ID: "sh", Status: "finished", IsShell: true}
	shell := model.RowLegend(ctx)
	if shell.Title != "Shell" || shell.Leads != nil || shell.Hint != "" {
		t.Fatalf("a shell row should drop the conversation leads, got %+v", shell)
	}
	if !slices.ContainsFunc(shell.Pairs, func(pair [2]string) bool { return pair[1] == "mark idle" }) {
		t.Fatalf("a finished row should offer mark idle, got %+v", shell.Pairs)
	}
}

func TestViewLegendCarriesTheUnreadAlert(t *testing.T) {
	model := rail.New(nil)
	ctx := rail.LegendContext{ListKeys: keybind.DefaultList()}
	if legend := model.ViewLegend(ctx); legend.Alerts != nil || !legend.Quiet {
		t.Fatalf("nothing unread should raise no alert, got %+v", legend)
	}
	ctx.Unread = [2]string{"m", "unread"}
	if legend := model.ViewLegend(ctx); !slices.Equal(legend.Alerts, [][2]string{{"m", "unread"}}) {
		t.Fatalf("unread messages should lead the tier, got %+v", legend.Alerts)
	}
	ctx.Unread = [2]string{}
	ctx.Messages = [2]string{"m", "messages"}
	legend := model.ViewLegend(ctx)
	if legend.Alerts != nil || len(legend.Pairs) == 0 || legend.Pairs[0] != [2]string{"m", "messages"} {
		t.Fatalf("read messages should lead the quiet pairs without an alert, got %+v", legend)
	}
}

func TestRootLegendOnlyOffersApplicableActions(t *testing.T) {
	model := rail.New(nil)
	ctx := rail.LegendContext{ListKeys: keybind.DefaultList(), RowOK: true, Row: rail.Selection{Kind: rail.GroupRow}}
	for _, archived := range []bool{false, true} {
		model.SetArchived(archived)
		legend := model.RowLegend(ctx)
		for _, pair := range legend.Pairs {
			switch pair[1] {
			case "rename", "move", "delete", "fold", "unfold", "archive", "restore", "close / open":
				t.Fatalf("archived=%v root offers %v", archived, pair)
			}
		}
	}
	model.SetArchived(true)
	for _, pair := range model.ViewLegend(ctx).Pairs {
		if pair[1] == "terminal" {
			t.Fatal("archived view offers terminal")
		}
	}
}

func TestRemoteLegendOffersDeleteOnAnAgentOnly(t *testing.T) {
	model := rail.New(nil)
	ctx := rail.LegendContext{ListKeys: keybind.DefaultList()}
	agent := model.RemoteLegend(ctx, rail.RemoteRow{Kind: rail.SessionRow, Host: "box"}, false)
	if !slices.Contains(agent.Pairs, [2]string{"d", "delete"}) {
		t.Fatalf("a remote agent should offer delete, got %+v", agent.Pairs)
	}
	// d on a shell closes it, which x already offers.
	shell := model.RemoteLegend(ctx, rail.RemoteRow{Kind: rail.SessionRow, Host: "box", Terminal: true}, false)
	if slices.Contains(shell.Pairs, [2]string{"d", "delete"}) {
		t.Fatalf("a remote shell should leave delete to close, got %+v", shell.Pairs)
	}
}

// d on a remote group deletes it there in the active view only; the
// archived view's delete has no host call.
func TestRemoteGroupLegendOffersDeleteOutsideTheArchive(t *testing.T) {
	model := rail.New(nil)
	ctx := rail.LegendContext{ListKeys: keybind.DefaultList()}
	group := rail.RemoteRow{Kind: rail.GroupRow, Host: "box"}
	if legend := model.RemoteLegendInView(ctx, group, false, false); !slices.Contains(legend.Pairs, [2]string{"d", "delete"}) {
		t.Fatalf("a remote group should offer delete, got %+v", legend.Pairs)
	}
	if legend := model.RemoteLegendInView(ctx, group, false, true); slices.ContainsFunc(legend.Pairs, func(pair [2]string) bool { return pair[1] == "delete" }) {
		t.Fatalf("a remote group in the archived view should not offer delete, got %+v", legend.Pairs)
	}
}

func TestRemoteLegendOffersRenameOnAgentsShellsAndGroups(t *testing.T) {
	model := rail.New(nil)
	ctx := rail.LegendContext{ListKeys: keybind.DefaultList()}
	for _, row := range []rail.RemoteRow{{Kind: rail.SessionRow, Host: "box"}, {Kind: rail.SessionRow, Host: "box", Terminal: true}, {Kind: rail.GroupRow, Host: "box"}} {
		if legend := model.RemoteLegend(ctx, row, false); !slices.Contains(legend.Pairs, [2]string{"r", "rename"}) {
			t.Fatalf("%s should offer rename, got %+v", legend.Title, legend.Pairs)
		}
	}
}

func TestRemoteLegendOffersMoveOnAnAgentOnly(t *testing.T) {
	model := rail.New(nil)
	remapped, err := keybind.Parse("M")
	if err != nil {
		t.Fatal(err)
	}
	for key, keys := range map[string]keybind.Table{
		"m": keybind.DefaultList(),
		"M": keybind.DefaultList().With(keybind.Move, keybind.Keys(remapped)),
	} {
		ctx := rail.LegendContext{ListKeys: keys}
		agent := model.RemoteLegend(ctx, rail.RemoteRow{Kind: rail.SessionRow, Host: "box"}, false)
		if !slices.Contains(agent.Pairs, [2]string{key, "move"}) {
			t.Fatalf("a remote agent should offer move on %s, got %+v", key, agent.Pairs)
		}
		shell := model.RemoteLegend(ctx, rail.RemoteRow{Kind: rail.SessionRow, Host: "box", Terminal: true}, false)
		if slices.ContainsFunc(shell.Pairs, func(pair [2]string) bool { return pair[1] == "move" }) {
			t.Fatalf("a remote shell is neither moved nor nested there, got %+v", shell.Pairs)
		}
	}
}
