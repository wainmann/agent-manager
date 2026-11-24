package theme

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestFillBackdropKeycapWidth(t *testing.T) {
	const width = 10
	tests := []struct{ name, line, want string }{
		{"keycap at width", "1\ufe0f\u20e3" + strings.Repeat("x", 8), "1\ufe0f\u20e3" + strings.Repeat("x", 8)},
		{"styled keycap", "\x1b[31m#\ufe0f\u20e3\x1b[0m" + strings.Repeat("x", 8), "#\ufe0f\u20e3" + strings.Repeat("x", 8)},
		{"short keycap", "*\ufe0f\u20e3x", "*\ufe0f\u20e3x" + strings.Repeat(" ", 7)},
		{"combining mark", "a\u0301" + strings.Repeat("x", 9), "a\u0301" + strings.Repeat("x", 9)},
		{"plain ASCII", "abc", "abc" + strings.Repeat(" ", 7)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := FillBackdrop(tt.line, width, "#000000", "#ffffff")
			if widthGot := ansi.StringWidth(got); widthGot != width {
				t.Errorf("got width %d, want %d", widthGot, width)
			}
			if text := ansi.Strip(got); text != tt.want {
				t.Errorf("visible text = %q, want %q", text, tt.want)
			}
		})
	}
}

func TestFillBackdropClearsOnlyRowsTerminalsSizeDifferently(t *testing.T) {
	const width = 10
	tests := []struct {
		name, line string
		clears     bool
	}{
		{"plain ASCII", "abc", false},
		{"styled text", "\x1b[1;31mhello\x1b[0m world", false},
		{"wide CJK", "示例", false},
		{"combining mark", "ábc", false},
		{"keycap", "1️⃣" + strings.Repeat("x", 8), true},
		{"emoji presentation selector", "⚠️ warn", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := FillBackdrop(tt.line, width, "#000000", "#ffffff")
			if clears := strings.Contains(got, ansi.EraseEntireLine); clears != tt.clears {
				t.Fatalf("row clears = %v, want %v: %q", clears, tt.clears, got)
			}
			if before, _, found := strings.Cut(got, ansi.EraseEntireLine); found && ansi.Strip(before) != "" {
				t.Fatalf("row clears after painting %q: %q", ansi.Strip(before), got)
			}
		})
	}
}
