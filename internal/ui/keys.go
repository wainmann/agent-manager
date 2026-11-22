package ui

import (
	"github.com/YoanWai/agent-manager/internal/keybind"
	uirail "github.com/YoanWai/agent-manager/internal/ui/rail"
	uisurface "github.com/YoanWai/agent-manager/internal/ui/surface"
	tea "github.com/charmbracelet/bubbletea"
)

type quitRoute uint8

const (
	quitPass       quitRoute = iota // the layer under the key takes it
	quitNow                         // the manager quits, or abandons its drain
	quitPopSurface                  // the surface on top closes
)

// routeQuit is the one place that decides ctrl+c. Update asks it before a
// drain swallows keys, and handleKey asks it again for a key a click
// presses, so no surface keeps its own idea of quit.
//
// This is the priority layer of model_surface.go's stack: it runs before
// any surface sees the key. Where the manager owns the keyboard ctrl+c
// quits, card or no card, as it always has. Where the agent owns it, a
// card the manager put over the pane closes on ctrl+c when it holds no
// draft, and otherwise keeps the key from the agent behind it; with no
// card, ctrl+c is the agent's. A drain already under way leaves on a
// second ctrl+c or quit key from anywhere, since by then the agent has
// nothing left to cancel.
func (m *Model) routeQuit(msg tea.KeyMsg) quitRoute {
	name := keybind.Normalize(msg.String())
	ctrlC := name == "ctrl+c"
	top, onSurface := m.top()
	switch {
	case m.effects.Quitting && (ctrlC || m.services.listKeys.Binding(keybind.Quit).Has(name)):
		return quitNow
	case !ctrlC:
		return quitPass
	case onSurface && m.baseOwner() == uisurface.Agent && top.Dismissible():
		return quitPopSurface
	case onSurface && m.baseOwner() == uisurface.Agent:
		return quitPass
	}
	return quitNow
}

func (m *Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch m.routeQuit(msg) {
	case quitNow:
		return m.requestQuit()
	case quitPopSurface:
		m.Pop()
		return m, nil
	}
	// Bubble Tea merges printable runes that arrive in one read into one
	// key, so a held j reaches here as "jjj" and matches no binding. The
	// agent's pane takes the run as typed text; the manager takes each
	// rune as its own press, routed by the state the one before it left.
	if msg.Type == tea.KeyRunes && len(msg.Runes) > 1 && !msg.Paste && !m.agentOwnsKeyboard() {
		cmds := make([]tea.Cmd, 0, len(msg.Runes))
		for _, r := range msg.Runes {
			_, cmd := m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
			cmds = append(cmds, cmd)
		}
		return m, tea.Batch(cmds...)
	}
	if cmd, ok := m.surfaceKey(msg); ok {
		return m, cmd
	}
	if m.base == baseReview {
		m.gens.foreground++
		return m.handleDiffKey(msg)
	}
	return m.handleRailKey(msg)
}

func (m *Model) railKeyContext() uirail.KeyContext {
	return uirail.KeyContext{ListKeys: m.services.listKeys, ArrowStep: m.prefs.arrowStep, StepInAttaches: m.prefs.arrowAttach, EnterFocuses: m.enterFocuses()}
}
