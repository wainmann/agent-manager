package execution

import (
	"testing"

	"github.com/YoanWai/agent-manager/internal/status"
	"github.com/YoanWai/agent-manager/internal/store"
)

func TestAckedWaitingHoldsIdleUntilTheScreenChanges(t *testing.T) {
	m := buildModel(t)
	sess := store.Session{ID: "ackwait01", Tool: "claude-hooked"}
	pane := "Run the tests?\nEnter to confirm\n❯ \n"
	writeHookStatus(t, m, sess.ID, status.Finished)

	if got := deriveStatus(t, m, sess, pane, true); got != status.Waiting {
		t.Fatalf("the prompt should derive waiting, got %q", got)
	}
	sess.Status, sess.Acked = status.Idle, true
	for range 2 {
		if got := deriveStatus(t, m, sess, pane, true); got != status.Idle {
			t.Fatalf("an acked waiting screen should hold idle, got %q", got)
		}
	}
	next := "Run the tests?\nyes\nDelete the cache?\nEnter to confirm\n❯ \n"
	if got := deriveStatus(t, m, sess, next, true); got != status.Waiting {
		t.Fatalf("a new prompt should derive waiting again, got %q", got)
	}
}

func TestAckOfAFinishedTurnDoesNotHoldALaterPrompt(t *testing.T) {
	m := buildModel(t)
	sess := store.Session{ID: "ackwait02", Tool: "claude-hooked", Status: status.Idle, Acked: true}
	writeHookStatus(t, m, sess.ID, status.Finished)
	if got := deriveStatus(t, m, sess, "Run the tests?\nEnter to confirm\n❯ \n", true); got != status.Waiting {
		t.Fatalf("a prompt no ack saw should derive waiting, got %q", got)
	}
}

func TestAckedWaitingHoldsIdleThroughStatusLineTicks(t *testing.T) {
	m := buildModel(t)
	sess := store.Session{ID: "ackwait03", Tool: "claude-hooked"}
	writeHookStatus(t, m, sess.ID, status.Finished)
	frame := func(clock string) string {
		return "Run the tests?\nEnter to confirm\n❯ \n  " + clock + " · 5h 20% · ctx 41k\n"
	}
	dialog := func(question string) string {
		return "❯ ship it\n● Bash(make release)\n Bash command\n " + question + "\n ❯ 1. Yes\n Enter to confirm"
	}

	if got := deriveStatus(t, m, sess, frame("12:01"), true); got != status.Waiting {
		t.Fatalf("the prompt should derive waiting, got %q", got)
	}
	sess.Status, sess.Acked = status.Idle, true
	if got := deriveStatus(t, m, sess, frame("12:02"), true); got != status.Idle {
		t.Fatalf("a status line tick should keep the acked session idle, got %q", got)
	}

	sess.Acked = false
	if got := deriveStatus(t, m, sess, dialog("Do you want to proceed?"), true); got != status.Waiting {
		t.Fatalf("a permission dialog should derive waiting, got %q", got)
	}
	sess.Acked = true
	if got := deriveStatus(t, m, sess, dialog("Do you want to proceed?"), true); got != status.Idle {
		t.Fatalf("an acked dialog should hold idle, got %q", got)
	}
	if got := deriveStatus(t, m, sess, dialog("Do you want to delete dist?"), true); got != status.Waiting {
		t.Fatalf("a new dialog below the input box should derive waiting again, got %q", got)
	}
}
