package execution

import (
	"os"
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
	requirePoll(t, m)
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
		requirePoll(t, m)
		assertStatus(t, m, held.ID, before.Status)
		assertStatus(t, m, polled.ID, status.Dead)
	})
	if err != nil {
		t.Fatal(err)
	}
	requirePoll(t, m)
	assertStatus(t, m, held.ID, status.Dead)
}

func requirePoll(t *testing.T, m *harness) {
	t.Helper()
	if result := m.poller.Step(); result.Err != nil {
		t.Fatal(result.Err)
	}
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
	if captured, err := p.captureAgentSessionIDs([]store.Session{sess}, panes, p.holdGeneration()); err != nil || captured != 0 {
		t.Fatalf("held: captured %d, err %v; want 0", captured, err)
	}
	delete(p.held, "sess-A")
	if captured, err := p.captureAgentSessionIDs([]store.Session{sess}, panes, p.holdGeneration()); err != nil || captured != 1 {
		t.Fatalf("released: captured %d, err %v; want 1", captured, err)
	}
}

func TestIntervalCPUNeedsABaseline(t *testing.T) {
	prev := map[int]float64{7: 40}
	if delta, seen := intervalCPU(prev, 7, 41.5); !seen || delta != 1.5 {
		t.Fatalf("pid with a baseline: %v, %t", delta, seen)
	}
	if delta, seen := intervalCPU(prev, 8, 40); seen || delta != 0 {
		t.Fatalf("pid skipped last pass counted its cumulative time: %v, %t", delta, seen)
	}
	if delta, _ := intervalCPU(prev, 7, 39); delta != 0 {
		t.Fatalf("a reused pid went negative: %v", delta)
	}
}

func TestHeldSessionStartsNoCapture(t *testing.T) {
	runner := &Runner{sessionStores: map[string]string{"codex": "codex"}, held: map[string]bool{"a": true}}
	runner.startCaptureIfIdle([]store.Session{{ID: "a", Tool: "codex"}}, map[string]tmux.Pane{"a": {PID: 1}}, runner.holdGeneration())
	if runner.captureBusy.Load() {
		t.Fatal("a held session started an id capture")
	}
}

// A capture that passed the held check before a hold began still finds the
// hold at bind time.
func TestBindWaitsOutAHoldThatBeganMidCapture(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.CreateSession(store.Session{ID: "sess-A", Name: "a", Tool: "codex", Cwd: t.TempDir(), Group: "g", Status: "idle", CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	sess, err := st.Get("sess-A")
	if err != nil {
		t.Fatal(err)
	}
	p := &Runner{store: st, held: map[string]bool{"sess-A": true}}
	if bound, held, err := p.bindUnlessHeld(sess, "A-id", []string{"sess-A"}, 0); bound || !held || err != nil {
		t.Fatalf("held: bound %t held %t err %v", bound, held, err)
	}
	got, err := st.Get("sess-A")
	if err != nil {
		t.Fatal(err)
	}
	if got.AgentSessionID != "" {
		t.Fatalf("held session got conversation %q", got.AgentSessionID)
	}
	delete(p.held, "sess-A")
	if bound, held, err := p.bindUnlessHeld(sess, "A-id", []string{"sess-A"}, 0); !bound || held || err != nil {
		t.Fatalf("released: bound %t held %t err %v", bound, held, err)
	}
}

// A first launch waits out a hold that began on a peer in its directory.
func TestFirstLaunchBindWaitsOutAPeersHold(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.CreateSession(store.Session{ID: "sess-A", Name: "a", Tool: "codex", Cwd: t.TempDir(), Group: "g", Status: "idle", CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	sess, err := st.Get("sess-A")
	if err != nil {
		t.Fatal(err)
	}
	p := &Runner{store: st, held: map[string]bool{"sess-B": true}}
	if bound, held, err := p.bindUnlessHeld(sess, "B-id", []string{"sess-A", "sess-B"}, 0); bound || !held || err != nil {
		t.Fatalf("peer held: bound %t held %t err %v", bound, held, err)
	}
	if got, err := st.Get("sess-A"); err != nil || got.AgentSessionID != "" {
		t.Fatalf("bound %q while a peer was held, err %v", got.AgentSessionID, err)
	}
}

// A peer's hold that began and ended while the capture read the store still
// keeps the bind off, and a later pass binds.
func TestFirstLaunchBindWaitsOutAPeersHoldThatAlreadyEnded(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.CreateSession(store.Session{ID: "sess-A", Name: "a", Tool: "codex", Cwd: t.TempDir(), Group: "g", Status: "idle", CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	sess, err := st.Get("sess-A")
	if err != nil {
		t.Fatal(err)
	}
	p := &Runner{store: st}
	since := p.holdGeneration()
	if err := p.HoldSessions([]string{"sess-B"}, func() {}); err != nil {
		t.Fatal(err)
	}
	peers := []string{"sess-A", "sess-B"}
	if bound, held, err := p.bindUnlessHeld(sess, "B-id", peers, since); bound || !held || err != nil {
		t.Fatalf("ended hold: bound %t held %t err %v", bound, held, err)
	}
	if bound, held, err := p.bindUnlessHeld(sess, "A-id", peers, p.holdGeneration()); !bound || held || err != nil {
		t.Fatalf("later pass: bound %t held %t err %v", bound, held, err)
	}
}

// A peer launched in a held session's directory leaves the held session's
// conversation alone until the hold ends.
func TestHeldSessionKeepsItsConversationFromAPeer(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	codexHome := t.TempDir()
	t.Setenv("CODEX_HOME", codexHome)
	cwd := t.TempDir()
	first := time.Now().Add(-2 * time.Minute)
	second := first.Add(time.Minute)
	writeCodexRollout(t, filepath.Join(codexHome, "sessions", "rollout-A.jsonl"), "A-id", cwd, first)
	writeCodexRollout(t, filepath.Join(codexHome, "sessions", "rollout-B.jsonl"), "B-id", cwd, second)
	for _, sess := range []store.Session{
		{ID: "sess-A", Name: "a", Tool: "codex", Cwd: cwd, Group: "g", Status: "idle", CreatedAt: first},
		{ID: "sess-B", Name: "b", Tool: "codex", Cwd: cwd, Group: "g", Status: "idle", CreatedAt: second},
	} {
		if err := st.CreateSession(sess); err != nil {
			t.Fatal(err)
		}
	}
	var sessions []store.Session
	for _, id := range []string{"sess-A", "sess-B"} {
		sess, err := st.Get(id)
		if err != nil {
			t.Fatal(err)
		}
		sessions = append(sessions, sess)
	}
	panes := map[string]tmux.Pane{"sess-A": {PID: 1}, "sess-B": {PID: 2}}
	p := &Runner{store: st, sessionStores: map[string]string{"codex": "codex"}, held: map[string]bool{"sess-A": true}}
	if captured, err := p.captureAgentSessionIDs(sessions, panes, p.holdGeneration()); err != nil || captured != 0 {
		t.Fatalf("held peer: captured %d, err %v; want 0", captured, err)
	}
	delete(p.held, "sess-A")
	if captured, err := p.captureAgentSessionIDs(sessions, panes, p.holdGeneration()); err != nil || captured != 2 {
		t.Fatalf("released: captured %d, err %v; want 2", captured, err)
	}
	for id, want := range map[string]string{"sess-A": "A-id", "sess-B": "B-id"} {
		got, err := st.Get(id)
		if err != nil {
			t.Fatal(err)
		}
		if got.AgentSessionID != want {
			t.Fatalf("%s bound %q, want %q", id, got.AgentSessionID, want)
		}
	}
}

// A peer that names the held session's directory through a symlink waits too.
func TestHeldSessionKeepsItsConversationFromAPeerViaASymlink(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	codexHome := t.TempDir()
	t.Setenv("CODEX_HOME", codexHome)
	cwd := t.TempDir()
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(cwd, link); err != nil {
		t.Fatal(err)
	}
	first := time.Now().Add(-2 * time.Minute)
	second := first.Add(time.Minute)
	writeCodexRollout(t, filepath.Join(codexHome, "sessions", "rollout-A.jsonl"), "A-id", cwd, first)
	writeCodexRollout(t, filepath.Join(codexHome, "sessions", "rollout-B.jsonl"), "B-id", cwd, second)
	for _, sess := range []store.Session{
		{ID: "sess-A", Name: "a", Tool: "codex", Cwd: cwd, Group: "g", Status: "idle", CreatedAt: first},
		{ID: "sess-B", Name: "b", Tool: "codex", Cwd: link, Group: "g", Status: "idle", CreatedAt: second},
	} {
		if err := st.CreateSession(sess); err != nil {
			t.Fatal(err)
		}
	}
	var sessions []store.Session
	for _, id := range []string{"sess-A", "sess-B"} {
		sess, err := st.Get(id)
		if err != nil {
			t.Fatal(err)
		}
		sessions = append(sessions, sess)
	}
	panes := map[string]tmux.Pane{"sess-A": {PID: 1}, "sess-B": {PID: 2}}
	p := &Runner{store: st, sessionStores: map[string]string{"codex": "codex"}, held: map[string]bool{"sess-A": true}}
	if captured, err := p.captureAgentSessionIDs(sessions, panes, p.holdGeneration()); err != nil || captured != 0 {
		t.Fatalf("held peer: captured %d, err %v; want 0", captured, err)
	}
	got, err := st.Get("sess-B")
	if err != nil {
		t.Fatal(err)
	}
	if got.AgentSessionID != "" {
		t.Fatalf("peer through a symlink bound %q while the held session waited", got.AgentSessionID)
	}
}

func TestFleetCPUCountsEachPidOnceAndCaps(t *testing.T) {
	if got := fleetCPU(200, 0, false, 0, 4); got != 50 {
		t.Fatalf("first pass = %v, want the pcpu share 50", got)
	}
	if got := fleetCPU(0, 2, true, 1, 4); got != 50 {
		t.Fatalf("every pid with a baseline = %v, want the interval share 50", got)
	}
	if got := fleetCPU(100, 2, true, 1, 4); got != 75 {
		t.Fatalf("one new pid beside the rest = %v, want 25 + 50", got)
	}
	if got := fleetCPU(400, 4, true, 1, 4); got != 100 {
		t.Fatalf("estimates past the machine = %v, want 100", got)
	}
}

func TestSetInputDoesNotWaitOnABindUnderTheHoldLock(t *testing.T) {
	p := &Runner{}
	p.holdMu.Lock()
	defer p.holdMu.Unlock()
	done := make(chan struct{})
	go func() {
		p.SetInput(true, "sess-A")
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("SetInput waited on the hold lock")
	}
}

func TestCaptureWaitsOutAPeersHoldThatEndedAfterTheSnapshot(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	codexHome := t.TempDir()
	t.Setenv("CODEX_HOME", codexHome)
	cwd := t.TempDir()
	launched := time.Now().Add(-time.Minute)
	writeCodexRollout(t, filepath.Join(codexHome, "sessions", "rollout-A.jsonl"), "A-new", cwd, launched.Add(time.Second))
	for _, sess := range []store.Session{
		{ID: "sess-A", Name: "a", Tool: "codex", Cwd: cwd, Group: "g", Status: "idle", CreatedAt: launched.Add(-time.Hour), AgentSessionID: "A-old"},
		{ID: "sess-B", Name: "b", Tool: "codex", Cwd: cwd, Group: "g", Status: "idle", CreatedAt: launched},
	} {
		if err := st.CreateSession(sess); err != nil {
			t.Fatal(err)
		}
	}
	var sessions []store.Session
	for _, id := range []string{"sess-A", "sess-B"} {
		sess, err := st.Get(id)
		if err != nil {
			t.Fatal(err)
		}
		sessions = append(sessions, sess)
	}
	panes := map[string]tmux.Pane{"sess-A": {PID: 1}, "sess-B": {PID: 2}}
	p := &Runner{store: st, sessionStores: map[string]string{"codex": "codex"}}
	since := p.holdGeneration()
	if err := p.HoldSessions([]string{"sess-A"}, func() {}); err != nil {
		t.Fatal(err)
	}
	if captured, err := p.captureAgentSessionIDs(sessions, panes, since); err != nil || captured != 0 {
		t.Fatalf("hold after snapshot: captured %d, err %v; want 0", captured, err)
	}
	if captured, err := p.captureAgentSessionIDs(sessions, panes, p.holdGeneration()); err != nil || captured != 1 {
		t.Fatalf("later pass: captured %d, err %v; want 1", captured, err)
	}
}

func TestBindWaitsOutAPeersHoldThatEndedAfterTheSnapshot(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.CreateSession(store.Session{ID: "sess-B", Name: "b", Tool: "codex", Cwd: t.TempDir(), Group: "g", Status: "idle", CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	sess, err := st.Get("sess-B")
	if err != nil {
		t.Fatal(err)
	}
	p := &Runner{store: st}
	var since uint64
	if err := p.HoldSessions([]string{"sess-A"}, func() { since = p.holdGeneration() }); err != nil {
		t.Fatal(err)
	}
	peers := []string{"sess-A", "sess-B"}
	if bound, held, err := p.bindUnlessHeld(sess, "A-new", peers, since); bound || !held || err != nil {
		t.Fatalf("hold ended mid-pass: bound %t held %t err %v", bound, held, err)
	}
	if bound, held, err := p.bindUnlessHeld(sess, "B-id", peers, p.holdGeneration()); !bound || held || err != nil {
		t.Fatalf("later pass: bound %t held %t err %v", bound, held, err)
	}
}

func TestCaptureWaitsOutARestoreHeldWhileArchived(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	codexHome := t.TempDir()
	t.Setenv("CODEX_HOME", codexHome)
	cwd := t.TempDir()
	launched := time.Now().Add(-time.Minute)
	writeCodexRollout(t, filepath.Join(codexHome, "sessions", "rollout-A.jsonl"), "A-new", cwd, launched.Add(time.Second))
	for _, sess := range []store.Session{
		{ID: "sess-A", Name: "a", Tool: "codex", Cwd: cwd, Group: "g", Status: "idle", CreatedAt: launched.Add(-time.Hour), Archived: true},
		{ID: "sess-B", Name: "b", Tool: "codex", Cwd: cwd, Group: "g", Status: "idle", CreatedAt: launched},
	} {
		if err := st.CreateSession(sess); err != nil {
			t.Fatal(err)
		}
	}
	visible, err := st.Get("sess-B")
	if err != nil {
		t.Fatal(err)
	}
	panes := map[string]tmux.Pane{"sess-B": {PID: 2}}
	p := &Runner{store: st, sessionStores: map[string]string{"codex": "codex"}, held: map[string]bool{"sess-A": true}}
	if captured, err := p.captureAgentSessionIDs([]store.Session{visible}, panes, p.holdGeneration()); err != nil || captured != 0 {
		t.Fatalf("archived peer held: captured %d, err %v; want 0", captured, err)
	}
	delete(p.held, "sess-A")
	if captured, err := p.captureAgentSessionIDs([]store.Session{visible}, panes, p.holdGeneration()); err != nil || captured != 1 {
		t.Fatalf("released: captured %d, err %v; want 1", captured, err)
	}
}
