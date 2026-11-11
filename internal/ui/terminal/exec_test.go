package terminal

import (
	"bytes"
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

func TestExecProcessKeepsChildStdoutOnTheTerminal(t *testing.T) {
	cmd := exec.CommandContext(context.Background(), "sh", "-c", "true")
	ExecProcess(cmd, nil)
	if cmd.Stdout != os.Stdout {
		t.Fatal("interactive child stdout was not pinned to the terminal")
	}
}

func TestExecProcessPreservesConfiguredStdout(t *testing.T) {
	file, err := os.CreateTemp(t.TempDir(), "stdout")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := file.Close(); err != nil {
			t.Errorf("close temporary stdout file: %v", err)
		}
	}()

	cmd := exec.CommandContext(context.Background(), "sh", "-c", "true")
	cmd.Stdout = file
	ExecProcess(cmd, nil)
	if cmd.Stdout != file {
		t.Fatal("preconfigured child stdout was overwritten")
	}
}

// childRun is the program the terminal is handed to. It writes straight to
// the terminal, as a child holding the TTY does.
type childRun struct {
	terminal *os.File
	err      error
	returned func()
}

func (c childRun) Run() error {
	defer c.returned()
	if _, err := c.terminal.WriteString("child"); err != nil {
		return err
	}
	return c.err
}

func (childRun) SetStdin(io.Reader)  {}
func (childRun) SetStdout(io.Writer) {}
func (childRun) SetStderr(io.Writer) {}

// programOutput is Bubble Tea's side of the terminal. Restoring the terminal
// after a child enters the alternate screen for the second time in a run,
// and restored runs there.
type programOutput struct {
	terminal *os.File
	entries  atomic.Int32
	restored func()
}

func (o *programOutput) Write(p []byte) (int, error) {
	n, err := o.terminal.Write(p)
	if bytes.Contains(p, []byte(ansi.SetAltScreenSaveCursorMode)) && o.entries.Add(1) == 2 {
		o.restored()
	}
	return n, err
}

type execRecorder struct {
	child tea.ExecCommand
	done  bool
	err   error
}

type execDoneMsg struct{ err error }

func (r *execRecorder) Init() tea.Cmd {
	return Exec(r.child, func(err error) tea.Msg { return execDoneMsg{err} })
}

func (r *execRecorder) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if done, ok := msg.(execDoneMsg); ok {
		r.done, r.err = true, done.err
		return r, tea.Quit
	}
	return r, nil
}

func (r *execRecorder) View() string { return "" }

var terminalMarks = map[string]string{
	ansi.SetAltScreenSaveCursorMode:   "enter",
	ansi.ResetAltScreenSaveCursorMode: "leave",
	ansi.ResetModeLightDark:           "off",
	ansi.SetModeLightDark:             "on",
	ansi.RequestLightDarkReport:       "request",
	"child":                           "child",
}

// marks reads what reached the terminal as the order of the alternate
// screen changes, the scheme sequences and the child's own output.
func marks(stream []byte) string {
	sequences := make([]string, 0, len(terminalMarks))
	for sequence := range terminalMarks {
		sequences = append(sequences, regexp.QuoteMeta(sequence))
	}
	var read []string
	for _, sequence := range regexp.MustCompile(strings.Join(sequences, "|")).FindAll(stream, -1) {
		read = append(read, terminalMarks[string(sequence)])
	}
	return strings.Join(read, " ")
}

// failures names what err joins, in order: the child's error by its text
// and a scheme write by the stream it failed on.
func failures(err error) []string {
	if err == nil {
		return nil
	}
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		var names []string
		for _, part := range joined.Unwrap() {
			names = append(names, failures(part)...)
		}
		return names
	}
	var write *fs.PathError
	if errors.As(err, &write) {
		return []string{filepath.Base(write.Path)}
	}
	return []string{err.Error()}
}

// A child owns the terminal with reports off, so a flip never reaches it as
// typed input, and the scheme is asked for only once Bubble Tea has the
// terminal back, since turning reports on again does not make it report.
// The program and the scheme writes share one stream here, so a request
// that goes out before the alternate screen is entered again fails the
// order. Each scheme write that fails reaches the callback beside the
// child's own error.
func TestExecPausesSchemeReportsAroundChild(t *testing.T) {
	const paused = "enter leave off child on enter request leave"
	exit := errors.New("child exit")
	for _, tt := range []struct {
		name  string
		child error
		// fails names the scheme writes that land on a closed stream.
		fails string
		marks string
		err   string
	}{
		{"child succeeds", nil, "", paused, ""},
		{"child fails", exit, "", paused, "child exit"},
		{"reports stay on", exit, "off", "enter leave enter request leave", "off"},
		{"reports stay off", nil, "on", "enter leave off child enter request leave", "on"},
		{"reports stay off after a failed child", exit, "on", "enter leave off child enter request leave", "child exit on"},
		{"request fails", nil, "request", "enter leave off child on enter leave", "request"},
		{"request fails after a failed child", exit, "request", "enter leave off child on enter leave", "child exit request"},
		{"terminal is gone when the child ends", exit, "on request", "enter leave off child enter leave", "child exit on request"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			want, wantErr := tt.marks, tt.err
			if !schemeReports {
				if tt.fails != "" {
					t.Skip("no scheme sequence is written on this platform")
				}
				want = "enter leave child enter leave"
			}
			read, terminal, err := os.Pipe()
			if err != nil {
				t.Fatalf("pipe: %v", err)
			}
			stream := make(chan []byte)
			go func() {
				got, _ := io.ReadAll(read)
				stream <- got
			}()
			stdout := func(write string) *os.File {
				if !slices.Contains(strings.Fields(tt.fails), write) {
					return terminal
				}
				closed, err := os.Create(filepath.Join(t.TempDir(), write))
				if err != nil {
					t.Fatalf("create: %v", err)
				}
				if err := closed.Close(); err != nil {
					t.Fatalf("close: %v", err)
				}
				return closed
			}
			off, on, request := stdout("off"), stdout("on"), stdout("request")
			saved := os.Stdout
			defer func() { os.Stdout = saved }()
			os.Stdout = off
			recorder := &execRecorder{child: childRun{terminal: terminal, err: tt.child, returned: func() { os.Stdout = on }}}
			output := &programOutput{terminal: terminal, restored: func() { os.Stdout = request }}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_, runErr := tea.NewProgram(recorder, tea.WithInput(strings.NewReader("")), tea.WithOutput(output),
				tea.WithAltScreen(), tea.WithContext(ctx)).Run()
			os.Stdout = saved
			if err := terminal.Close(); err != nil {
				t.Fatalf("close terminal: %v", err)
			}
			got := <-stream
			if runErr != nil {
				t.Fatalf("run: %v", runErr)
			}
			if !recorder.done {
				t.Fatal("the callback never ran")
			}
			if joined := strings.Join(failures(recorder.err), " "); joined != wantErr {
				t.Fatalf("callback error joins %q, want %q", joined, wantErr)
			}
			if read := marks(got); read != want {
				t.Fatalf("terminal read %q, want %q", read, want)
			}
		})
	}
}
