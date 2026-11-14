package execution

import (
	"testing"

	"github.com/YoanWai/agent-manager/internal/hooks"
	"github.com/YoanWai/agent-manager/internal/status"
	"github.com/YoanWai/agent-manager/internal/store"
)

// A hooks review dialog blocks on the user. The selected row is the
// activity cutoff and no waiting rule used to match, so a session that
// was working settled to finished once the pane stopped changing.
func TestQuietCodexHooksReviewStaysWaiting(t *testing.T) {
	disableQuietEndGrace(t)
	m := buildModel(t)
	defaultEngine(t, m)
	sess := store.Session{ID: "hooks-review", Tool: "codex", Status: status.Working}
	pane := "" +
		"  Hooks need review\n" +
		"  2 hooks are new or changed.\n" +
		"  Hooks can run outside the sandbox\n" +
		"  after you trust them.\n" +
		"\n" +
		"\n" +
		"› 1. Review hooks\n" +
		"  2. Trust all and continue\n" +
		"  3. Continue without trusting (hooks\n" +
		"     won't run)\n" +
		"\n" +
		"  enter confirm · esc skip\n"
	seedRegionHash(t, m, sess, pane)
	if got := deriveStatus(t, m, sess, pane, true); got != status.Waiting {
		t.Fatalf("quiet hooks review = %q, want waiting", got)
	}
}

// A failed turn fires StopFailure instead of Stop and still draws its
// turn summary under the error, so the pane reads finished. The hook's
// errored stands until the next prompt or Stop replaces it.
func TestHookErroredHoldsOverTheFailedTurnSummary(t *testing.T) {
	m := buildModel(t)
	defaultEngine(t, m)
	m.poller.statusSources["claude"] = hooks.StatusSourceClaude
	sess := store.Session{ID: "hooked-failed", Tool: "claude", Status: status.Working}
	writeHookStatus(t, m, sess.ID, status.Errored)

	// real captures, Claude Code 2.1.319: signed out, and an API error from a local fixture
	for name, pane := range map[string]string{
		"signed out": "❯ Say hello.\n  ⎿ \u00a0Not logged in · Please run /login\n\n✻ Crunched for 0s · done 10:36 PM\n\n" +
			"────\n❯ \n────\n  ⏵⏵ auto mode on (shift+tab to cycle)                    Not logged in · Run /login",
		"api error": "❯ Say hello.\n\n● API Error: 400 Review fixture: invalid request\n\n✻ Cooked for 0s · done 6:30 PM\n\n────\n❯ \n────",
		"under a status row": "❯ Say hello.\n  ⎿  Not logged in · Please run /login\n✻ Cogitated for 0s · done 10:40 PM\n" +
			"● full  $0.00  last $0.00  0s                         Review  Audit  Board  Details [-]\n" +
			"────\n❯ \n────\n  ⏵⏵ auto mode on (shift+tab to cycle)                      Not logged in · Run /login",
	} {
		for _, acked := range []bool{false, true} {
			sess.Acked = acked
			if got := deriveStatus(t, m, sess, pane, true); got != status.Errored {
				t.Fatalf("%s, acked=%v: failed turn = %q, want errored", name, acked, got)
			}
		}
	}

	sess.Acked = false
	writeHookStatus(t, m, sess.ID, status.Working)
	starting := "❯ Say hello.\n\n✻ Actioning… (running UserPromptSubmit hook · 0s)\n\n" +
		"────\n❯ \n────\n  ⏵⏵ auto mode on (shift+tab to cycle) · esc to interrupt\n                    Not logged in · Run /login"
	if got := deriveStatus(t, m, sess, starting, true); got != status.Working {
		t.Fatalf("next prompt after the failure = %q, want working", got)
	}
	writeHookStatus(t, m, sess.ID, status.Finished)
	done := "❯ Say hello.\n⏺ Hello.\n\n✻ Worked for 2s\n\n────\n❯ \n────"
	if got := deriveStatus(t, m, sess, done, true); got != status.Finished {
		t.Fatalf("next turn's Stop after the failure = %q, want finished", got)
	}
}

// The turn that launched a background agent ends on a wait line, and the
// agent's report wakes the main agent while the agent still runs, so that
// turn prints no summary of its own. Once the agent ends the hook holds the
// last Stop's finished, which the stale wait line must not overrule.
func TestHookFinishedHoldsOnceBackgroundAgentsEnd(t *testing.T) {
	m := buildModel(t)
	defaultEngine(t, m)
	m.poller.statusSources["claude"] = hooks.StatusSourceClaude
	sess := store.Session{ID: "hooked-drained", Tool: "claude", Status: status.Working}
	writeHookStatus(t, m, sess.ID, status.Finished)

	// real captures, Claude Code 2.1.322
	wait := "● Agent(Run three sleeps)\n  ⎿  Backgrounded agent (↓ to manage · ctrl+o to expand)\n\n● launched\n\n" +
		"✻ Waiting for 1 background agent to finish\n\n"
	rest := "────\n❯ Is the agent done yet?\n────\n  -- INSERT -- ⏵⏵ auto mode on (shift+tab to cycle)"
	running := wait + rest + "\n  ● main\n  ◯ general-purpose  Running sleep 20 in Bash      32s · ↓ 34.8k tokens"
	ended := wait + "› Message from @general-purpose (ctrl+o to expand)\n\n" +
		"● The background agent finished and reported \"bg done\". It ran the sleep 20 command three times.\n\n" +
		"● Agent \"Run three sleeps\" finished · 1m 7s\n\n" +
		"● full  $0.22  last $0.04  8s                                Review  Audit  Board  Details [-]\n\n" + rest

	if got := deriveStatus(t, m, sess, running, true); got != status.Working {
		t.Fatalf("agent still running = %q, want working", got)
	}
	if got := deriveStatus(t, m, sess, ended, true); got != status.Finished {
		t.Fatalf("agent ended = %q, want finished", got)
	}
	sess.Acked = true
	if got := deriveStatus(t, m, sess, ended, true); got != status.Idle {
		t.Fatalf("agent ended, acked = %q, want idle", got)
	}
}

// The Notification hook writes waiting while a dialog is open, and Esc
// closes it without Stop or PostToolUse. The user dismissed it, so once no
// dialog is on screen the stale waiting reads idle, even though Claude's
// closing line carries a question mark.
func TestHookWaitingYieldsOnceTheDialogIsDismissed(t *testing.T) {
	m := buildModel(t)
	defaultEngine(t, m)
	m.poller.statusSources["claude"] = hooks.StatusSourceClaude
	sess := store.Session{ID: "hooked-dismissed", Tool: "claude", Status: status.Waiting}
	writeHookStatus(t, m, sess.ID, status.Waiting)

	// real captures, Claude Code 2.1.322
	ask := "❯ Run the Bash command: touch esc-probe.txt\n● Creating empty file esc-probe.txt\n  ⎿  $ touch esc-probe.txt\n────\n" +
		" Bash command\n Create empty file esc-probe.txt\n touch esc-probe.txt\n Do you want to proceed?\n ❯ 1. Yes\n" +
		"   2. Yes, and always allow access to /tmp/work from this project\n   4. No\n Esc to cancel · Tab to amend"
	if got := deriveStatus(t, m, sess, ask, true); got != status.Waiting {
		t.Fatalf("open permission prompt = %q, want waiting", got)
	}
	composer := "────\n❯ \n────\n  -- INSERT -- ⏸ manual mode on"
	for name, pane := range map[string]string{
		"permission prompt": "❯ Run the Bash command: touch esc-probe.txt\n  Ran 1 shell command\n" +
			"  ⎿  Interrupted · What should Claude do instead?\n✻ Baked for 2s · done 22:11\n" + composer,
		"question dialog": "❯ Use the AskUserQuestion tool to ask me which color I prefer: red, green or blue.\n" +
			"● User declined to answer questions\n  ⎿  · Which color do you prefer? (Red / Green / Blue)\n" +
			"✻ Cogitated for 1s · done 22:12\n" +
			"● full  $0.09  last $0.01  4s                                Review  Audit  Board  Details [-]\n" + composer,
	} {
		if got := deriveStatus(t, m, sess, pane, true); got != status.Idle {
			t.Fatalf("%s dismissed with Esc = %q, want idle", name, got)
		}
	}
}

// Esc before the Notification hook fires, or during a running turn, leaves
// the hook at working: no Stop follows. The user cut the turn short, so
// it reads idle, with or without a band row the rules cannot read.
func TestHookWorkingYieldsWhenEscCutsTheTurnShort(t *testing.T) {
	m := buildModel(t)
	defaultEngine(t, m)
	m.poller.statusSources["claude"] = hooks.StatusSourceClaude
	sess := store.Session{ID: "hooked-cut", Tool: "claude", Status: status.Working}
	writeHookStatus(t, m, sess.ID, status.Working)

	// real capture, Claude Code 2.1.322
	cut := "❯ Run the Bash command: touch esc-probe2.txt\n  ⎿  16 skills available\n  Ran 1 shell command\n" +
		"  ⎿  Interrupted · What should Claude do instead?\n✻ Baked for 1s · done 22:19\n"
	band := "● full  $0.11  last $0.02  5s                                Review  Audit  Board  Details [-]\n"
	composer := "────\n❯ \n────\n  -- INSERT -- ⏸ manual mode on"
	declined := "❯ Use the AskUserQuestion tool to ask me which color I prefer: red, green or blue.\n" +
		"● User declined to answer questions\n  ⎿  · Which color do you prefer? (Red / Green / Blue)\n✻ Cogitated for 1s · done 22:12\n"
	for name, pane := range map[string]string{
		"interrupt":           cut + composer,
		"interrupt over band": cut + band + composer,
		"declined question":   declined + band + composer,
	} {
		if got := deriveStatus(t, m, sess, pane, true); got != status.Idle {
			t.Fatalf("%s = %q, want idle", name, got)
		}
	}

	next := cut + "❯ Try again\n✻ Drizzling… (3s · thinking)\n" + composer
	if got := deriveStatus(t, m, sess, next, true); got != status.Working {
		t.Fatalf("a new turn under an older interrupt = %q, want working", got)
	}
}

// Text that only reads like a failure, in a tool result, a quote or a
// submitted prompt, must not turn a successful turn errored.
func TestHookFinishedHoldsOverQuotedSignedOutText(t *testing.T) {
	m := buildModel(t)
	defaultEngine(t, m)
	m.poller.statusSources["claude"] = hooks.StatusSourceClaude
	sess := store.Session{ID: "hooked-quoted", Tool: "claude", Status: status.Working}
	writeHookStatus(t, m, sess.ID, status.Finished)

	for name, pane := range map[string]string{
		"tool result":      "❯ Print the login message.\n⏺ Bash(printf ...)\n  ⎿  Not logged in · Please run /login\n✻ Worked for 2s\n────\n❯ \n────",
		"indented quote":   "❯ What does the footer say?\n⏺ It reads:\n  Not logged in · Run /login\n✻ Worked for 2s\n────\n❯ \n────",
		"multiline prompt": "❯ Explain this footer:\n    Not logged in · Run /login\n⏺ It means the CLI has no account.\n✻ Worked for 2s\n────\n❯ \n────",
	} {
		if got := deriveStatus(t, m, sess, pane, true); got != status.Finished {
			t.Fatalf("%s: successful turn = %q, want finished", name, got)
		}
	}
}
