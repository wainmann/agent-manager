//go:build windows

package tmux

import (
	"strings"
	"testing"
	"time"

	"github.com/YoanWai/agent-manager/internal/keybind"
)

// Temporary: records how psmux runs a command list given as one invocation,
// beside the one process per command the driver starts today. It asserts
// nothing and comes off this branch with the rest of the measurement.
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
	show := func(label string, args ...string) {
		out, took, err := raw(args...)
		note("%s: %v err=%v took=%s out=%q", label, args, err, took, out)
	}

	styleID := psmuxSession(t, driver, "nats", "", "")
	style := sessionName(styleID)
	styleList := func(length, right, mouse string) []string {
		return commandList(
			[]string{"set-option", "-t", style, "status", "on"},
			[]string{"set-option", "-t", style, "status-right-length", length},
			[]string{"set-option", "-t", style, "status-right", right},
			[]string{"set-option", "-t", style, "status-style", "bg=colour236,fg=colour249"},
			[]string{"set-option", "-t", style, "window-status-format", hiddenWindowStatus},
			[]string{"set-option", "-t", style, "window-status-current-format", hiddenWindowStatus},
			[]string{"set-option", "-t", style, "mouse", mouse},
		)
	}
	for round := range 5 {
		begun := time.Now()
		_, err := driver.run(styleList("111", " split · Ctrl+q / Ctrl+\\ / C-b d = back ", "off")...)
		split := time.Since(begun)
		_, native, nativeErr := raw(styleList("99", " native · Ctrl+q / Ctrl+\\ / C-b d = back ", "on")...)
		note("style round %d: split=%s err=%v native=%s err=%v", round, split, err, native, nativeErr)
	}
	for _, option := range []string{"status", "status-right-length", "status-right", "status-style", "window-status-format", "window-status-current-format", "mouse"} {
		show("style readback", "show-options", "-t", style, "-v", option)
	}

	show("label native", commandList(
		[]string{"set-option", "-t", style, "status-left-length", "80"},
		[]string{"set-option", "-t", style, "status-left", " work / api · a;b ##(x) "},
	)...)
	show("label readback", "show-options", "-t", style, "-v", "status-left")
	show("label length readback", "show-options", "-t", style, "-v", "status-left-length")

	show("pin native", commandList(
		[]string{"set-option", "-t", style, "prefix", "C-a"},
		[]string{"set-option", "-t", style, "prefix2", "None"},
		[]string{"set-option", "-t", style, pinnedPrefixOption, "on"},
	)...)
	show("pin readback prefix", "show-options", "-t", style, "-v", "prefix")
	show("pin readback prefix2", "show-options", "-t", style, "-v", "prefix2")
	show("pin readback mark", "show-options", "-q", "-v", "-t", style, pinnedPrefixOption)
	show("unpin native", commandList(
		[]string{"set-option", "-u", "-t", style, "prefix"},
		[]string{"set-option", "-u", "-t", style, "prefix2"},
		[]string{"set-option", "-u", "-t", style, pinnedPrefixOption},
	)...)
	show("unpin readback prefix", "show-options", "-t", style, "-v", "prefix")
	show("unpin readback mark", "show-options", "-q", "-v", "-t", style, pinnedPrefixOption)

	show("display and capture native", commandList(
		[]string{"display-message", "-p", "-t", PaneTarget(styleID), "#{pane_id} #{cursor_x}"},
		[]string{"capture-pane", "-p", "-e", "-t", PaneTarget(styleID)},
	)...)
	out, err := driver.run(commandList(
		[]string{"display-message", "-p", "-t", PaneTarget(styleID), "#{pane_id} #{cursor_x}"},
		[]string{"capture-pane", "-p", "-e", "-t", PaneTarget(styleID)},
	)...)
	note("display and capture split: err=%v out=%q", err, out)

	show("failing list", commandList(
		[]string{"set-option", "-t", style, "@am_before", "yes"},
		[]string{"no-such-command"},
		[]string{"set-option", "-t", style, "@am_after", "yes"},
	)...)
	show("failing list before", "show-options", "-q", "-v", "-t", style, "@am_before")
	show("failing list after", "show-options", "-q", "-v", "-t", style, "@am_after")
	show("missing session list", commandList(
		[]string{"set-option", "-t", "am_diagmissing", "status-left-length", "80"},
		[]string{"set-option", "-t", "am_diagmissing", "status-left", "x"},
	)...)

	quotedID := psmuxSession(t, driver, "natq", "", "")
	show("bindings baseline root", append(sessionRoute(quotedID), "list-keys", "-T", "root")...)
	show("bindings baseline prefix d", append(sessionRoute(quotedID), "list-keys", "-T", "prefix")...)
	f7, _ := keybind.Parse("f7")
	f8, _ := keybind.Parse("f8")
	quoted := [][]string{
		{"set-window-option", "-g", "pane-base-index", "0"},
		{"unbind-key", "-T", "root", "C-q"},
		rootBinding(f7, "detach-client"),
		rootBinding(f8, driver.requestAction(quotedID, RequestReview)...),
		{"set-option", "-g", rootKeysOption, "F7 F8"},
		{"bind-key", "-T", "prefix", "e", bindingArg("detach-client")},
	}
	show("bindings quoted native", append(sessionRoute(quotedID), commandList(quoted...)...)...)
	show("bindings quoted root", append(sessionRoute(quotedID), "list-keys", "-T", "root")...)
	show("bindings quoted prefix", append(sessionRoute(quotedID), "list-keys", "-T", "prefix")...)
	show("bindings quoted recorded", append(sessionRoute(quotedID), "show-options", "-gqv", rootKeysOption)...)
	show("bindings quoted base index", append(sessionRoute(quotedID), "show-window-options", "-g", "-v", "pane-base-index")...)

	plainID := psmuxSession(t, driver, "natp", "", "")
	plain := [][]string{
		{"set-window-option", "-g", "pane-base-index", "0"},
		{"unbind-key", "-T", "root", "C-q"},
		{"bind-key", "-n", "F7", "detach-client"},
		append([]string{"bind-key", "-n", "F8"}, driver.requestAction(plainID, RequestReview)...),
		{"set-option", "-g", rootKeysOption, "F7 F8"},
		{"bind-key", "-T", "prefix", "e", "detach-client"},
	}
	show("bindings plain native", append(sessionRoute(plainID), commandList(plain...)...)...)
	show("bindings plain root", append(sessionRoute(plainID), "list-keys", "-T", "root")...)
	show("bindings plain prefix", append(sessionRoute(plainID), "list-keys", "-T", "prefix")...)
	show("bindings plain recorded", append(sessionRoute(plainID), "show-options", "-gqv", rootKeysOption)...)

	firstID := psmuxSession(t, driver, "natf", "", "")
	show("bind first native", append(sessionRoute(firstID), commandList(
		rootBinding(f7, "detach-client"),
		[]string{"set-option", "-g", "@am_diag", "yes"},
		rootBinding(f8, driver.requestAction(firstID, RequestReview)...),
	)...)...)
	show("bind first root", append(sessionRoute(firstID), "list-keys", "-T", "root")...)
	show("bind first option", append(sessionRoute(firstID), "show-options", "-gqv", "@am_diag")...)
}
