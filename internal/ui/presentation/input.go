package presentation

import "github.com/charmbracelet/bubbles/textinput"

// FitInput sizes a text field and rebuilds its scroll window. bubbles only
// moves that window once the cursor leaves it, so a width change alone keeps
// showing the old span until a keystroke.
func FitInput(input *textinput.Model, width int) {
	width = max(1, width)
	if input.Width == width {
		return
	}
	input.Width = width
	pos := input.Position()
	input.CursorEnd()
	input.SetCursor(pos)
}
