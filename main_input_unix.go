//go:build unix

package main

import (
	"os"

	"github.com/YoanWai/agent-manager/internal/inputguard"
	"github.com/YoanWai/agent-manager/internal/ui/terminal"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/term"
)

func init() { terminal.ResetInput = inputguard.ResetActive }

// inputOption hands the TUI whole escape sequences when stdin is a terminal,
// so a short read cannot split a mouse report into keys and text. Input that
// is not a terminal keeps Bubble Tea's own reader.
func inputOption() tea.ProgramOption {
	if !term.IsTerminal(os.Stdin.Fd()) {
		return func(*tea.Program) {}
	}
	return tea.WithInput(inputguard.New(os.Stdin))
}
