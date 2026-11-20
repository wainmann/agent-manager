//go:build unix

package inputguard

import (
	"bytes"
	"io"
	"os"
	"sync/atomic"
	"time"
	"unicode/utf8"
)

const (
	// briefTimeout is how long a short read waits for the rest of an SS3 key
	// or half a UTF-8 character. It stays well under Bubble Tea's 500ms
	// ReleaseTerminal budget, so the read loop cannot outlive a release.
	briefTimeout = 50 * time.Millisecond
	readSize     = 1024
)

var (
	pasteStart = []byte("\x1b[200~")
	pasteEnd   = []byte("\x1b[201~")
)

// Guard wraps terminal input for Bubble Tea. It hands over whole messages and
// never blocks on a sequence that may not arrive: only an SS3 head or half a
// UTF-8 character waits, briefly, because a terminal writes those together.
type Guard struct {
	file *os.File
	// reader is the file in production. Tests replace it to land a reset
	// inside a read and pin the stale-length path.
	reader  io.Reader
	fd      int
	pending []byte
	// paste passes everything literally until the bracketed paste end marker.
	paste bool
	// lookBehind keeps the last few delivered bytes while a paste is open, so
	// an end marker split across reads is still recognised.
	lookBehind []byte
	// filled records that the last read used the caller's room (when that room
	// is below the scratch size), so a trailing ESC may have been cut by the
	// read rather than typed.
	filled bool
	// reset is bumped by ResetActive. The read loop compares it and drops
	// what it holds, so reset never touches the reader's state directly.
	reset   atomic.Uint64
	gen     uint64
	err     error
	scratch [readSize]byte
}

// New wraps a terminal file and makes it the guard ResetActive resets. One
// reader must use it at a time.
func New(file *os.File) *Guard {
	g := &Guard{file: file, reader: file, fd: int(file.Fd())}
	current.Store(g)
	return g
}

var current atomic.Pointer[Guard]

// ResetActive asks the active guard to drop bytes it is holding, so a command
// handed the terminal (an editor, ssh attach) starts from a clean boundary.
// The read loop performs the drop, because it is the only owner of that state.
func ResetActive() {
	if g := current.Load(); g != nil {
		g.reset.Add(1)
	}
}

// resetSeen reports a reset that the read loop has not applied yet. Only the
// read loop clears state, so a read that raced a reset cannot hand a caller a
// length computed before the clear.
func (g *Guard) resetSeen() bool {
	return g.reset.Load() != g.gen
}

// Read returns whole messages only. A partial sequence stays in pending and
// is completed by a later read; a broken SGR report is cut at the first byte
// that cannot belong to it. Returning (0, nil) is safe because the bytes it
// holds are not yet a complete message; an SS3 head is handed over as Alt+key
// after its brief wait. A reset is applied only by the read loop, so a read
// that raced one never slices a buffer the reset already cleared.
func (g *Guard) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	didRead := false
	for {
		if gen := g.reset.Load(); gen != g.gen {
			g.gen = gen
			g.pending = nil
			g.paste = false
			g.lookBehind = nil
			g.filled = false
			g.err = nil
			continue
		}
		if g.err != nil {
			if complete := completeLen(g.pending); complete > 0 {
				return g.emit(p, complete), nil
			}
			g.pending = nil
			return 0, g.err
		}
		if g.paste {
			if len(g.pending) > 0 {
				combined := append(append([]byte(nil), g.lookBehind...), g.pending...)
				if idx := bytes.Index(combined, pasteEnd); idx >= 0 {
					markerEnd := idx + len(pasteEnd)
					take := markerEnd - len(g.lookBehind)
					out := append([]byte(nil), g.pending[:take]...)
					rest, tail, pasting := sanitizePaste(g.pending[take:])
					out = append(out, rest...)
					g.pending = tail
					g.paste = pasting
					g.lookBehind = nil
					if pasting {
						g.lookBehind = tailBytes(out, len(pasteEnd)-1)
					} else {
						out = g.extendTail(p, out)
						if g.resetSeen() {
							continue
						}
					}
					return g.emitBytes(p, out), nil
				}
				// Not finished: deliver what arrived and remember the tail.
				g.lookBehind = tailBytes(combined, len(pasteEnd)-1)
				return g.emit(p, len(g.pending)), nil
			}
			if didRead {
				return 0, nil
			}
			didRead = true
			g.readOnce(len(p) - len(g.pending))
			continue
		}

		if trimmed := abortedCSI(g.pending); len(trimmed) != len(g.pending) {
			g.replace(trimmed)
			continue
		}
		if bytes.Contains(g.pending, pasteStart) {
			out, tail, pasting := sanitizePaste(g.pending)
			g.pending = tail
			g.paste = pasting
			if pasting {
				g.lookBehind = tailBytes(out, len(pasteEnd)-1)
			} else {
				out = g.extendTail(p, out)
				if g.resetSeen() {
					continue
				}
			}
			if len(out) > 0 {
				return g.emitBytes(p, out), nil
			}
		}
		complete := completeLen(g.pending)
		if complete > 0 {
			if trimmed := abortedCSI(g.pending[complete:]); len(trimmed) != len(g.pending)-complete {
				g.pending = append(g.pending[:complete:complete], trimmed...)
				continue
			}
			tail := g.pending[complete:]
			if len(tail) == 1 && tail[0] == esc {
				if g.filled {
					// The limit cut a sequence after its ESC; keep it, as
					// Bubble Tea itself does at a full buffer.
					return g.emit(p, complete), nil
				}
				if g.peek(p) {
					continue
				}
				if g.resetSeen() {
					continue
				}
				// A standalone Escape is a key: hand it over with the prefix
				// instead of leaving it for the next keystroke.
				return g.emit(p, complete+1), nil
			}
			if isSS3(tail) && !g.filled {
				// The tail is a complete Alt+key now, but it may still become
				// an SS3 key. Wait once, then hand the prefix and the head
				// over together; otherwise Alt+O waits for the next keystroke.
				if g.waitBriefly(p) {
					continue
				}
				if g.resetSeen() {
					continue
				}
				return g.emit(p, len(g.pending)), nil
			}
			return g.emit(p, complete), nil
		}

		if len(g.pending) == 0 {
			if didRead {
				return 0, nil
			}
			didRead = true
			g.readOnce(len(p))
			continue
		}
		if len(g.pending) == 1 && g.pending[0] == esc {
			if g.filled {
				// The limit cut a sequence after its ESC. Read the rest; if
				// none is queued, hold the byte for the next read as Bubble
				// Tea does at a full buffer.
				if didRead {
					return 0, nil
				}
				didRead = true
				g.readOnce(len(p) - 1)
				continue
			}
			if g.peek(p) {
				continue
			}
			if g.resetSeen() {
				continue
			}
			return g.emit(p, 1), nil
		}
		if isSS3(g.pending) || isPartialRune(g.pending) || isEscPartialRune(g.pending) {
			// Poll even after the read above: this branch may have arrived in
			// that same read, and the cancel reader only wakes on a new fd
			// event. A bare Alt+O must resolve without another keystroke.
			didRead = true
			if g.waitBriefly(p) {
				continue
			}
			if g.resetSeen() {
				continue
			}
			return g.emit(p, len(g.pending)), nil
		}

		room := len(p) - len(g.pending)
		if room <= 0 {
			// A sequence larger than the caller's buffer cannot be delivered;
			// drop its head and let the rest be parsed on its own.
			g.pending = nil
			continue
		}
		if didRead {
			return 0, nil
		}
		didRead = true
		g.readOnce(room)
	}
}

// sanitizePaste copies complete input and pastes, dropping broken CSI heads
// and undecidable prefixes that would otherwise strand a paste behind an
// empty fd. The returned tail is an incomplete non-paste sequence to keep.
func sanitizePaste(b []byte) (out, tail []byte, pasting bool) {
	i := 0
	for i < len(b) {
		if b[i] == esc && bytes.HasPrefix(b[i+1:], pasteStart) {
			// A lone Escape glued to a paste start would turn the marker into
			// Alt+Esc plus text; keep the paste.
			i++
			continue
		}
		if isCSI(b[i:]) {
			if trimmed := abortedCSI(b[i:]); len(trimmed) < len(b[i:]) {
				// Drop the broken head; its offending byte is parsed next.
				i += len(b[i:]) - len(trimmed)
				continue
			}
		}
		j := bytes.Index(b[i:], pasteStart)
		if j == 0 {
			rest := b[i+len(pasteStart):]
			if end := bytes.Index(rest, pasteEnd); end >= 0 {
				at := i + len(pasteStart) + end + len(pasteEnd)
				out = append(out, b[i:at]...)
				i = at
				continue
			}
			return append(out, b[i:]...), nil, true
		}
		if j > 0 {
			prefix := b[i : i+j]
			complete := completeLen(prefix)
			if complete == 0 {
				// An undecidable prefix before a paste: drop it, keep the
				// paste rather than strand it behind an empty fd.
				i += j
				continue
			}
			out = append(out, prefix[:complete]...)
			i += complete
			continue
		}
		complete := completeLen(b[i:])
		if complete == 0 {
			return out, append([]byte(nil), b[i:]...), false
		}
		out = append(out, b[i:i+complete]...)
		i += complete
	}
	return out, nil, false
}

// tailBytes copies at most n trailing bytes of b.
func tailBytes(b []byte, n int) []byte {
	if n <= 0 || len(b) == 0 {
		return nil
	}
	if len(b) > n {
		b = b[len(b)-n:]
	}
	return append([]byte(nil), b...)
}

// extendTail resolves an Escape or SS3 head that follows bytes ready to
// return. A complete key must not stay in pending: the cancel reader only
// calls Read again when the fd has data, so the next keystroke would be the
// only thing that wakes it.
func (g *Guard) extendTail(p []byte, out []byte) []byte {
	for !g.filled {
		if len(out)+len(g.pending) > len(p) {
			return out
		}
		if bytes.Contains(g.pending, pasteStart) {
			rest, tail, pasting := sanitizePaste(g.pending)
			if len(out)+len(rest) > len(p) {
				return out
			}
			out = append(out, rest...)
			g.pending = tail
			g.paste = pasting
			if pasting {
				g.lookBehind = tailBytes(out, len(pasteEnd)-1)
				return out
			}
			continue
		}
		switch {
		case len(g.pending) == 1 && g.pending[0] == esc:
			if !g.peek(p[len(out):]) {
				if g.resetSeen() {
					return out
				}
				if len(out)+len(g.pending) > len(p) {
					return out
				}
				out = append(out, g.pending...)
				g.pending = nil
				return out
			}
		case isSS3(g.pending):
			if !g.waitBriefly(p[len(out):]) {
				if g.resetSeen() {
					return out
				}
				if len(out)+len(g.pending) > len(p) {
					return out
				}
				out = append(out, g.pending...)
				g.pending = nil
				return out
			}
		default:
			return out
		}
		if g.resetSeen() {
			return out
		}
		if complete := completeLen(g.pending); complete > 0 {
			if len(out)+complete > len(p) {
				return out
			}
			out = append(out, g.pending[:complete]...)
			g.pending = g.pending[complete:]
			return out
		}
	}
	return out
}

// emitBytes copies already-compacted bytes out.
func (g *Guard) emitBytes(p, b []byte) int {
	return copy(p, b)
}

// abortedCSI drops the head of a broken CSI when a byte that cannot belong to
// it arrives, keeping that byte. SGR reports accept only their parameters and
// M/m; other CSI accept parameter and intermediate bytes and one final byte.
// A letter in an SGR report, a control byte or a non-ASCII byte therefore ends
// the sequence instead of being swallowed.
func abortedCSI(b []byte) []byte {
	if !isCSI(b) {
		return b
	}
	if len(b) > 2 && b[2] == '<' {
		for i := 3; i < len(b); i++ {
			switch c := b[i]; {
			case c == 'M' || c == 'm':
				return b
			case c >= '0' && c <= '9', c == ';', c == ':':
			default:
				return b[i:]
			}
		}
		return b
	}
	intermediate := false
	for i := 2; i < len(b); i++ {
		c := b[i]
		if !intermediate {
			switch {
			case c >= 0x30 && c <= 0x3f:
				continue
			case c == '$' && i == 3 && (b[2] == '7' || b[2] == '8'):
				// urxvt Shift+Home/End end on $, which Bubble Tea maps.
				return b
			case c >= 0x20 && c <= 0x2f:
				intermediate = true
				continue
			case c >= 0x40 && c <= 0x7e:
				return b
			}
			return b[i:]
		}
		switch {
		case c >= 0x20 && c <= 0x2f:
			continue
		case c >= 0x40 && c <= 0x7e:
			return b
		default:
			return b[i:]
		}
	}
	return b
}

func isCSI(b []byte) bool {
	return len(b) >= 2 && b[0] == esc && b[1] == '['
}

// isSS3 reports whether b is the head of an SS3 key sequence.
func isSS3(b []byte) bool {
	return len(b) == 2 && b[0] == esc && b[1] == 'O'
}

// isPartialRune reports whether b is the beginning of a multi-byte UTF-8
// character whose remaining bytes have not arrived yet.
func isPartialRune(b []byte) bool {
	return len(b) > 0 && b[0] >= 0x80 && !utf8.FullRune(b)
}

// isEscPartialRune covers an Alt-modified character cut inside its UTF-8
// bytes: ESC followed by a partial rune.
func isEscPartialRune(b []byte) bool {
	return len(b) >= 2 && b[0] == esc && b[1] >= 0x80 && !utf8.FullRune(b[1:])
}

// waitBriefly reads for the rest of an SS3 key or a split character.
func (g *Guard) waitBriefly(p []byte) bool {
	return g.waitFor(p, briefTimeout)
}

// peek reads only bytes already queued. A bare Escape stays a key unless the
// rest of its sequence is waiting at this moment, which is crossterm's rule.
func (g *Guard) peek(p []byte) bool {
	return g.waitFor(p, 0)
}

// waitFor reads for up to timeout, reporting whether new bytes arrived.
func (g *Guard) waitFor(p []byte, timeout time.Duration) bool {
	room := len(p) - len(g.pending)
	if room <= 0 {
		return false
	}
	ready, err := waitInput(g.fd, timeout)
	if err != nil {
		g.err = err
		return true
	}
	if !ready {
		return false
	}
	return g.readOnce(room)
}

// readOnce performs at most one terminal read, bounded by the caller's room.
func (g *Guard) readOnce(limit int) bool {
	if limit > readSize {
		limit = readSize
	}
	if limit <= 0 {
		return false
	}
	gen := g.reset.Load()
	n, err := g.reader.Read(g.scratch[:limit])
	if g.reset.Load() != gen {
		// A reset landed while this read was in flight; the bytes belong to
		// the command that asked for the handoff, not to the manager. The
		// read loop applies the reset, so a caller that computed a length
		// before this read cannot slice a buffer that is already cleared.
		return false
	}
	if n > 0 {
		g.pending = append(g.pending, g.scratch[:n]...)
		g.filled = n == limit && limit < readSize
	}
	if err != nil {
		g.err = err
	}
	return n > 0
}

func (g *Guard) emit(p []byte, n int) int {
	copied := copy(p, g.pending[:n])
	g.pending = g.pending[copied:]
	if len(g.pending) == 0 {
		g.pending = nil
	}
	return copied
}

func (g *Guard) replace(b []byte) {
	g.pending = append(g.pending[:0], b...)
}

// Write, Close, Fd and Name keep the wrapped input recognizable as a terminal
// file to Bubble Tea and its cancel reader.
func (g *Guard) Write(p []byte) (int, error) { return g.file.Write(p) }
func (g *Guard) Close() error                { return g.file.Close() }
func (g *Guard) Fd() uintptr                 { return uintptr(g.fd) }
func (g *Guard) Name() string                { return g.file.Name() }

var _ io.ReadWriteCloser = (*Guard)(nil)
