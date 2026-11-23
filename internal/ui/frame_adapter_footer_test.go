package ui

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	uidialogs "github.com/YoanWai/agent-manager/internal/ui/dialogs"
	"github.com/charmbracelet/lipgloss"
)

func TestPreparedFooterKeepsBodyGeometryAndHits(t *testing.T) {
	for _, full := range []bool{false, true} {
		for _, state := range []string{"list", "focus", "rename", "resize", "quick"} {
			t.Run(fmt.Sprintf("%s/full=%t", state, full), func(t *testing.T) {
				m := shotModel()
				m.prefs.fullLayout = full
				switch state {
				case "focus":
					m.push(newFocusSurface(m))
				case "rename":
					m.push(uidialogs.NewRenameSurface(renameRoot{m}, uidialogs.RenameSession("s", "alpha", "claude", nil)))
				case "resize":
					m.layout.split.ResizeMode = true
					m.push(newResizeSurface(m))
				case "quick":
					m.openQuickMode()
				}
				for _, width := range []int{15, 28, 40, 80, 130, 200, 256, 80} {
					m.layout.width, m.layout.height = width, 67
					footer := m.footer()
					wantBody := m.listBodyHeight()
					if got := m.listBodyHeightForFooter(footer.Text); got != wantBody {
						t.Fatalf("%d: reused footer body=%d want%d", width, got, wantBody)
					}
					m.layout.keyHits = nil
					frame := m.viewListFrame()
					start := m.listChromeRows() + wantBody + 1
					wantHits := footer.Hits.Moved(0, start)
					if !reflect.DeepEqual(m.layout.keyHits[len(m.layout.keyHits)-len(wantHits):], wantHits) {
						t.Fatalf("%d: footer hit positions changed", width)
					}
					if got := lipgloss.Height(frame); got != m.layout.height {
						t.Fatalf("%d: frame height=%d want%d", width, got, m.layout.height)
					}
					for _, hit := range wantHits {
						if hit.Y >= len(strings.Split(frame, "\n")) {
							t.Fatalf("hit below frame:%+v", hit)
						}
					}
				}
			})
		}
	}
}
