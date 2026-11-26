package ui

import (
	"fmt"
	"github.com/YoanWai/agent-manager/internal/keybind"
	"github.com/YoanWai/agent-manager/internal/store"
	uiconnections "github.com/YoanWai/agent-manager/internal/ui/connections"
	uiframe "github.com/YoanWai/agent-manager/internal/ui/frame"
	uihelp "github.com/YoanWai/agent-manager/internal/ui/help"
	uinotices "github.com/YoanWai/agent-manager/internal/ui/notices"
	"github.com/YoanWai/agent-manager/internal/ui/presentation"
	uirail "github.com/YoanWai/agent-manager/internal/ui/rail"
	uisettings "github.com/YoanWai/agent-manager/internal/ui/settings"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"strings"
	"testing"
)

func narrowFooterModel(t *testing.T) *Model {
	t.Helper()
	m := buildModel(t)
	if err := m.services.store.CreateGroup("narrow-group", ""); err != nil {
		t.Fatal(err)
	}
	m.applyCmd(t, m.refreshCmd())
	createSession(t, m, "narrow", t.TempDir(), "")
	return m
}
func TestListFooterLayoutAcrossViewStates(t *testing.T) {
	m := narrowFooterModel(t)
	m.selectSessionRow(t, "narrow")
	for _, glyph := range []presentation.GlyphMode{presentation.GlyphModeAdvanced, presentation.GlyphModeBasic} {
		m.prefs.glyphMode = glyph
		for _, width := range []int{10, 12, 15, 20, 28, 30, 35, 40, 60, 80, 100} {
			m.layout.width, m.layout.height = width, 40
			m.rail.SetArchived(false)
			m.rail.SetCollapsed("narrow-group", false)
			m.rail.SetFilteringAttention(false)
			m.rail.SetHideEmptyGroups(false)
			m.rebuildRows()
			m.selectSessionRow(t, "narrow")
			wantMode, wantRows := m.footerOf().Layout()
			states := []func(){func() { m.selectGroupRow(t, "narrow-group") }, func() { m.release.FeedPending = true }, func() { m.rail.SetCollapsed("narrow-group", true) }, func() { m.rail.SetArchived(true) }, func() { m.rail.SetArchived(false) }, func() { m.rail.SetFilteringAttention(true) }, func() { m.rail.SetHideEmptyGroups(true) }, func() { m.release.FeedPending = false }, func() { resetRailModel(m) }}
			for i, change := range states {
				change()
				gotMode, gotRows := m.footerOf().Layout()
				if gotMode != wantMode || gotRows != wantRows {
					t.Fatalf("%s/%d/state%d layout=%d,%d want %d,%d", glyph, width, i, gotMode, gotRows, wantMode, wantRows)
				}
				if got := lipgloss.Height(m.listFooter()); got != wantRows {
					t.Fatalf("footer height=%d want %d", got, wantRows)
				}
				for _, tier := range []legendSection{{Title: "Resize", Pairs: [][2]string{{"←→", "nudge"}, {"enter", "set"}, {"esc", "cancel"}}}, {Title: "Rename", Pairs: [][2]string{{"enter", "save"}, {"esc", "cancel"}}}, {Title: "Focused", Pairs: [][2]string{{"ctrl+q", "back"}}}} {
					if got := lipgloss.Height(m.transientFooter(tier).Text); got != wantRows {
						t.Fatalf("%s footer height=%d want %d", tier.Title, got, wantRows)
					}
				}
			}
		}
	}
}
func TestNarrowFooterKeepsTheBodyAtShortHeights(t *testing.T) {
	m := narrowFooterModel(t)
	m.selectSessionRow(t, "narrow")
	for _, height := range []int{8, 12, 16} {
		for _, width := range []int{10, 15, 30} {
			m.layout.width, m.layout.height = width, height
			if got := lipgloss.Height(m.listFooter()); got > max(uiframe.MaxRows, (height-m.listChromeRows()-1)/2) {
				t.Fatalf("%dx%d footer too tall: %d", width, height, got)
			}
			if body := m.listBodyHeight(); body < 3 {
				t.Fatalf("%dx%d body=%d", width, height, body)
			}
		}
	}
}
func TestNarrowListFooterKeepsConfiguredActionKeys(t *testing.T) {
	for _, glyph := range []presentation.GlyphMode{presentation.GlyphModeAdvanced, presentation.GlyphModeBasic} {
		for _, width := range []int{12, 20, 28} {
			for _, binding := range []struct{ action, key string }{{keybind.Settings, "pgup"}, {keybind.Messages, "pgup"}, {keybind.Help, "up"}} {
				t.Run(fmt.Sprintf("%s/%d/%s", glyph, width, binding.action), func(t *testing.T) {
					m := narrowFooterModel(t)
					m.prefs.glyphMode = glyph
					m.layout.width, m.layout.height = width, 60
					if binding.key == "up" {
						m.services.listKeys = m.services.listKeys.With(keybind.Up, keybind.Keys())
					}
					m.services.listKeys = m.services.listKeys.With(binding.action, bindingOf(t, binding.key))
					m.release.FeedPending = binding.action == keybind.Messages
					m.selectSessionRow(t, "narrow")
					preparedView(m)
					var hit keyHit
					found := false
					for _, candidate := range m.layout.keyHits {
						if candidate.Key.String() == binding.key {
							hit = candidate
							found = true
							break
						}
					}
					if !found {
						t.Fatalf("no hit for %s=%s: %s", binding.action, binding.key, m.listFooter())
					}
					check := func() {
						var ok bool
						switch binding.action {
						case keybind.Settings:
							_, ok = surfaceOf[*uisettings.Surface](m)
						case keybind.Messages:
							_, ok = surfaceOf[*uinotices.Surface](m)
						case keybind.Help:
							_, ok = surfaceOf[*uihelp.Surface](m)
						}
						if !ok {
							t.Fatalf("%s surface did not open", binding.action)
						}
					}
					m = leftClick(m, hit.X0, hit.Y)
					check()
					m.handleKey(tea.KeyMsg{Type: tea.KeyEsc})
					key := tea.KeyMsg{Type: tea.KeyPgUp}
					if binding.key == "up" {
						key.Type = tea.KeyUp
					}
					m.handleKey(key)
					check()
				})
			}
		}
	}
}

func TestListFooterKeepsPaneHeightAcrossSessionStatuses(t *testing.T) {
	for _, glyph := range []presentation.GlyphMode{presentation.GlyphModeAdvanced, presentation.GlyphModeBasic} {
		for _, width := range []int{10, 20, 40, 80, 120, 200, 256, 280, 320, 500} {
			m := shotModel()
			m.prefs.glyphMode = glyph
			m.layout.width, m.layout.height = width, 67
			m.selectSessionRow(t, "db-migrations")
			want := -1
			for _, state := range []string{"finished", "idle", "working", "waiting", "errored"} {
				m.workspace.sessions[0].Status = state
				m.rebuildRows()
				height := m.previewPaneHeight()
				if want < 0 {
					want = height
				}
				if height != want {
					t.Errorf("%s/%d/%s pane height=%d, want %d", glyph, width, state, height, want)
				}
			}
		}
	}
}

func TestWideListFooterKeepsRoomForThePreview(t *testing.T) {
	for _, glyph := range []presentation.GlyphMode{presentation.GlyphModeAdvanced, presentation.GlyphModeBasic} {
		for _, width := range []int{320, 500} {
			for _, fullLayout := range []bool{false, true} {
				m := shotModel()
				m.prefs.glyphMode, m.prefs.fullLayout = glyph, fullLayout
				m.layout.width, m.layout.height = width, 12
				m.selectSessionRow(t, "db-migrations")
				m.workspace.sessions[0].Status = "idle"
				m.rebuildRows()
				m.workspace.preview = "wide-preview-visible\n"
				if got := lipgloss.Height(m.listFooter()); got != 2 {
					t.Errorf("%s/%d/full=%t footer height=%d, want 2", glyph, width, fullLayout, got)
				}
				if !fullLayout && !strings.Contains(ansi.Strip(preparedView(m)), "wide-preview-visible") {
					t.Errorf("%s/%d: footer hid the preview", glyph, width)
				}
			}
		}
	}
}

func TestRemoteFooterKeepsPaneHeightWhenReviveAppears(t *testing.T) {
	m := listedModel(t, &fakeSSH{})
	m.prefs.glyphMode = presentation.GlyphModeBasic
	m.layout.width, m.layout.height = 110, 40
	for _, action := range m.services.listKeys.Actions() {
		switch action.Name {
		case keybind.Open, keybind.Attach, keybind.Prompt, keybind.Revive, keybind.Up, keybind.Down,
			keybind.NewSession, keybind.NewConnection, keybind.Help, keybind.Quit, keybind.Settings:
		default:
			m.services.listKeys = m.services.listKeys.With(action.Name, keybind.Keys())
		}
	}
	host := strings.Repeat("h", 40)
	m.ssh.SetList([]store.Connection{{Name: host, Destination: "me@box"}})
	state := uiconnections.Host{OK: true, Sessions: []uiconnections.Session{{ID: "s1", Name: "api", Tool: "claude", Status: "idle"}}}
	setHost(m, host, state)
	m.rail.Focus(uirail.Selection{Kind: uirail.SessionRow, Host: host, SessionID: "s1"})
	active := m.listFooter()
	height := m.previewPaneHeight()
	state.Sessions[0].Status = "dead"
	setHost(m, host, state)
	dead := m.listFooter()
	if !strings.Contains(active, host) || strings.Contains(active, "revive") || !strings.Contains(dead, "revive") {
		t.Fatalf("unexpected remote footers: active=%q dead=%q", active, dead)
	}
	if lipgloss.Height(active) != lipgloss.Height(dead) || height != m.previewPaneHeight() {
		t.Fatalf("footer height %d -> %d, pane height %d -> %d", lipgloss.Height(active), lipgloss.Height(dead), height, m.previewPaneHeight())
	}
}
