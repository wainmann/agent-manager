//go:build unix

package inputguard

import (
	"io"
	"os"
	"sync/atomic"
)

// readSize bounds one call to the terminal. The caller's room is smaller in
// production (Bubble Tea reads 256 bytes), so a burst leaves the rest for the
// next call instead of splitting a message across two reads.
const readSize = 1024

// Guard wraps terminal input for Bubble Tea. It never polls: an incomplete
// tail waits for the next fd event, which the cancel reader is already
// watching. Escape, Alt+key and SS3 keys are delivered without a timer.
type Guard struct {
	file *os.File
	// reader is the file in production. Tests replace it to land a reset
	// inside a read.
	reader  io.Reader
	pending []byte
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
	g := &Guard{file: file, reader: file}
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

// Read returns whole messages only. A partial CSI or UTF-8 tail stays in
// pending and is completed by a later read; returning (0, nil) while holding
// it is safe because the cancel reader only calls Read again on a new fd
// event, and the missing bytes are what that event delivers.
func (g *Guard) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	read := false
	for {
		if gen := g.reset.Load(); gen != g.gen {
			g.gen = gen
			g.pending = nil
			g.err = nil
			continue
		}
		if complete := completeLen(g.pending); complete > 0 {
			n := copy(p, g.pending[:complete])
			g.pending = g.pending[n:]
			if len(g.pending) == 0 {
				g.pending = nil
			}
			return n, nil
		}
		if g.err != nil {
			err := g.err
			g.err = nil
			return 0, err
		}
		if read {
			return 0, nil
		}
		read = true
		room := len(p) - len(g.pending)
		if room <= 0 {
			// An unterminated tail as large as the caller's buffer cannot be
			// delivered; drop it so the next key is not wedged forever.
			g.pending = nil
			return 0, nil
		}
		if err := g.readOnce(room); err != nil {
			g.err = err
		}
	}
}

// readOnce performs at most one terminal read, bounded by the caller's room.
// Bytes that arrived while a reset was in flight belong to the command that
// asked for the handoff, not to the manager, so they are discarded.
func (g *Guard) readOnce(limit int) error {
	if limit > readSize {
		limit = readSize
	}
	if limit <= 0 {
		return nil
	}
	gen := g.reset.Load()
	n, err := g.reader.Read(g.scratch[:limit])
	if g.reset.Load() != gen {
		return nil
	}
	if n > 0 {
		g.pending = append(g.pending, g.scratch[:n]...)
	}
	return err
}

// Write, Close, Fd and Name keep the wrapped input recognizable as a terminal
// file to Bubble Tea and its cancel reader.
func (g *Guard) Write(p []byte) (int, error) { return g.file.Write(p) }
func (g *Guard) Close() error                { return g.file.Close() }
func (g *Guard) Fd() uintptr                 { return g.file.Fd() }
func (g *Guard) Name() string                { return g.file.Name() }

var _ io.ReadWriteCloser = (*Guard)(nil)
