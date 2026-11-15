package terminal

import (
	"errors"
	"io"
	"os"
	"os/exec"

	tea "github.com/charmbracelet/bubbletea"
)

// ExecProcess hands the terminal to cmd for the length of its run.
//
// Bubble Tea's program output is wrapped for IME cursor placement, but
// os/exec only gives a child a TTY when Stdout is the actual *os.File.
func ExecProcess(cmd *exec.Cmd, fn tea.ExecCallback) tea.Cmd {
	if cmd.Stdout == nil {
		cmd.Stdout = os.Stdout
	}
	return Exec(processCommand{cmd}, fn)
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
