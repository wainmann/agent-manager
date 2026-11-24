//go:build unix

package inputguard

import (
	"bytes"
	"os"
	"strings"
	"testing"
	"time"
)

func guardFor(t *testing.T) (*Guard, *os.File) {
	t.Helper()
	read, write, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	t.Cleanup(func() { _ = read.Close(); _ = write.Close() })
	return New(read), write
}

func writeAfter(t *testing.T, w *os.File, first, second string, delay time.Duration) {
	t.Helper()
	if _, err := w.WriteString(first); err != nil {
		t.Fatalf("write first chunk: %v", err)
	}
	go func() {
		time.Sleep(delay)
		_, _ = w.WriteString(second)
	}()
}

// readEvent reads until at least one byte arrives. A zero-length read means
// the guard is holding a partial sequence, which is expected.
func readEvent(t *testing.T, g *Guard) string {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	buf := make([]byte, 256)
	for time.Now().Before(deadline) {
		type result struct {
			text string
			err  error
		}
		done := make(chan result, 1)
		go func() {
			n, err := g.Read(buf)
			done <- result{string(buf[:n]), err}
		}()
		select {
		case got := <-done:
			if got.err != nil {
				t.Fatalf("read: %v", got.err)
			}
			if got.text != "" {
				return got.text
			}
		case <-time.After(time.Second):
			t.Fatal("read did not return")
		}
	}
	t.Fatal("read did not produce a whole message")
	return ""
}

// A split after the first byte is not covered: a short read holding only ESC
// is the Escape key, as Bubble Tea reads it.
func TestSplitMouseReportAtEveryByte(t *testing.T) {
	const report = "\x1b[<35;163;38M"
	for split := 2; split < len(report); split++ {
		t.Run(strings.ReplaceAll(report[:split], "\x1b", "ESC"), func(t *testing.T) {
			g, w := guardFor(t)
			writeAfter(t, w, report[:split], report[split:], 2*time.Millisecond)
			if got := readEvent(t, g); got != report {
				t.Fatalf("read %q, want %q", got, report)
			}
		})
	}
}

func TestModeReportKeepsItsBytes(t *testing.T) {
	for _, input := range []string{"\x1b[?2026;1$y", "\x1b[?2004;1$y", "\x1b[1 q", "a\x1b[?2026;1$y", "a\x1b[1 q"} {
		g, w := guardFor(t)
		if _, err := w.WriteString(input); err != nil {
			t.Fatalf("write: %v", err)
		}
		if got := readEvent(t, g); got != input {
			t.Fatalf("read %q, want %q", got, input)
		}
	}
}

func TestSS3AloneIsDeliveredAtOnce(t *testing.T) {
	g, w := guardFor(t)
	if _, err := w.WriteString("\x1bO"); err != nil {
		t.Fatalf("write: %v", err)
	}
	start := time.Now()
	if got := readEvent(t, g); got != "\x1bO" {
		t.Fatalf("read %q, want Alt+O", got)
	}
	if waited := time.Since(start); waited > 80*time.Millisecond {
		t.Fatalf("Alt+O waited %v", waited)
	}
}

func TestSS3WithFinalStaysWhole(t *testing.T) {
	g, w := guardFor(t)
	if _, err := w.WriteString("\x1bOP"); err != nil {
		t.Fatalf("write: %v", err)
	}
	if got := readEvent(t, g); got != "\x1bOP" {
		t.Fatalf("read %q, want the whole SS3 key", got)
	}
}

func TestAltOThenCJK(t *testing.T) {
	g, w := guardFor(t)
	// A multibyte rune right after Alt+O must not lose its first byte to the
	// SS3 final. Split inside the rune so the old i+3 rule would deliver
	// ESC O and the first rune byte together.
	if _, err := w.Write([]byte("\x1bO\xe4")); err != nil {
		t.Fatalf("write: %v", err)
	}
	if got := readEvent(t, g); got != "\x1bO" {
		t.Fatalf("read %q, want Alt+O without the rune head", got)
	}
	if _, err := w.Write([]byte("\xb8\xad")); err != nil {
		t.Fatalf("write rest: %v", err)
	}
	if got := readEvent(t, g); got != "中" {
		t.Fatalf("read %q, want the whole character", got)
	}
}

func TestStandaloneEscapeIsDeliveredAtOnce(t *testing.T) {
	g, w := guardFor(t)
	if _, err := w.Write([]byte{esc}); err != nil {
		t.Fatalf("write Escape: %v", err)
	}
	start := time.Now()
	if got := readEvent(t, g); got != string([]byte{esc}) {
		t.Fatalf("read %q, want Escape", got)
	}
	if waited := time.Since(start); waited > 80*time.Millisecond {
		t.Fatalf("Escape waited %v", waited)
	}
}

func TestOrdinaryInputDoesNotWait(t *testing.T) {
	g, w := guardFor(t)
	if _, err := w.WriteString("hello\r"); err != nil {
		t.Fatalf("write: %v", err)
	}
	start := time.Now()
	if got := readEvent(t, g); got != "hello\r" {
		t.Fatalf("read %q", got)
	}
	if waited := time.Since(start); waited > 80*time.Millisecond {
		t.Fatalf("ordinary input waited %v", waited)
	}
}

func TestLoneEscapeAfterAWholeMessageIsNotStranded(t *testing.T) {
	g, w := guardFor(t)
	if _, err := w.WriteString("a\x1b"); err != nil {
		t.Fatalf("write: %v", err)
	}
	start := time.Now()
	if got := readEvent(t, g); got != "a\x1b" {
		t.Fatalf("read %q, want the key and the Escape", got)
	}
	if waited := time.Since(start); waited > 80*time.Millisecond {
		t.Fatalf("Escape waited %v", waited)
	}
}

func TestChunksNeverEndInsideAMessage(t *testing.T) {
	const report = "\x1b[<35;40;12M"
	want := strings.Repeat(report, 40)
	g, w := guardFor(t)
	if _, err := w.WriteString(want + "\x1b[<35;4"); err != nil {
		t.Fatalf("write burst: %v", err)
	}
	var got strings.Builder
	for got.Len() < len(want) {
		buf := make([]byte, 256)
		n, err := g.Read(buf)
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		if n > 0 && completeLen(buf[:n]) != n {
			t.Fatalf("chunk %q ends inside a message", buf[:n])
		}
		got.Write(buf[:n])
	}
	if got.String() != want {
		t.Fatalf("read %d bytes, want the 40 whole reports", got.Len())
	}
}

func TestBrokenSGRKeepsCtrlC(t *testing.T) {
	g, w := guardFor(t)
	if _, err := w.WriteString("\x1b[<35;"); err != nil {
		t.Fatalf("write mouse prefix: %v", err)
	}
	if _, err := w.Write([]byte{0x03}); err != nil {
		t.Fatalf("write Ctrl+C: %v", err)
	}
	var got strings.Builder
	for !strings.Contains(got.String(), "\x03") {
		got.WriteString(readEvent(t, g))
	}
	if !strings.HasPrefix(got.String(), "\x1b[<35;") {
		t.Fatalf("read %q, want the broken head before Ctrl+C", got.String())
	}
}

func TestSplitRuneWaitsForItsRemainingBytes(t *testing.T) {
	g, w := guardFor(t)
	writeAfter(t, w, "中"[:1], "中"[1:], 2*time.Millisecond)
	if got := readEvent(t, g); got != "中" {
		t.Fatalf("read %q, want the whole character", got)
	}
}

func TestSplitEscRuneWaitsForItsRemainingBytes(t *testing.T) {
	g, w := guardFor(t)
	writeAfter(t, w, "\x1b"+"中"[:1], "中"[1:], 2*time.Millisecond)
	if got := readEvent(t, g); got != "\x1b中" {
		t.Fatalf("read %q, want ESC and the whole character", got)
	}
}

func TestFullBufferKeepsTheCSITail(t *testing.T) {
	g, w := guardFor(t)
	prefix := strings.Repeat("a", 254) + "\x1b["
	if _, err := w.WriteString(prefix); err != nil {
		t.Fatalf("write: %v", err)
	}
	if got := readEvent(t, g); len(got) != 254 {
		t.Fatalf("read %d bytes, want the text before the tail", len(got))
	}
	if _, err := w.WriteString("201~"); err != nil {
		t.Fatalf("write tail: %v", err)
	}
	if got := readEvent(t, g); got != "\x1b[201~" {
		t.Fatalf("read %q, want the whole sequence", got)
	}
}

func TestPastePassesThrough(t *testing.T) {
	const paste = "\x1b[200~a\x1b[\nb\x1b[201~"
	g, w := guardFor(t)
	if _, err := w.WriteString(paste); err != nil {
		t.Fatalf("write: %v", err)
	}
	var got strings.Builder
	for got.Len() < len(paste) {
		got.WriteString(readEvent(t, g))
	}
	if got.String() != paste {
		t.Fatalf("read %q, want the paste unchanged", got.String())
	}
}

func TestResetDropsHeldBytes(t *testing.T) {
	g, w := guardFor(t)
	if _, err := w.WriteString("\x1b[<35;"); err != nil {
		t.Fatalf("write: %v", err)
	}
	buf := make([]byte, 256)
	if n, err := g.Read(buf); err != nil || n != 0 {
		t.Fatalf("first read = %d, %v; want no output", n, err)
	}
	ResetActive()
	if _, err := w.WriteString("y"); err != nil {
		t.Fatalf("write: %v", err)
	}
	if got := readEvent(t, g); got != "y" {
		t.Fatalf("read %q, want y after the reset", got)
	}
}

func TestOverlongCSIDoesNotWedgeInput(t *testing.T) {
	g, w := guardFor(t)
	head := append([]byte("\x1b["), bytes.Repeat([]byte("0"), 254)...)
	if _, err := w.Write(head); err != nil {
		t.Fatalf("write: %v", err)
	}
	buf := make([]byte, 256)
	if n, err := g.Read(buf); err != nil || n != 0 {
		t.Fatalf("read = %d, %v; want no output", n, err)
	}
	if _, err := w.WriteString("y"); err != nil {
		t.Fatalf("write: %v", err)
	}
	if got := readEvent(t, g); got != "y" {
		t.Fatalf("read %q, want the key after the overlong tail", got)
	}
}

type resetOnRead struct {
	file  *os.File
	first int
	reads int
	reset bool
}

func (r *resetOnRead) Read(p []byte) (int, error) {
	r.reads++
	if r.reads == 1 && len(p) > r.first {
		p = p[:r.first]
	}
	if r.reads == 2 {
		if !r.reset {
			r.reset = true
			ResetActive()
		}
		return 0, nil
	}
	return r.file.Read(p)
}

func TestResetDuringReadDoesNotPanic(t *testing.T) {
	g, w := guardFor(t)
	if _, err := w.WriteString("\x1b[<"); err != nil {
		t.Fatalf("write: %v", err)
	}
	rr := &resetOnRead{file: g.file, first: 3}
	g.reader = rr
	buf := make([]byte, 256)
	if n, err := g.Read(buf); err != nil || n != 0 {
		t.Fatalf("read = %d, %v; want no output", n, err)
	}
	if n, err := g.Read(buf); err != nil || n != 0 {
		t.Fatalf("read = %d, %v; want no output after the reset", n, err)
	}
	if !rr.reset {
		t.Fatal("reset hook did not run")
	}
	if len(g.pending) != 0 {
		t.Fatalf("pending = %q, want empty after reset", g.pending)
	}
	if _, err := w.WriteString("y"); err != nil {
		t.Fatalf("write after reset: %v", err)
	}
	if got := readEvent(t, g); got != "y" {
		t.Fatalf("read after reset = %q, want y", got)
	}
}
