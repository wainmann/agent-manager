package frame

import (
	"bytes"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
)

func inputField(placeholder string, limit int) textinput.Model {
	in := textinput.New()
	in.Placeholder = placeholder
	in.CharLimit = limit
	return in
}

func areaField() textarea.Model {
	in := textarea.New()
	in.CharLimit = 2000
	in.ShowLineNumbers = false
	in.SetPromptFunc(2, func(lineIndex int) string {
		if lineIndex == 0 {
			return "> "
		}
		return "  "
	})
	return in
}

func TestTextInputCursorMarkerSurvivesBothBlinkPhases(t *testing.T) {
	input := inputField("", 60)
	input.Prompt = "» "
	input.SetValue("甲x")
	input.SetCursor(1)
	input.Focus()

	var position [2]int
	for _, blink := range []bool{false, true} {
		input.Cursor.Blink = blink
		view := TextInputView(input)
		if !strings.Contains(view, CursorMarker) {
			t.Fatalf("blink=%v: input view has no cursor marker", blink)
		}
		clean, col, row, ok := CursorMarkerPosition(view)
		if !ok {
			t.Fatalf("blink=%v: cursor marker was not found", blink)
		}
		if strings.Contains(clean, CursorMarker) {
			t.Fatalf("blink=%v: cursor marker leaked into clean view", blink)
		}
		if row != 1 || col != 5 { // two prompt cells, then the two-cell CJK rune
			t.Fatalf("blink=%v: cursor = (%d, %d), want (5, 1)", blink, col, row)
		}
		if blink {
			if position != [2]int{col, row} {
				t.Fatalf("blink-off cursor moved from %v to (%d, %d)", position, col, row)
			}
		} else {
			position = [2]int{col, row}
		}
	}
}

func TestTextAreaCursorMarkerTracksWrappedWideText(t *testing.T) {
	input := areaField()
	input.Placeholder = ""
	input.SetWidth(6)
	input.SetHeight(2)
	input.SetValue("甲乙ab")
	input.SetCursor(3)
	input.Focus()

	var position [2]int
	for _, blink := range []bool{false, true} {
		input.Cursor.Blink = blink
		view := TextAreaView(input)
		_, col, row, ok := CursorMarkerPosition(view)
		if !ok {
			t.Fatalf("blink=%v: textarea view has no cursor marker", blink)
		}
		if col != 4 || row != 2 {
			t.Fatalf("blink=%v: textarea cursor = (%d, %d), want (4, 2)", blink, col, row)
		}
		if blink && position != [2]int{col, row} {
			t.Fatalf("blink-off textarea cursor moved from %v to (%d, %d)", position, col, row)
		}
		position = [2]int{col, row}
	}
}

func TestCursorMarkerPositionHandlesANSIAndWideGlyphs(t *testing.T) {
	frame := "first\n\x1b[31m甲乙\x1b[0m" + CursorMarker + "x"
	clean, col, row, ok := CursorMarkerPosition(frame)
	if !ok || col != 5 || row != 2 {
		t.Fatalf("cursor = (%d, %d, %v), want (5, 2, true)", col, row, ok)
	}
	if strings.Contains(clean, CursorMarker) || ansi.Strip(clean) != "first\n甲乙x" {
		t.Fatalf("clean frame = %q", clean)
	}
}

// startProgram writes what Bubble Tea writes at startup: the cursor is hidden
// first, then the alternate screen opens.
func startProgram(t *testing.T, out *bytes.Buffer, w *CursorOutputWriter) {
	t.Helper()
	for _, seq := range []string{ansi.HideCursor, ansi.SetModeAltScreenSaveCursor} {
		if _, err := w.Write([]byte(seq)); err != nil {
			t.Fatal(err)
		}
	}
	out.Reset()
}

func write(t *testing.T, w io.Writer, chunks ...string) {
	t.Helper()
	for _, chunk := range chunks {
		if _, err := w.Write([]byte(chunk)); err != nil {
			t.Fatal(err)
		}
	}
}

// terminalCursor replays the cursor visibility and CUP sequences in output,
// the way the terminal does, and reports where it leaves the cursor.
func terminalCursor(output string, visible bool) (col, row int, shown bool) {
	shown = visible
	for index := 0; index < len(output); {
		rest := output[index:]
		switch {
		case strings.HasPrefix(rest, ansi.ShowCursor):
			shown = true
			index += len(ansi.ShowCursor)
		case strings.HasPrefix(rest, ansi.HideCursor):
			shown = false
			index += len(ansi.HideCursor)
		case strings.HasPrefix(rest, "\x1b["):
			end := strings.IndexAny(rest[2:], "ABCDEFGHJKSTfhlmnsu")
			if end < 0 {
				return col, row, shown
			}
			if rest[2+end] == 'H' {
				col, row = 1, 1
				fmt.Sscanf(rest[2:2+end], "%d;%d", &row, &col)
			}
			index += 3 + end
		default:
			index++
		}
	}
	return col, row, shown
}

func TestCursorOutputShowsTheRealCursorAtTheAnchor(t *testing.T) {
	var out bytes.Buffer
	anchor := &CursorAnchor{}
	w := NewCursorOutputWriter(&out, anchor)
	startProgram(t, &out, w)
	anchor.Set(14, 9, true)

	write(t, w, "frame")
	if want := "frame" + ansi.CursorPosition(14, 9) + ansi.ShowCursor; out.String() != want {
		t.Fatalf("output = %q, want %q", out.String(), want)
	}
	if col, row, shown := terminalCursor(out.String(), false); !shown || col != 14 || row != 9 {
		t.Fatalf("terminal cursor = (%d, %d) shown=%v, want (14, 9) shown", col, row, shown)
	}
}

func TestCursorOutputHidesTheCursorWhilePainting(t *testing.T) {
	var out bytes.Buffer
	anchor := &CursorAnchor{}
	anchor.Set(14, 9, true)
	w := NewCursorOutputWriter(&out, anchor)
	startProgram(t, &out, w)

	write(t, w, "first")
	out.Reset()
	anchor.Set(15, 9, true)
	write(t, w, "second")
	want := ansi.HideCursor + "second" + ansi.CursorPosition(15, 9) + ansi.ShowCursor
	if out.String() != want {
		t.Fatalf("repaint = %q, want %q", out.String(), want)
	}
}

func TestCursorOutputHidesTheCursorWhenTheAnchorClears(t *testing.T) {
	var out bytes.Buffer
	anchor := &CursorAnchor{}
	w := NewCursorOutputWriter(&out, anchor)
	startProgram(t, &out, w)

	write(t, w, "list")
	if out.String() != "list" {
		t.Fatalf("frame without an input = %q, want it untouched", out.String())
	}
	anchor.Set(4, 3, true)
	write(t, w, "focused")
	anchor.Set(0, 0, false)
	write(t, w, "list")
	want := "list" + "focused" + ansi.CursorPosition(4, 3) + ansi.ShowCursor + ansi.HideCursor + "list"
	if out.String() != want {
		t.Fatalf("output = %q, want %q", out.String(), want)
	}
	if _, _, shown := terminalCursor(out.String(), false); shown {
		t.Fatal("cursor stayed visible after the input closed")
	}
}

func TestCursorOutputLeavesOrdinaryOutputAlone(t *testing.T) {
	var out bytes.Buffer
	anchor := &CursorAnchor{}
	anchor.Set(14, 9, true)
	w := NewCursorOutputWriter(&out, anchor)

	write(t, w, "shell", ansi.SetModeAltScreenSaveCursor, "frame")
	if want := "shell" + ansi.SetModeAltScreenSaveCursor + "frame"; out.String() != want {
		t.Fatalf("output before Bubble Tea hid its cursor = %q, want %q", out.String(), want)
	}
}

// Quitting, suspending and attaching all run Bubble Tea's
// restoreTerminalState: show the cursor, then leave the alternate screen.
func TestCursorOutputHandsBackAVisibleCursor(t *testing.T) {
	var out bytes.Buffer
	anchor := &CursorAnchor{}
	anchor.Set(14, 9, true)
	w := NewCursorOutputWriter(&out, anchor)
	startProgram(t, &out, w)
	write(t, w, "frame")

	for _, anchored := range []bool{true, false} {
		anchor.Set(14, 9, anchored)
		out.Reset()
		write(t, w, ansi.ShowCursor, ansi.ResetModeAltScreenSaveCursor, ansi.ShowCursor, "shell")
		if _, _, shown := terminalCursor(out.String(), true); !shown {
			t.Fatalf("anchored=%v: cursor hidden after Bubble Tea released the terminal: %q", anchored, out.String())
		}
		if strings.Contains(out.String(), "\x1b[9;14H") {
			t.Fatalf("anchored=%v: released terminal was repositioned: %q", anchored, out.String())
		}
		startProgram(t, &out, w)
	}
}

func TestCursorOutputShowsTheCursorAgainAfterAttach(t *testing.T) {
	var out bytes.Buffer
	anchor := &CursorAnchor{}
	anchor.Set(14, 9, true)
	w := NewCursorOutputWriter(&out, anchor)
	startProgram(t, &out, w)
	write(t, w, "frame", ansi.ShowCursor, ansi.ResetModeAltScreenSaveCursor)
	out.Reset()

	write(t, w, "child output")
	if out.String() != "child output" {
		t.Fatalf("output while attached = %q, want it untouched", out.String())
	}
	write(t, w, ansi.HideCursor, ansi.SetModeAltScreenSaveCursor, ansi.EraseEntireScreen, "frame")
	if col, row, shown := terminalCursor(out.String(), true); !shown || col != 14 || row != 9 {
		t.Fatalf("terminal cursor after attach = (%d, %d) shown=%v, want (14, 9) shown", col, row, shown)
	}
}

func TestCursorOutputTracksSplitCursorHide(t *testing.T) {
	sequence := ansi.HideCursor
	for split := 1; split < len(sequence); split++ {
		t.Run(fmt.Sprintf("split_at_%d", split), func(t *testing.T) {
			var out bytes.Buffer
			anchor := &CursorAnchor{}
			anchor.Set(14, 9, true)
			w := NewCursorOutputWriter(&out, anchor)
			write(t, w, ansi.SetModeAltScreenSaveCursor, sequence[:split])
			if got := out.String(); got != ansi.SetModeAltScreenSaveCursor+sequence[:split] {
				t.Fatalf("partial hide output = %q", got)
			}
			write(t, w, sequence[split:]+"frame")
			want := ansi.SetModeAltScreenSaveCursor + sequence + "frame" + ansi.CursorPosition(14, 9) + ansi.ShowCursor
			if got := out.String(); got != want {
				t.Fatalf("completed hide output = %q, want %q", got, want)
			}
		})
	}
}

func TestCursorOutputTracksSplitAlternateScreenEntry(t *testing.T) {
	sequence := ansi.SetModeAltScreenSaveCursor
	for split := 1; split < len(sequence); split++ {
		t.Run(fmt.Sprintf("split_at_%d", split), func(t *testing.T) {
			var out bytes.Buffer
			anchor := &CursorAnchor{}
			anchor.Set(14, 9, true)
			w := NewCursorOutputWriter(&out, anchor)
			write(t, w, ansi.HideCursor)
			out.Reset()

			write(t, w, sequence[:split])
			if got := out.String(); got != sequence[:split] {
				t.Fatalf("partial entry output = %q, want %q", got, sequence[:split])
			}
			write(t, w, sequence[split:]+"frame")
			want := sequence + "frame" + ansi.CursorPosition(14, 9) + ansi.ShowCursor
			if got := out.String(); got != want {
				t.Fatalf("completed entry output = %q, want %q", got, want)
			}
		})
	}
}

func TestCursorOutputTracksSplitAlternateScreenExit(t *testing.T) {
	sequence := ansi.ResetModeAltScreenSaveCursor
	for split := 1; split < len(sequence); split++ {
		t.Run(fmt.Sprintf("split_at_%d", split), func(t *testing.T) {
			var out bytes.Buffer
			anchor := &CursorAnchor{}
			w := NewCursorOutputWriter(&out, anchor)
			startProgram(t, &out, w)
			anchor.Set(14, 9, true)

			write(t, w, sequence[:split])
			out.Reset()
			write(t, w, sequence[split:]+"shell")
			if got := out.String(); got != sequence[split:]+"shell" {
				t.Fatalf("completed exit output = %q, want %q", got, sequence[split:]+"shell")
			}
		})
	}
}

func TestCursorStampRepaintsOnACaretMoveAndNeverReachesTheTerminal(t *testing.T) {
	anchor := &CursorAnchor{}
	if anchor.Stamp() != "" {
		t.Fatal("cleared anchor has a stamp")
	}
	anchor.Set(4, 3, true)
	first := anchor.Stamp()
	anchor.Set(5, 3, true)
	second := anchor.Stamp()
	if first == "" || first == second {
		t.Fatalf("stamps %q and %q do not tell the caret cells apart", first, second)
	}
	if width := ansi.StringWidth(second + "frame"); width != len("frame") {
		t.Fatalf("stamped frame width = %d, want %d", width, len("frame"))
	}

	var out bytes.Buffer
	anchor.Set(0, 0, false)
	w := NewCursorOutputWriter(&out, anchor)
	startProgram(t, &out, w)
	anchor.Set(5, 3, true)
	frame := second + "row one\r\nrow two"
	n, err := w.Write([]byte(frame))
	if err != nil || n != len(frame) {
		t.Fatalf("write = (%d, %v), want (%d, nil)", n, err, len(frame))
	}
	if want := "row one\r\nrow two" + ansi.CursorPosition(5, 3) + ansi.ShowCursor; out.String() != want {
		t.Fatalf("output = %q, want %q", out.String(), want)
	}
}

func TestCursorTTYOutputRoutesStringWritesThroughCursorWriter(t *testing.T) {
	var out bytes.Buffer
	anchor := &CursorAnchor{}
	anchor.Set(14, 9, true)
	w := NewCursorOutputWriter(&out, anchor)
	tty := &CursorTTYOutput{writer: w}

	for _, seq := range []string{ansi.HideCursor, ansi.SetModeAltScreenSaveCursor} {
		if _, err := io.WriteString(tty, seq); err != nil {
			t.Fatal(err)
		}
	}
	if !strings.HasSuffix(out.String(), ansi.CursorPosition(14, 9)+ansi.ShowCursor) {
		t.Fatalf("string writes bypassed the cursor writer: %q", out.String())
	}
}

type scriptedWriter struct {
	writes  [][]byte
	failOn  int
	shortOn int
	call    int
}

func (w *scriptedWriter) Write(p []byte) (int, error) {
	w.call++
	w.writes = append(w.writes, append([]byte(nil), p...))
	if w.call == w.failOn {
		return 0, io.ErrClosedPipe
	}
	if w.call == w.shortOn {
		return len(p) - 1, io.ErrShortWrite
	}
	return len(p), nil
}

func TestCursorOutputStopsAnchoringAfterAFailedWrite(t *testing.T) {
	for name, out := range map[string]*scriptedWriter{
		"short":  {shortOn: 3},
		"failed": {failOn: 3},
	} {
		t.Run(name, func(t *testing.T) {
			anchor := &CursorAnchor{}
			anchor.Set(4, 3, true)
			w := NewCursorOutputWriter(out, anchor)
			write(t, w, ansi.HideCursor, ansi.SetModeAltScreenSaveCursor)

			if _, err := w.Write([]byte("frame")); err == nil {
				t.Fatal("failed write reported no error")
			}
			write(t, w, "next")
			if got := string(out.writes[len(out.writes)-1]); got != "next" {
				t.Fatalf("write after a failure = %q, want it untouched", got)
			}
		})
	}
}

// The real terminal cursor marks a focused field's caret, so the widget's own
// reversed cell is left out in both blink phases.
func TestFocusedInputsPaintNoCaretOfTheirOwn(t *testing.T) {
	previous := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(previous) })

	input := inputField("", 60)
	input.SetValue("abc")
	input.SetCursor(1)
	input.Focus()
	area := areaField()
	area.SetWidth(20)
	area.SetValue("abc")
	area.Focus()

	for _, blink := range []bool{false, true} {
		input.Cursor.Blink, area.Cursor.Blink = blink, blink
		offInput, offArea := input, area
		offInput.Cursor.Blink, offArea.Cursor.Blink = true, true
		if !blink && offInput.View() == input.View() {
			t.Fatal("test setup: the widget paints no caret to leave out")
		}
		if got := StripCursorMarker(TextInputView(input)); got != offInput.View() {
			t.Fatalf("blink=%v: input view = %q, want the unpainted %q", blink, got, offInput.View())
		}
		if got := StripCursorMarker(TextAreaView(area)); got != offArea.View() {
			t.Fatalf("blink=%v: textarea view = %q, want the unpainted %q", blink, got, offArea.View())
		}
	}
}
