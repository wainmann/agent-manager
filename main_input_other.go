//go:build !unix

package main

import tea "github.com/charmbracelet/bubbletea"

// inputOption keeps Bubble Tea's native reader on platforms whose console
// input has no wait primitive this package can share.
func inputOption() tea.ProgramOption { return func(*tea.Program) {} }
