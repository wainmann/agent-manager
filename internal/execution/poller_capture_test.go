package execution

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/YoanWai/agent-manager/internal/config"
	"github.com/YoanWai/agent-manager/internal/status"
	"github.com/YoanWai/agent-manager/internal/store"
	"github.com/YoanWai/agent-manager/internal/testenv"
)

// A prompt echo that scrolled off the screen is recovered from the pane's
// history, for every session of the pass that lost one.
func TestPromptsScrolledOffAreRecoveredFromHistory(t *testing.T) {
	testenv.SkipPOSIXShell(t)
	m := buildModel(t)
	useScrollingTool(t, m)
	for _, name := range []string{"first", "second"} {
		dir := filepath.Join(t.TempDir(), name)
		if err := os.Mkdir(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := m.spawnSession("scrolling-tool", name, dir, "", "", false, false); err != nil {
			t.Fatal(err)
		}
	}
	for _, sess := range m.sessionRows() {
		if pane := settledPane(t, m, sess.ID, "60", "❯"); strings.Contains(pane, "prompt in") {
			t.Fatalf("the echo of %s is still on screen:\n%s", sess.Name, pane)
		}
	}
	result := m.poller.Step()
	if result.Err != nil {
		t.Fatal(result.Err)
	}
	for _, sess := range m.sessionRows() {
		if got, want := result.Snapshot.PanePrompts[sess.ID], "prompt in "+sess.Name; got != want {
			t.Errorf("prompt of %s = %q, want %q read from history", sess.Name, got, want)
		}
	}
}

// The pass reads every pane at once, before it types into any of them, and
// a send can take seconds. A session reached after one is read again, so
// its row quotes what its pane shows now.
func TestAPaneReachedAfterASendIsReadAgain(t *testing.T) {
	m := buildModel(t)
	if err := m.spawnSession("ready-tool", "sender-abcd", t.TempDir(), "", "", true, false); err != nil {
		t.Fatal(err)
	}
	if err := m.spawnSession("quietchat", "reached-later", t.TempDir(), "", "", false, false); err != nil {
		t.Fatal(err)
	}
	sender, later := m.sessionRows()[0], m.sessionRows()[1]
	settledPane(t, m, sender.ID, "❯")

	const marker = "typed-after-the-send"
	typed := false
	prev := followDuringPoll
	t.Cleanup(func() { followDuringPoll = prev })
	followDuringPoll = func(p *Runner, row *store.Session, agentAlive, paneGone bool) error {
		if row.ID != later.ID || typed {
			return nil
		}
		current, err := m.store.Get(sender.ID)
		if err != nil {
			return err
		}
		if len(current.PendingInputs) == 0 {
			typed = true
			waitForPane(t, m, later.ID, marker)
		}
		return nil
	}

	for deadline := time.Now().Add(10 * time.Second); !typed; time.Sleep(50 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the pending input was never sent")
		}
		result := m.poller.Step()
		if result.Err != nil {
			t.Fatal(result.Err)
		}
		if rows := result.Snapshot.Sessions; rows[0].ID != sender.ID {
			t.Fatalf("pass order = %s first, want the sender %s", rows[0].ID, sender.ID)
		}
		if typed && !strings.Contains(result.Snapshot.PaneLines[later.ID], marker) {
			t.Fatalf("row quotes %q, want the line typed after the send", result.Snapshot.PaneLines[later.ID])
		}
	}
}

// A delivery ends in Enter, so it decides on the pane as it is when the row
// is reached: a line someone began after the pass started holds the message.
func TestAMessageWaitsOnALineTypedAfterThePassBegan(t *testing.T) {
	m := buildModel(t)
	sess := spawnedSession(t, m, "ready-tool")
	settledPane(t, m, sess.ID, "❯")
	for deadline := time.Now().Add(30 * time.Second); m.poller.Step().Snapshot.Sessions[0].Status != status.Finished; time.Sleep(50 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the session never came to rest")
		}
	}
	queueMessage(t, m, sess.ID, "rebase on main")

	prev := followDuringPoll
	t.Cleanup(func() { followDuringPoll = prev })
	followDuringPoll = func(p *Runner, row *store.Session, agentAlive, paneGone bool) error {
		if err := m.tmux.Paste(row.ID, "USERTEXT-in-progress"); err != nil {
			return err
		}
		settledPane(t, m, row.ID, "USERTEXT-in-progress")
		return nil
	}

	if result := m.poller.Step(); result.Err != nil {
		t.Fatal(result.Err)
	}
	if queued, _ := m.store.QueuedCount(sess.ID); queued != 1 {
		t.Fatal("a message was typed on top of a line begun during the pass")
	}
}

// A row read again for a delivery reads its history again too, so the prompt
// it shows is the one above the pane as it is now.
func TestARowReadAgainReadsItsHistoryAgain(t *testing.T) {
	testenv.SkipPOSIXShell(t)
	m := buildModel(t)
	useScrollingTool(t, m)
	if err := m.spawnSession("scrolling-tool", "worker", t.TempDir(), "", "", false, false); err != nil {
		t.Fatal(err)
	}
	sess := m.sessionRows()[0]
	settledPane(t, m, sess.ID, "60", "❯")
	queueMessage(t, m, sess.ID, "rebase on main")

	prev := followDuringPoll
	t.Cleanup(func() { followDuringPoll = prev })
	followDuringPoll = func(p *Runner, row *store.Session, agentAlive, paneGone bool) error {
		if err := m.tmux.Paste(row.ID, "next\n"); err != nil {
			return err
		}
		settledPane(t, m, row.ID, "120", "❯")
		return nil
	}

	result := m.poller.Step()
	if result.Err != nil {
		t.Fatal(result.Err)
	}
	if got, want := result.Snapshot.PanePrompts[sess.ID], "a later prompt"; got != want {
		t.Fatalf("prompt = %q, want %q read from the history above the pane as it is now", got, want)
	}
}

// useScrollingTool defines a tool whose prompt echo has scrolled off the
// screen by the time its pane settles, and which takes one more turn the
// same way once a line is typed into it.
func useScrollingTool(t *testing.T, m *harness) {
	t.Helper()
	m.cfg.Tools["scrolling-tool"] = config.Tool{
		Command:        `sh -c 'printf "> prompt in %s\n" "$(basename "$PWD")"; seq 1 60; printf "❯ "; read turn; printf "> a later prompt\n"; seq 61 120; printf "❯ "; exec cat'`,
		DefaultStatus:  status.Idle,
		ActivityCutoff: "(?m)^❯",
		UserEcho:       "^> ",
	}
	engine, err := status.NewEngine(m.cfg)
	if err != nil {
		t.Fatal(err)
	}
	m.poller.engine = engine
}

// An archive or a kill asked for after the turn acts on the rest this pass
// reports, so the row is read when it is reached: a turn begun since the pass
// started is not a rest.
func TestAnEndAfterTheTurnWaitsOnATurnBegunAfterThePassBegan(t *testing.T) {
	m := buildModel(t)
	sess := spawnedSession(t, m, "ready-tool")
	settledPane(t, m, sess.ID, "❯")
	for deadline := time.Now().Add(30 * time.Second); m.poller.Step().Snapshot.Sessions[0].Status != status.Finished; time.Sleep(50 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the session never came to rest")
		}
	}
	if err := m.store.RequestAfterTurn(sess.ID, store.AfterTurnArchive, time.Now()); err != nil {
		t.Fatal(err)
	}
	grace := quietEndGrace
	quietEndGrace = 0
	t.Cleanup(func() { quietEndGrace = grace })

	prev := followDuringPoll
	t.Cleanup(func() { followDuringPoll = prev })
	followDuringPoll = func(p *Runner, row *store.Session, agentAlive, paneGone bool) error {
		const typed = "a-turn-begun-since"
		waitForPane(t, m, row.ID, typed)
		// The line is a turn once the tool has drawn its next prompt under it.
		for deadline := time.Now().Add(30 * time.Second); ; time.Sleep(50 * time.Millisecond) {
			pane, err := m.tmux.CapturePane(row.ID)
			if err != nil {
				return err
			}
			if _, after, _ := strings.Cut(pane, typed); strings.Contains(after, "❯") {
				return nil
			}
			if time.Now().After(deadline) {
				t.Fatalf("the tool never took the typed line:\n%s", pane)
			}
		}
	}

	result := m.poller.Step()
	if result.Err != nil {
		t.Fatal(result.Err)
	}
	if slices.Contains(result.Snapshot.TurnsEnded, sess.ID) {
		t.Fatalf("the turn was reported ended as %s, with a new one on screen", result.Snapshot.Sessions[0].Status)
	}
}
