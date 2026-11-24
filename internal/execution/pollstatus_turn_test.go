package execution

import (
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/YoanWai/agent-manager/internal/lifecyclehook"
	"github.com/YoanWai/agent-manager/internal/status"
)

// A turn that starts and ends between two polls still reports working and
// then finished, once each, whether the status comes from the tool's hooks
// or from its pane rules. A resting pane that only redraws (a hint row, a
// draft in the composer, a resize) reports nothing.
func TestATurnBetweenPollsEmitsWorkingThenFinishedOnce(t *testing.T) {
	for _, hooked := range []bool{true, false} {
		name := "pane rules"
		if hooked {
			name = "hook status"
		}
		t.Run(name, func(t *testing.T) {
			m := buildModel(t)
			// Redraws the whole screen from the file named on each line, the
			// way a full-screen CLI repaints, with nothing echoed.
			drawer := m.cfg.CLIs["claude-hooked"]
			drawer.Command = `sh -c 'stty -echo; while IFS= read -r f; do printf "\033[H\033[2J"; cat "$f"; done' --`
			drawer.UserEcho = "^❯ "
			drawer.ChromeLine = `^\s+Tip: `
			m.cfg.CLIs["claude-hooked"] = drawer
			engine, err := status.NewEngine(m.cfg)
			if err != nil {
				t.Fatal(err)
			}
			m.poller.engine = engine
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
			frames := t.TempDir()
			drawn := 0
			draw := func(rows ...string) {
				t.Helper()
				drawn++
				marker := "frame " + strconv.Itoa(drawn)
				path := filepath.Join(frames, strconv.Itoa(drawn))
				body := strings.Join(append(rows, "  "+marker), "\n")
				if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
					t.Fatal(err)
				}
				if err := m.tmux.Paste(sess.ID, path+"\n"); err != nil {
					t.Fatal(err)
				}
				settledPane(t, m, sess.ID, marker)
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
			rows := func(groups ...[]string) []string { return slices.Concat(groups...) }
			finished := []string{"session_state:working>finished", "notification:working>finished"}
			first := []string{"❯ first prompt", "● first reply"}
			firstEnd := []string{"✻ Worked for 3s"}
			composer := []string{"❯ "}

			hookStatus(status.Finished)
			draw(rows([]string{"● older reply"}, first, firstEnd, composer)...)
			if err := m.store.UpdateStatus(sess.ID, status.Finished); err != nil {
				t.Fatal(err)
			}
			step()
			want("a poll with nothing new", step())

			draw(rows([]string{"● oldest reply", "● older reply"}, first, []string{"  Tip: name the tab with /title"}, firstEnd, []string{"❯ a draft never sent"})...)
			want("a hint row, a moved top edge and a draft", step())
			if err := m.tmux.Resize(sess.ID, 60, 20); err != nil {
				t.Fatal(err)
			}
			draw(rows(first, firstEnd, composer)...)
			want("a resize", step())
			if err := m.tmux.Resize(sess.ID, 80, 24); err != nil {
				t.Fatal(err)
			}
			draw(rows([]string{"● older reply"}, first, firstEnd, composer)...)
			want("a resize back", step())

			again := rows(first, firstEnd, []string{"❯ again", "● again reply", "✻ Worked for 1s"})
			draw(rows(again, composer)...)
			want("the poll after a turn it never saw run", step(), "session_state:finished>working")
			want("the poll after that", step(), finished...)
			want("a quiet poll after the turn", step())

			hookStatus(status.Working)
			draw(rows(again, []string{"❯ watched", "● reading"}, composer)...)
			want("a turn a poll sees running", step(), "session_state:finished>working")
			hookStatus(status.Finished)
			draw(rows(again, []string{"❯ watched", "● read it", "✻ Worked for 2s"}, composer)...)
			want("the poll that sees it end", step(), finished...)
			want("a quiet poll after the watched turn", step())
		})
	}
}

// A pass that ends on another session's failed write still keeps the turn
// it announced for the sessions before it, so the next passes announce it
// once and then go quiet instead of reporting it again.
func TestATurnAnnouncedBeforeAFailedPassIsNotAnnouncedAgain(t *testing.T) {
	m := buildModel(t)
	drawer := m.cfg.CLIs["claude-hooked"]
	drawer.Command = `sh -c 'stty -echo; while IFS= read -r f; do printf "\033[H\033[2J"; cat "$f"; done' --`
	drawer.StatusSource = ""
	drawer.UserEcho = "^❯ "
	m.cfg.CLIs["claude-hooked"] = drawer
	engine, err := status.NewEngine(m.cfg)
	if err != nil {
		t.Fatal(err)
	}
	m.poller.engine = engine
	createSessionOn(t, m, "turns", "claude-hooked", t.TempDir())
	createSessionOn(t, m, "failing", "claude", t.TempDir())
	rows := m.sessionRows()
	if len(rows) != 2 || rows[0].Name != "turns" {
		t.Fatalf("sessions = %+v, want turns listed first", rows)
	}
	turns, failing := rows[0], rows[1]
	waitForPane(t, m, turns.ID, "boot-marker")
	frames := t.TempDir()
	drawn := 0
	draw := func(rows ...string) {
		t.Helper()
		drawn++
		marker := "frame " + strconv.Itoa(drawn)
		path := filepath.Join(frames, strconv.Itoa(drawn))
		if err := os.WriteFile(path, []byte(strings.Join(append(rows, "  "+marker), "\n")), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := m.tmux.Paste(turns.ID, path+"\n"); err != nil {
			t.Fatal(err)
		}
		settledPane(t, m, turns.ID, marker)
	}
	rec := &lifecyclehook.Recorder{}
	m.poller.events = rec
	step := func() (events []string, failed bool) {
		t.Helper()
		rec.Reset()
		_, failed = testPollMessage(m.poller.Step()).(errMsg)
		for _, ev := range rec.Events() {
			if ev.Session.ID == turns.ID {
				events = append(events, ev.Name+":"+ev.PreviousStatus+">"+ev.Session.Status)
			}
		}
		return events, failed
	}

	draw("❯ first prompt", "● first reply", "✻ Worked for 3s", "❯ ")
	if err := m.store.UpdateStatus(turns.ID, status.Finished); err != nil {
		t.Fatal(err)
	}
	if _, failed := step(); failed {
		t.Fatal("the first pass failed")
	}
	if err := m.store.UpdateStatus(failing.ID, status.Errored); err != nil {
		t.Fatal(err)
	}
	rawSQL(t, m.dbPath, "CREATE TRIGGER refuse_status BEFORE UPDATE OF status ON sessions WHEN NEW.id = '"+failing.ID+"' BEGIN SELECT RAISE(ABORT, 'status write refused'); END")
	draw("❯ first prompt", "● first reply", "✻ Worked for 3s", "❯ again", "● again reply", "✻ Worked for 1s", "❯ ")
	var got []string
	for range 4 {
		events, failed := step()
		if !failed {
			t.Fatal("a pass wrote the refused session's status")
		}
		got = append(got, events...)
	}
	want := []string{"session_state:finished>working", "session_state:working>finished"}
	if !slices.Equal(got, want) {
		t.Fatalf("events = %q, want %q", got, want)
	}
}
