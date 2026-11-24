package terminal

import (
	"errors"
	"io"
	"os"
	"os/exec"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/term"
)

// ExecProcess hands the terminal to cmd for the length of its run.
//
// Bubble Tea's program input and output are wrapped (input by the guard that
// keeps escape sequences whole, output for IME cursor placement), but os/exec
// only gives a child a TTY when both ends are the actual *os.File.
// isTerminal is a seam so the Exec wiring can be tested without a real PTY.
var isTerminal = term.IsTerminal

func ExecProcess(cmd *exec.Cmd, fn tea.ExecCallback) tea.Cmd {
	return execProcess(cmd, fn, os.Stdin, isTerminal(os.Stdin.Fd()))
}

func execProcess(cmd *exec.Cmd, fn tea.ExecCallback, stdin *os.File, terminal bool) tea.Cmd {
	pinStdin(cmd, stdin, terminal)
	if cmd.Stdout == nil {
		cmd.Stdout = os.Stdout
	}
	return Exec(processCommand{cmd}, fn)
}

// ResetInput drops input the program read but has not parsed, so a child that
// takes over the terminal starts from a message boundary. The manager sets it
// to the active guard; it is a hook so this package stays free of the guard.
var ResetInput = func() {}

// pinStdin hands the child the terminal only when that is where the manager
// reads input; a redirected stdin stays unset, so Bubble Tea's /dev/tty wins.
func pinStdin(cmd *exec.Cmd, stdin *os.File, terminal bool) {
	if cmd.Stdin == nil && terminal {
		cmd.Stdin = stdin
	}
}

// Exec hands the terminal to c with scheme reports off: Bubble Tea releases
// only the modes it set, so a flip would otherwise reach the child as typed
// input. The callback runs once the terminal is restored and asks for the
// scheme then, which catches up on a flip made while the child ran.
func Exec(c tea.ExecCommand, fn tea.ExecCallback) tea.Cmd {
	return tea.Exec(schemeReportsPaused{c}, func(err error) tea.Msg {
		err = errors.Join(err, RequestSchemeReport())
		if fn == nil {
			return nil
		}
		return fn(err)
	})
}

type schemeReportsPaused struct{ tea.ExecCommand }

func (c schemeReportsPaused) Run() error {
	// Bubble Tea has stopped its reader and released the terminal by the time
	// Run is called. Drop anything the guard read before that boundary, and
	// again after the child, so a byte read during the handoff cannot reach
	// the manager.
	ResetInput()
	defer ResetInput()
	if err := DisableSchemeReports(); err != nil {
		return err
	}
	return errors.Join(c.ExecCommand.Run(), EnableSchemeReports())
}

// processCommand mirrors the adapter tea.ExecProcess builds, which Bubble
// Tea does not export: a stream the caller already set is kept.
type processCommand struct{ *exec.Cmd }

func (c processCommand) SetStdin(r io.Reader) {
	if c.Stdin == nil {
		c.Stdin = r
	}
}

func (c processCommand) SetStdout(w io.Writer) {
	if c.Stdout == nil {
		c.Stdout = w
	}
}

func (c processCommand) SetStderr(w io.Writer) {
	if c.Stderr == nil {
		c.Stderr = w
	}
}
