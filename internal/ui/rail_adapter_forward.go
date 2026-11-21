package ui

import (
	"errors"
	"fmt"
	"time"

	"github.com/YoanWai/agent-manager/internal/sessioncmd"
	uifocus "github.com/YoanWai/agent-manager/internal/ui/focus"
	uigeometry "github.com/YoanWai/agent-manager/internal/ui/geometry"
	uihelp "github.com/YoanWai/agent-manager/internal/ui/help"
	uinotices "github.com/YoanWai/agent-manager/internal/ui/notices"
	uistatusbar "github.com/YoanWai/agent-manager/internal/ui/statusbar"
	uisurface "github.com/YoanWai/agent-manager/internal/ui/surface"
	tea "github.com/charmbracelet/bubbletea"
)

// focusRequestMsg carries one request from the focus endpoint into Update.
type focusRequestMsg struct{ sessioncmd.FocusDelivery }

// pendingForward is a focus request whose open runs on the effect lane: its
// caller hears back once the open lands or fails.
type pendingForward struct {
	sessionID string
	answer    chan<- error
}

// forwardKind is what bringing a session forward asks of the manager.
type forwardKind int

const (
	// forwardSelect moves the cursor to the session on the list.
	forwardSelect forwardKind = iota
	// forwardOpen also opens it the way the enter key does.
	forwardOpen
	// forwardClick is a clicked notification. It moves the cursor like a
	// select but leaves the user in the session when that is the one named.
	forwardClick
)

// overlayLayers bounds the esc presses bringForward spends closing what
// covers the list. Each press closes at most one layer; the bound is for a
// layer whose esc does not close it, which would otherwise loop.
const overlayLayers = 8

func (m *Model) routeForwardMsg(msg tea.Msg) (tea.Model, tea.Cmd, bool) {
	request, ok := msg.(focusRequestMsg)
	if !ok {
		return nil, nil, false
	}
	if time.Now().After(request.Expires) {
		return routed(m, nil)
	}
	if m.effects.Quitting {
		request.Answer <- errors.New("the manager is quitting")
		return routed(m, nil)
	}
	if pending := m.focus.forward; pending.answer != nil {
		pending.answer <- fmt.Errorf("session %s was selected but not opened: another request to bring a session forward took its place", pending.sessionID)
		m.focus.forward = pendingForward{}
	}
	kind := forwardSelect
	if request.Request.Enter {
		kind = forwardOpen
	}
	opening, cmd, err := m.bringForward(request.Request.ID, kind)
	if opening {
		m.focus.forward = pendingForward{sessionID: request.Request.ID, answer: request.Answer}
	} else {
		request.Answer <- err
	}
	m.syncPollInput()
	return routed(m, cmd)
}

// bringForward serves a request from another process: it unfolds the
// groups hiding the session, moves the cursor to its row and, when asked,
// opens it the way the enter key does. Layers that hold nothing the user
// typed (help, messages, the search, the row menu, a lifted row, a divider
// drag) close the way esc closes them. A dialog that may hold unfinished
// input stays open with the cursor moved under it, and the session is not
// opened over it. opening reports an open handed to the effect lane, whose
// completion answers the request; otherwise err is the answer.
func (m *Model) bringForward(id string, kind forwardKind) (opening bool, cmd tea.Cmd, err error) {
	sess, ok := m.sessionByID(id)
	if !ok || (sess.Archived && !m.rail.ShowArchived()) {
		return false, nil, fmt.Errorf("session %s has no row in Agent Manager's list yet", id)
	}
	cmds := m.closeInputFreeLayers()
	if focused, ok := m.selected(); m.focused() && (kind == forwardSelect || !ok || focused.ID != sess.ID) {
		if top, _ := m.top(); !isFocusSurface(top) {
			return false, tea.Batch(cmds...), fmt.Errorf("a dialog is open over the session Agent Manager is in, and it is left as it is; close it to move to session %s", sess.ID)
		}
		// A select is a request to look at the list, and a focus on another
		// session would leave the keyboard pinned to the one the user was in
		// while the list claims a different row.
		cmds = append(cmds, m.leaveFocus())
		cmds = append(cmds, m.closeInputFreeLayers()...)
	}
	if m.focused() {
		return false, tea.Batch(cmds...), nil
	}
	decision, found := m.rail.RevealSession(sess.ID, sess.Group)
	_, reveal := m.applyRailDecision(decision)
	cmds = append(cmds, reveal)
	if !found {
		return false, tea.Batch(cmds...), fmt.Errorf("session %s has no row in Agent Manager's list", sess.ID)
	}
	if kind != forwardOpen {
		return false, tea.Batch(cmds...), nil
	}
	if !m.listUncovered() {
		return false, tea.Batch(cmds...), fmt.Errorf("session %s is selected under a dialog open in Agent Manager, which is left as it is; close it to open the session", sess.ID)
	}
	// Enter attaches instead when Settings swaps the pair.
	open := m.focusSelected
	if !m.enterFocuses() {
		open = m.attachSelected
	}
	_, openCmd := open()
	return true, tea.Batch(append(cmds, openCmd)...), nil
}

func (m *Model) closeInputFreeLayers() []tea.Cmd {
	var cmds []tea.Cmd
	for presses := 0; presses < overlayLayers && m.inputFreeLayerOpen(); presses++ {
		_, cmd := m.handleKey(tea.KeyMsg{Type: tea.KeyEsc})
		cmds = append(cmds, cmd)
	}
	return cmds
}

// inputFreeLayerOpen reports whether a layer that holds nothing the user
// typed stands over the rows, so esc can close it without losing work.
// Focus is not one; esc there belongs to the pane.
func (m *Model) inputFreeLayerOpen() bool {
	if top, ok := m.top(); ok {
		switch top.(type) {
		case *uihelp.Surface, *uinotices.Surface, *uigeometry.Surface:
			return true
		}
		return false
	}
	return m.base == baseList && (m.rail.Busy() || m.rail.Search() != "")
}

// listUncovered reports whether the list is on screen with nothing over it,
// which is where the enter key opens the selected row.
func (m *Model) listUncovered() bool {
	return m.base == baseList && len(m.surfaces) == 0 && !m.inputFreeLayerOpen()
}

func isFocusSurface(s uisurface.Surface) bool {
	_, ok := s.(*uifocus.Surface)
	return ok
}

// answerForward settles the pending request for a session once its open
// ran. A failure the open put on the status bar after before is the reason
// the caller hears.
func (m *Model) answerForward(sessionID string, opened bool, before errBar) {
	pending := m.focus.forward
	if pending.answer == nil || pending.sessionID != sessionID {
		return
	}
	m.focus.forward = pendingForward{}
	if opened {
		pending.answer <- nil
		return
	}
	reason := "Agent Manager moved on before it opened"
	if m.errBar != before && m.errBar.kind() == uistatusbar.Failure {
		reason = m.errBar.text
	}
	pending.answer <- fmt.Errorf("session %s was selected but not opened: %s", sessionID, reason)
}
