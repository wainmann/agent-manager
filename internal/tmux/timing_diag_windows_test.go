//go:build windows

package tmux

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/YoanWai/agent-manager/internal/keybind"
)

func recordNote(format string, a ...any) {
	recordTiming("note", 0, 0, time.Now(), 0, 0, nil, []string{fmt.Sprintf(format, a...)})
}

// Temporary: records what psmux leaves behind when a command list arrives as
// one invocation, beside one process per command. It asserts nothing and
// comes off this branch with the rest of the measurement.
func TestDiagPsmuxNativeLists(t *testing.T) {
	driver := requireTmux(t)
	note := func(format string, a ...any) {
		t.Helper()
		t.Logf(format, a...)
		recordNote(format, a...)
	}
	raw := func(args ...string) (string, time.Duration, error) {
		begun := time.Now()
		out, err := tmuxCmd(args...).CombinedOutput()
		return strings.TrimRight(string(out), "\r\n"), time.Since(begun), err
	}
	show := func(label string, args ...string) string {
		out, took, err := raw(args...)
		note("%s: %v err=%v took=%s out=%q", label, args, err, took, out)
		return out
	}
	bindings := func(id string) [][]string {
		keys := driver.currentSessionKeys()
		commands := [][]string{{"set-window-option", "-g", "pane-base-index", "0"}}
		var bound []string
		bind := func(binding keybind.Binding, action []string) {
			for _, key := range binding.Keys() {
				commands = append(commands, rootBinding(key, action...))
				bound = append(bound, key.Tmux())
			}
		}
		bind(keys.Binding(keybind.Detach), []string{"detach-client"})
		bind(keys.Binding(keybind.Review), driver.requestAction(id, RequestReview))
		bind(keys.Binding(keybind.Editor), driver.requestAction(id, RequestEditor))
		commands = append(commands, recordRootBindings(bound)...)
		return append(commands, []string{"bind-key", "-T", "prefix", "d", bindingArg("detach-client")})
	}
	newServer := func(prefix string) string {
		id := prefixedID(prefix)
		if out, _, err := raw("new-session", "-d", "-s", sessionName(id), "-c", paneDir()); err != nil {
			t.Fatalf("new-session: %v: %s", err, out)
		}
		t.Cleanup(func() { driver.Kill(id) })
		return id
	}
	tables := func(id string) string {
		var all strings.Builder
		for _, args := range [][]string{
			{"list-keys", "-T", "root"},
			{"list-keys", "-T", "prefix"},
			{"show-options", "-gqv", rootKeysOption},
			{"show-window-options", "-g", "-v", "pane-base-index"},
		} {
			out, _, err := raw(append(sessionRoute(id), args...)...)
			fmt.Fprintf(&all, "%v err=%v\n%s\n", args, err, out)
		}
		return strings.ReplaceAll(all.String(), sessionName(id), "am_SESSION")
	}

	apartID := newServer("nata")
	for _, command := range bindings(apartID) {
		if out, _, err := raw(append(sessionRoute(apartID), command...)...); err != nil {
			note("bindings apart: %v err=%v out=%q", command, err, out)
		}
	}
	wholeID := newServer("natw")
	show("bindings whole", append(sessionRoute(wholeID), commandList(bindings(wholeID)...)...)...)
	apart, whole := tables(apartID), tables(wholeID)
	note("bindings tables equal=%v", apart == whole)
	note("bindings apart tables: %q", apart)
	note("bindings whole tables: %q", whole)

	driverID := psmuxSession(t, driver, "natd", "", "")
	created := tables(driverID)
	note("bindings created equal to apart=%v", created == apart)
	note("bindings created tables: %q", created)

	name := sessionName(driverID)
	show("escaped semicolon list", commandList(
		[]string{"set-option", "-t", name, "@first", `alpha\;`},
		[]string{"set-option", "-t", name, "@second", "beta"},
	)...)
	show("escaped semicolon first", "show-options", "-v", "-t", name, "@first")
	show("escaped semicolon second", "show-options", "-v", "-t", name, "@second")
	show("escaped semicolon alone", "set-option", "-t", name, "@third", `alpha\;`)
	show("escaped semicolon third", "show-options", "-v", "-t", name, "@third")

	errorID := psmuxSession(t, driver, "nate", "Write-Output 'ERROR: boom'", "ERROR: boom")
	show("capture list over an ERROR line", commandList(
		[]string{"display-message", "-p", "-t", PaneTarget(errorID), "#{pane_id}"},
		[]string{"capture-pane", "-p", "-t", PaneTarget(errorID)},
	)...)
	show("capture alone over an ERROR line", "capture-pane", "-p", "-t", PaneTarget(errorID))
}
