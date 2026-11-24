package status

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// copilotFrame reads a capture the way the poller does, through Plain.
func copilotFrame(t *testing.T, engine *Engine, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "copilot", "1.0.114", name+".txt"))
	if err != nil {
		t.Fatal(err)
	}
	return engine.Plain("copilot", strings.ReplaceAll(string(data), "\r\n", "\n"))
}

func TestCopilotPanes(t *testing.T) {
	engine := defaultEngine(t)
	for _, tc := range []struct{ frame, want string }{
		{"trust", Waiting},
		{"idle", Idle},
		{"draft", Idle},
		{"working", Working},
		{"streaming", Working},
		{"approval", Waiting},
		{"ask-user", Waiting},
		{"ask-choice", Waiting},
		{"finished", Idle},
		{"edited", Idle},
		{"question", Idle},
		{"interrupted", Idle},
		{"resumed", Idle},
		{"picker", Waiting},
		{"errored", Errored},
	} {
		t.Run(tc.frame, func(t *testing.T) {
			if got, _ := engine.Match("copilot", copilotFrame(t, engine, tc.frame)); got != tc.want {
				t.Fatalf("Match(%s) = %q want %q", tc.frame, got, tc.want)
			}
		})
	}
}

func TestCopilotComposer(t *testing.T) {
	engine := defaultEngine(t)
	if draft, ok := engine.InputDraft("copilot", copilotFrame(t, engine, "idle")); ok || draft != "" {
		t.Fatalf("idle draft = %q %v", draft, ok)
	}
	draft, ok := engine.InputDraft("copilot", copilotFrame(t, engine, "draft"))
	if !ok || draft != "Run the shell command `date` and tell me the output." {
		t.Fatalf("draft = %q %v", draft, ok)
	}
	got, _, ok := engine.LastMessage("copilot", copilotFrame(t, engine, "question"))
	if !ok || got != "What is your favorite color?" {
		t.Fatalf("LastMessage = %q %v", got, ok)
	}
	got, _, ok = engine.LastMessage("copilot", copilotFrame(t, engine, "approval"))
	if !ok || got != "Do you want to create /private/tmp/cpl/work/hello.txt?" {
		t.Fatalf("approval LastMessage = %q %v", got, ok)
	}
	echo, ok := engine.LastUserEcho("copilot", copilotFrame(t, engine, "question"))
	if !ok || echo != "Ask me which color I like, as a single question, and wait for my answer. Do not use any tools." {
		t.Fatalf("LastUserEcho = %q %v", echo, ok)
	}
}

// Copilot draws no turn summary, so a turn that stops changing settles on
// its last content line.
func TestCopilotTurnEnd(t *testing.T) {
	engine := defaultEngine(t)
	for _, tc := range []struct{ frame, want string }{
		{"question", Waiting},
		{"finished", Finished},
		{"interrupted", Finished},
		{"streaming", Finished},
	} {
		region, ok := engine.ActivityRegion("copilot", copilotFrame(t, engine, tc.frame))
		if !ok {
			t.Fatalf("%s: no activity region", tc.frame)
		}
		if got := engine.TurnEndedState("copilot", region); got != tc.want {
			t.Fatalf("TurnEndedState(%s) = %q want %q", tc.frame, got, tc.want)
		}
	}
}

// The blinking mascot and the credit counter redraw on their own, and
// neither is the agent at work.
func TestCopilotFrameIsChrome(t *testing.T) {
	engine := defaultEngine(t)
	region, ok := engine.ActivityRegion("copilot", copilotFrame(t, engine, "idle"))
	if !ok {
		t.Fatal("no activity region")
	}
	content := strings.TrimSpace(engine.RegionContent("copilot", region))
	want := "● No Copilot instructions found for this repository. Run /init to generate them.\n ● MCP Servers reloaded: 1 server connected"
	if content != want {
		t.Fatalf("RegionContent = %q", content)
	}
}

func TestCopilotQuotedFooterStaysIdle(t *testing.T) {
	engine := defaultEngine(t)
	finished := copilotFrame(t, engine, "finished")
	for _, quote := range []string{"◉ Working ctrl+c interrupt", "✗ Execution failed: boom", "│ ↑/↓ to navigate · enter to select · esc to cancel │"} {
		if got, _ := engine.Match("copilot", strings.Replace(finished, "Fri Nov 20 14:20:57 IST 2026", quote, 1)); got != Idle {
			t.Fatalf("quote %q = %q want idle", quote, got)
		}
	}
}

// Only rows that reach the scrollbar column lose the glyph; a short reply
// line ending in it keeps it.
func TestCopilotScrollbarStripsOnlyTheRightEdge(t *testing.T) {
	engine := defaultEngine(t)
	pane := " ● Keep this glyph: ┃\n" + strings.Repeat(" ", 30) + "┃\n ● a reply" + strings.Repeat(" ", 20) + "┃"
	want := " ● Keep this glyph: ┃\n\n ● a reply"
	if got := engine.Plain("copilot", pane); got != want {
		t.Fatalf("Plain = %q want %q", got, want)
	}
}

// A prompt the manager sends wraps, and Copilot then leaves one space
// between its first row and the clock.
func TestCopilotWrappedEchoReadsItsFirstRow(t *testing.T) {
	engine := defaultEngine(t)
	echo, ok := engine.LastUserEcho("copilot", copilotFrame(t, engine, "wrapped-echo"))
	if !ok || echo != `This session is already named. You can rename it later with agent-manager rename "<name>" only if the user` {
		t.Fatalf("LastUserEcho = %q %v", echo, ok)
	}
	if got, _ := engine.Match("copilot", copilotFrame(t, engine, "wrapped-echo")); got != Idle {
		t.Fatalf("Match = %q want idle", got)
	}
}

// A table in a reply is indented under its bullet, so its rows stay part of
// the quote while the header box at the left edge stays frame.
func TestCopilotReplyTableStaysInTheQuote(t *testing.T) {
	engine := defaultEngine(t)
	got, anchored, ok := engine.LastMessage("copilot", copilotFrame(t, engine, "table"))
	if !ok || !anchored || !strings.Contains(got, "│ Strawberry │ Red    │") || !strings.HasSuffix(got, "These are just a few common examples of colorful fruits.") {
		t.Fatalf("LastMessage = %q anchored=%v ok=%v", got, anchored, ok)
	}
}
