package frame

import (
	"bytes"
	"io"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

const (
	cursorAnchorValid  = uint64(1) << 63
	cursorAnchorMarker = "\x1b]1337;agent-manager-ime-cursor\x07"
)

var (
	altScreenEnter = []byte(ansi.SetModeAltScreenSaveCursor)
	altScreenExit  = []byte(ansi.ResetModeAltScreenSaveCursor)
)

// Bubble Tea v2 places the cursor itself through View.Cursor, so this file
// and the marker plumbing in the input views go away with that migration.
//
// CursorAnchor is the real terminal cell an input method should open beside.
// Bubble Tea v1 finishes every alternate-screen render at the bottom-left,
// while focus mode paints a pane caret elsewhere, so an IME otherwise follows
// that renderer cursor instead of the caret the user is typing at.
type CursorAnchor struct {
	packed atomic.Uint64
}

func (a *CursorAnchor) Set(col, row int, ok bool) {
	if !ok || col < 1 || row < 1 {
		a.packed.Store(0)
		return
	}
	a.packed.Store(cursorAnchorValid | uint64(uint32(row))<<32 | uint64(uint32(col)))
}

func (a *CursorAnchor) Get() (col, row int, ok bool) {
	packed := a.packed.Load()
	if packed&cursorAnchorValid == 0 {
		return 0, 0, false
	}
	return int(uint32(packed)), int(uint32((packed &^ cursorAnchorValid) >> 32)), true
}

// CursorMarker is the private marker an input view paints at its caret, so a
// finished frame can name the cell the IME belongs at.
const CursorMarker = cursorAnchorMarker

// StripCursorMarker removes the private marker before Bubble Tea writes the
// frame.
func StripCursorMarker(frame string) string {
	return strings.ReplaceAll(frame, cursorAnchorMarker, "")
}

func CursorMarkerPosition(frame string) (clean string, col, row int, ok bool) {
	index := strings.Index(frame, cursorAnchorMarker)
	if index < 0 {
		return frame, 0, 0, false
	}
	clean = strings.ReplaceAll(frame, cursorAnchorMarker, "")
	prefix := frame[:index]
	row = strings.Count(prefix, "\n") + 1
	if newline := strings.LastIndexByte(prefix, '\n'); newline >= 0 {
		prefix = prefix[newline+1:]
	}
	return clean, ansi.StringWidth(prefix) + 1, row, true
}

// TextInputView and TextAreaView mark the cursor cell and leave it unpainted:
// the writer shows the real terminal cursor there, and a painted block on the
// same cell would draw a second caret.
func TextInputView(input textinput.Model) string {
	if !input.Focused() {
		return input.View()
	}
	marked := input
	marked.Cursor.Blink = false
	marked.Cursor.Style = cursorMarkerStyle(marked.Cursor.Style)
	plain := input
	plain.Cursor.Blink = true
	return insertMarkerAtCursor(plain.View(), marked.View())
}

func TextAreaView(input textarea.Model) string {
	if !input.Focused() {
		return input.View()
	}
	marked := input
	marked.Cursor.Blink = false
	marked.Cursor.Style = cursorMarkerStyle(marked.Cursor.Style)
	plain := input
	plain.Cursor.Blink = true
	return insertMarkerAtCursor(plain.View(), marked.View())
}

func cursorMarkerStyle(style lipgloss.Style) lipgloss.Style {
	transform := style.GetTransform()
	return style.Transform(func(value string) string {
		if transform != nil {
			value = transform(value)
		}
		return cursorAnchorMarker + value
	})
}

func insertMarkerAtCursor(view, markedView string) string {
	_, col, row, ok := CursorMarkerPosition(markedView)
	if !ok {
		return view
	}
	lines := strings.Split(view, "\n")
	row--
	col--
	if row < 0 || row >= len(lines) {
		return view
	}
	line := lines[row]
	cell, state := 0, ansi.NormalState
	for index := 0; index < len(line); {
		_, width, n, nextState := ansi.GraphemeWidth.DecodeSequenceInString(line[index:], state, nil)
		if n <= 0 {
			break
		}
		if width > 0 && cell+width > col {
			lines[row] = line[:index] + cursorAnchorMarker + line[index:]
			return strings.Join(lines, "\n")
		}
		cell += width
		index += n
		state = nextState
	}
	if cell == col {
		lines[row] += cursorAnchorMarker
		return strings.Join(lines, "\n")
	}
	return view
}

// Stamp is a zero-width private sequence carrying the anchor, prepended to
// the frame so Bubble Tea repaints when only the caret moved. The writer
// strips it before the terminal sees it.
func (a *CursorAnchor) Stamp() string {
	packed := a.packed.Load()
	if packed&cursorAnchorValid == 0 {
		return ""
	}
	return cursorStampPrefix + strconv.FormatUint(packed&^cursorAnchorValid, 36) + "\x07"
}

const cursorStampPrefix = "\x1b]1337;agent-manager-ime-anchor="

// StripCursorStamps reports frame as the terminal receives it.
func StripCursorStamps(frame string) string {
	return string(stripCursorStamps([]byte(frame)))
}

func stripCursorStamps(p []byte) []byte {
	prefix := []byte(cursorStampPrefix)
	if !bytes.Contains(p, prefix) {
		return p
	}
	out := make([]byte, 0, len(p))
	for {
		start := bytes.Index(p, prefix)
		if start < 0 {
			return append(out, p...)
		}
		end := bytes.IndexByte(p[start:], '\x07')
		if end < 0 {
			return append(out, p...)
		}
		out = append(out, p[:start]...)
		p = p[start+end+1:]
	}
}

// CursorOutputWriter shows the real terminal cursor at the active input caret
// after each Bubble Tea render, and hides it again when no input is active.
// Terminals such as iTerm2 open the IME candidate window only beside a
// visible cursor. It acts only inside the alternate screen while Bubble Tea
// keeps its own cursor hidden, so an attached session, an editor, and the
// shell after quit keep the cursor state Bubble Tea hands them.
type CursorOutputWriter struct {
	out          io.Writer
	anchor       *CursorAnchor
	mu           sync.Mutex
	altScreen    bool
	cursorHidden bool
	shown        bool
	modePrefix   []byte
}

// NewCursorOutputWriter reports the writer that places out's cursor at anchor
// after every rendered frame.
func NewCursorOutputWriter(out io.Writer, anchor *CursorAnchor) *CursorOutputWriter {
	return &CursorOutputWriter{out: out, anchor: anchor}
}

func (w *CursorOutputWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	data := stripCursorStamps(p)
	var buf []byte
	// Hidden while the frame paints, so the cursor never flashes at the
	// renderer's intermediate positions.
	if w.shown {
		buf = append(buf, ansi.HideCursor...)
	}
	buf = append(buf, data...)
	w.shown = false
	if incomplete := w.trackModes(data); !incomplete && w.altScreen && w.cursorHidden {
		if col, row, ok := w.anchor.Get(); ok {
			buf = append(buf, ansi.CursorPosition(col, row)...)
			buf = append(buf, ansi.ShowCursor...)
			w.shown = true
		}
	}
	n, err := w.out.Write(buf)
	if err == nil && n != len(buf) {
		err = io.ErrShortWrite
	}
	if err != nil {
		w.resetTerminalState()
		return 0, err
	}
	return len(p), nil
}

func (w *CursorOutputWriter) resetTerminalState() {
	w.altScreen = false
	w.cursorHidden = false
	w.shown = false
	w.modePrefix = nil
}

var trackedModes = [][]byte{altScreenEnter, altScreenExit, cursorHide, cursorShow}

var (
	cursorHide = []byte(ansi.HideCursor)
	cursorShow = []byte(ansi.ShowCursor)
)

// trackModes follows Bubble Tea's alternate-screen and cursor-visibility
// sequences, including one split across writes, and reports whether data
// ends inside one.
func (w *CursorOutputWriter) trackModes(p []byte) bool {
	data := p
	if len(w.modePrefix) > 0 {
		data = slices.Concat(w.modePrefix, p)
	}
	if enter, exit := bytes.LastIndex(data, altScreenEnter), bytes.LastIndex(data, altScreenExit); enter >= 0 || exit >= 0 {
		w.altScreen = enter > exit
	}
	if hide, show := bytes.LastIndex(data, cursorHide), bytes.LastIndex(data, cursorShow); hide >= 0 || show >= 0 {
		w.cursorHidden = hide > show
	}
	w.modePrefix = trailingModePrefix(data)
	return len(w.modePrefix) > 0
}

func trailingModePrefix(data []byte) []byte {
	longest := 0
	for _, mode := range trackedModes {
		longest = max(longest, len(mode)-1)
	}
	for size := min(len(data), longest); size > 0; size-- {
		suffix := data[len(data)-size:]
		for _, mode := range trackedModes {
			if size < len(mode) && bytes.Equal(suffix, mode[:size]) {
				return bytes.Clone(suffix)
			}
		}
	}
	return nil
}

// CursorTTYOutput preserves stdout's terminal identity for Bubble Tea while
// overriding writes with the cursor anchor behavior above.
type CursorTTYOutput struct {
	*os.File
	writer *CursorOutputWriter
}

// NewCursorTTYOutput wraps output so Bubble Tea keeps its terminal sizing and
// the caret lands where the writer puts it.
func NewCursorTTYOutput(output *os.File, anchor *CursorAnchor) *CursorTTYOutput {
	return &CursorTTYOutput{File: output, writer: NewCursorOutputWriter(output, anchor)}
}

func (w *CursorTTYOutput) Write(p []byte) (int, error) { return w.writer.Write(p) }

// *os.File's promoted WriteString would bypass Write when Bubble Tea emits
// alternate-screen lifecycle sequences through io.WriteString.
func (w *CursorTTYOutput) WriteString(s string) (int, error) {
	return w.writer.Write([]byte(s))
}
