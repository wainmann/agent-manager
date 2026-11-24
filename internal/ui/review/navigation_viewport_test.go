package review

import "testing"

// Sizing the viewport renders a row to count its painted rows, so it must
// ask only for the rows between the scroll and the cursor, and each once:
// asking for the whole file re-rendered every line on every frame.
func TestPrepareViewportMeasuresOnlyTheRowsItShows(t *testing.T) {
	m := New(false)
	m.cursorLine, m.scroll = 5000, 4970
	asked := map[int]int{}
	m.PrepareViewport(9000, func(i int) int {
		asked[i]++
		return 2
	}, 20)
	for i, n := range asked {
		if i < 4970 || i > 5000 {
			t.Fatalf("measured row %d, outside scroll 4970 through cursor 5000", i)
		}
		if n != 1 {
			t.Fatalf("measured row %d %d times", i, n)
		}
	}
	// Two-row lines in a 20-row viewport with an overflow row each side fit
	// nine lines, so the cursor ends at the bottom of a window from 4992.
	if m.scroll != 4992 {
		t.Fatalf("scroll = %d, want 4992", m.scroll)
	}
}
