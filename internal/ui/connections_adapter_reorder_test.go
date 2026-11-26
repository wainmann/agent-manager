package ui

import (
	"slices"
	"testing"

	"github.com/YoanWai/agent-manager/internal/store"
	uilink "github.com/YoanWai/agent-manager/internal/ui/connections/link"
	uirail "github.com/YoanWai/agent-manager/internal/ui/rail"
	tea "github.com/charmbracelet/bubbletea"
)

// connectionsModel stores and lists the named connections, none refreshed.
func connectionsModel(t *testing.T, fake *fakeSSH, names ...string) *Model {
	t.Helper()
	m := listedModel(t, fake)
	for _, name := range names {
		if err := m.services.store.AddConnection(store.Connection{Name: name, Destination: "me@" + name}); err != nil {
			t.Fatal(err)
		}
	}
	stored, err := m.services.store.Connections()
	if err != nil {
		t.Fatal(err)
	}
	m.ssh = uilink.New(m.ssh.Client, stored)
	m.rebuildRows()
	return m
}

func listedConnections(m *Model) []string {
	var names []string
	for _, conn := range m.ssh.List() {
		names = append(names, conn.Name)
	}
	return names
}

func storedConnections(t *testing.T, m *Model) []string {
	t.Helper()
	stored, err := m.services.store.Connections()
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, conn := range stored {
		names = append(names, conn.Name)
	}
	return names
}

// pressList sends a list key and runs all it started.
func (m *Model) pressList(t *testing.T, key string) {
	t.Helper()
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)})
	m.runRemoteCmd(t, cmd)
}

// A connection's place is this machine's own, so no host hears of a move,
// and a refresh that was under way still lands.
func TestReorderingAConnectionStaysOnThisMachine(t *testing.T) {
	fake := &fakeSSH{}
	m := connectionsModel(t, fake, "box", "gpu", "lab")
	refresh := m.ssh.PollHost("box")
	m.rail.Focus(uirail.Selection{Kind: uirail.ConnectionRow, Host: "box"})
	m.pressList(t, "J")
	want := []string{"gpu", "box", "lab"}
	if got := storedConnections(t, m); !slices.Equal(got, want) {
		t.Fatalf("stored = %v, want %v", got, want)
	}
	if got := listedConnections(m); !slices.Equal(got, want) {
		t.Fatalf("listed = %v, want %v", got, want)
	}
	if calls := fake.taken(); len(calls) != 0 {
		t.Fatalf("calls = %q, want none", calls)
	}
	m.applyTestMsg(t, refresh())
	if !hostState(m, "box").OK {
		t.Error("the refresh started before the move was dropped")
	}
	if selected, _ := m.rail.Selected(); selected != boxRow {
		t.Errorf("selected = %+v", selected)
	}
}

// Another manager on this machine removed the connection meanwhile.
func TestReorderingPastARemovedConnectionReportsIt(t *testing.T) {
	m := connectionsModel(t, &fakeSSH{}, "box", "gpu")
	if err := m.services.store.DeleteConnection("gpu"); err != nil {
		t.Fatal(err)
	}
	m.rail.Focus(boxRow)
	m.pressList(t, "J")
	if m.errBar.text != `connection "gpu": connection does not exist` {
		t.Errorf("status = %q", m.errBar.text)
	}
	if got := listedConnections(m); !slices.Equal(got, []string{"box", "gpu"}) {
		t.Errorf("listed = %v", got)
	}
}
