package settings

import (
	"testing"

	"github.com/YoanWai/agent-manager/internal/ui/presentation"
)

func TestMirrorReadsTheRowTheFrameCaresAbout(t *testing.T) {
	glyphs := Mirror(GlyphModeSetting, string(presentation.GlyphModeAdvanced))
	if glyphs.GlyphMode != presentation.GlyphModeAdvanced || glyphs.Flag != nil {
		t.Fatalf("the symbols row should mirror its mode only: %+v", glyphs)
	}
	editor := Mirror(EditorSetting, "nvim")
	if editor.Editor != "nvim" || editor.Flag != nil {
		t.Fatalf("the editor row should mirror its line only: %+v", editor)
	}
	step := Mirror(ArrowStepSetting, "attach")
	if step.ArrowStep != ArrowStepAttach || step.Flag != nil {
		t.Fatalf("the arrow row should mirror its value only: %+v", step)
	}
	if unknown := Mirror(ArrowStepSetting, ""); unknown.ArrowStep != ArrowStepFocus {
		t.Fatalf("an unset arrow row should mirror the focus default: %+v", unknown)
	}
	notify := Mirror(NotificationsSetting, "on")
	if notify.Flag == nil || !*notify.Flag {
		t.Fatalf("a two-way row stored on should mirror on: %+v", notify)
	}
	// The mouse row's flag is MouseDisabled, so a stored on mirrors off.
	mouse := Mirror(MouseSetting, "on")
	if mouse.Flag == nil || *mouse.Flag {
		t.Fatalf("mouse on should mirror an enabled mouse: %+v", mouse)
	}
	layout := Mirror(SessionLayoutSetting, "full")
	if layout.Flag == nil || !*layout.Flag {
		t.Fatalf("the layout row's full choice should mirror on: %+v", layout)
	}
	if unset := Mirror("theme", "nord"); unset != (Mirrored{}) {
		t.Fatalf("a key the frame never reads should mirror nothing: %+v", unset)
	}
}
