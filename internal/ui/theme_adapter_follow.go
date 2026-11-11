package ui

import (
	"github.com/YoanWai/agent-manager/internal/systheme"
	uisettings "github.com/YoanWai/agent-manager/internal/ui/settings"
	uiterminal "github.com/YoanWai/agent-manager/internal/ui/terminal"
	uitheme "github.com/YoanWai/agent-manager/internal/ui/theme"
	tea "github.com/charmbracelet/bubbletea"
)

// followColorScheme repaints for a reported scheme while theme follows OS.
// An open Settings dialog decides with its own, possibly unsaved, toggle
// and shows the theme in use; the saved choice is never written. First-run
// setup resolves its preview against the report the same way.
func (m *Model) followColorScheme(msg uiterminal.SchemeMsg) tea.Cmd {
	m.env.scheme = systheme.SchemeDark
	if msg.Light {
		m.env.scheme = systheme.SchemeLight
	}
	if card, ok := surfaceOf[*uisettings.Surface](m); ok {
		d := card.State()
		if !d.ThemeAuto {
			return nil
		}
		d.ShowTheme(uitheme.AutoName(d.ManualTheme, msg.Light))
		return m.showPalette(uitheme.Palettes[d.ThemeIndex].Name)
	}
	if m.setupOpen() {
		return m.syncSetupPalette()
	}
	cache := m.settings.Cache
	if !uisettings.FlagOn(uisettings.ThemeAutoSetting, cache.Value(uisettings.ThemeAutoSetting)) {
		return nil
	}
	stored := uitheme.Palettes[uitheme.Index(cache.Value(uisettings.ThemeSetting))].Name
	return m.showPalette(uitheme.AutoName(stored, msg.Light))
}

// liveScheme is the scheme the terminal last reported, or detect's answer
// before any report arrives: once the terminal has spoken, a detection made
// earlier or from the colors the manager set itself is stale.
func (m *Model) liveScheme(detect func() systheme.Scheme) systheme.Scheme {
	if m.env.scheme != systheme.SchemeUnknown {
		return m.env.scheme
	}
	return detect()
}

// showPalette repaints the manager, the terminal and the agent panes in a
// palette, and does nothing when it is already the one in use.
func (m *Model) showPalette(name string) tea.Cmd {
	if name == uitheme.Current().Name {
		return nil
	}
	applyTheme(uitheme.Palettes[uitheme.Index(name)])
	SyncTerminalColors()
	return m.syncPaneTheme()
}
