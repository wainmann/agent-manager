package ui

import (
	"slices"
	"strings"
	"testing"

	"github.com/YoanWai/agent-manager/internal/keybind"
	"github.com/YoanWai/agent-manager/internal/tmux"
	uifocus "github.com/YoanWai/agent-manager/internal/ui/focus"
	focusruntime "github.com/YoanWai/agent-manager/internal/ui/focus/runtime"
	uigeometrypanes "github.com/YoanWai/agent-manager/internal/ui/geometry/panes"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

// A modified key the armed gesture does not handle still reaches the agent:
// Shift+Tab is a manager binding only when a table names it, and Ctrl+Space
// may belong to the agent's own mode keys.
func TestExtendedKeyTheGestureDoesNotHandleReachesTheAgent(t *testing.T) {
	t.Parallel()
	m := focusedModel(t)
	m.layout.width, m.layout.height = 100, 40
	preparedView(m)
	updated, _ := m.handleKey(tea.KeyMsg{Type: tea.KeyCtrlCloseBracket})
	m = updated.(*Model)
	m.drainEffects(t)
	if !m.layout.split.ResizeMode {
		t.Fatal("test setup: the divider should be armed")
	}

	updated, _ = m.Update(uifocus.ExtendedKeyMsg{Name: "S-Tab"})
	m = updated.(*Model)
	if m.layout.split.ResizeMode {
		t.Fatal("a foreign modified key should end the gesture")
	}
	if !m.focused() {
		t.Fatalf("the key left focus, screen = %v", m.screenID())
	}
	request, ok := focusruntime.InputRequest{}, false
	if job := m.effects.Input.Active; job != nil {
		request, ok = job.Request.(focusruntime.InputRequest)
	}
	want, wantArgs := tmux.GuardedExtendedKey(tmux.PaneTarget(railSelectedSession(m).ID), "S-Tab")
	if !ok || request.Value.Kind != focusruntime.Keys || request.Value.Command != want || !slices.Equal(request.Value.Args, wantArgs) {
		t.Fatalf("Shift+Tab = %q %q, want %q %q", request.Value.Command, request.Value.Args, want, wantArgs)
	}
}

// A resize key the terminal sends as an extended sequence still arms the
// gesture: ctrl+@ arrives as Ctrl+Space, and the session binding decides.
func TestExtendedResizeKeyArmsTheDivider(t *testing.T) {
	t.Parallel()
	m := focusedModel(t)
	m.services.keys = m.services.keys.With(keybind.Resize, bindingOf(t, "ctrl+@"))
	m.services.tmux.SetSessionKeys(m.services.keys)

	updated, _ := m.Update(uifocus.ExtendedKeyMsg{Name: "C-Space"})
	m = updated.(*Model)
	queued := focusInputQueued(m)
	m.drainEffects(t)
	if !m.layout.split.ResizeMode || !m.focused() {
		t.Fatalf("Ctrl+Space on a resize bound to ctrl+@: resize = %v, focused = %v", m.layout.split.ResizeMode, m.focused())
	}
	if queued {
		t.Fatal("the extended resize key reached the agent")
	}
}

// A resize key the terminal sends as an extended sequence commits the
// gesture it armed, rather than being dropped under the surface.
func TestExtendedResizeKeyCommitsAnArmedGesture(t *testing.T) {
	t.Parallel()
	m := focusedModel(t)
	m.layout.width, m.layout.height = 100, 40
	m.services.keys = m.services.keys.With(keybind.Resize, bindingOf(t, "ctrl+@"))
	m.services.tmux.SetSessionKeys(m.services.keys)

	updated, _ := m.Update(uifocus.ExtendedKeyMsg{Name: "C-Space"})
	m = updated.(*Model)
	m.drainEffects(t)
	if !m.layout.split.ResizeMode {
		t.Fatal("Ctrl+Space should arm a resize bound to ctrl+@")
	}
	before := m.layout.split.Ratio
	updated, _ = m.handleKey(tea.KeyMsg{Type: tea.KeyRight})
	m = updated.(*Model)
	m.drainEffects(t)

	updated, _ = m.Update(uifocus.ExtendedKeyMsg{Name: "C-Space"})
	m = updated.(*Model)
	m.drainEffects(t)
	if m.layout.split.ResizeMode {
		t.Fatal("the second Ctrl+Space should commit and leave the mode")
	}
	if !m.focused() {
		t.Fatalf("committing left focus, screen = %v", m.screenID())
	}
	if got, want := uigeometrypanes.Load(m.services.store), m.layout.split.Ratio; got != want || got == before {
		t.Fatalf("stored ratio = %v, want the nudged %v", got, want)
	}
}

// esc puts back the ratio the focused gesture started from.
func TestFocusedResizeCancelRestoresTheRatio(t *testing.T) {
	t.Parallel()
	m := focusedModel(t)
	m.layout.width, m.layout.height, m.layout.split.Ratio = 100, 40, 0.34
	updated, _ := m.handleKey(tea.KeyMsg{Type: tea.KeyCtrlCloseBracket})
	m = updated.(*Model)
	m.handleKey(tea.KeyMsg{Type: tea.KeyRight})
	m.handleKey(tea.KeyMsg{Type: tea.KeyRight})
	updated, _ = m.handleKey(tea.KeyMsg{Type: tea.KeyEsc})
	m.drainEffects(t)
	m = updated.(*Model)
	if m.layout.split.ResizeMode || m.layout.split.Dragging {
		t.Fatal("esc should leave the mode")
	}
	if !m.focused() {
		t.Fatalf("esc left focus, screen = %v", m.screenID())
	}
	if left, _ := m.splitWidths(); left != 34 {
		t.Fatalf("esc restored left=%d, want 34", left)
	}
}

// With a session focused the resize key arms the same gesture over the
// pane: the arrows nudge, enter stores the ratio, and no key reaches the
// agent while the mode holds the keyboard.
func TestFocusedResizeKeyArmsTheDivider(t *testing.T) {
	t.Parallel()
	m := focusedModel(t)
	st := m.services.store
	m.layout.width, m.layout.height, m.layout.split.Ratio = 100, 40, 0.34
	before := m.layout.split.Ratio
	updated, _ := m.handleKey(tea.KeyMsg{Type: tea.KeyCtrlCloseBracket})
	m = updated.(*Model)
	armedQueued := focusInputQueued(m)
	m.drainEffects(t)
	if !m.layout.split.ResizeMode || m.layout.split.Dragging {
		t.Fatalf("ctrl+] should arm the divider, resize = %v, dragging = %v", m.layout.split.ResizeMode, m.layout.split.Dragging)
	}
	if !m.focused() {
		t.Fatalf("arming the divider left focus, screen = %v", m.screenID())
	}
	if armedQueued {
		t.Fatal("the resize key reached the agent")
	}

	updated, _ = m.handleKey(tea.KeyMsg{Type: tea.KeyRight})
	m = updated.(*Model)
	nudgeQueued := focusInputQueued(m)
	m.drainEffects(t)
	if left, _ := m.splitWidths(); left <= 34 {
		t.Fatalf("right arrow left the divider at %d", left)
	}
	if nudgeQueued {
		t.Fatal("a nudge reached the agent")
	}

	updated, _ = m.handleKey(tea.KeyMsg{Type: tea.KeyEnter})
	m.drainEffects(t)
	m = updated.(*Model)
	if m.layout.split.ResizeMode || m.layout.split.Dragging {
		t.Fatal("enter should commit and leave the mode")
	}
	if !m.focused() {
		t.Fatalf("committing left focus, screen = %v", m.screenID())
	}
	if got, want := uigeometrypanes.Load(st), m.layout.split.Ratio; got != want || got == before {
		t.Fatalf("stored ratio = %v, want the committed %v", got, want)
	}
}

// The armed gesture speaks the table of the screen it sits on: a focused
// session commits on its own resize key, and names that key in the legend,
// even when the list keeps the default.
func TestFocusedResizeSpeaksTheSessionTable(t *testing.T) {
	t.Parallel()
	m := focusedModel(t)
	m.layout.width, m.layout.height = 100, 40
	m.services.keys = m.services.keys.With(keybind.Resize, bindingOf(t, "ctrl+@"))
	m.services.tmux.SetSessionKeys(m.services.keys)
	preparedView(m)

	updated, _ := m.handleKey(tea.KeyMsg{Type: tea.KeyCtrlAt})
	m = updated.(*Model)
	m.drainEffects(t)
	if !m.layout.split.ResizeMode {
		t.Fatal("ctrl+@ should arm the divider from the session table")
	}
	ratio := m.layout.split.Ratio
	if footer := ansi.Strip(m.viewFooter()); !strings.Contains(footer, "ctrl+@ / release commit") {
		t.Fatalf("armed legend names the wrong key: %q", footer)
	}

	// A key the session table does not bind ends the gesture where it stands,
	// keeping a nudge that had already moved the divider.
	updated, _ = m.handleKey(tea.KeyMsg{Type: tea.KeyRight})
	m = updated.(*Model)
	m.drainEffects(t)
	nudged := m.layout.split.Ratio
	if nudged == ratio {
		t.Fatal("test setup: the arrow should have nudged the divider")
	}
	updated, _ = m.handleKey(tea.KeyMsg{Type: tea.KeyCtrlCloseBracket})
	m = updated.(*Model)
	m.drainEffects(t)
	if m.layout.split.ResizeMode {
		t.Fatal("ctrl+] is not the session's resize key, so it ends the gesture")
	}
	if m.layout.split.Ratio != nudged {
		t.Fatalf("ending the gesture discarded the nudge: ratio = %v, want %v", m.layout.split.Ratio, nudged)
	}
	if got := uigeometrypanes.Load(m.services.store); uigeometrypanes.RatioText(got) != uigeometrypanes.RatioText(nudged) {
		t.Fatalf("stored ratio = %v, want the nudged %v", got, nudged)
	}

	// A gesture that changed nothing persists nothing: the saved value stays
	// where it was and no save reaches the lane.
	stored := uigeometrypanes.Load(m.services.store)
	m.effects.Main.Pending = nil
	updated, _ = m.handleKey(tea.KeyMsg{Type: tea.KeyCtrlAt})
	m = updated.(*Model)
	updated, _ = m.handleKey(tea.KeyMsg{Type: tea.KeyCtrlCloseBracket})
	m = updated.(*Model)
	if m.layout.split.ResizeMode {
		t.Fatal("the foreign key should end the gesture")
	}
	for _, job := range m.effects.Main.Pending {
		if _, save := job.Request.(uigeometrypanes.SplitSaveRequest); save {
			t.Fatal("a gesture that moved nothing queued a save")
		}
	}
	m.drainEffects(t)
	if got := uigeometrypanes.Load(m.services.store); got != stored {
		t.Fatalf("stored ratio = %v, want the unchanged %v", got, stored)
	}

	// The session's own key commits, and the nudge it carries is stored.
	updated, _ = m.handleKey(tea.KeyMsg{Type: tea.KeyCtrlAt})
	m = updated.(*Model)
	m.drainEffects(t)
	updated, _ = m.handleKey(tea.KeyMsg{Type: tea.KeyRight})
	m = updated.(*Model)
	m.drainEffects(t)
	committed := m.layout.split.Ratio
	updated, _ = m.handleKey(tea.KeyMsg{Type: tea.KeyCtrlAt})
	m = updated.(*Model)
	m.drainEffects(t)
	if m.layout.split.ResizeMode {
		t.Fatal("the session resize key should commit and leave the mode")
	}
	if !m.focused() {
		t.Fatalf("committing left focus, screen = %v", m.screenID())
	}
	if got := uigeometrypanes.Load(m.services.store); got != committed {
		t.Fatalf("stored ratio = %v, want the committed %v", got, committed)
	}
}

// The full screen layout paints no divider, so the resize key does nothing
// there: no gesture, no stored ratio, and the keys a focused session needs
// stay theirs.
func TestFullScreenArmsNothing(t *testing.T) {
	t.Parallel()
	for _, focused := range []bool{false, true} {
		m := buildModel(t)
		createSession(t, m, "alpha", t.TempDir(), "")
		m.selectSessionRow(t, "alpha")
		if focused {
			updated, _ := m.focusSelected()
			m = updated.(*Model)
			m.drainEffects(t)
		}
		m.prefs.fullLayout = true
		m.layout.width, m.layout.height = 120, 40
		preparedView(m)
		before := uigeometrypanes.RatioText(m.layout.split.Ratio)

		updated, _ := m.handleKey(tea.KeyMsg{Type: tea.KeyCtrlCloseBracket})
		m = updated.(*Model)
		queued := focusInputQueued(m)
		m.drainEffects(t)
		if m.layout.split.ResizeMode || m.layout.split.Dragging {
			t.Fatalf("focused=%v: the key armed a gesture with no divider", focused)
		}
		if queued {
			t.Fatalf("focused=%v: the resize key reached the agent", focused)
		}
		updated, _ = m.handleKey(tea.KeyMsg{Type: tea.KeyRight})
		m = updated.(*Model)
		m.drainEffects(t)
		if got := uigeometrypanes.RatioText(uigeometrypanes.Load(m.services.store)); got != before {
			t.Fatalf("focused=%v: full screen stored %s, want %s", focused, got, before)
		}
	}
}

// The full screen list footer does not offer a key that cannot work there.
func TestFullScreenListFooterLeavesTheResizeKeyOut(t *testing.T) {
	t.Parallel()
	m := buildModel(t)
	createSession(t, m, "alpha", t.TempDir(), "")
	m.prefs.fullLayout = true
	m.layout.width, m.layout.height = 240, 40
	preparedView(m)
	if footer := ansi.Strip(m.viewFooter()); strings.Contains(footer, "resize") {
		t.Fatalf("full screen list footer names resize: %q", footer)
	}
	m.prefs.fullLayout = false
	preparedView(m)
	if footer := ansi.Strip(m.viewFooter()); !strings.Contains(footer, "resize") {
		t.Fatalf("split footer omits resize: %q", footer)
	}
}

// Ending an armed gesture with a key the agent owns keeps the nudges made
// with the keyboard, which never set the drag-only Moved flag.
func TestKeyEndingAnArmedFocusedResizeKeepsTheNudge(t *testing.T) {
	t.Parallel()
	m := focusedModel(t)
	m.layout.width, m.layout.height = 100, 40
	preparedView(m)
	updated, _ := m.handleKey(tea.KeyMsg{Type: tea.KeyCtrlCloseBracket})
	m = updated.(*Model)
	m.drainEffects(t)
	before := m.layout.split.Ratio
	updated, _ = m.handleKey(tea.KeyMsg{Type: tea.KeyRight})
	m = updated.(*Model)
	m.drainEffects(t)
	nudged := m.layout.split.Ratio
	if nudged == before {
		t.Fatal("test setup: the arrow should have nudged the divider")
	}

	updated, _ = m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	m = updated.(*Model)
	queued := wroteToFocus(m, "q")
	m.drainEffects(t)
	if m.effects.Quitting || m.layout.split.ResizeMode {
		t.Fatalf("quitting = %v, resize = %v", m.effects.Quitting, m.layout.split.ResizeMode)
	}
	if !queued {
		t.Fatal("the key that ended the gesture never reached the pane")
	}
	if got := uigeometrypanes.Load(m.services.store); got != nudged {
		t.Fatalf("stored ratio = %v, want the nudged %v (the nudge was thrown away)", got, nudged)
	}
}

// The armed gesture hands a key it does not own back to a focused agent, and
// the quit key is one of those: on the list it still quits.
func TestQuitKeyDuringAnArmedFocusedResizeReachesTheAgent(t *testing.T) {
	t.Parallel()
	m := focusedModel(t)
	m.layout.width, m.layout.height = 100, 40
	preparedView(m)
	updated, _ := m.handleKey(tea.KeyMsg{Type: tea.KeyCtrlCloseBracket})
	m = updated.(*Model)
	m.drainEffects(t)
	if !m.layout.split.ResizeMode {
		t.Fatal("test setup: the divider should be armed")
	}
	updated, _ = m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	m = updated.(*Model)
	queued := wroteToFocus(m, "q")
	m.drainEffects(t)
	if m.effects.Quitting {
		t.Fatal("the quit key quit the manager over a focused agent")
	}
	if m.layout.split.ResizeMode {
		t.Fatal("the quit key should have ended the gesture")
	}
	if !m.focused() {
		t.Fatalf("the quit key left focus, screen = %v", m.screenID())
	}
	if !queued {
		t.Fatal("the quit key never reached the pane")
	}

	updated, _ = m.handleKey(tea.KeyMsg{Type: tea.KeyCtrlQ})
	m = updated.(*Model)
	m.drainEffects(t)
	updated, _ = m.handleKey(tea.KeyMsg{Type: tea.KeyCtrlCloseBracket})
	m = updated.(*Model)
	m.drainEffects(t)
	if !m.layout.split.ResizeMode {
		t.Fatal("test setup: the list's divider should be armed")
	}
	updated, cmd := m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	m = updated.(*Model)
	m.drainEffects(t)
	if !m.effects.Quitting || cmd == nil {
		t.Fatal("the list's quit key should still quit")
	}
}

// A modified key the resize surface cannot even name still ends an armed
// gesture over a focused pane and reaches the agent: Shift+Enter is the
// agent's newline.
func TestUnbindableModifiedKeyEndsAnArmedGesture(t *testing.T) {
	t.Parallel()
	m := focusedModel(t)
	m.layout.width, m.layout.height = 100, 40
	preparedView(m)
	updated, _ := m.handleKey(tea.KeyMsg{Type: tea.KeyCtrlCloseBracket})
	m = updated.(*Model)
	m.drainEffects(t)
	if !m.layout.split.ResizeMode {
		t.Fatal("test setup: the divider should be armed")
	}

	updated, _ = m.Update(uifocus.ExtendedKeyMsg{Name: "S-Enter"})
	m = updated.(*Model)
	if m.layout.split.ResizeMode {
		t.Fatal("Shift+Enter should end the gesture")
	}
	if !m.focused() {
		t.Fatalf("the key left focus, screen = %v", m.screenID())
	}
	request, ok := focusruntime.InputRequest{}, false
	if job := m.effects.Input.Active; job != nil {
		request, ok = job.Request.(focusruntime.InputRequest)
	}
	want, wantArgs := tmux.GuardedExtendedKey(tmux.PaneTarget(railSelectedSession(m).ID), "S-Enter")
	if !ok || request.Value.Command != want || !slices.Equal(request.Value.Args, wantArgs) {
		t.Fatalf("Shift+Enter = %q %q, want %q %q", request.Value.Command, request.Value.Args, want, wantArgs)
	}
}

// A modified key the surface cannot name leaves the list's armed gesture
// alone: only a focused session hands the keyboard to the agent.
func TestUnbindableModifiedKeyLeavesTheListsResizeAlone(t *testing.T) {
	t.Parallel()
	m := buildModel(t)
	createSession(t, m, "alpha", t.TempDir(), "")
	m.selectSessionRow(t, "alpha")
	preparedView(m)
	updated, _ := m.handleKey(tea.KeyMsg{Type: tea.KeyCtrlCloseBracket})
	m = updated.(*Model)
	m.drainEffects(t)
	if !m.layout.split.ResizeMode {
		t.Fatal("test setup: the list's divider should be armed")
	}

	updated, _ = m.Update(uifocus.ExtendedKeyMsg{Name: "S-Enter"})
	m = updated.(*Model)
	m.drainEffects(t)
	if !m.layout.split.ResizeMode {
		t.Fatal("Shift+Enter ended the list's gesture; the list swallows it")
	}
}

// A held nudge key reaches the armed gesture as one merged run (#797). Each
// rune nudges, as each arrow does, and none of it is typed into the agent.
func TestHeldNudgeKeyOverAFocusedResizeNudgesEachPress(t *testing.T) {
	t.Parallel()
	nudge := func(keys ...tea.KeyMsg) *Model {
		m := focusedModel(t)
		m.layout.width, m.layout.height = 100, 40
		preparedView(m)
		for _, key := range append([]tea.KeyMsg{{Type: tea.KeyCtrlCloseBracket}}, keys...) {
			updated, _ := m.handleKey(key)
			m = updated.(*Model)
		}
		return m
	}
	right := tea.KeyMsg{Type: tea.KeyRight}
	arrows := nudge(right, right, right)
	held := nudge(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("lll")})
	if !held.layout.split.ResizeMode || wroteToFocus(held, "l") {
		t.Fatalf("resize = %v, typed into the agent = %v", held.layout.split.ResizeMode, wroteToFocus(held, "l"))
	}
	if held.layout.split.Ratio != arrows.layout.split.Ratio {
		t.Fatalf("lll left the divider at %v, three arrows at %v", held.layout.split.Ratio, arrows.layout.split.Ratio)
	}
}
