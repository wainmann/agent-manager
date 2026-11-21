package ui

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/YoanWai/agent-manager/internal/sessioncmd"
	"github.com/YoanWai/agent-manager/internal/store"
)

// focusRequestModel lays out a root session and one filed two groups deep,
// with both groups folded so the deep one has no row until it is unfolded.
func focusRequestModel(t *testing.T) (*Model, store.Session) {
	t.Helper()
	m := buildModel(t)
	if err := m.services.store.CreateGroup("backend/api", ""); err != nil {
		t.Fatal(err)
	}
	m.applyCmd(t, m.refreshCmd())
	createSession(t, m, "top", t.TempDir(), "")
	createSession(t, m, "deep", t.TempDir(), "backend/api")
	var deep store.Session
	for _, sess := range m.workspace.sessions {
		if sess.Name == "deep" {
			deep = sess
		}
	}
	if deep.ID == "" {
		t.Fatal("deep session missing")
	}
	m.rail.SetCollapsed("backend", true)
	m.rail.SetCollapsed("backend/api", true)
	m.selectSessionRow(t, "top")
	return m, deep
}

// serveFocusRequest hands the model a request the way the focus endpoint
// does, runs the open it starts, and returns the answer the endpoint would
// write back.
func (m *Model) serveFocusRequest(t *testing.T, id string, enter bool) error {
	t.Helper()
	answer := make(chan error, 1)
	updated, cmd := m.Update(focusRequestMsg{sessioncmd.FocusDelivery{
		Request: sessioncmd.FocusRequest{ID: id, Enter: enter},
		Answer:  answer,
		Expires: time.Now().Add(time.Minute),
	}})
	*m = *updated.(*Model)
	if len(answer) == 0 && cmd != nil {
		m.applyTestMsg(t, cmd())
		m.drainEffects(t)
	}
	select {
	case err := <-answer:
		return err
	default:
		t.Fatal("the request was not answered")
		return nil
	}
}

// A focus request lands on the session from wherever the user is, closing
// the layers that hold no typed input the way esc would and unfolding the
// groups that hide the row, then opens it the way enter does.
func TestFocusRequestUnfoldsAndEntersFromEveryStartingState(t *testing.T) {
	t.Parallel()
	for _, start := range []struct {
		name  string
		setup func(t *testing.T, m *Model)
	}{
		{"list", func(t *testing.T, m *Model) {}},
		{"search hiding the row", func(t *testing.T, m *Model) {
			m.rail.SetSearch("top", true)
		}},
		{"help", func(t *testing.T, m *Model) {
			m.openHelp()
		}},
		{"focused on another session", func(t *testing.T, m *Model) {
			_, cmd := m.focusSelected()
			m.applyCmd(t, cmd)
			if !m.focused() {
				t.Fatalf("setup: not focused, err = %q", m.errBar.text)
			}
		}},
	} {
		t.Run(start.name, func(t *testing.T) {
			m, deep := focusRequestModel(t)
			start.setup(t, m)
			if err := m.serveFocusRequest(t, deep.ID, true); err != nil {
				t.Fatalf("focus answered %v", err)
			}
			if !m.focused() {
				t.Fatalf("screen = %v, err = %q; want focus", m.screenID(), m.errBar.text)
			}
			if sess, ok := m.selected(); !ok || sess.ID != deep.ID {
				t.Fatalf("cursor is on %+v, want deep", sess)
			}
			if m.rail.IsCollapsed("backend") || m.rail.IsCollapsed("backend/api") {
				t.Fatalf("groups above the session are still folded: %v", m.rail.Collapsed())
			}
			if m.rail.Search() != "" || m.rail.Searching() {
				t.Fatalf("search = %q (field open %v), want it cleared", m.rail.Search(), m.rail.Searching())
			}
		})
	}
}

// Without enter the request only selects, so a focused pane is left for the
// list even when it is the session asked for: staying would keep every key
// going to that agent while the request reported a selection.
func TestSelectRequestLeavesFocusAndStaysOnTheList(t *testing.T) {
	t.Parallel()
	for _, start := range []string{"top", "deep"} {
		t.Run("focused on "+start, func(t *testing.T) {
			m, deep := focusRequestModel(t)
			if start == "deep" {
				if err := m.serveFocusRequest(t, deep.ID, true); err != nil {
					t.Fatalf("setup: focus answered %v", err)
				}
			} else {
				_, cmd := m.focusSelected()
				m.applyCmd(t, cmd)
			}
			if !m.focused() {
				t.Fatalf("setup: not focused, err = %q", m.errBar.text)
			}
			if err := m.serveFocusRequest(t, deep.ID, false); err != nil {
				t.Fatalf("select answered %v", err)
			}
			if !m.onList() {
				t.Fatalf("screen = %v, want the list", m.screenID())
			}
			if sess, ok := m.selected(); !ok || sess.ID != deep.ID {
				t.Fatalf("cursor is on %+v, want deep", sess)
			}
		})
	}
}

// A dialog may hold input the user has not finished, so a request never
// closes it: the cursor moves under it, as a clicked banner always did. A
// focus is answered with why the session was not opened over it.
func TestFocusRequestKeepsADialogHoldingInput(t *testing.T) {
	t.Parallel()
	for _, enter := range []bool{false, true} {
		t.Run(map[bool]string{false: "select", true: "focus"}[enter], func(t *testing.T) {
			m, deep := focusRequestModel(t)
			m.openForm()
			m.form().SetName("half typed")
			err := m.serveFocusRequest(t, deep.ID, enter)
			if enter != (err != nil && strings.Contains(err.Error(), "dialog")) {
				t.Fatalf("answer = %v, want the dialog named only for a focus", err)
			}
			if m.form() == nil || m.form().Name() != "half typed" {
				t.Fatalf("screen = %v; want the form kept with its text", m.screenID())
			}
			if sess, ok := m.selected(); !ok || sess.ID != deep.ID {
				t.Fatalf("cursor is on %+v, want deep under the form", sess)
			}
		})
	}
}

// A status filter that hides the session is dropped rather than leaving the
// request with nowhere to land.
func TestFocusRequestDropsAStatusFilterHidingTheRow(t *testing.T) {
	t.Parallel()
	m, deep := focusRequestModel(t)
	m.rail.SetCollapsed("backend", false)
	m.rail.SetCollapsed("backend/api", false)
	m.rail.SetFilteringAttention(true)
	for _, sess := range m.sessionRows() {
		if sess.ID == deep.ID {
			t.Fatal("setup: the attention filter still shows the idle session")
		}
	}
	if err := m.serveFocusRequest(t, deep.ID, false); err != nil {
		t.Fatalf("select answered %v", err)
	}
	if sess, ok := m.selected(); !ok || sess.ID != deep.ID {
		t.Fatalf("cursor is on %+v, want deep", sess)
	}
}

// A request the program could not take in time has already been answered
// as timed out, so it must not move the list when it finally arrives.
func TestExpiredFocusRequestIsDropped(t *testing.T) {
	t.Parallel()
	m, deep := focusRequestModel(t)
	answer := make(chan error, 1)
	updated, _ := m.Update(focusRequestMsg{sessioncmd.FocusDelivery{
		Request: sessioncmd.FocusRequest{ID: deep.ID, Enter: true},
		Answer:  answer,
		Expires: time.Now().Add(-time.Second),
	}})
	m = updated.(*Model)
	if sess, ok := m.selected(); !ok || sess.Name != "top" || !m.onList() {
		t.Fatalf("an expired request moved the list: cursor %+v, screen %v", sess, m.screenID())
	}
	if len(answer) != 0 {
		t.Fatal("an expired request was answered a second time")
	}
}

// clickNotification delivers a clicked notification the way the poller
// does, on the next listing.
func (m *Model) clickNotification(t *testing.T, id string) {
	t.Helper()
	updated, cmd := m.Update(refreshMsg{
		sessions: slices.Clone(m.workspace.sessions),
		listedAt: time.Now(),
		focusID:  id,
	})
	*m = *updated.(*Model)
	if cmd != nil {
		m.applyCmd(t, cmd)
	}
}

// A click on the session the user is already in leaves them in it, as it
// did before requests from other shells shared this path. A click on
// another session steps back to the list with that row unfolded.
func TestNotificationClickStaysInTheSessionItNames(t *testing.T) {
	t.Parallel()
	m, deep := focusRequestModel(t)
	if err := m.serveFocusRequest(t, deep.ID, true); err != nil {
		t.Fatalf("setup: focus answered %v", err)
	}
	m.clickNotification(t, deep.ID)
	if !m.focused() {
		t.Fatalf("screen = %v after a click on the focused session, want focus kept", m.screenID())
	}
	m.rail.SetCollapsed("backend", true)
	var top store.Session
	for _, sess := range m.workspace.sessions {
		if sess.Name == "top" {
			top = sess
		}
	}
	m.clickNotification(t, top.ID)
	if !m.onList() {
		t.Fatalf("screen = %v after a click on another session, want the list", m.screenID())
	}
	if sess, ok := m.selected(); !ok || sess.ID != top.ID {
		t.Fatalf("cursor is on %+v, want top", sess)
	}
}

// A click while the New Session form is open moves the cursor under the
// form and keeps what was typed into it.
func TestNotificationClickKeepsTheNewSessionForm(t *testing.T) {
	t.Parallel()
	m, deep := focusRequestModel(t)
	m.openForm()
	m.form().SetName("half typed")
	m.clickNotification(t, deep.ID)
	if m.form() == nil || m.form().Name() != "half typed" {
		t.Fatalf("screen = %v; want the form kept with its text", m.screenID())
	}
	if sess, ok := m.selected(); !ok || sess.ID != deep.ID {
		t.Fatalf("cursor is on %+v, want deep under the form", sess)
	}
}

// The archive view lists only archived sessions, so a request for a live
// one switches back to the live list.
func TestFocusRequestLeavesTheArchiveView(t *testing.T) {
	t.Parallel()
	m, deep := focusRequestModel(t)
	m.rail.SetArchived(true)
	if err := m.serveFocusRequest(t, deep.ID, false); err != nil {
		t.Fatalf("select answered %v", err)
	}
	if m.rail.ShowArchived() {
		t.Fatal("the archive view is still on")
	}
	if sess, ok := m.selected(); !ok || sess.ID != deep.ID {
		t.Fatalf("cursor is on %+v, want deep", sess)
	}
}

// A message already on the status bar, a success one included, says
// nothing about why this open did not land, so only a failure the open
// itself reported reaches the caller.
func TestFocusAnswerNamesOnlyAFailureTheOpenReported(t *testing.T) {
	t.Parallel()
	m, deep := focusRequestModel(t)
	for _, step := range []struct {
		name       string
		reportedBy func()
		want       string
	}{
		{"stale success", func() {}, "moved on before it opened"},
		{"failure during the open", func() { m.reportErr("pane is gone") }, "pane is gone"},
	} {
		answer := make(chan error, 1)
		m.focus.forward = pendingForward{sessionID: deep.ID, answer: answer}
		m.reportDone("renamed top")
		before := m.errBar
		step.reportedBy()
		m.answerForward(deep.ID, false, before)
		if err := <-answer; err == nil || !strings.Contains(err.Error(), step.want) {
			t.Fatalf("%s: answer = %v, want it to say %q", step.name, err, step.want)
		}
	}
}
