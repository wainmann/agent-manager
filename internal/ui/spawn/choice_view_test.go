package spawn

import (
	"slices"
	"strings"
	"testing"

	catalog "github.com/YoanWai/agent-manager/internal/catalog/value"
	"github.com/charmbracelet/x/ansi"
)

// The default row reads cli default and sits above both headings.
func TestModelListNamesTheDefaultRowAboveItsHeadings(t *testing.T) {
	h := newFakeChoiceHost()
	h.settings[recentModelsKey("claude")] = `["opus"]`
	ch := NewChoice(h, "claude")
	ch.OpenFilter(h, "claude")
	lines, entries := ch.ViewSuggestions(h, "claude", 0, 60, ModelListRows, Styles{})
	if !slices.Equal(entries, []int{0, -1, 1, -1, 2, 3}) {
		t.Fatalf("entries = %v", entries)
	}
	for i, want := range []string{"cli default", "recent", "opus", "from claude · 3 models", "default", "haiku"} {
		if got := ansi.Strip(lines[i]); !strings.Contains(got, want) {
			t.Fatalf("line %d = %q, want %q", i, got, want)
		}
	}
}

// Stepping back up a scrolled list to the first model brings the default
// row and the headings above it back.
func TestModelListScrollsBackToTheDefaultRow(t *testing.T) {
	h := newFakeChoiceHost()
	h.answers["claude"] = &Answer{Loaded: true, Cat: catalog.Catalog{Models: []catalog.Model{
		{ID: "opus"}, {ID: "sonnet"}, {ID: "haiku"}, {ID: "fable"}, {ID: "luna"},
	}}}
	h.settings[recentModelsKey("claude")] = `["opus"]`
	ch := NewChoice(h, "claude")
	ch.OpenFilter(h, "claude")
	for range 4 {
		ch.StepList(h, "claude", 1)
		ch.ViewSuggestions(h, "claude", 0, 60, 3, Styles{})
	}
	for range 4 {
		ch.StepList(h, "claude", -1)
	}
	lines, _ := ch.ViewSuggestions(h, "claude", 0, 60, 3, Styles{})
	for i, want := range []string{"cli default", "recent", "opus"} {
		if got := ansi.Strip(lines[i]); !strings.Contains(got, want) {
			t.Fatalf("line %d = %q, want %q", i, got, want)
		}
	}
}

// A list two rows tall cannot hold the default row, the heading and the
// first model, so it keeps the highlighted model on screen instead.
func TestShortModelListKeepsTheHighlightOnScreen(t *testing.T) {
	h := newFakeChoiceHost()
	ch := NewChoice(h, "claude")
	ch.OpenFilter(h, "claude")
	lines, entries := ch.ViewSuggestions(h, "claude", 0, 60, 2, Styles{})
	if !slices.Contains(entries, ch.sugg.index) {
		t.Fatalf("the highlighted entry %d is off screen: entries %v, lines %q", ch.sugg.index, entries, lines)
	}
}
