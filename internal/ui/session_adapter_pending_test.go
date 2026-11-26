package ui

import (
	"strings"
	"testing"

	"github.com/YoanWai/agent-manager/internal/status"
	"github.com/YoanWai/agent-manager/internal/store"
	uilifecycle "github.com/YoanWai/agent-manager/internal/ui/session/lifecycle"
	tea "github.com/charmbracelet/bubbletea"
)

func TestConfirmedArchiveShowsOnTheRowAndRefusesARepeat(t *testing.T) {
	t.Parallel()
	m := buildModel(t)
	createSession(t, m, "agent", t.TempDir(), "")
	m.selectSessionRow(t, "agent")
	m.archiveSelected()
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})

	if row := railText(t, m)[lineWith(t, railText(t, m), "agent")]; !strings.Contains(row, "archiving…") {
		t.Fatalf("row before the archive ran = %q, want archiving…", row)
	}
	if !m.needsLoaderTick() {
		t.Fatal("a pending row must keep the spinner ticking")
	}
	for name, press := range map[string]func(){
		"archive": func() { m.archiveSelected() },
		"kill":    func() { m.killSelected() },
		"restart": func() { m.restartSelected() },
		"delete":  m.prepareDelete,
	} {
		press()
		if confirmOpen(m) {
			t.Fatalf("%s on a row with an archive in flight opened a card", name)
		}
	}

	m.applyCmd(t, cmd)
	if len(m.ledger.lifecycle.Pending) != 0 {
		t.Fatalf("pending after the archive completed: %v", m.ledger.lifecycle.Pending)
	}
}

func TestFailedActionPutsTheRowBackAndReports(t *testing.T) {
	t.Parallel()
	m := buildModel(t)
	dir := t.TempDir()
	createSession(t, m, "agent", dir, "")
	m.selectSessionRow(t, "agent")
	agent, _ := m.selected()
	m.archiveSelected()
	late := store.Session{ID: newID(), Name: "late-terminal", Tool: shellToolName, Cwd: dir, ParentID: agent.ID, Status: status.Dead}
	if err := m.services.store.CreateSession(late); err != nil {
		t.Fatal(err)
	}
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	m.applyCmd(t, cmd)

	if m.errBar.text == "" {
		t.Fatal("the failed archive reported nothing")
	}
	if row := railText(t, m)[lineWith(t, railText(t, m), "agent")]; strings.Contains(row, "archiving") {
		t.Fatalf("row after a failed archive = %q, still pending", row)
	}
	m.archiveSelected()
	if !confirmOpen(m) {
		t.Fatal("a failed action left the row refusing a new one")
	}
}

func TestGroupActionWithAMemberInFlightIsIgnored(t *testing.T) {
	t.Parallel()
	m := buildModel(t)
	dir := t.TempDir()
	if err := m.services.store.CreateGroup("work", dir); err != nil {
		t.Fatal(err)
	}
	m.applyCmd(t, m.refreshCmd())
	createSession(t, m, "one", dir, "work")
	createSession(t, m, "two", dir, "work")
	m.selectSessionRow(t, "one")
	m.killSelected()
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})

	m.selectGroupRow(t, "work")
	m.archiveSelected()
	if confirmOpen(m) {
		t.Fatal("a group archive over a row with a kill in flight opened a card")
	}
}

func TestAfterTurnEndLeavesAQueuedActionPending(t *testing.T) {
	t.Parallel()
	m := buildModel(t)
	createSession(t, m, "agent", t.TempDir(), "")
	m.selectSessionRow(t, "agent")
	agent, _ := m.selected()
	target := uilifecycle.Target{Action: uilifecycle.Restart, Sessions: []store.Session{agent}}
	m.ledger.lifecycle.MarkPending(target)
	ended := uilifecycle.Target{Action: uilifecycle.Kill, Sessions: []store.Session{agent}}
	m.applyAfterTurn(uilifecycle.AfterTurnRequest{ID: agent.ID}, uilifecycle.AfterTurnResult{Target: ended}, nil)
	if m.ledger.lifecycle.Pending[agent.ID] != uilifecycle.Restart {
		t.Fatalf("an after-turn kill cleared the restart still running, pending = %v", m.ledger.lifecycle.Pending)
	}
}

func TestKillWithAPendingDeadChildIsIgnored(t *testing.T) {
	t.Parallel()
	m := buildModel(t)
	dir := t.TempDir()
	createSession(t, m, "agent", dir, "")
	m.selectSessionRow(t, "agent")
	agent, _ := m.selected()
	child := store.Session{ID: newID(), Name: "child-terminal", Tool: shellToolName, Cwd: dir, ParentID: agent.ID, Status: status.Dead}
	if err := m.services.store.CreateSession(child); err != nil {
		t.Fatal(err)
	}
	m.applyCmd(t, m.refreshCmd())
	m.selectSessionRow(t, "agent")
	m.ledger.lifecycle.MarkPending(uilifecycle.Target{Action: uilifecycle.Revive, Sessions: []store.Session{child}})
	m.killSelected()
	if confirmOpen(m) {
		t.Fatal("kill on a row whose dead child has a revive in flight opened a card")
	}
}
