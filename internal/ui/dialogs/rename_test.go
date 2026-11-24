package dialogs

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
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
	if got := RenameSession("s", "n", "zsh", []string{"zsh"}).Hints(); got[0] != [2]string{"tab", "runs zsh"} {
		t.Fatalf("session hints = %v", got)
	}
}

func TestNameFieldsKeepALongName(t *testing.T) {
	long := strings.Repeat("issue-title-", 10)
	if got := RenameSession("s1", long, "claude", []string{"claude"}).Name(); got != long {
		t.Fatalf("rename opened %q, want the whole name", got)
	}
	if got := NewGroupEdit(long, "/srv", 0, 1).Name(); got != long {
		t.Fatalf("group edit opened %q, want the whole name", got)
	}
}

func TestInlineRenameOpensFittedToTheRow(t *testing.T) {
	long := strings.Repeat("issue-title-", 10)
	d := RenameSession("s1", long, "claude", []string{"claude"})
	view := ansi.Strip(d.NameField(20).View())
	if got := ansi.StringWidth(view); got > 23 {
		t.Fatalf("rename field drew %d columns in a 20 column row: %q", got, view)
	}
	if !strings.HasSuffix(strings.TrimRight(view, " "), "title-") {
		t.Fatalf("rename field %q hides the end of the name, where the cursor is", view)
	}
}
