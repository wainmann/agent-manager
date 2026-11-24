//go:build unix

package inputguard

import (
	"os"
	"strings"
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

// TestBubbleTeaKeepsAReportCutAtTheBufferLimit pins the user-visible result at
// the 256-byte boundary. The guard delivers a trailing ESC as a complete
// Escape because it cannot know more is coming. Because the read filled
// Bubble Tea's 256-byte buffer, Bubble Tea sets canHaveMoreData and keeps that
// ESC as leftover; when the next read completes a report it is parsed as one.
// A next read that does not complete a report stays the split-after-ESC known
// limit. The split the guard itself holds is covered by TestFullBufferKeepsTheCSITail.
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
