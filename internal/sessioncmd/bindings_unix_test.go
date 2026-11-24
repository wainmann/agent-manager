//go:build !windows

package sessioncmd

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/YoanWai/agent-manager/internal/status"
	"github.com/YoanWai/agent-manager/internal/store"
	"github.com/YoanWai/agent-manager/internal/tmux"
)

// A long-lived process, such as a running TUI, binds ← from the stored
// setting when it creates a session, even after another process changed it.
func TestLifecycleLaunchBindsTheStoredArrowStep(t *testing.T) {
	h := newSessionHarness(t)
	runtime := harnessRuntime(t, h)
	if err := ApplyBindingSettings(runtime.Store, runtime.Driver); err != nil {
		t.Fatal(err)
	}
	lifecycle, err := NewLifecycle(runtime)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.store.SetSetting(store.ArrowStepSetting, store.ArrowStepAttach); err != nil {
		t.Fatal(err)
	}
	sess := store.Session{ID: "arrowstep", Name: "arrowstep", Tool: "echoer", Cwd: t.TempDir(), Status: status.Starting}
	if _, err := lifecycle.Launch(LaunchRequest{
		Session: sess, Tool: runtime.Config.CLIs[sess.Tool], BaseCommand: "sleep 30", Pane: PaneSize{Width: 80, Height: 24},
	}); err != nil {
		t.Fatalf("Launch: %v", err)
	}
	out, err := exec.Command(tmux.Binary, tmux.SocketArgs(h.driver.SocketName(), "show-options", "-gv", "@am_prompt_head")...).CombinedOutput()
	if err != nil {
		t.Fatalf("show-options: %v: %s", err, out)
	}
	if got := strings.TrimSpace(string(out)); got != "1" {
		t.Fatalf("@am_prompt_head = %q, want 1 from the stored attach value", got)
	}
}
