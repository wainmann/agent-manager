package dialogs

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// The row submits the values the dialog validated; the request itself stays
// the root's to build.
func TestRenameSurfaceSubmitsTheValidatedRequest(t *testing.T) {
	root := &renameStub{}
	s := NewRenameSurface(root, RenameSession("s1", "old", "claude", []string{"claude"}))
	host := &stubHost{}
	s.SetName("new")
	s.Key(host, tea.KeyMsg{Type: tea.KeyEnter})
	want := RenameRequest{SessionID: "s1", Name: "new", Tool: "claude"}
	if len(root.requests) != 1 || root.requests[0] != want {
		t.Fatalf("requests = %+v, want %+v", root.requests, want)
	}
	s.Key(host, tea.KeyMsg{Type: tea.KeyEsc})
	if host.popped != 1 {
		t.Fatalf("esc popped %d times, want the row closed", host.popped)
	}
}

// An invalid name is the root's to report, and the row stays open.
func TestRenameSurfaceReportsAnInvalidName(t *testing.T) {
	root := &renameStub{}
	s := NewRenameSurface(root, RenameSession("s", "alpha", "claude", []string{"claude"}))
	host := &stubHost{}
	s.SetName("")
	s.Key(host, tea.KeyMsg{Type: tea.KeyEnter})
	if len(root.errs) != 1 || host.popped != 0 {
		t.Fatalf("an empty name reported %v and popped %d times", root.errs, host.popped)
	}
}

// The row is not a card: it leaves the frame as it is and holds the list
// still under a stray click.
func TestRenameSurfacePaintsNoCard(t *testing.T) {
	s := NewRenameSurface(&renameStub{}, RenameSession("s", "alpha", "claude", []string{"claude"}))
	if s.Dismissible() {
		t.Fatal("a rename holds a typed draft and must not close on a stray click")
	}
	if got := s.View(&stubHost{}, "below"); got != "below" {
		t.Fatalf("view = %q, want the frame unchanged", got)
	}
}
