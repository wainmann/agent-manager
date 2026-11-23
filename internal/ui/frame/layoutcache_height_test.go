package frame

import (
	"fmt"
	"strings"
	"testing"

	"github.com/YoanWai/agent-manager/internal/ui/presentation"
	"github.com/charmbracelet/lipgloss"
)

func TestLayoutCacheListHeightUsesActualSelectedTiers(t *testing.T) {
	for _, glyph := range []presentation.GlyphMode{presentation.GlyphModeAdvanced, presentation.GlyphModeBasic} {
		for _, width := range []int{10, 15, 28, 40, 60, 80, 130, 200, 256} {
			t.Run(fmt.Sprintf("%s/%d", glyph, width), func(t *testing.T) {
				var cache LayoutCache
				list := []Section{{Title: "Session", Pairs: [][2]string{{"enter", "focus"}, {"r", "rename"}}}, {Title: "View", Quiet: true, Pairs: [][2]string{{"q", "quit"}, {"?", "help"}}}}
				refs := [][]Section{list, {{Title: "Session", Pairs: [][2]string{{"alt+backspace", "a longer action"}, {"ctrl+shift+r", "another long action"}}}, list[1]}}
				for _, change := range []func(){func() {}, func() { list[0].Pairs[0] = [2]string{"ctrl+alt+backspace", "a different action"} }, func() { list[0].Hint = "a hint" }, func() { list[1].Alerts = [][2]string{{"M", "unread messages"}} }, func() { list[0].Title = "A much longer title" }} {
					change()
					f := Footer{Mode: glyph, List: list, Measurement: cache.Measure(refs, width, 8, glyph), Width: width}
					want := lipgloss.Height(f.ListLegend().Text)
					height := cache.ListHeight(f)
					f.ListHeight = func() int { return cache.ListHeight(f) }
					if height != want {
						t.Fatalf("height=%d want%d", height, want)
					}
					tier := Section{Title: "Focused", Pairs: [][2]string{{"ctrl+q", "back"}}}
					bar := f.Transient(tier)
					if got := lipgloss.Height(bar.Text); got < want {
						t.Fatalf("transient height=%d base=%d", got, want)
					}
					if len(bar.Hits) == 0 {
						t.Fatal("transient lost back key hit")
					}
				}
			})
		}
	}
}

func TestLayoutCacheListHeightDoesNotUseReferenceBudget(t *testing.T) {
	var cache LayoutCache
	f := Footer{Mode: presentation.GlyphModeAdvanced, Width: 200, Measurement: Measurement{Mode: Titled, Rows: MaxRows}, List: []Section{{Title: "One", Pairs: [][2]string{{"x", "go"}}}, {Title: "Two", Pairs: [][2]string{{"q", "quit"}}}}}
	if got := cache.ListHeight(f); got != 2 {
		t.Fatalf("actual list height=%d want2, reference budget=%d", got, f.Measurement.Rows)
	}
	f.List[0].Pairs = append(f.List[0].Pairs, [2]string{"r", strings.Repeat("long ", 60)})
	if got, want := cache.ListHeight(f), lipgloss.Height(f.ListLegend().Text); got != want || got != 3 {
		t.Fatalf("changed tier height=%d want%d", got, want)
	}
}
