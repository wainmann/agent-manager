package ui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/YoanWai/agent-manager/internal/execution"
	"github.com/YoanWai/agent-manager/internal/store"
	uifocus "github.com/YoanWai/agent-manager/internal/ui/focus"
	uitheme "github.com/YoanWai/agent-manager/internal/ui/theme"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

func benchmarkFocusModel(b *testing.B) *Model {
	b.Helper()
	prev := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	b.Cleanup(func() { lipgloss.SetColorProfile(prev) })

	m := shotModel()
	m.poller = &poller{runner: execution.New(execution.Dependencies{}, execution.Options{})}
	m.layout.width, m.layout.height = 180, 50
	var pane strings.Builder
	for i := range 44 {
		fmt.Fprintf(&pane, "\x1b[38;5;250mline %02d: \x1b[38;5;114mfunc handleRequest(ctx context.Context) error\x1b[0m — working on focus performance\n", i)
	}
	m.workspace.preview = pane.String()
	m.push(newFocusSurface(m))
	sess, ok := m.selected()
	if !ok {
		sess = store.Session{ID: "bench-1", Name: "bench", Tool: "claude"}
		m.workspace.sessions = []store.Session{sess}
		m.rebuildRows()
	}
	m.focus.pane.Enter(uifocus.EnterContext{SessionID: sess.ID})
	m.focus.pane.ApplyPane(uifocus.PaneUpdate{
		SessionID: sess.ID,
		Cursor:    uifocus.Cursor{X: 12, Y: 40, Visible: true, PositionKnown: true},
	}, sess.ID)
	m.prepareFrame()
	return m
}

func BenchmarkFocusPrepareFrame(b *testing.B) {
	m := benchmarkFocusModel(b)
	b.ResetTimer()
	for range b.N {
		m.prepareFrame()
	}
}

func BenchmarkFocusBackspaceUpdate(b *testing.B) {
	const burst = 32
	m := benchmarkFocusModel(b)
	key := tea.KeyMsg{Type: tea.KeyBackspace}
	b.ResetTimer()
	for range b.N {
		for range burst {
			m.Update(key)
		}
		b.StopTimer()
		m.effects.Input.Active = nil
		m.effects.Input.Pending = m.effects.Input.Pending[:0]
		b.StartTimer()
	}
}

func BenchmarkFocusMouseHoverUpdate(b *testing.B) {
	m := benchmarkFocusModel(b)
	b.ResetTimer()
	for i := range b.N {
		m.Update(tea.MouseMsg{X: 60 + (i % 40), Y: 10 + (i % 20), Action: tea.MouseActionMotion, Button: tea.MouseButtonNone})
	}
}

func BenchmarkFillBackdrop(b *testing.B) {
	m := benchmarkFocusModel(b)
	raw := m.renderFrame()
	bg, fg := uitheme.Current().Bg, uitheme.Current().Text
	b.ResetTimer()
	for range b.N {
		_ = uitheme.FillBackdrop(raw, m.layout.width, bg, fg)
	}
}
