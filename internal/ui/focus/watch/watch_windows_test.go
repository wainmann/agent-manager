//go:build windows

package watch

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/YoanWai/agent-manager/internal/tmux"
)

// psmuxPane creates a managed session whose PowerShell pane printed ready.
func psmuxPane(t *testing.T, driver *tmux.Driver, prefix string) string {
	t.Helper()
	id := prefix + strings.ReplaceAll(time.Now().Format("150405.000000"), ".", "")
	if err := driver.Create(id, os.TempDir(), "Write-Host ready", nil, 100, 30); err != nil {
		t.Fatalf("Create: %v", err)
	}
	t.Cleanup(func() { driver.Kill(id) })
	for deadline := time.Now().Add(15 * time.Second); ; time.Sleep(100 * time.Millisecond) {
		pane, err := driver.CapturePane(id)
		if err == nil && strings.Contains(pane, "ready") {
			return id
		}
		if time.Now().After(deadline) {
			t.Fatalf("pane %s never printed ready: %q", id, pane)
		}
	}
}

func waitServing(t *testing.T, watch *Watcher, id string) {
	t.Helper()
	for deadline := time.Now().Add(10 * time.Second); !watch.Serving(id); time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("watcher never served %s", id)
		}
	}
}

// The focused psmux pane is served by its control client: a frame is pushed
// when the pane paints rather than found by a forked look, and a write rides
// the client only where its server answers writes.
func TestFocusWatchServesAPsmuxPaneOverItsControlClient(t *testing.T) {
	driver := requireDriver(t)
	id := psmuxPane(t, driver, "psctl")
	frames, send := framesOf(id)
	watch := newTestWatcher(driver, send)
	t.Cleanup(watch.Close)
	watch.SetFocus(id)
	waitServing(t, watch, id)
	watch.mu.Lock()
	control, polled := watch.control, watch.polled
	watch.mu.Unlock()
	if control == nil || polled {
		t.Fatalf("watcher serves %s by polling, want its control client", id)
	}
	command := tmux.SendKeysHex(tmux.PaneTarget(id), []byte("Write-Output ('pushed'+'frame')\r"))
	// A guarded send forks on psmux whatever the client carries, so it is
	// counted on every server.
	if !watch.CountWrite(id, true) {
		t.Fatal("a guarded send that forks was not counted")
	}
	if watch.CaughtUp(0) {
		t.Fatal("a frame from before the guarded send reads as caught up")
	}
	watch.WriteLanded(id)
	// A forked key lands a fork after the frames streaming meanwhile, so
	// it is counted and no frame reads as caught up until it lands.
	counted := watch.CountWrite(id, false)
	queued := uint64(1)
	if counted {
		queued++
	}
	if counted == control.CarriesWrites() {
		t.Fatalf("CountWrite = %v with writes over the client %v, want only forked writes counted", counted, control.CarriesWrites())
	}
	if counted && watch.CaughtUp(queued-1) {
		t.Fatal("a frame from before the counted write reads as caught up")
	}
	available, err := watch.Forward(command)
	if available != control.CarriesWrites() {
		t.Fatalf("Forward available = %v, want the probe's answer %v", available, control.CarriesWrites())
	}
	if err != nil {
		t.Fatalf("Forward: %v", err)
	}
	if !available {
		if err := driver.SendRaw(command); err != nil {
			t.Fatalf("forked send-keys: %v", err)
		}
	}
	watch.InputSent(id)
	if counted {
		watch.WriteLanded(id)
	}
	// A capture can start after the key lands but before WriteLanded, and
	// show the echo with the older count; a later frame catches up.
	start := time.Now()
	echoed := false
	for deadline := time.After(5 * time.Second); ; {
		select {
		case frame := <-frames:
			if !strings.Contains(frame.Preview, "pushedframe") {
				continue
			}
			echoed = true
			if !watch.CaughtUp(frame.Writes) {
				continue
			}
			t.Logf("writes over the client: %v; typed command to its output on screen: %v", available, time.Since(start))
			return
		case <-deadline:
			t.Fatalf("no caught-up frame showing the echo within 5s (echo seen: %v)", echoed)
		}
	}
}

// A forked write that lands after the psmux pane last painted still gets a
// frame that shows it, so the caret is trusted again.
func TestFocusWatchCapturesAWriteLandingOnAQuietPsmuxPane(t *testing.T) {
	driver := requireDriver(t)
	id := psmuxPane(t, driver, "psquiet")
	frames, send := framesOf(id)
	watch := newTestWatcher(driver, send)
	t.Cleanup(watch.Close)
	watch.Pace(true)
	watch.SetFocus(id)
	waitServing(t, watch, id)
	watch.mu.Lock()
	control := watch.control
	watch.mu.Unlock()
	if control == nil {
		t.Fatalf("watcher serves %s by polling, want its control client", id)
	}
	waitFrame(t, frames, "ready", 10*time.Second)
	requireLandedWriteCaptured(t, watch, id, frames)
}

// A has-session that fails without saying the session is gone, as one that
// runs out its budget does, does not read as a missing session: the watcher
// still opens the session's client.
func TestFocusWatchOpensAfterAnUnansweredHasSession(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "attached")
	// An exit /b inside the for block reaches the caller as 0, so each
	// answer exits from a label.
	script := "@echo off\r\n" +
		"for %%a in (%*) do (\r\n" +
		"  if \"%%~a\"==\"-V\" goto version\r\n" +
		"  if \"%%~a\"==\"has-session\" goto busy\r\n" +
		"  if \"%%~a\"==\"attach-session\" goto attach\r\n" +
		")\r\n" +
		"exit /b 1\r\n" +
		":version\r\necho psmux 3.4.0\r\nexit /b 0\r\n" +
		":busy\r\necho server busy 1>&2\r\nexit /b 2\r\n" +
		":attach\r\ntype nul > \"" + marker + "\"\r\nexit /b 1\r\n"
	if err := os.WriteFile(filepath.Join(dir, "psmux.bat"), []byte(script), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	driver, err := tmux.NewWithSocket("focus-watch-unanswered")
	if err != nil {
		t.Fatal(err)
	}
	watch := newTestWatcher(driver, func(any) {})
	t.Cleanup(watch.Close)
	watch.SetFocus("fixture")
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		if _, err := os.Stat(marker); err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("the watcher never opened a client after has-session failed")
		}
	}
}

// Moving focus between psmux panes opens the next pane's client at once.
// psmux 3.4.0's client lingers 2 s after its stdin closes, and the next
// attach waited on it under the gate until Close ended it outright; the
// pane left behind keeps no client.
func TestFocusWatchMovesBetweenPsmuxPanes(t *testing.T) {
	driver := requireDriver(t)
	first, second := psmuxPane(t, driver, "psfirst"), psmuxPane(t, driver, "pssecond")
	frames := map[string]chan Frame{}
	sends := map[string]func(any){}
	for _, id := range []string{first, second} {
		frames[id], sends[id] = framesOf(id)
	}
	watch := newTestWatcher(driver, func(msg any) {
		for _, send := range sends {
			send(msg)
		}
	})
	t.Cleanup(watch.Close)
	var moves []time.Duration
	for _, id := range []string{first, second, first, second} {
		start := time.Now()
		watch.SetFocus(id)
		waitServing(t, watch, id)
		waitFrame(t, frames[id], "ready", 10*time.Second)
		moves = append(moves, time.Since(start))
	}
	t.Logf("focus to first pushed frame: %v", moves)
	for _, move := range moves[1:] {
		if move > time.Second {
			t.Fatalf("a focus move took %v to serve the pane, want under 1s: %v", move, moves)
		}
	}
	for deadline := time.Now().Add(3 * time.Second); ; time.Sleep(50 * time.Millisecond) {
		attached, err := driver.Display(first, "#{session_attached}")
		if err == nil && strings.TrimSpace(attached) == "0" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the pane focus left still has %q clients (%v)", strings.TrimSpace(attached), err)
		}
	}
}
