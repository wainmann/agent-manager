//go:build !unix

package main

import tea "github.com/charmbracelet/bubbletea"

// inputOption keeps Bubble Tea's native reader on platforms whose console
// input has no terminal file this package can wrap.
func inputOption() tea.ProgramOption { return func(*tea.Program) {} }
