package focus

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestBottomWindow(t *testing.T) {
	capture := "a\nb\nc\nd\ne\n"
	cases := []struct {
		rows, offset int
		want         string
	}{
		{2, 0, "d\ne\n"},
		{2, 1, "c\nd\n"},
		{3, 4, "a\n"},
		{2, 9, "\n"},
	}
	for _, c := range cases {
		if got := BottomWindow(capture, c.rows, c.offset); got != c.want {
			t.Errorf("BottomWindow(rows %d, offset %d) = %q, want %q", c.rows, c.offset, got, c.want)
		}
	}
}

func TestShiftArrowsScrollByTheStep(t *testing.T) {
	m := Model{pane: paneState{sessionID: "s", history: 50}}
	m.SetScrollStep(5)
	ctx := KeyContext{SessionID: "s", Rows: 20}
	if r := m.Key(tea.KeyMsg{Type: tea.KeyShiftUp}, ctx); r.Action != ScrollPane || m.scroll != 5 {
		t.Fatalf("shift+up: action %v, scroll %d; want ScrollPane, 5", r.Action, m.scroll)
	}
	m.fetchInFlight = false
	if r := m.Key(tea.KeyMsg{Type: tea.KeyShiftDown}, ctx); r.Action != ScrollPane || m.scroll != 0 {
		t.Fatalf("shift+down: action %v, scroll %d; want ScrollPane, 0", r.Action, m.scroll)
	}
	m.pane.alt = true // an app-owned screen keeps its own keys
	if r := m.Key(tea.KeyMsg{Type: tea.KeyShiftUp}, ctx); r.Action != ForwardKey {
		t.Fatalf("alt screen: action %v, want ForwardKey", r.Action)
	}
}
