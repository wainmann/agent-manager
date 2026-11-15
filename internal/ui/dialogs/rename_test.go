package dialogs

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestRenameSessionCyclesToolsAndSubmits(t *testing.T) {
	d := RenameSession("s1", "box", "claude", []string{"claude", "zsh"})
	d.Key(tea.KeyMsg{Type: tea.KeyTab})
	if d.Tool() != "zsh" {
		t.Fatalf("tab picked %q, want the next tool", d.Tool())
	}
	d.input.SetValue(" a/b ")
	got := d.Key(tea.KeyMsg{Type: tea.KeyEnter})
	want := RenameRequest{SessionID: "s1", Name: "a-b", Tool: "zsh"}
	if got.Action != RenameSubmit || got.Request != want {
		t.Fatalf("enter = %+v, want %+v", got, want)
	}
	if got := d.Key(tea.KeyMsg{Type: tea.KeyEsc}); got.Action != RenameCancel {
		t.Fatalf("esc = %+v", got)
	}
}

func TestRenameSessionKeepsAMissingTool(t *testing.T) {
	d := RenameSession("s1", "box", "gone", []string{"claude"})
	if d.Tool() != "gone" || len(d.tools) != 2 {
		t.Fatalf("tool %q tools %v, want the missing tool kept selectable", d.Tool(), d.tools)
	}
}

func TestRenameHints(t *testing.T) {
	if got := RenameSession("s", "n", "zsh", []string{"zsh"}).Hints(); got[0] != [2]string{"tab", "tool: zsh"} {
		t.Fatalf("session hints = %v", got)
	}
}
