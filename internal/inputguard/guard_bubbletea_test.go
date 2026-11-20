//go:build unix

package inputguard

import (
	"io"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

type msgRecorder struct {
	msgs    chan tea.Msg
	started chan struct{}
}

func (m *msgRecorder) Init() tea.Cmd {
	return func() tea.Msg {
		if m.started != nil {
			close(m.started)
		}
		return nil
	}
}

func (m *msgRecorder) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	select {
	case m.msgs <- msg:
	default:
	}
	return m, nil
}

func (m *msgRecorder) View() string { return "" }

type slowRecorder struct {
	msgs  chan tea.Msg
	delay time.Duration
	once  sync.Once
}

func (m *slowRecorder) Init() tea.Cmd { return nil }

func (m *slowRecorder) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	m.once.Do(func() { time.Sleep(m.delay) })
	m.msgs <- msg
	return m, nil
}

func (m *slowRecorder) View() string { return "" }

// pipeInput returns the two ends of a pipe. Only the write end is closed in
// cleanup: Bubble Tea's kill path abandons its read loop, and closing the
// read end underneath it races with the cancel reader's own Fd call.
func pipeInput(t *testing.T) (*os.File, *os.File) {
	t.Helper()
	read, write, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	t.Cleanup(func() { _ = write.Close() })
	return read, write
}

// TestBubbleTeaNeverTurnsAMouseBurstIntoText is the regression for the leak
// seen in focus: a burst longer than Bubble Tea's read buffer leaves the rest
// of a report in the guard, and the next read can return nothing. Bubble Tea
// must not flush that remainder as alt+[ and plain text.
func TestBubbleTeaNeverTurnsAMouseBurstIntoText(t *testing.T) {
	read, write := pipeInput(t)
	recorder := &msgRecorder{msgs: make(chan tea.Msg, 256)}
	program := tea.NewProgram(recorder,
		tea.WithInput(New(read)),
		tea.WithoutRenderer(),
		tea.WithMouseCellMotion(),
	)
	t.Cleanup(program.Kill)
	go func() { _, _ = program.Run() }()

	burst := strings.Repeat("\x1b[<35;40;12M", 24)
	if _, err := write.WriteString(burst); err != nil {
		t.Fatalf("write burst: %v", err)
	}
	if _, err := write.WriteString("\x1b[<35;40"); err != nil {
		t.Fatalf("write partial report: %v", err)
	}
	time.Sleep(20 * time.Millisecond)
	if _, err := write.WriteString(";13M"); err != nil {
		t.Fatalf("write report tail: %v", err)
	}

	deadline := time.After(2 * time.Second)
	mice := 0
	for mice < 25 {
		select {
		case msg := <-recorder.msgs:
			switch msg := msg.(type) {
			case tea.MouseMsg:
				mice++
			case tea.KeyMsg:
				t.Fatalf("Bubble Tea parsed input as %+v, want only mouse events", msg)
			}
		case <-deadline:
			t.Fatalf("saw %d mouse events, want 25", mice)
		}
	}
}

// TestBubbleTeaDeliversABurstLongerThanItsBuffer sends one burst with no
// follow-up write. The guard must not drain the terminal into its own buffer,
// or the rest of the burst waits for a keystroke the test never sends.
func TestBubbleTeaDeliversABurstLongerThanItsBuffer(t *testing.T) {
	read, write := pipeInput(t)
	const perEvent = 64
	recorder := &msgRecorder{msgs: make(chan tea.Msg, perEvent+8)}
	program := tea.NewProgram(recorder,
		tea.WithInput(New(read)),
		tea.WithoutRenderer(),
		tea.WithMouseCellMotion(),
	)
	t.Cleanup(program.Kill)
	go func() { _, _ = program.Run() }()

	if _, err := write.WriteString(strings.Repeat("\x1b[<35;40;12M", perEvent)); err != nil {
		t.Fatalf("write burst: %v", err)
	}

	deadline := time.After(3 * time.Second)
	mice := 0
	for mice < perEvent {
		select {
		case msg := <-recorder.msgs:
			switch msg := msg.(type) {
			case tea.MouseMsg:
				mice++
			case tea.KeyMsg:
				t.Fatalf("Bubble Tea parsed input as %+v, want only mouse events", msg)
			}
		case <-deadline:
			t.Fatalf("saw %d of %d mouse events; the burst stalled in the guard", mice, perEvent)
		}
	}
}

// TestBubbleTeaKeepsASplitCharacterWhole pins that a character split across
// reads reaches the program as one rune, not a replacement and a fragment.
func TestBubbleTeaKeepsASplitCharacterWhole(t *testing.T) {
	read, write := pipeInput(t)
	recorder := &msgRecorder{msgs: make(chan tea.Msg, 8)}
	program := tea.NewProgram(recorder,
		tea.WithInput(New(read)),
		tea.WithoutRenderer(),
	)
	t.Cleanup(program.Kill)
	go func() { _, _ = program.Run() }()

	if _, err := write.WriteString("a" + "中"[:1]); err != nil {
		t.Fatalf("write: %v", err)
	}
	time.Sleep(5 * time.Millisecond)
	if _, err := write.WriteString("中"[1:]); err != nil {
		t.Fatalf("write: %v", err)
	}

	deadline := time.After(2 * time.Second)
	text := ""
	for text != "a中" {
		select {
		case msg := <-recorder.msgs:
			key, ok := msg.(tea.KeyMsg)
			if !ok {
				continue
			}
			text += string(key.Runes)
		case <-deadline:
			t.Fatalf("typed %q so far, want the whole character", text)
		}
	}
}

// TestBubbleTeaKeepsASplitEscCharacterWhole covers an Alt-modified character
// split inside its UTF-8 bytes.
func TestBubbleTeaKeepsASplitEscCharacterWhole(t *testing.T) {
	read, write := pipeInput(t)
	recorder := &msgRecorder{msgs: make(chan tea.Msg, 8)}
	program := tea.NewProgram(recorder,
		tea.WithInput(New(read)),
		tea.WithoutRenderer(),
	)
	t.Cleanup(program.Kill)
	go func() { _, _ = program.Run() }()

	if _, err := write.WriteString("\x1b" + "中"[:1]); err != nil {
		t.Fatalf("write: %v", err)
	}
	time.Sleep(5 * time.Millisecond)
	if _, err := write.WriteString("中"[1:]); err != nil {
		t.Fatalf("write rest: %v", err)
	}

	deadline := time.After(2 * time.Second)
	for {
		select {
		case msg := <-recorder.msgs:
			key, ok := msg.(tea.KeyMsg)
			if ok && key.Alt && string(key.Runes) == "中" {
				return
			}
			if ok {
				t.Fatalf("parsed %+v, want Alt+中", key)
			}
		case <-deadline:
			t.Fatal("Alt+中 did not arrive")
		}
	}
}

// TestBubbleTeaKeepsASplitEscCharacterAfterText covers the same split after a
// complete text message.
func TestBubbleTeaKeepsASplitEscCharacterAfterText(t *testing.T) {
	read, write := pipeInput(t)
	recorder := &msgRecorder{msgs: make(chan tea.Msg, 8)}
	program := tea.NewProgram(recorder,
		tea.WithInput(New(read)),
		tea.WithoutRenderer(),
	)
	t.Cleanup(program.Kill)
	go func() { _, _ = program.Run() }()

	if _, err := write.WriteString("a\x1b" + "中"[:1]); err != nil {
		t.Fatalf("write: %v", err)
	}
	time.Sleep(5 * time.Millisecond)
	if _, err := write.WriteString("中"[1:]); err != nil {
		t.Fatalf("write rest: %v", err)
	}

	deadline := time.After(2 * time.Second)
	sawText, sawAlt := false, false
	for !sawText || !sawAlt {
		select {
		case msg := <-recorder.msgs:
			key, ok := msg.(tea.KeyMsg)
			if !ok {
				continue
			}
			if !key.Alt && string(key.Runes) == "a" {
				sawText = true
				continue
			}
			if key.Alt && string(key.Runes) == "中" {
				sawAlt = true
				continue
			}
			t.Fatalf("parsed %+v, want a and Alt+中", key)
		case <-deadline:
			t.Fatalf("text=%t alt=%t; want both", sawText, sawAlt)
		}
	}
}

// TestBubbleTeaKeepsAReportCutAtTheBufferLimit pins the 256-byte case: when
// the caller's buffer ends exactly after a report's ESC, the rest must still
// arrive as a mouse event and never as an Escape key plus text.
func TestBubbleTeaKeepsAReportCutAtTheBufferLimit(t *testing.T) {
	read, write := pipeInput(t)
	recorder := &msgRecorder{msgs: make(chan tea.Msg, 8)}
	program := tea.NewProgram(recorder,
		tea.WithInput(New(read)),
		tea.WithoutRenderer(),
		tea.WithMouseCellMotion(),
	)
	t.Cleanup(program.Kill)
	go func() { _, _ = program.Run() }()

	const report = "\x1b[<35;40;12M"
	if _, err := write.WriteString(strings.Repeat("a", 255) + report); err != nil {
		t.Fatalf("write: %v", err)
	}
	deadline := time.After(2 * time.Second)
	mice := 0
	for mice == 0 {
		select {
		case msg := <-recorder.msgs:
			switch msg := msg.(type) {
			case tea.MouseMsg:
				mice++
			case tea.KeyMsg:
				if msg.Type == tea.KeyEsc {
					t.Fatal("the cut report became an Escape key")
				}
			}
		case <-deadline:
			t.Fatal("Bubble Tea did not receive the report")
		}
	}
}

// TestSlowConsumerDoesNotTurnAReportIntoText pins that a stalled event loop
// does not make a later read treat a report already on its way as typing.
func TestSlowConsumerDoesNotTurnAReportIntoText(t *testing.T) {
	read, write := pipeInput(t)
	recorder := &slowRecorder{msgs: make(chan tea.Msg, 8), delay: 200 * time.Millisecond}
	program := tea.NewProgram(recorder,
		tea.WithInput(New(read)),
		tea.WithoutRenderer(),
		tea.WithMouseCellMotion(),
	)
	t.Cleanup(program.Kill)
	go func() { _, _ = program.Run() }()

	if _, err := write.WriteString("z"); err != nil {
		t.Fatalf("write blocker: %v", err)
	}
	time.Sleep(20 * time.Millisecond)
	if _, err := write.WriteString(strings.Repeat("a", 254) + "\x1b[<35;40;12M"); err != nil {
		t.Fatalf("write report: %v", err)
	}
	deadline := time.After(3 * time.Second)
	for {
		select {
		case msg := <-recorder.msgs:
			switch msg := msg.(type) {
			case tea.MouseMsg:
				return
			case tea.KeyMsg:
				if msg.Type == tea.KeyEsc {
					t.Fatal("the report became an Escape key")
				}
			}
		case <-deadline:
			t.Fatal("the report never arrived as a mouse event")
		}
	}
}

// TestBubbleTeaPasteStartAtTheBufferLimit pins that a paste whose start
// marker lands on the 256-byte boundary still becomes one paste.
func TestBubbleTeaPasteStartAtTheBufferLimit(t *testing.T) {
	read, write := pipeInput(t)
	recorder := &msgRecorder{msgs: make(chan tea.Msg, 8)}
	program := tea.NewProgram(recorder,
		tea.WithInput(New(read)),
		tea.WithoutRenderer(),
	)
	t.Cleanup(program.Kill)
	go func() { _, _ = program.Run() }()

	const text = "hello"
	paste := strings.Repeat("a", 250) + "\x1b[200~" + text + "\x1b[201~"
	if _, err := write.WriteString(paste); err != nil {
		t.Fatalf("write: %v", err)
	}
	deadline := time.After(2 * time.Second)
	for {
		select {
		case msg := <-recorder.msgs:
			key, ok := msg.(tea.KeyMsg)
			if ok && key.Paste {
				if string(key.Runes) != text {
					t.Fatalf("paste %q, want %q", string(key.Runes), text)
				}
				return
			}
			if ok && strings.ContainsRune(string(key.Runes), '[') {
				t.Fatalf("parsed the paste marker as text: %q", string(key.Runes))
			}
		case <-deadline:
			t.Fatal("Bubble Tea did not deliver the paste")
		}
	}
}

func TestBubbleTeaPasteThenEnter(t *testing.T) {
	read, write := pipeInput(t)
	recorder := &msgRecorder{msgs: make(chan tea.Msg, 8)}
	program := tea.NewProgram(recorder,
		tea.WithInput(New(read)),
		tea.WithoutRenderer(),
	)
	t.Cleanup(program.Kill)
	go func() { _, _ = program.Run() }()

	paste := "\x1b[200~hello\x1b[201~"
	if _, err := write.WriteString(paste + "\r"); err != nil {
		t.Fatalf("write: %v", err)
	}
	deadline := time.After(2 * time.Second)
	sawPaste, sawEnter := false, false
	for !sawPaste || !sawEnter {
		select {
		case msg := <-recorder.msgs:
			key, ok := msg.(tea.KeyMsg)
			if !ok {
				continue
			}
			if key.Paste {
				sawPaste = true
			}
			if key.Type == tea.KeyEnter {
				sawEnter = true
			}
		case <-deadline:
			t.Fatalf("paste=%t enter=%t; want both", sawPaste, sawEnter)
		}
	}
}

func TestBubbleTeaPasteThenSecondPaste(t *testing.T) {
	read, write := pipeInput(t)
	recorder := &msgRecorder{msgs: make(chan tea.Msg, 8)}
	program := tea.NewProgram(recorder,
		tea.WithInput(New(read)),
		tea.WithoutRenderer(),
	)
	t.Cleanup(program.Kill)
	go func() { _, _ = program.Run() }()

	first, second := "one", "x\x1b[<35;中"
	input := "\x1b[200~" + first + "\x1b[201~\x1b[200~" + second + "\x1b[201~"
	if _, err := write.WriteString(input); err != nil {
		t.Fatalf("write: %v", err)
	}
	deadline := time.After(2 * time.Second)
	pastes := []string{}
	for len(pastes) < 2 {
		select {
		case msg := <-recorder.msgs:
			key, ok := msg.(tea.KeyMsg)
			if ok && key.Paste {
				pastes = append(pastes, string(key.Runes))
			}
		case <-deadline:
			t.Fatalf("pastes %q, want both", pastes)
		}
	}
	if pastes[0] != first || pastes[1] != second {
		t.Fatalf("pastes %q, want %q and %q", pastes, first, second)
	}
}

func TestBubbleTeaSecondPasteSplitAcrossReads(t *testing.T) {
	read, write := pipeInput(t)
	recorder := &msgRecorder{msgs: make(chan tea.Msg, 8)}
	program := tea.NewProgram(recorder,
		tea.WithInput(New(read)),
		tea.WithoutRenderer(),
	)
	t.Cleanup(program.Kill)
	go func() { _, _ = program.Run() }()

	first, second := "one", "x\x1b[<35;中"
	if _, err := write.WriteString("\x1b[200~" + first + "\x1b[201~\x1b[200~" + second[:4]); err != nil {
		t.Fatalf("write: %v", err)
	}
	time.Sleep(20 * time.Millisecond)
	if _, err := write.WriteString(second[4:] + "\x1b[201~"); err != nil {
		t.Fatalf("write tail: %v", err)
	}
	deadline := time.After(2 * time.Second)
	pastes := []string{}
	for len(pastes) < 2 {
		select {
		case msg := <-recorder.msgs:
			key, ok := msg.(tea.KeyMsg)
			if ok && key.Paste {
				pastes = append(pastes, string(key.Runes))
			}
		case <-deadline:
			t.Fatalf("pastes %q, want both", pastes)
		}
	}
	if pastes[0] != first || pastes[1] != second {
		t.Fatalf("pastes %q, want %q and %q", pastes, first, second)
	}
}

func TestBubbleTeaPasteAfterLoneESC(t *testing.T) {
	read, write := pipeInput(t)
	recorder := &msgRecorder{msgs: make(chan tea.Msg, 8)}
	program := tea.NewProgram(recorder,
		tea.WithInput(New(read)),
		tea.WithoutRenderer(),
	)
	t.Cleanup(program.Kill)
	go func() { _, _ = program.Run() }()

	input := "\x1b[200~one\x1b[201~\x1b\x1b[200~two\x1b[201~"
	if _, err := write.WriteString(input); err != nil {
		t.Fatalf("write: %v", err)
	}
	deadline := time.After(2 * time.Second)
	pastes := []string{}
	for len(pastes) < 2 {
		select {
		case msg := <-recorder.msgs:
			key, ok := msg.(tea.KeyMsg)
			if !ok {
				continue
			}
			if !key.Paste {
				t.Fatalf("parsed %+v, want only pastes", msg)
			}
			pastes = append(pastes, string(key.Runes))
		case <-deadline:
			t.Fatalf("pastes %q, want both", pastes)
		}
	}
	if pastes[0] != "one" || pastes[1] != "two" {
		t.Fatalf("pastes %q, want one and two", pastes)
	}
}

func TestBubbleTeaPasteEndWithAPartialReport(t *testing.T) {
	read, write := pipeInput(t)
	recorder := &msgRecorder{msgs: make(chan tea.Msg, 16)}
	program := tea.NewProgram(recorder,
		tea.WithInput(New(read)),
		tea.WithoutRenderer(),
		tea.WithMouseCellMotion(),
	)
	t.Cleanup(program.Kill)
	go func() { _, _ = program.Run() }()

	if _, err := write.WriteString("\x1b[200~hello"); err != nil {
		t.Fatalf("write: %v", err)
	}
	time.Sleep(20 * time.Millisecond)
	if _, err := write.WriteString("\x1b[201~\x1b[<35;4"); err != nil {
		t.Fatalf("write tail: %v", err)
	}
	time.Sleep(20 * time.Millisecond)
	if _, err := write.WriteString("0;12M"); err != nil {
		t.Fatalf("write report tail: %v", err)
	}

	deadline := time.After(2 * time.Second)
	sawPaste, sawMouse := false, false
	for !sawPaste || !sawMouse {
		select {
		case msg := <-recorder.msgs:
			switch msg := msg.(type) {
			case tea.KeyMsg:
				if msg.Paste {
					sawPaste = true
					continue
				}
				t.Fatalf("parsed %+v, want no keys around the paste", msg)
			case tea.MouseMsg:
				sawMouse = true
			}
		case <-deadline:
			t.Fatalf("paste=%t mouse=%t; want both", sawPaste, sawMouse)
		}
	}
}

func TestBubbleTeaAltOAloneResolves(t *testing.T) {
	read, write := pipeInput(t)
	recorder := &msgRecorder{msgs: make(chan tea.Msg, 8)}
	program := tea.NewProgram(recorder,
		tea.WithInput(New(read)),
		tea.WithoutRenderer(),
	)
	t.Cleanup(program.Kill)
	go func() { _, _ = program.Run() }()

	if _, err := write.WriteString("\x1bO"); err != nil {
		t.Fatalf("write: %v", err)
	}
	deadline := time.After(2 * time.Second)
	for {
		select {
		case msg := <-recorder.msgs:
			key, ok := msg.(tea.KeyMsg)
			if ok && key.Alt && string(key.Runes) == "O" {
				return
			}
		case <-deadline:
			t.Fatal("Alt+O did not resolve without another write")
		}
	}
}

func TestBubbleTeaSS3TailAfterText(t *testing.T) {
	read, write := pipeInput(t)
	recorder := &msgRecorder{msgs: make(chan tea.Msg, 8)}
	program := tea.NewProgram(recorder,
		tea.WithInput(New(read)),
		tea.WithoutRenderer(),
	)
	t.Cleanup(program.Kill)
	go func() { _, _ = program.Run() }()

	if _, err := write.WriteString("a\x1bO"); err != nil {
		t.Fatalf("write: %v", err)
	}
	deadline := time.After(2 * time.Second)
	sawA, sawAltO := false, false
	for !sawA || !sawAltO {
		select {
		case msg := <-recorder.msgs:
			key, ok := msg.(tea.KeyMsg)
			if !ok {
				continue
			}
			if key.Alt && string(key.Runes) == "O" {
				sawAltO = true
				continue
			}
			if !key.Alt && string(key.Runes) == "a" {
				sawA = true
				continue
			}
			t.Fatalf("parsed %+v, want a and Alt+O", key)
		case <-deadline:
			t.Fatalf("a=%t alt+O=%t; want both", sawA, sawAltO)
		}
	}
}

func TestBubbleTeaSS3TailAfterTextKeepsNextKey(t *testing.T) {
	read, write := pipeInput(t)
	recorder := &msgRecorder{msgs: make(chan tea.Msg, 8)}
	program := tea.NewProgram(recorder,
		tea.WithInput(New(read)),
		tea.WithoutRenderer(),
	)
	t.Cleanup(program.Kill)
	go func() { _, _ = program.Run() }()

	if _, err := write.WriteString("a\x1bO"); err != nil {
		t.Fatalf("write: %v", err)
	}
	deadline := time.After(2 * time.Second)
	sawAltO := false
	for !sawAltO {
		select {
		case msg := <-recorder.msgs:
			key, ok := msg.(tea.KeyMsg)
			if ok && key.Alt && string(key.Runes) == "O" {
				sawAltO = true
			}
		case <-deadline:
			t.Fatal("Alt+O did not resolve before the next key")
		}
	}
	if _, err := write.WriteString("P"); err != nil {
		t.Fatalf("write next key: %v", err)
	}
	deadline = time.After(2 * time.Second)
	for {
		select {
		case msg := <-recorder.msgs:
			key, ok := msg.(tea.KeyMsg)
			if !ok {
				continue
			}
			if !key.Alt && string(key.Runes) == "P" {
				return
			}
			t.Fatalf("parsed %+v, want the next key as P", key)
		case <-deadline:
			t.Fatal("P did not arrive")
		}
	}
}

type chunkReader struct {
	chunks [][]byte
}

func (r *chunkReader) Read(p []byte) (int, error) {
	if len(r.chunks) == 0 {
		return 0, io.EOF
	}
	chunk := r.chunks[0]
	r.chunks = r.chunks[1:]
	return copy(p, chunk), nil
}

// TestBubbleTeaPlainReaderPasteThenEscapeBaseline is a control: it bypasses
// Guard and only proves Bubble Tea's own reader handles this sequence. It is
// not a regression test for the manager's input path.
func TestBubbleTeaPlainReaderPasteThenEscapeBaseline(t *testing.T) {
	recorder := &msgRecorder{msgs: make(chan tea.Msg, 8)}
	program := tea.NewProgram(recorder,
		tea.WithInput(&chunkReader{chunks: [][]byte{
			[]byte("\x1b[200~hi\x1b[201~"),
			[]byte("\x1b"),
		}}),
		tea.WithoutRenderer(),
	)
	t.Cleanup(program.Kill)
	go func() { _, _ = program.Run() }()

	deadline := time.After(2 * time.Second)
	sawPaste, sawEsc := false, false
	for !sawPaste || !sawEsc {
		select {
		case msg := <-recorder.msgs:
			key, ok := msg.(tea.KeyMsg)
			if !ok {
				continue
			}
			if key.Paste {
				sawPaste = true
				continue
			}
			if key.Type == tea.KeyEsc {
				sawEsc = true
				continue
			}
			t.Fatalf("parsed %+v, want the paste and Escape", key)
		case <-deadline:
			t.Fatalf("paste=%t escape=%t; want both", sawPaste, sawEsc)
		}
	}
}

func TestBubbleTeaPasteThenEscape(t *testing.T) {
	read, write := pipeInput(t)
	recorder := &msgRecorder{msgs: make(chan tea.Msg, 8)}
	program := tea.NewProgram(recorder,
		tea.WithInput(New(read)),
		tea.WithoutRenderer(),
	)
	t.Cleanup(program.Kill)
	go func() { _, _ = program.Run() }()

	if _, err := write.WriteString("\x1b[200~hi\x1b[201~\x1b"); err != nil {
		t.Fatalf("write: %v", err)
	}
	deadline := time.After(2 * time.Second)
	sawPaste, sawEsc := false, false
	for !sawPaste || !sawEsc {
		select {
		case msg := <-recorder.msgs:
			key, ok := msg.(tea.KeyMsg)
			if !ok {
				continue
			}
			if key.Paste {
				sawPaste = true
				continue
			}
			if key.Type == tea.KeyEsc {
				sawEsc = true
				continue
			}
			t.Fatalf("parsed %+v, want the paste and Escape", key)
		case <-deadline:
			t.Fatalf("paste=%t escape=%t; want both", sawPaste, sawEsc)
		}
	}
}

func TestBubbleTeaPasteThenSS3(t *testing.T) {
	read, write := pipeInput(t)
	recorder := &msgRecorder{msgs: make(chan tea.Msg, 8)}
	program := tea.NewProgram(recorder,
		tea.WithInput(New(read)),
		tea.WithoutRenderer(),
	)
	t.Cleanup(program.Kill)
	go func() { _, _ = program.Run() }()

	if _, err := write.WriteString("\x1b[200~hi\x1b[201~\x1bO"); err != nil {
		t.Fatalf("write: %v", err)
	}
	deadline := time.After(2 * time.Second)
	sawPaste, sawAltO := false, false
	for !sawPaste || !sawAltO {
		select {
		case msg := <-recorder.msgs:
			key, ok := msg.(tea.KeyMsg)
			if !ok {
				continue
			}
			if key.Paste {
				sawPaste = true
				continue
			}
			if key.Alt && string(key.Runes) == "O" {
				sawAltO = true
				continue
			}
			t.Fatalf("parsed %+v, want the paste and Alt+O", key)
		case <-deadline:
			t.Fatalf("paste=%t alt+O=%t; want both", sawPaste, sawAltO)
		}
	}
}

func TestBubbleTeaModeReportIsNotTyped(t *testing.T) {
	for _, input := range []string{"\x1b[?2026;1$y", "\x1b[1 q"} {
		t.Run(input, func(t *testing.T) {
			read, write := pipeInput(t)
			recorder := &msgRecorder{msgs: make(chan tea.Msg, 8)}
			program := tea.NewProgram(recorder,
				tea.WithInput(New(read)),
				tea.WithoutRenderer(),
			)
			t.Cleanup(program.Kill)
			go func() { _, _ = program.Run() }()

			if _, err := write.WriteString(input); err != nil {
				t.Fatalf("write report: %v", err)
			}
			time.Sleep(20 * time.Millisecond)
			if _, err := write.WriteString("z"); err != nil {
				t.Fatalf("write key: %v", err)
			}
			deadline := time.After(2 * time.Second)
			for {
				select {
				case msg := <-recorder.msgs:
					key, ok := msg.(tea.KeyMsg)
					if !ok {
						continue
					}
					if !key.Alt && string(key.Runes) == "z" {
						return
					}
					t.Fatalf("parsed %+v, want the report to stay out of the keys", key)
				case <-deadline:
					t.Fatal("the key after the report did not arrive")
				}
			}
		})
	}
}

// TestBubbleTeaKeepsTypedTextAfterAHalfReport pins that a broken report head
// does not turn the following character into text behind alt+'['.
func TestBubbleTeaKeepsTypedTextAfterAHalfReport(t *testing.T) {
	read, write := pipeInput(t)
	recorder := &msgRecorder{msgs: make(chan tea.Msg, 8)}
	program := tea.NewProgram(recorder,
		tea.WithInput(New(read)),
		tea.WithoutRenderer(),
	)
	t.Cleanup(program.Kill)
	go func() { _, _ = program.Run() }()

	if _, err := write.WriteString("a\x1b[<35;中"); err != nil {
		t.Fatalf("write: %v", err)
	}
	deadline := time.After(2 * time.Second)
	text := ""
	for text != "a中" {
		select {
		case msg := <-recorder.msgs:
			key, ok := msg.(tea.KeyMsg)
			if !ok {
				continue
			}
			if key.Alt || strings.ContainsRune(string(key.Runes), '[') {
				t.Fatalf("parsed %+v, want only the typed text", msg)
			}
			text += string(key.Runes)
		case <-deadline:
			t.Fatalf("typed %q so far, want a and the character", text)
		}
	}
}

// TestReleaseTerminalKeepsTheTerminalFree pins the release contract: a
// partial CSI must not block ReleaseTerminal, and keys typed while a child
// owns the terminal must not be read by the manager.
func TestReleaseTerminalKeepsTheTerminalFree(t *testing.T) {
	read, write := pipeInput(t)
	started := make(chan struct{})
	recorder := &msgRecorder{msgs: make(chan tea.Msg, 8), started: started}
	program := tea.NewProgram(recorder,
		tea.WithInput(New(read)),
		tea.WithoutRenderer(),
	)
	go func() { _, _ = program.Run() }()
	t.Cleanup(program.Kill)
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("program did not start")
	}

	if _, err := write.WriteString("\x1b[<35;"); err != nil {
		t.Fatalf("write partial CSI: %v", err)
	}
	time.Sleep(30 * time.Millisecond)

	ResetActive()
	released := time.Now()
	if err := program.ReleaseTerminal(); err != nil {
		t.Fatalf("release terminal: %v", err)
	}
	if waited := time.Since(released); waited > 200*time.Millisecond {
		t.Fatalf("ReleaseTerminal waited %v for a partial CSI", waited)
	}

	if _, err := write.WriteString("q"); err != nil {
		t.Fatalf("write during release: %v", err)
	}
	select {
	case msg := <-recorder.msgs:
		if key, ok := msg.(tea.KeyMsg); ok && string(key.Runes) == "q" {
			t.Fatal("a key typed during the release reached the program")
		}
	case <-time.After(150 * time.Millisecond):
	}

	if err := program.RestoreTerminal(); err != nil {
		t.Fatalf("restore terminal: %v", err)
	}
}

func TestBubbleTeaPasteKeepsItsLinefeeds(t *testing.T) {
	read, write := pipeInput(t)
	recorder := &msgRecorder{msgs: make(chan tea.Msg, 8)}
	program := tea.NewProgram(recorder,
		tea.WithInput(New(read)),
		tea.WithoutRenderer(),
	)
	t.Cleanup(program.Kill)
	go func() { _, _ = program.Run() }()

	const text = "paste-one\npaste-two\npaste-three\n"
	paste := "\x1b[200~" + text + "\x1b[201~"
	if _, err := write.WriteString(paste); err != nil {
		t.Fatalf("write: %v", err)
	}
	deadline := time.After(2 * time.Second)
	for {
		select {
		case msg := <-recorder.msgs:
			key, ok := msg.(tea.KeyMsg)
			if ok && key.Paste {
				if string(key.Runes) != text {
					t.Fatalf("paste %q, want %q", string(key.Runes), text)
				}
				return
			}
		case <-deadline:
			t.Fatal("Bubble Tea did not deliver the paste")
		}
	}
}
