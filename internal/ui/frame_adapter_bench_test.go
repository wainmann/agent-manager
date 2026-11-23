package ui

import (
	"fmt"
	"strings"
	"testing"

	uifocus "github.com/YoanWai/agent-manager/internal/ui/focus"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

func BenchmarkFocusedSplitFrame(b *testing.B) {
	previous := lipgloss.ColorProfile()
	b.Cleanup(func() { lipgloss.SetColorProfile(previous) })
	for _, profile := range []struct {
		name  string
		value termenv.Profile
	}{{"ANSI256", termenv.ANSI256}, {"TrueColor", termenv.TrueColor}} {
		b.Run(profile.name, func(b *testing.B) {
			lipgloss.SetColorProfile(profile.value)
			for _, size := range []struct{ width, height int }{{80, 41}, {130, 41}, {200, 41}, {256, 67}} {
				width, height := size.width, size.height
				name := fmt.Sprintf("width%d", width)
				if height != 41 {
					name = fmt.Sprintf("width%dheight%d", width, height)
				}
				b.Run(name, func(b *testing.B) {
					for _, changing := range []bool{false, true} {
						name := "stationary"
						if changing {
							name = "changing"
						}
						b.Run(name, func(b *testing.B) {
							m := shotModel()
							m.layout.width, m.layout.height = width, height
							m.prefs.fullLayout, m.prefs.terminalBackground = false, false
							m.push(newFocusSurface(m))
							sess, ok := m.selected()
							if !ok {
								b.Fatal("benchmark must have a selected session")
							}
							m.focus.pane.Enter(uifocus.EnterContext{SessionID: sess.ID})
							setFocusPaneID(m, sess.ID)
							sample := strings.Repeat("an agent output line with a complete reply\n", 35)
							m.workspace.preview = sample
							first := preparedView(m)
							if !strings.Contains(first, "an agent output") {
								b.Fatal("focused preview fixture is empty")
							}
							if !m.focused() || m.fullFocus() {
								b.Fatal("benchmark must paint focused split")
							}
							b.ReportAllocs()
							b.ResetTimer()
							for i := 0; i < b.N; i++ {
								if changing {
									m.workspace.preview = fmt.Sprintf("frame %d\n%s", i, sample)
								}
								_ = preparedView(m)
							}
						})
					}
				})
			}
		})
	}
}
