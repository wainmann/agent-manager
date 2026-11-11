package notify

import (
	"slices"
	"strings"
	"testing"
)

func TestCommandEnvContract(t *testing.T) {
	event := Event{ID: "sess-1", Session: "deploy", Tool: "claude", Dir: "/src/api-worktrees/am-x", Branch: "am/fix-login"}
	for kind, want := range map[Kind][2]string{
		Waiting:  {"waiting", "◆ Waiting for your input"},
		Finished: {"finished", "● Finished"},
		Errored:  {"errored", "✕ Errored"},
	} {
		event.Kind = kind
		got := CommandEnv(event)
		expected := []string{
			"AM_NOTIFY_KIND=" + want[0],
			"AM_TITLE=deploy · am-x · am/fix-login · claude",
			"AM_BODY=" + want[1],
		}
		if !slices.Equal(got, expected) {
			t.Fatalf("%s env = %q, want %q", want[0], got, expected)
		}
	}
	if env := CommandEnv(Event{ID: "x"}); env != nil {
		t.Fatalf("an event of no kind should have no env, got %q", env)
	}
}

// The new-command banner names no session, so a click brings the manager
// forward without moving its cursor, and it shows the command itself.
func TestShowCommandShowsTheCommandOnABanner(t *testing.T) {
	defer restore()()
	rec := plainMac(t)
	var posted []string
	macPost = func(sessionID, subtitle, body, sound string) error {
		posted = []string{sessionID, subtitle, body, sound}
		return nil
	}
	ShowCommand("curl -d \"$AM_BODY\"\n ntfy.sh/topic")
	want := []string{"", "New hook command", `curl -d "$AM_BODY" ntfy.sh/topic`, "Glass"}
	if !slices.Equal(posted, want) {
		t.Fatalf("helper fields = %q, want %q", posted, want)
	}
	if len(rec.calls()) != 0 {
		t.Fatalf("nothing else should run once the helper posted, got %v", rec.calls())
	}
}

func TestShowCommandCutsALongCommand(t *testing.T) {
	defer restore()()
	plainMac(t)
	var body string
	macPost = func(_, _, text, _ string) error {
		body = text
		return nil
	}
	ShowCommand(strings.Repeat("x", commandBannerRunes+50))
	if runes := []rune(body); len(runes) != commandBannerRunes || !strings.HasSuffix(body, "…") {
		t.Fatalf("banner body is %d runes, want %d ending in …", len(runes), commandBannerRunes)
	}
}

func TestShowCommandReachesGhostty(t *testing.T) {
	defer restore()()
	plainMac(t)
	getenv = func(key string) string {
		if key == "TERM_PROGRAM" {
			return "ghostty"
		}
		return ""
	}
	var emitted []string
	emitSeq = func(seq string) error {
		emitted = append(emitted, seq)
		return nil
	}
	ShowCommand("say done")
	if !slices.Equal(emitted, []string{"\x1b]777;notify;agent-manager;say done — New hook command\a"}) {
		t.Fatalf("want one OSC 777 sequence, got %q", emitted)
	}
}
