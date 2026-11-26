package execution

import (
	"path/filepath"
	"testing"
	"time"

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

func TestHeldSessionIsNotCapturedUntilReleased(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	codexHome := t.TempDir()
	t.Setenv("CODEX_HOME", codexHome)
	cwd := t.TempDir()
	launch := time.Now().Add(-time.Minute)
	writeCodexRollout(t, filepath.Join(codexHome, "sessions", "rollout-A.jsonl"), "A-id", cwd, launch)
	if err := st.CreateSession(store.Session{ID: "sess-A", Name: "a", Tool: "codex", Cwd: cwd, Group: "g", Status: "idle", CreatedAt: launch}); err != nil {
		t.Fatal(err)
	}
	sess, err := st.Get("sess-A")
	if err != nil {
		t.Fatal(err)
	}
	panes := map[string]tmux.Pane{"sess-A": {PID: 123}}
	p := &Runner{store: st, sessionStores: map[string]string{"codex": "codex"}, held: map[string]bool{"sess-A": true}}
	if !p.idMinting(sess, panes) {
		t.Fatal("a held session dropped out of the shared-scope count")
	}
	if captured, err := p.captureAgentSessionIDs([]store.Session{sess}, panes); err != nil || captured != 0 {
		t.Fatalf("held: captured %d, err %v; want 0", captured, err)
	}
	delete(p.held, "sess-A")
	if captured, err := p.captureAgentSessionIDs([]store.Session{sess}, panes); err != nil || captured != 1 {
		t.Fatalf("released: captured %d, err %v; want 1", captured, err)
	}
}
