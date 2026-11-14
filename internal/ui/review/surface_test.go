package review

import (
	"testing"

	"github.com/YoanWai/agent-manager/internal/ui/presentation"
)

type pickerViewHost struct{}

func (pickerViewHost) Size() (int, int)     { return 120, 34 }
func (pickerViewHost) Pop()                 {}
func (pickerViewHost) CardWidth(int) int    { return 64 }
func (pickerViewHost) InnerWidth(w int) int { return w - 4 }
func (pickerViewHost) CardSized(_ int, title, _ string, _ [][2]string) string {
	return title
}

func TestSurfaceViewTitleFollowsGlyphMode(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mode   presentation.GlyphMode
		prefix string
	}{
		{"basic", presentation.GlyphModeBasic, ""},
		{"advanced", presentation.GlyphModeAdvanced, "⌥ "},
		{"default", "", "⌥ "},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, title := range []string{"Review repo", "Review branch", "Review commit", "Diff base"} {
				host := pickerViewHost{}
				s := NewSurface(host, nil, title, PickRepo, "", PickerSource{}, "",
					RenderContext{GlyphMode: tc.mode}, presentation.GlyphsFor(tc.mode).ReviewGlyph)
				if got, want := s.View(host, ""), tc.prefix+title; got != want {
					t.Errorf("%s: title %q, want %q", title, got, want)
				}
			}
		})
	}
}
