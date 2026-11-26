package execution

import (
	"testing"

	"github.com/YoanWai/agent-manager/internal/status"
	"github.com/YoanWai/agent-manager/internal/store"
	"github.com/YoanWai/agent-manager/internal/tmux"
)

func TestHoldSessionsKeepsThePollOffHeldPanesOnly(t *testing.T) {
	m := buildModel(t)
	createSession(t, m, "held", t.TempDir(), "")
	createSession(t, m, "polled", t.TempDir(), "")
	held, polled := m.sessionRows()[0], m.sessionRows()[1]
	testPollMessage(m.poller.Step())
	before, err := m.store.Get(held.ID)
	if err != nil {
		t.Fatal(err)
	}

	err = m.poller.HoldSessions([]string{held.ID}, func() {
		for _, id := range []string{held.ID, polled.ID} {
			if err := m.tmux.Kill(id); err != nil {
				t.Fatal(err)
			}
		}
		testPollMessage(m.poller.Step())
		assertStatus(t, m, held.ID, before.Status)
		assertStatus(t, m, polled.ID, status.Dead)
	})
	if err != nil {
		t.Fatal(err)
	}
	testPollMessage(m.poller.Step())
	assertStatus(t, m, held.ID, status.Dead)
}

func assertStatus(t *testing.T, m *harness, id, want string) {
	t.Helper()
	got, err := m.store.Get(id)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != want {
		t.Fatalf("%s status = %q, want %q", id, got.Status, want)
	}
}

func TestStoppedHoldReportsUnexecutedWork(t *testing.T) {
	runner := &Runner{stopped: true}
	ran := false
	if err := runner.HoldSessions([]string{"a"}, func() { ran = true }); err == nil || ran {
		t.Fatalf("stopped hold: err %v, ran %t", err, ran)
	}
}

func TestHeldSessionMintsNoID(t *testing.T) {
	runner := &Runner{sessionStores: map[string]string{"claude": "/x"}, held: map[string]bool{"a": true}}
	panes := map[string]tmux.Pane{"a": {PID: 1}, "b": {PID: 2}}
	if runner.idMinting(store.Session{ID: "a", Tool: "claude"}, panes) {
		t.Fatal("a held session was offered to id capture")
	}
	if !runner.idMinting(store.Session{ID: "b", Tool: "claude"}, panes) {
		t.Fatal("an unheld session was kept from id capture")
	}
}
