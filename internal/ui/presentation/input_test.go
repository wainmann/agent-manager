package presentation

import (
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/x/ansi"
)

func TestFitInputRebuildsTheScrollWindow(t *testing.T) {
	value := "head-" + strings.Repeat("x", 140) + "-tail"
	for _, tc := range []struct {
		name        string
		from, to    int
		atStart     bool
		wantVisible string
	}{
		{name: "opened with the cursor at the end", from: 0, to: 20, wantVisible: "-tail"},
		{name: "shrunk with the cursor at the start", from: 80, to: 30, atStart: true, wantVisible: "head-"},
		{name: "grown with the cursor at the start", from: 30, to: 80, atStart: true, wantVisible: "head-"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input := textinput.New()
			input.SetValue(value)
			input.CursorEnd()
			if tc.from > 0 {
				FitInput(&input, tc.from)
			}
			if tc.atStart {
				input.CursorStart()
			}
			FitInput(&input, tc.to)
			view := ansi.Strip(input.View())
			// "> " plus the cursor cell past the last character.
			if got, limit := ansi.StringWidth(view), tc.to+3; got > limit {
				t.Fatalf("view is %d columns, want at most %d: %q", got, limit, view)
			}
			if got := ansi.StringWidth(view); got < tc.to {
				t.Fatalf("view is %d columns, want the field filled to %d: %q", got, tc.to, view)
			}
			if !strings.Contains(view, tc.wantVisible) {
				t.Fatalf("view %q lost %q", view, tc.wantVisible)
			}
		})
	}
}
