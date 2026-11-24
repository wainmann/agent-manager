package theme

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

type backdropTick struct{}

type backdropModel struct{ updated bool }

func (m backdropModel) Init() tea.Cmd {
	return tea.Tick(2*time.Second, func(time.Time) tea.Msg { return backdropTick{} })
}

func (m backdropModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if _, ok := msg.(backdropTick); ok {
		m.updated = true
	}
	return m, nil
}

func (m backdropModel) View() string {
	if m.updated {
		return FillBackdrop("1\ufe0f\u20e3BBBBBBBB", 10, "#234567", "#ffffff")
	}
	return FillBackdrop("AAAAAAAAAA", 10, "#234567", "#ffffff")
}

func TestFillBackdropRedrawsNarrowKeycap(t *testing.T) {
	if os.Getenv("AM_BACKDROP_TEST_CHILD") == "1" {
		lipgloss.SetColorProfile(termenv.TrueColor)
		if _, err := tea.NewProgram(backdropModel{}, tea.WithAltScreen()).Run(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	if runtime.GOOS == "windows" {
		t.Skip("requires a Unix tmux server")
	}
	tmux, err := exec.LookPath("tmux")
	if err != nil {
		t.Skip("tmux is not installed")
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	// Keep the Unix socket below tmux's path limit, including on macOS.
	t.Setenv("TMPDIR", "/tmp")
	socket := filepath.Join(t.TempDir(), "tmux.sock")
	var env []string
	for _, value := range os.Environ() {
		if !strings.HasPrefix(value, "TMUX=") {
			env = append(env, value)
		}
	}
	env = append(env, "AM_BACKDROP_TEST_CHILD=1", "TERM=xterm-256color")
	run := func(args ...string) ([]byte, error) {
		cmd := exec.Command(tmux, append([]string{"-u", "-S", socket}, args...)...)
		cmd.Env = env
		return cmd.CombinedOutput()
	}
	if output, err := run("new-session", "-d", "-x", "10", "-y", "5", executable, "-test.run=^TestFillBackdropRedrawsNarrowKeycap$"); err != nil {
		t.Fatalf("start isolated tmux: %v: %s", err, output)
	}
	t.Cleanup(func() { _, _ = run("kill-server") })
	capture := func() string {
		output, err := run("capture-pane", "-p", "-t", "0")
		if err != nil {
			t.Fatalf("capture pane: %v: %s", err, output)
		}
		return strings.SplitN(string(output), "\n", 2)[0]
	}
	waitFor := func(want string) string {
		deadline := time.Now().Add(12 * time.Second)
		var line string
		for time.Now().Before(deadline) {
			line = capture()
			if strings.Contains(line, want) {
				return line
			}
			time.Sleep(50 * time.Millisecond)
		}
		t.Fatalf("screen did not show %q; last row %q", want, line)
		return ""
	}
	waitFor("AAAAAAAAAA")
	line := waitFor("BBBBBBBB")
	if line != "1\ufe0f\u20e3BBBBBBBB" {
		t.Fatalf("redrawn row = %q, want the keycap and eight B characters without stale text", line)
	}
	styled, err := run("capture-pane", "-p", "-e", "-N", "-t", "0")
	if err != nil {
		t.Fatalf("capture styled pane: %v: %s", err, styled)
	}
	firstRow := strings.SplitN(string(styled), "\n", 2)[0]
	backgrounds, _ := cellColors(firstRow, 10, 1)
	for x, color := range backgrounds[0][:9] {
		if !sameColor(color, hexColor("#234567")) {
			t.Errorf("visible cell %d background = %v, want backdrop color", x, color)
		}
	}
}
