package ui

import (
	"testing"

	"github.com/YoanWai/agent-manager/internal/systheme"
	uionboarding "github.com/YoanWai/agent-manager/internal/ui/onboarding"
	onboardingruntime "github.com/YoanWai/agent-manager/internal/ui/onboarding/runtime"
	uisettings "github.com/YoanWai/agent-manager/internal/ui/settings"
	uiterminal "github.com/YoanWai/agent-manager/internal/ui/terminal"
	uitheme "github.com/YoanWai/agent-manager/internal/ui/theme"
)

func followModel(t *testing.T, auto, theme string) *Model {
	t.Helper()
	previous := uitheme.Current()
	t.Cleanup(func() { applyTheme(previous) })
	m := buildModel(t)
	for key, value := range map[string]string{uisettings.ThemeAutoSetting: auto, uisettings.ThemeSetting: theme} {
		if err := m.services.store.SetSetting(key, value); err != nil {
			t.Fatal(err)
		}
	}
	m.settings.Cache.ApplyValues([]uisettings.Value{
		{Key: uisettings.ThemeAutoSetting, Value: auto},
		{Key: uisettings.ThemeSetting, Value: theme},
	})
	applyTheme(uitheme.Palettes[uitheme.Index(theme)])
	return m
}

func report(m *Model, light bool) { m.Update(uiterminal.SchemeMsg{Light: light}) }

func TestFollowColorScheme(t *testing.T) {
	tests := []struct {
		name  string
		auto  string
		light bool
		want  string
	}{
		{"light flips a dark theme to its counterpart", "on", true, "catppuccin latte"},
		{"dark flips back to the stored theme", "on", false, "catppuccin mocha"},
		{"auto off keeps the theme", "off", true, "catppuccin mocha"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := followModel(t, tt.auto, "catppuccin mocha")
			report(m, !tt.light)
			report(m, tt.light)
			if got := uitheme.Current().Name; got != tt.want {
				t.Fatalf("theme = %q, want %q", got, tt.want)
			}
		})
	}
}

// The open dialog shows the followed theme but keeps the saved choice, so
// esc never writes the OS-picked counterpart as the user's theme.
func TestFollowColorSchemeWithSettingsOpen(t *testing.T) {
	m := followModel(t, "on", "catppuccin mocha")
	m.applyTestMsg(t, m.openSettings()())
	report(m, true)
	d := m.settingsCard().State()
	if got := uitheme.Current().Name; got != "catppuccin latte" {
		t.Fatalf("theme = %q, want catppuccin latte", got)
	}
	if got := uitheme.Palettes[d.ThemeIndex].Name; got != "catppuccin latte" {
		t.Fatalf("dialog shows %q, want catppuccin latte", got)
	}
	if d.ManualTheme != "catppuccin mocha" {
		t.Fatalf("manual theme = %q, want catppuccin mocha", d.ManualTheme)
	}
}

// Turning the toggle on resolves against the scheme the terminal reported,
// not a detection that may only read back the colors the manager set.
func TestSettingsToggleResolvesAgainstTheReportedScheme(t *testing.T) {
	m := followModel(t, "off", "catppuccin mocha")
	report(m, true)
	m.applyTestMsg(t, m.openSettings()())
	m.settingsCard().State().Field = uisettings.FieldThemeAuto
	m.cycleSetting(1)
	if got := uitheme.Current().Name; got != "catppuccin latte" {
		t.Fatalf("theme = %q, want catppuccin latte", got)
	}
}

// First-run setup previews against the live scheme, so neither the next key
// nor the save puts back the scheme its probe detected.
func TestSetupFollowsTheReportedScheme(t *testing.T) {
	m := followModel(t, "on", "catppuccin mocha")
	m.layout.width, m.layout.height = 120, 36
	m.openSetup(true)
	m.applySetupProbe(onboardingruntime.ProbeRequest{Target: setupState(t, m).ID()},
		onboardingruntime.ProbeResult{Installed: map[string]bool{"claude": true}, Scheme: systheme.SchemeDark}, nil)
	report(m, true)
	if got := uitheme.Current().Name; got != "catppuccin latte" {
		t.Fatalf("after the report the theme = %q, want catppuccin latte", got)
	}
	pressKeys(m, enterKey)
	if got := uitheme.Current().Name; got != "catppuccin latte" {
		t.Fatalf("after a key the theme = %q, want catppuccin latte", got)
	}
	pressKeys(m, enterKey, enterKey, enterKey, enterKey)
	m.drainEffects(t)
	if step := setupState(t, m).Step(); step != uionboarding.QuickPrompt {
		t.Fatalf("setup stopped at %v, want it past the theme step", step)
	}
	if got := uitheme.Current().Name; got != "catppuccin latte" {
		t.Fatalf("after the save the theme = %q, want catppuccin latte", got)
	}
	if got := storedSetting(t, m, uisettings.ThemeSetting); got != "catppuccin mocha" {
		t.Fatalf("stored theme = %q, want the manual catppuccin mocha", got)
	}
	if got := storedSetting(t, m, uisettings.ThemeAutoSetting); got != "on" {
		t.Fatalf("stored theme_auto = %q, want on", got)
	}
}
