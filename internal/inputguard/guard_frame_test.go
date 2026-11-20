package inputguard

import (
	"testing"
)

func TestCompleteLen(t *testing.T) {
	cases := []struct {
		input string
		want  int
	}{
		{"", 0},
		{"a", 1},
		{"\x1b", 0},
		{"\x1b[", 0},
		{"\x1b[<35;1;2", 0},
		{"\x1b[<35;\xe4\xb8\xad", 0},
		{"\x1b[<35;\x7f", 0},
		{"\x1b[7$", 4},
		{"\x1b[8$", 4},
		{"\x1b[1 q", 5},
		{"\x1b[?2026;1$y", 11},
		{"\x1b[?2004;1$y", 11},
		{"\x1b[<35;1;2M", 10},
		{"\x1b[A", 3},
		{"\x1b[200~", 6},
		{"\x1b[Mabc", 6},
		{"\x1b]x", 3},
		{"\xe4\xb8", 0},
		{"中", 3},
	}
	for _, test := range cases {
		if got := completeLen([]byte(test.input)); got != test.want {
			t.Errorf("completeLen(%q) = %d, want %d", test.input, got, test.want)
		}
	}
}
