package execution

import (
	"slices"
	"strings"
	"testing"

	"github.com/YoanWai/agent-manager/internal/lifecyclehook"
	"github.com/YoanWai/agent-manager/internal/status"
)

// A turn that starts and ends between two polls still reports working and
// then finished, once each, whether the status comes from the tool's hooks
// or from its pane rules.
func TestATurnBetweenPollsEmitsWorkingThenFinishedOnce(t *testing.T) {
	for _, hooked := range []bool{true, false} {
		name := "pane rules"
		if hooked {
			name = "hook status"
		}
		t.Run(name, func(t *testing.T) {
			m := buildModel(t)
			// Without the tty echo the pane holds only what the tool prints,
			// so the summary row stays the last thing above the composer.
			quiet := m.cfg.Tools["claude-hooked"]
			quiet.Command = `sh -c 'stty -echo; exec cat' --`
			m.cfg.Tools["claude-hooked"] = quiet
			createSessionOn(t, m, "quick", "claude-hooked", t.TempDir())
			sess := m.sessionRows()[0]
			waitForPane(t, m, sess.ID, "boot-marker")
			if err := m.store.SetSetting(notifyFinishedSetting, "on"); err != nil {
				t.Fatal(err)
			}
			hookStatus := func(state string) {
				if hooked {
					writeHookStatus(t, m, sess.ID, state)
				}
			}
			print := func(lines ...string) {
				t.Helper()
				if err := m.tmux.Paste(sess.ID, strings.Join(lines, "\n")+"\n"); err != nil {
					t.Fatal(err)
				}
				settledPane(t, m, sess.ID, lines[len(lines)-1])
			}
			turn := func(prompt, summary string) {
				t.Helper()
				print("❯ "+prompt, "● "+prompt+" done", summary, "❯ ")
			}
			rec := &lifecyclehook.Recorder{}
			m.poller.events = rec
			step := func() []string {
				t.Helper()
				rec.Reset()
				if msg, failed := testPollMessage(m.poller.Step()).(errMsg); failed {
					t.Fatal(msg.err)
				}
				var got []string
				for _, ev := range rec.Events() {
					got = append(got, ev.Name+":"+ev.PreviousStatus+">"+ev.Session.Status)
				}
				return got
			}
			want := func(label string, got []string, events ...string) {
				t.Helper()
				if !slices.Equal(got, events) {
					t.Fatalf("%s: events = %q, want %q", label, got, events)
				}
			}
			finished := []string{"session_state:working>finished", "notification:working>finished"}

			hookStatus(status.Finished)
			turn("first", "✻ Worked for 3s")
			if err := m.store.UpdateStatus(sess.ID, status.Finished); err != nil {
				t.Fatal(err)
			}
			step()
			want("a poll with nothing new", step())

			turn("again", "✻ Worked for 1s")
			want("the poll after a turn it never saw run", step(), "session_state:finished>working")
			want("the poll after that", step(), finished...)
			want("a quiet poll after the turn", step())

			hookStatus(status.Working)
			print("❯ watched", "● reading")
			want("a turn a poll sees running", step(), "session_state:finished>working")
			hookStatus(status.Finished)
			turn("watched", "✻ Worked for 2s")
			want("the poll that sees it end", step(), finished...)
			want("a quiet poll after the watched turn", step())
		})
	}
}
