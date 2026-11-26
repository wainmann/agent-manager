package rail

import (
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/YoanWai/agent-manager/internal/keybind"
	"github.com/YoanWai/agent-manager/internal/ui/presentation"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

func hostsSnapshot(state HostState) Snapshot {
	return Snapshot{
		Groups: []string{"work"},
		Sessions: []Session{
			{ID: "local", Name: "local", Status: "idle", CreatedAt: time.Now()},
			{ID: "near", Name: "near", Group: "work", Status: "idle", CreatedAt: time.Now()},
		},
		Hosts: []Host{{
			Name:   "box",
			State:  state,
			Groups: []string{"ops"},
			Sessions: []Session{
				{ID: "far", Name: "far", Tool: "claude", Status: "working"},
				{ID: "deep", Name: "deep", Tool: "codex", Group: "ops", Status: "waiting"},
				{ID: "shell", Name: "shell", Tool: "terminal", Group: "ops", ParentID: "deep", IsShell: true, Status: "idle"},
			},
		}},
	}
}

func hostsContext(width, height int) RenderContext {
	return RenderContext{
		Width: width, Height: height, TerminalWidth: 80, TerminalHeight: 24,
		ListKeys: keybind.DefaultList(),
		Theme: Theme{
			Bg: "#101010", Surface: "#202020", Overlay: "#303030", Border: "#404040",
			Bright: "#ffffff", Text: "#dddddd", Dim: "#aaaaaa", Subtle: "#777777",
			Accent: "#00aaaa", Accent2: "#008888", Remote: "#cc66aa", Working: "#ffaa00",
			Waiting: "#aa88ff", Finished: "#00aa00", Errored: "#ff0000", Idle: "#777777",
		},
	}
}

func runes(key string) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)}
}

func rowKeys(m Model) []string {
	var keys []string
	for _, row := range m.rows {
		keys = append(keys, strings.Repeat(" ", row.depth)+row.key())
	}
	return keys
}

func TestConnectionRowsNestUnderTheConnectionAfterTheLocalTree(t *testing.T) {
	model := New(nil)
	model.Reconcile(hostsSnapshot(HostOnline))
	want := []string{
		"g:", "s:local", "g:work", " s:near",
		"c:box", " rs:box::far", " rg:box::ops", "  rs:box::deep", "   rs:box::shell",
	}
	if got := rowKeys(model); !slices.Equal(got, want) {
		t.Fatalf("rows =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	for _, row := range model.Rows() {
		if remote := strings.Contains(row.Selection.key(), "box"); remote != row.Remote() {
			t.Errorf("row %+v: Remote() = %v", row, row.Remote())
		}
	}
}

func TestRemoteSelectionIsNeverALocalSessionOrGroup(t *testing.T) {
	model := New(nil)
	model.Reconcile(hostsSnapshot(HostOnline))
	if !model.Focus(Selection{Kind: SessionRow, SessionID: "far", Host: "box"}) {
		t.Fatal("remote session row not found")
	}
	if _, ok := model.SelectedSession(); ok {
		t.Error("a remote session reads as a local selected session")
	}
	selected, _ := model.Selected()
	if selected.Host != "box" || selected.SessionID != "far" {
		t.Errorf("selected = %+v", selected)
	}
	model.Focus(Selection{Kind: GroupRow, Group: "ops", Host: "box"})
	if _, ok := model.SelectedGroup(); ok {
		t.Error("a remote group reads as a local selected group")
	}
	if model.FocusSession("far") {
		t.Error("FocusSession found a local session on a remote row")
	}
	if ids := model.ListedSessionIDs(); !slices.Equal(ids, []string{"local", "near"}) {
		t.Errorf("listed local sessions = %v", ids)
	}
}

func TestConnectionRowFoldsLikeAGroup(t *testing.T) {
	model := New(nil)
	model.Reconcile(hostsSnapshot(HostOnline))
	model.Focus(Selection{Kind: ConnectionRow, Host: "box"})
	ctx := KeyContext{ListKeys: keybind.DefaultList()}
	decision := model.Key(tea.KeyMsg{Type: tea.KeyEnter}, Frame{}, ctx)
	if len(decision.Mutations) != 1 || !slices.Contains(decision.Mutations[0].HostCollapsed, HostFold{Host: "box"}) || len(decision.Mutations[0].Collapsed) != 0 {
		t.Fatalf("fold decision = %+v", decision)
	}
	if got := rowKeys(model); got[len(got)-1] != "c:box" {
		t.Fatalf("folded connection still shows rows: %v", got)
	}
	if !model.SelectionCollapsed(Selection{Kind: ConnectionRow, Host: "box"}) {
		t.Error("connection does not report folded")
	}
	model.Key(tea.KeyMsg{Type: tea.KeyEnter}, Frame{}, ctx)
	model.Focus(Selection{Kind: GroupRow, Group: "ops", Host: "box"})
	model.Key(tea.KeyMsg{Type: tea.KeyEnter}, Frame{}, ctx)
	if model.collapsed["ops"] || !model.hostFolds[HostFold{Host: "box", Path: "ops"}] {
		t.Errorf("remote group fold landed on %v", model.Collapsed())
	}
}

func TestLocalGroupNamedLikeARemotePathFoldsAlone(t *testing.T) {
	snapshot := hostsSnapshot(HostOnline)
	snapshot.Groups = append(snapshot.Groups, "box::ops")
	snapshot.Sessions = append(snapshot.Sessions, Session{ID: "twin", Name: "twin", Group: "box::ops", Status: "idle", CreatedAt: time.Now()})
	model := New(nil)
	model.Reconcile(snapshot)
	ctx := KeyContext{ListKeys: keybind.DefaultList()}
	model.Focus(Selection{Kind: GroupRow, Group: "ops", Host: "box"})
	model.Key(tea.KeyMsg{Type: tea.KeyEnter}, Frame{}, ctx)
	if model.SelectionCollapsed(Selection{Kind: GroupRow, Group: "box::ops"}) {
		t.Error("folding the remote group folded the local one")
	}
	if !model.SelectionCollapsed(Selection{Kind: GroupRow, Group: "ops", Host: "box"}) {
		t.Error("remote group did not fold")
	}
	model.Reconcile(snapshot)
	if selected, _ := model.Selected(); selected.Host != "box" || selected.Group != "ops" {
		t.Errorf("refresh moved the cursor to %+v", selected)
	}
	model.Focus(Selection{Kind: GroupRow, Group: "box::ops"})
	decision := model.Key(tea.KeyMsg{Type: tea.KeyEnter}, Frame{}, ctx)
	if !slices.Equal(decision.Mutations[0].Collapsed, []string{"box::ops"}) || !slices.Equal(decision.Mutations[0].HostCollapsed, []HostFold{{Host: "box", Path: "ops"}}) {
		t.Errorf("saved folds = %+v", decision.Mutations[0])
	}
	restored := New(decision.Mutations[0].Collapsed)
	restored.RestoreHostFolds(decision.Mutations[0].HostCollapsed)
	if !restored.folded("", "box::ops") || !restored.folded("box", "ops") || restored.folded("box", "") {
		t.Errorf("restored folds: local %v, remote %v", restored.Collapsed(), restored.HostCollapsed())
	}
}

func TestRemoteRowsRefuseReorder(t *testing.T) {
	model := New(nil)
	model.Reconcile(hostsSnapshot(HostOnline))
	ctx := KeyContext{ListKeys: keybind.DefaultList()}
	for _, selection := range []Selection{
		{Kind: SessionRow, SessionID: "far", Host: "box"},
		{Kind: GroupRow, Group: "ops", Host: "box"},
	} {
		model.Focus(selection)
		decision := model.Key(runes("K"), Frame{}, ctx)
		if len(decision.Mutations) != 0 || !strings.Contains(decision.Error, "isn't available on SSH connections yet") {
			t.Errorf("%+v: decision = %+v", selection, decision)
		}
	}
	// The last local root session stepping down must not swap with the
	// remote root session that shares its empty group.
	model.Focus(Selection{Kind: SessionRow, SessionID: "local"})
	if decision := model.Key(runes("J"), Frame{}, ctx); len(decision.Mutations) != 0 {
		t.Errorf("local session swapped across the host boundary: %+v", decision.Mutations)
	}
	model.Focus(Selection{Kind: SessionRow, SessionID: "near", Group: "work"})
	model.enterReorder(model.rows[model.cursor].key())
	if _, ok := model.resolveDrop(Selection{Kind: GroupRow, Group: "ops", Host: "box"}); ok {
		t.Error("a local session can be dropped into a remote group")
	}
	if _, ok := model.resolveDrop(Selection{Kind: ConnectionRow, Host: "box"}); ok {
		t.Error("a local session can be dropped onto a connection")
	}
}

func threeConnections() Snapshot {
	snapshot := hostsSnapshot(HostOnline)
	for _, name := range []string{"gpu", "lab"} {
		snapshot.Hosts = append(snapshot.Hosts, Host{Name: name, State: HostOnline, Sessions: []Session{{ID: name + "1", Name: name, Tool: "claude", Status: "idle"}}})
	}
	return snapshot
}

func hostNames(m Model) []string {
	var names []string
	for _, host := range m.snapshot.Hosts {
		names = append(names, host.Name)
	}
	return names
}

// applied answers a decision's mutations the way root does once the store
// has them.
func applied(m *Model, decision Decision) {
	for _, mutation := range decision.Mutations {
		m.ApplyMutation(mutation, nil)
	}
}

func TestReorderKeyMovesAConnectionPastTheRowsOfItsNeighbour(t *testing.T) {
	model := New(nil)
	model.Reconcile(threeConnections())
	ctx := KeyContext{ListKeys: keybind.DefaultList()}
	model.Focus(Selection{Kind: ConnectionRow, Host: "gpu"})
	decision := model.Key(runes("K"), Frame{}, ctx)
	if len(decision.Mutations) != 1 || decision.Mutations[0].Kind != SwapConnection || decision.Mutations[0].Host != "gpu" || decision.Mutations[0].TargetHost != "box" {
		t.Fatalf("decision = %+v", decision)
	}
	applied(&model, decision)
	if got := hostNames(model); !slices.Equal(got, []string{"gpu", "box", "lab"}) {
		t.Fatalf("connections = %v", got)
	}
	if selected, _ := model.Selected(); selected != (Selection{Kind: ConnectionRow, Host: "gpu"}) {
		t.Errorf("the cursor left the connection it moved: %+v", selected)
	}
	if decision := model.Key(runes("K"), Frame{}, ctx); len(decision.Mutations) != 0 || decision.Error != "connection already at the top of its level" {
		t.Errorf("past the top: %+v", decision)
	}
	model.Focus(Selection{Kind: ConnectionRow, Host: "lab"})
	if decision := model.Key(runes("J"), Frame{}, ctx); len(decision.Mutations) != 0 || decision.Error != "connection already at the bottom of its level" {
		t.Errorf("past the bottom: %+v", decision)
	}
}

func TestDraggingAConnectionReordersItAndEscPutsItBack(t *testing.T) {
	model := New(nil)
	model.Reconcile(threeConnections())
	model.Focus(Selection{Kind: ConnectionRow, Host: "box"})
	model.enterReorder("c:box")
	// A row inside a connection counts as that connection.
	target := Selection{Kind: SessionRow, SessionID: "lab1", Host: "lab"}
	if drop, ok := model.resolveDrop(target); ok {
		t.Fatalf("a connection drops into a row: %+v", drop)
	}
	applied(&model, Decision{Mutations: model.planDrag(target)})
	if got := hostNames(model); !slices.Equal(got, []string{"gpu", "lab", "box"}) {
		t.Fatalf("dragged = %v", got)
	}
	applied(&model, model.Key(tea.KeyMsg{Type: tea.KeyEsc}, Frame{}, KeyContext{ListKeys: keybind.DefaultList()}))
	if got := hostNames(model); !slices.Equal(got, []string{"box", "gpu", "lab"}) {
		t.Fatalf("esc left %v", got)
	}
	if model.Reordering() {
		t.Error("esc kept the row lifted")
	}
	// The local rows sit above every connection, so a drag up there stops
	// at the top of the connections.
	model.Focus(Selection{Kind: ConnectionRow, Host: "lab"})
	model.enterReorder("c:lab")
	applied(&model, Decision{Mutations: model.planDrag(Selection{Kind: GroupRow, Group: "work"})})
	if got := hostNames(model); !slices.Equal(got, []string{"lab", "box", "gpu"}) {
		t.Fatalf("dragged above the local rows = %v", got)
	}
}

func TestConnectionRowRendersItsLabelAndState(t *testing.T) {
	for _, tc := range []struct {
		state HostState
		want  string
	}{
		{HostConnecting, "connecting…"},
		{HostOffline, "unreachable"},
		{HostOnline, "◐ 1  ◆ 1"},
	} {
		model := New(nil)
		snapshot := hostsSnapshot(tc.state)
		snapshot.Hosts[0].Trouble = "unreachable"
		model.Reconcile(snapshot)
		frame := model.Render(hostsContext(48, 14), Frame{})
		var line string
		for _, painted := range frame.Lines {
			if painted.HitOK && painted.Hit.Kind == ConnectionRow {
				line = ansi.Strip(painted.Text)
			}
		}
		if !strings.Contains(line, "box  ssh") || !strings.Contains(line, tc.want) {
			t.Errorf("state %d: connection line %q, want %q", tc.state, line, tc.want)
		}
		if _, grip := frame.Handles["c:box"]; !grip {
			t.Errorf("state %d: a connection row offers no reorder grip", tc.state)
		}
		if _, grip := frame.Handles["rs:box::far"]; grip {
			t.Error("a remote session row offers a reorder grip")
		}
	}
}

func TestConnectionMenusOfferOnlyRemoteActions(t *testing.T) {
	model := New(nil)
	model.Reconcile(hostsSnapshot(HostOnline))
	labels := func(selection Selection) []string {
		index := model.rowIndex(selection.key())
		var got []string
		for _, item := range model.rowMenuItems(model.rows[index]) {
			if item.label != "" {
				got = append(got, item.label)
			}
		}
		return got
	}
	if got := labels(Selection{Kind: ConnectionRow, Host: "box"}); !slices.Contains(got, "Remove connection") || !slices.Contains(got, "Edit connection") {
		t.Errorf("connection menu = %v", got)
	}
	session := labels(Selection{Kind: SessionRow, SessionID: "far", Host: "box"})
	for _, refused := range []string{"Fork", "Review changes", "Open in editor", "Restart"} {
		if slices.Contains(session, refused) {
			t.Errorf("remote session menu offers %s: %v", refused, session)
		}
	}
	if !slices.Contains(session, "Kill") || !slices.Contains(session, "Attach") || !slices.Contains(session, "Delete") || !slices.Contains(session, "Move to group") || !slices.Contains(session, "Rename") {
		t.Errorf("remote session menu = %v", session)
	}
	remapped, err := keybind.Parse("M")
	if err != nil {
		t.Fatal(err)
	}
	// Move remapped to M: the menu names M and M picks it.
	keys := keybind.DefaultList().With(keybind.Move, keybind.Keys(remapped))
	far := Selection{Kind: SessionRow, SessionID: "far", Host: "box"}
	model.openMenu(far, 0, 0, false)
	ctx := hostsContext(48, 14)
	ctx.ListKeys = keys
	if menu := ansi.Strip(strings.Join(renderer{model: model, ctx: ctx, palette: newPalette(ctx.Theme)}.renderMenu(), "\n")); !regexp.MustCompile(`Move to group +M `).MatchString(menu) {
		t.Errorf("remote session menu with move on M:\n%s", menu)
	}
	if decision := model.menuKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("M")}, KeyContext{ListKeys: keys}); decision.Intent != (Intent{Kind: MoveToGroup, Target: far}) {
		t.Errorf("M in the remote session menu = %+v", decision.Intent)
	}
	// The host archives, revives and moves agents only.
	if got := labels(Selection{Kind: SessionRow, SessionID: "shell", Group: "ops", Host: "box"}); !slices.Equal(got, []string{"Attach", "Rename", "Kill"}) {
		t.Errorf("remote terminal menu = %v, want Attach, Rename and Kill", got)
	}
	if got := labels(Selection{Kind: GroupRow}); !slices.Contains(got, "New connection") {
		t.Errorf("root menu = %v, want New connection", got)
	}
	if got := labels(Selection{Kind: GroupRow, Group: "ops", Host: "box"}); !slices.Contains(got, "Rename") || !slices.Contains(got, "Delete") {
		t.Errorf("remote group menu = %v, want Rename and Delete", got)
	}
	model.Reconcile(Snapshot{Hosts: []Host{{Name: "box", State: HostOnline, Sessions: []Session{{ID: "far", Name: "far", Tool: "claude", Status: "dead"}}}}})
	if got := labels(Selection{Kind: SessionRow, SessionID: "far", Host: "box"}); !slices.Equal(got, []string{"Rename", "Move to group", "Revive", "Archive", "Delete"}) {
		t.Errorf("dead remote session menu = %v, want Rename, Move to group, Revive, Archive and Delete", got)
	}
	model.SetArchived(true)
	model.Reconcile(Snapshot{Hosts: []Host{{Name: "box", State: HostOnline, Sessions: []Session{{ID: "far", Name: "far", Tool: "claude", Status: "dead", Archived: true}}}}})
	if got := labels(Selection{Kind: SessionRow, SessionID: "far", Host: "box"}); !slices.Equal(got, []string{"Rename", "Move to group", "Restore", "Delete"}) {
		t.Errorf("archived remote session menu = %v, want Rename, Move to group, Restore and Delete", got)
	}
	model.SetArchived(false)
	model.Reconcile(Snapshot{Hosts: []Host{{Name: "box", State: HostOnline, Sessions: []Session{{ID: "far", Name: "far", Tool: "terminal", IsShell: true, Status: "dead"}}}}})
	if got := labels(Selection{Kind: SessionRow, SessionID: "far", Host: "box"}); !slices.Equal(got, []string{"Rename", "Kill"}) {
		t.Errorf("dead remote terminal menu = %v, want Rename and Kill", got)
	}
	// The archived view deletes only a group's archived rows, which the
	// host has no call for.
	model.SetArchived(true)
	model.Reconcile(Snapshot{Hosts: []Host{{Name: "box", State: HostOnline, Groups: []string{"ops"}, Sessions: []Session{{ID: "old", Name: "old", Tool: "claude", Group: "ops", Status: "dead", Archived: true}}}}})
	if got := labels(Selection{Kind: GroupRow, Group: "ops", Host: "box"}); len(got) == 0 || slices.Contains(got, "Delete") {
		t.Errorf("archived remote group menu = %v, want one without Delete", got)
	}
}

func TestHostFoldsFollowTheirConnection(t *testing.T) {
	model := New([]string{"work"})
	model.RestoreHostFolds([]HostFold{{Host: "box"}, {Host: "box", Path: "ops"}, {Host: "lab", Path: "ops"}})
	decision := model.RenameHost("box", "gpu")
	want := []HostFold{{Host: "gpu"}, {Host: "gpu", Path: "ops"}, {Host: "lab", Path: "ops"}}
	if got := decision.Mutations[0].HostCollapsed; !slices.Equal(got, want) {
		t.Fatalf("after rename: %+v, want %+v", got, want)
	}
	decision = model.ForgetHost("gpu")
	if got := decision.Mutations[0].HostCollapsed; !slices.Equal(got, []HostFold{{Host: "lab", Path: "ops"}}) {
		t.Fatalf("after removal: %+v", got)
	}
	if got := decision.Mutations[0].Collapsed; !slices.Equal(got, []string{"work"}) {
		t.Fatalf("local folds changed: %v", got)
	}
}

func TestCompatibleConnectionFoldsUseTheSelectedMarks(t *testing.T) {
	for _, mode := range []presentation.GlyphMode{presentation.GlyphModeBasic, presentation.GlyphModeAdvanced} {
		ctx := hostsContext(40, 24)
		ctx.GlyphMode = mode
		model := New(nil)
		model.Reconcile(hostsSnapshot(HostOnline))
		r := renderer{model: model, ctx: ctx, palette: newPalette(ctx.Theme)}
		for _, folded := range []bool{false, true} {
			r.model.hostFolds[HostFold{Host: "box"}] = folded
			got := ansi.Strip(r.renderConnection(treeRow{host: "box", kind: ConnectionRow}, false, 40, "", "").text)
			want := "v ="
			if folded {
				want = "> ="
			}
			if mode == presentation.GlyphModeAdvanced {
				want = "▾ ⠿"
				if folded {
					want = "▸ ⠿"
				}
			}
			if !strings.HasPrefix(got, want+" box") {
				t.Fatalf("%s folded=%t connection=%q", mode, folded, got)
			}
		}
	}
}
