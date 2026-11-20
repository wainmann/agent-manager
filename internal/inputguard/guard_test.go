//go:build unix

package inputguard

import (
	"io"
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

// readEvent reads until at least one byte arrives, failing on a deadline so a
// reader that blocks cannot hang the package. A zero-length read means the
// guard is holding a partial sequence, which is expected.
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
				if !g.paste {
					if left := completeLen(g.pending); left > 0 {
						t.Fatalf("read left %d bytes of a complete message behind", left)
					}
				}
				return got.text
			}
		case <-time.After(time.Second):
			t.Fatal("read did not return")
		}
	}
	t.Fatal("read did not produce a whole message")
	return ""
}

// A split after the first byte is not covered: a short read holding only
// ESC is the Escape key, as Bubble Tea reads it.
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

type resetOnRead struct {
	file       *os.File
	firstBytes int
	resetAt    int
	reads      int
	reset      bool
}

func (r *resetOnRead) Read(p []byte) (int, error) {
	r.reads++
	if r.reads == 1 && len(p) > r.firstBytes {
		p = p[:r.firstBytes]
	}
	if r.reads == r.resetAt {
		var one [1]byte
		_, _ = r.file.Read(one[:])
		if !r.reset {
			r.reset = true
			ResetActive()
		}
		return 0, nil
	}
	return r.file.Read(p)
}

func TestPasteThenEscapeKeepsBoth(t *testing.T) {
	g, w := guardFor(t)
	if _, err := w.WriteString("\x1b[200~hi\x1b[201~\x1b"); err != nil {
		t.Fatalf("write: %v", err)
	}
	if got := readEvent(t, g); got != "\x1b[200~hi\x1b[201~\x1b" {
		t.Fatalf("read %q, want the paste and the Escape", got)
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

func TestSS3TailAfterTextResolves(t *testing.T) {
	g, w := guardFor(t)
	if _, err := w.WriteString("a\x1bO"); err != nil {
		t.Fatalf("write: %v", err)
	}
	if got := readEvent(t, g); got != "a\x1bO" {
		t.Fatalf("read %q, want the text and the SS3 head", got)
	}
}

func TestResetDuringPeekDoesNotPanic(t *testing.T) {
	cases := []struct {
		name  string
		input string
		first int
	}{
		{"text then escape", "a\x1b[", 2},
		{"escape only", "\x1b[", 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g, w := guardFor(t)
			if _, err := w.WriteString(tc.input); err != nil {
				t.Fatalf("write: %v", err)
			}
			rr := &resetOnRead{file: g.file, firstBytes: tc.first, resetAt: 2}
			g.reader = rr
			done := make(chan struct{})
			var n int
			var err error
			go func() {
				n, err = g.Read(make([]byte, 256))
				close(done)
			}()
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("Read blocked")
			}
			if err != nil {
				t.Fatalf("Read: %v", err)
			}
			if n != 0 {
				t.Fatalf("Read = %d, want no output", n)
			}
			if !rr.reset {
				t.Fatal("reset hook did not run")
			}
			if len(g.pending) != 0 {
				t.Fatalf("pending = %q, want empty after reset", g.pending)
			}
			if g.paste {
				t.Fatal("paste still true after reset")
			}
			if _, err := w.WriteString("y"); err != nil {
				t.Fatalf("write after reset: %v", err)
			}
			if got := readEvent(t, g); got != "y" {
				t.Fatalf("read after reset = %q, want y", got)
			}
		})
	}
}

func TestBrokenSGRKeepsALaterKey(t *testing.T) {
	g, w := guardFor(t)
	if _, err := w.WriteString("\x1b[<35;"); err != nil {
		t.Fatalf("write head: %v", err)
	}
	buf := make([]byte, 256)
	if n, err := g.Read(buf); err != nil || n != 0 {
		t.Fatalf("first read = %d, %v; want no output", n, err)
	}
	time.Sleep(50 * time.Millisecond)
	if _, err := w.WriteString("a"); err != nil {
		t.Fatalf("write key: %v", err)
	}
	if got := readEvent(t, g); got != "a" {
		t.Fatalf("read %q, want the typed key", got)
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

func TestIncompleteMouseReportDoesNotConsumeCtrlC(t *testing.T) {
	g, w := guardFor(t)
	if _, err := w.WriteString("\x1b[<35;"); err != nil {
		t.Fatalf("write mouse prefix: %v", err)
	}
	if _, err := w.Write([]byte{0x03}); err != nil {
		t.Fatalf("write Ctrl+C: %v", err)
	}
	if got := readEvent(t, g); got != string([]byte{0x03}) {
		t.Fatalf("read %q, want Ctrl+C", got)
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

func TestRetainedCSIDoesNotEatThePrefix(t *testing.T) {
	g, w := guardFor(t)
	if _, err := w.WriteString("hello\x1b[<35;"); err != nil {
		t.Fatalf("write: %v", err)
	}
	if got := readEvent(t, g); got != "hello" {
		t.Fatalf("read %q, want the text before the broken report", got)
	}
	if _, err := w.Write([]byte{0x03}); err != nil {
		t.Fatalf("write Ctrl+C: %v", err)
	}
	if got := readEvent(t, g); got != "\x03" {
		t.Fatalf("read %q, want Ctrl+C after the skipped report", got)
	}
}

func TestSkipKeepsEnter(t *testing.T) {
	g, w := guardFor(t)
	if _, err := w.WriteString("x\x1b[<35;"); err != nil {
		t.Fatalf("write: %v", err)
	}
	if got := readEvent(t, g); got != "x" {
		t.Fatalf("read %q, want the text before the broken report", got)
	}
	if _, err := w.Write([]byte{'\r'}); err != nil {
		t.Fatalf("write Enter: %v", err)
	}
	if got := readEvent(t, g); got != "\r" {
		t.Fatalf("read %q, want Enter", got)
	}
}

func TestIncompleteArrowCSIKeepsCtrlC(t *testing.T) {
	g, w := guardFor(t)
	if _, err := w.WriteString("\x1b[1;2\x03"); err != nil {
		t.Fatalf("write: %v", err)
	}
	if got := readEvent(t, g); got != "\x03" {
		t.Fatalf("read %q, want Ctrl+C", got)
	}
}

func TestEndOfInputWithAHalfReportDoesNotSpin(t *testing.T) {
	g, w := guardFor(t)
	if _, err := w.WriteString("a\x1b[<35;"); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	start := time.Now()
	if got := readEvent(t, g); got != "a" {
		t.Fatalf("read %q, want the whole byte before the broken report", got)
	}
	buf := make([]byte, 256)
	if _, err := g.Read(buf); err == nil {
		t.Fatal("read after end of input returned no error")
	}
	if waited := time.Since(start); waited > 2*time.Second {
		t.Fatalf("end of input took %v", waited)
	}
}

func TestHalfCSIFollowedByTypedTextKeepsTheText(t *testing.T) {
	for _, typed := range []string{"中", "\x7f"} {
		g, w := guardFor(t)
		if _, err := w.WriteString("a\x1b[<35;" + typed); err != nil {
			t.Fatalf("write: %v", err)
		}
		if got := readEvent(t, g); got != "a"+typed {
			t.Fatalf("read %q, want the text around the broken report", got)
		}
	}
}

func TestSkipKeepsANonASCIICharacter(t *testing.T) {
	g, w := guardFor(t)
	if _, err := w.WriteString("x\x1b[<35;"); err != nil {
		t.Fatalf("write: %v", err)
	}
	if got := readEvent(t, g); got != "x" {
		t.Fatalf("read %q, want the text before the broken report", got)
	}
	if _, err := w.WriteString("中\x03"); err != nil {
		t.Fatalf("write: %v", err)
	}
	if got := readEvent(t, g); got != "中\x03" {
		t.Fatalf("read %q, want the character and Ctrl+C", got)
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

func TestSplitEscRuneAfterCompleteMessage(t *testing.T) {
	g, w := guardFor(t)
	writeAfter(t, w, "a\x1b"+"中"[:1], "中"[1:], 2*time.Millisecond)
	var got strings.Builder
	for got.String() != "a\x1b中" {
		got.WriteString(readEvent(t, g))
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

func TestSplitSS3WaitsForItsFinalByte(t *testing.T) {
	g, w := guardFor(t)
	writeAfter(t, w, "\x1bO", "P", 2*time.Millisecond)
	if got := readEvent(t, g); got != "\x1bOP" {
		t.Fatalf("read %q, want the whole SS3 key", got)
	}
}

func TestFullBufferKeepsTheSS3Tail(t *testing.T) {
	g, w := guardFor(t)
	if _, err := w.WriteString(strings.Repeat("a", 254) + "\x1bO"); err != nil {
		t.Fatalf("write: %v", err)
	}
	if got := readEvent(t, g); len(got) != 254 {
		t.Fatalf("read %d bytes, want the text before the tail", len(got))
	}
	if _, err := w.WriteString("P"); err != nil {
		t.Fatalf("write tail: %v", err)
	}
	if got := readEvent(t, g); got != "\x1bOP" {
		t.Fatalf("read %q, want the whole SS3 key", got)
	}
}

func TestFullBufferESCWaitsForItsReport(t *testing.T) {
	g, w := guardFor(t)
	const report = "\x1b[<35;40;12M"
	if _, err := w.WriteString(strings.Repeat("a", 255) + report); err != nil {
		t.Fatalf("write: %v", err)
	}
	if got := readEvent(t, g); len(got) != 255 {
		t.Fatalf("read %d bytes, want the text before the report", len(got))
	}
	if got := readEvent(t, g); got != report {
		t.Fatalf("read %q, want the whole report", got)
	}
}

func TestBrokenSGRKeepsTheTypedKey(t *testing.T) {
	g, w := guardFor(t)
	if _, err := w.WriteString("\x1b[<35;a"); err != nil {
		t.Fatalf("write: %v", err)
	}
	if got := readEvent(t, g); got != "a" {
		t.Fatalf("read %q, want the typed key", got)
	}
}

func TestPasteKeepsLiteralSequences(t *testing.T) {
	const paste = "\x1b[200~a\x1b[\nb\x1b[201~"
	g, w := guardFor(t)
	if _, err := w.WriteString(paste); err != nil {
		t.Fatalf("write: %v", err)
	}
	var got strings.Builder
	for !strings.HasSuffix(got.String(), "\x1b[201~") {
		got.WriteString(readEvent(t, g))
	}
	if got.String() != paste {
		t.Fatalf("read %q, want the literal paste", got.String())
	}
}

func TestPasteStraddlesTheCallerBuffer(t *testing.T) {
	content := strings.Repeat("x", 251)
	paste := "\x1b[200~" + content + "\x1b[201~"
	g, w := guardFor(t)
	if _, err := w.WriteString(paste); err != nil {
		t.Fatalf("write: %v", err)
	}
	var got strings.Builder
	for !strings.HasSuffix(got.String(), "\x1b[201~") {
		got.WriteString(readEvent(t, g))
	}
	if got.String() != paste {
		t.Fatalf("read %d bytes, want the whole paste", got.Len())
	}
	if g.paste {
		t.Fatal("guard still in paste mode after the end marker")
	}
}

func TestResetDropsAPartialSequence(t *testing.T) {
	g, w := guardFor(t)
	if _, err := w.WriteString("\x1b[<35;"); err != nil {
		t.Fatalf("write: %v", err)
	}
	buf := make([]byte, 256)
	if n, err := g.Read(buf); err != nil || n != 0 {
		t.Fatalf("first read = %d, %v; want no output", n, err)
	}
	ResetActive()
	if _, err := w.WriteString("a"); err != nil {
		t.Fatalf("write: %v", err)
	}
	if got := readEvent(t, g); got != "a" {
		t.Fatalf("read %q, want the typed key", got)
	}
}

func TestPartialCSIDoesNotBlockTheReader(t *testing.T) {
	g, w := guardFor(t)
	if _, err := w.WriteString("\x1b[<35;"); err != nil {
		t.Fatalf("write: %v", err)
	}
	done := make(chan struct{})
	go func() {
		buf := make([]byte, 256)
		if n, err := g.Read(buf); err != nil || n != 0 {
			t.Errorf("read = %d, %v; want no output", n, err)
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("a partial CSI blocked the reader")
	}
}

func TestPasteStartMarkerArrivesWhole(t *testing.T) {
	for _, extra := range []string{"", "x", "\x1b["} {
		g, w := guardFor(t)
		if _, err := w.WriteString("\x1b[200~" + extra); err != nil {
			t.Fatalf("write: %v", err)
		}
		var got strings.Builder
		got.WriteString(readEvent(t, g))
		if !strings.HasPrefix(got.String(), "\x1b[200~") {
			t.Fatalf("first chunk %q, want the start marker unsplit", got.String())
		}
		if _, err := w.WriteString("hello\x1b[201~"); err != nil {
			t.Fatalf("write rest: %v", err)
		}
		for !strings.HasSuffix(got.String(), "\x1b[201~") {
			got.WriteString(readEvent(t, g))
		}
		if want := "\x1b[200~" + extra + "hello\x1b[201~"; got.String() != want {
			t.Fatalf("read %q, want %q", got.String(), want)
		}
	}
}
func TestPasteStartAtTheBufferLimit(t *testing.T) {
	g, w := guardFor(t)
	paste := strings.Repeat("a", 250) + "\x1b[200~hello\x1b[201~"
	if _, err := w.WriteString(paste); err != nil {
		t.Fatalf("write: %v", err)
	}
	var got strings.Builder
	for !strings.HasSuffix(got.String(), "\x1b[201~") {
		got.WriteString(readEvent(t, g))
	}
	if got.String() != paste {
		t.Fatalf("read %d bytes, want the whole paste", got.Len())
	}
	if g.paste {
		t.Fatal("guard still in paste mode after the end marker")
	}
}

func TestURXVTShiftHomeAndEndPassThrough(t *testing.T) {
	g, w := guardFor(t)
	if _, err := w.WriteString("\x1b[7$\x1b[8$"); err != nil {
		t.Fatalf("write: %v", err)
	}
	if got := readEvent(t, g); got != "\x1b[7$\x1b[8$" {
		t.Fatalf("read %q, want both urxvt sequences", got)
	}
}

func TestResetClearsAStoredEOF(t *testing.T) {
	g, w := guardFor(t)
	g.err = io.EOF
	ResetActive()
	if _, err := w.WriteString("a"); err != nil {
		t.Fatalf("write: %v", err)
	}
	if got := readEvent(t, g); got != "a" {
		t.Fatalf("read %q, want the key typed after the reset", got)
	}
}

func TestCompletePasteWithLeadingTextArrivesTogether(t *testing.T) {
	const paste = "\x1b[200~hello\x1b[201~"
	g, w := guardFor(t)
	if _, err := w.WriteString("a" + paste); err != nil {
		t.Fatalf("write: %v", err)
	}
	got := readEvent(t, g)
	if got != "a"+paste {
		t.Fatalf("read %q, want the text and the whole paste", got)
	}
}

func TestTextAndEnterAfterAPasteArriveTogether(t *testing.T) {
	for _, tail := range []string{"a", "\r"} {
		g, w := guardFor(t)
		paste := "\x1b[200~hello\x1b[201~"
		if _, err := w.WriteString(paste + tail); err != nil {
			t.Fatalf("write: %v", err)
		}
		if got := readEvent(t, g); got != paste+tail {
			t.Fatalf("read %q, want the paste and %q", got, tail)
		}
	}
}

func TestTwoPastesInOneRead(t *testing.T) {
	g, w := guardFor(t)
	both := "\x1b[200~one\x1b[201~\x1b[200~two\x1b[201~"
	if _, err := w.WriteString(both); err != nil {
		t.Fatalf("write: %v", err)
	}
	if got := readEvent(t, g); got != both {
		t.Fatalf("read %q, want both pastes", got)
	}
}

func TestSecondPasteKeepsItsBytes(t *testing.T) {
	const both = "\x1b[200~one\x1b[201~\x1b[200~x\x1b[<35;中\x1b[201~"
	g, w := guardFor(t)
	if _, err := w.WriteString(both); err != nil {
		t.Fatalf("write: %v", err)
	}
	got := readEvent(t, g)
	if got != both {
		t.Fatalf("read %q, want both pastes verbatim", got)
	}
}

func TestPasteAfterABrokenCSIPrefixKeepsItsBytes(t *testing.T) {
	both := "\x1b[200~one\x1b[201~\x1b[<35;\x1b[200~two\x1b[201~"
	g, w := guardFor(t)
	if _, err := w.WriteString(both); err != nil {
		t.Fatalf("write: %v", err)
	}
	got := readEvent(t, g)
	if !strings.Contains(got, "two") || strings.Contains(got, "<35;") {
		t.Fatalf("read %q, want the second paste without the broken head", got)
	}
}

func TestPasteAfterALoneESCDoesNotWaitForAKey(t *testing.T) {
	input := "\x1b[200~one\x1b[201~\x1b\x1b[200~two\x1b[201~"
	g, w := guardFor(t)
	if _, err := w.WriteString(input); err != nil {
		t.Fatalf("write: %v", err)
	}
	want := "\x1b[200~one\x1b[201~\x1b[200~two\x1b[201~"
	if got := readEvent(t, g); got != want {
		t.Fatalf("read %q, want both pastes", got)
	}
}

func TestPasteEndWithAPartialReportKeepsItWhole(t *testing.T) {
	g, w := guardFor(t)
	if _, err := w.WriteString("\x1b[200~hello"); err != nil {
		t.Fatalf("write: %v", err)
	}
	if got := readEvent(t, g); got != "\x1b[200~hello" {
		t.Fatalf("read %q, want the open paste", got)
	}
	if _, err := w.WriteString("\x1b[201~\x1b[<35;4"); err != nil {
		t.Fatalf("write tail: %v", err)
	}
	if got := readEvent(t, g); got != "\x1b[201~" {
		t.Fatalf("read %q, want the paste end only", got)
	}
	if _, err := w.WriteString("0;12M"); err != nil {
		t.Fatalf("write report tail: %v", err)
	}
	if got := readEvent(t, g); got != "\x1b[<35;40;12M" {
		t.Fatalf("read %q, want the whole report", got)
	}
}

func TestPasteWithTextBeforeItKeepsItsBytes(t *testing.T) {
	paste := "x\x1b[200~a\x1b[\nb\x1b[201~"
	g, w := guardFor(t)
	if _, err := w.WriteString(paste); err != nil {
		t.Fatalf("write: %v", err)
	}
	if got := readEvent(t, g); got != paste {
		t.Fatalf("read %q, want the text and the literal paste", got)
	}
}

func TestLongPasteWithTheEndOnTheBufferBoundary(t *testing.T) {
	g, w := guardFor(t)
	content := strings.Repeat("a", 245)
	paste := "\x1b[200~" + content + "\x1b[201~"
	if _, err := w.WriteString(paste); err != nil {
		t.Fatalf("write: %v", err)
	}
	var got strings.Builder
	for !strings.HasSuffix(got.String(), "\x1b[201~") {
		got.WriteString(readEvent(t, g))
	}
	if got.String() != paste {
		t.Fatalf("read %d bytes, want the whole paste", got.Len())
	}
	if g.paste {
		t.Fatal("guard still in paste mode after the end marker")
	}
}

func TestPasteEndSplitAtEveryByte(t *testing.T) {
	end := "\x1b[201~"
	for split := 1; split < len(end); split++ {
		g, w := guardFor(t)
		paste := "\x1b[200~hello" + end
		if _, err := w.WriteString(paste[:6+5+split]); err != nil {
			t.Fatalf("write: %v", err)
		}
		var got strings.Builder
		got.WriteString(readEvent(t, g))
		if _, err := w.WriteString(paste[6+5+split:]); err != nil {
			t.Fatalf("write rest: %v", err)
		}
		for !strings.HasSuffix(got.String(), end) {
			got.WriteString(readEvent(t, g))
		}
		if got.String() != paste {
			t.Fatalf("split %d: read %q, want %q", split, got.String(), paste)
		}
		if g.paste {
			t.Fatalf("split %d: guard still in paste mode", split)
		}
	}
}

// firstReadLimit models the real 256-byte reader receiving a short first read
// followed by already-queued bytes. It makes the OS read boundary deterministic.
type firstReadLimit struct {
	file  *os.File
	limit int
	first bool
}

func (r *firstReadLimit) Read(p []byte) (int, error) {
	if !r.first {
		r.first = true
		if len(p) > r.limit {
			p = p[:r.limit]
		}
	}
	return r.file.Read(p)
}

func TestPasteTailReadRespectsRemainingOutputRoom(t *testing.T) {
	paste := "\x1b[200~" + strings.Repeat("a", 241) + "\x1b[201~"
	for _, tc := range []struct {
		name  string
		first string
		after string
		want  string
	}{
		{name: "SS3 tail", first: paste + "\x1bO", after: "PQ", want: paste + "\x1bOP"},
		{name: "CSI tail", first: paste + "\x1b", after: "[AP", want: paste + "\x1b[A"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g, w := guardFor(t)
			g.reader = &firstReadLimit{file: g.file, limit: len(tc.first)}
			if _, err := w.WriteString(tc.first + tc.after); err != nil {
				t.Fatal(err)
			}
			buf := make([]byte, 256)
			n, err := g.Read(buf)
			if err != nil {
				t.Fatal(err)
			}
			if got := string(buf[:n]); got != tc.want {
				t.Fatalf("Read returned %q, want %q", got, tc.want)
			}
			if complete := completeLen(g.pending); complete > 0 {
				t.Fatalf("%d complete bytes stranded in guard pending: %q", complete, g.pending)
			}
		})
	}
}
