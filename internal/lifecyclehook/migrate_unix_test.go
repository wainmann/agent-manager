//go:build !windows

package lifecyclehook

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/YoanWai/agent-manager/internal/status"
	"github.com/YoanWai/agent-manager/internal/store"
	"github.com/YoanWai/agent-manager/internal/testenv"
)

func TestMigratedCommandReadsStdinAndTheBannerVariables(t *testing.T) {
	testenv.SkipPOSIXShell(t)
	dir := t.TempDir()
	st := openStore(t)
	out := filepath.Join(dir, "out")
	command := "cat > " + quote(out) + "; printf '%s\\n' \"$AM_TITLE\" \"$AM_BODY\" >> " + quote(out)
	if err := st.SetSetting(store.NotifyCommandKey, command); err != nil {
		t.Fatal(err)
	}
	if err := MigrateNotifyCommand(dir, st); err != nil {
		t.Fatal(err)
	}
	d, _ := openDispatcher(t, dir)
	d.Emit(Event{
		Name:      EventSessionState,
		Session:   store.Session{ID: "s1", Name: "deploy", Tool: "claude", Status: status.Waiting, Cwd: dir},
		NotifyEnv: []string{"AM_NOTIFY_KIND=waiting", "AM_TITLE=Hello", "AM_BODY=Need you"},
	})
	waitUntil(t, func() bool { return strings.Contains(readFile(t, out), "Need you\n") })
	got := readFile(t, out)
	if !strings.Contains(got, `"event":"session_state"`) || !strings.Contains(got, "Hello") || !strings.Contains(got, "Need you") {
		t.Fatalf("migrated command saw %q", got)
	}
	if strings.Count(strings.Split(got, "\n")[0], "\n") != 0 {
		t.Fatalf("stdin was not one line: %q", got)
	}
}
