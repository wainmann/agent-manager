package settings

import (
	"testing"

	"github.com/YoanWai/agent-manager/internal/keybind"
)

// keyWindow always fits the room it is given and always shows the cursor's
// row, at any cursor position. It can fall short of the room when the next
// row would open a new table, since that costs a blank line and a heading
// before the row itself; the ui package's picker test states that cost.
func TestKeyWindowFitsAndShowsTheCursor(t *testing.T) {
	rows := KeyRows([]keybind.Table{keybind.DefaultSession(), keybind.DefaultList()})
	for room := 2; room <= 20; room++ {
		for cursor := 0; cursor < len(rows); cursor++ {
			s := &State{KeyCursor: cursor}
			first, last := s.keyWindow(rows, room)
			if first > cursor || cursor >= last {
				t.Fatalf("room %d cursor %d: the cursor's row is outside [%d,%d)", room, cursor, first, last)
			}
			if last-first == 1 {
				// The cursor's row shows even when the heading and the counter
				// around it do not fit; the card clips rather than lose it.
				continue
			}
			if lines := keyLineCount(rows, first, last); lines > room {
				t.Fatalf("room %d cursor %d: the window paints %d lines", room, cursor, lines)
			}
		}
	}
}
