//go:build darwin || linux

package tmux

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Every pane is read by one tmux process, each capture is the one a single
// capture returns, and a session that has gone costs the others nothing.
func TestCapturePanesReadsEveryPaneInOneInvocation(t *testing.T) {
	requireTmux(t)
	real, err := exec.LookPath(Binary)
	if err != nil {
		t.Fatal(err)
	}
	calls := filepath.Join(t.TempDir(), "calls")
	counting := filepath.Join(t.TempDir(), "counting-tmux")
	script := "#!/bin/sh\necho call >> " + ShellQuote(calls) + "\nexec " + ShellQuote(real) + " \"$@\"\n"
	if err := os.WriteFile(counting, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	driver := &Driver{bin: counting, socket: testSocket}

	stamp := strings.ReplaceAll(time.Now().Format("150405.000000"), ".", "")
	ids := []string{"capa" + stamp, "capb" + stamp, "capc" + stamp}
	want := map[string]string{}
	for _, id := range ids {
		if err := driver.Create(id, "/tmp", "sh -c "+ShellQuote("printf 'pane of "+id+"\\n'; exec sleep 30"), nil, 0, 0); err != nil {
			t.Fatalf("Create: %v", err)
		}
		t.Cleanup(func() { driver.Kill(id) })
		for deadline := time.Now().Add(5 * time.Second); !strings.Contains(want[id], "pane of "+id); time.Sleep(20 * time.Millisecond) {
			if time.Now().After(deadline) {
				t.Fatalf("pane %s never drew its line:\n%s", id, want[id])
			}
			if want[id], err = driver.CapturePane(id); err != nil {
				t.Fatalf("CapturePane: %v", err)
			}
		}
	}

	if err := os.Remove(calls); err != nil {
		t.Fatal(err)
	}
	got := driver.CapturePanes(ids)
	logged, err := os.ReadFile(calls)
	if err != nil {
		t.Fatal(err)
	}
	if spawned := strings.Count(string(logged), "call"); spawned != 1 {
		t.Fatalf("CapturePanes spawned tmux %d times for %d panes, want 1", spawned, len(ids))
	}
	for _, id := range ids {
		if got[id] != want[id] {
			t.Fatalf("CapturePanes[%s] = %q, want what CapturePane returns, %q", id, got[id], want[id])
		}
	}

	deep := driver.CapturePanesHistoryContext(context.Background(), ids, 300)
	for _, id := range ids {
		single, err := driver.CapturePaneHistory(id, 300)
		if err != nil {
			t.Fatalf("CapturePaneHistory: %v", err)
		}
		if deep[id] != single {
			t.Fatalf("CapturePanesHistoryContext[%s] = %q, want what CapturePaneHistory returns, %q", id, deep[id], single)
		}
	}

	gone := "capgone" + stamp
	got = driver.CapturePanes([]string{ids[0], gone, ids[2]})
	if _, read := got[gone]; read {
		t.Fatalf("CapturePanes answered for a session that does not exist: %q", got[gone])
	}
	for _, id := range []string{ids[0], ids[2]} {
		if got[id] != want[id] {
			t.Fatalf("beside a closed pane, CapturePanes[%s] = %q, want %q", id, got[id], want[id])
		}
	}
}

// A history capture its caller gave up on returns at once, instead of
// holding a quote pass or a reply copy until the tmux child gives up.
func TestCapturePaneHistoryContextStopsWithItsContext(t *testing.T) {
	blocked := filepath.Join(t.TempDir(), "blocked-tmux")
	if err := os.WriteFile(blocked, []byte("#!/bin/sh\nwhile :; do sleep 1; done\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(50*time.Millisecond, cancel)
	defer cancel()

	started := time.Now()
	_, err := (&Driver{bin: blocked, socket: testSocket}).CapturePaneHistoryContext(ctx, "x1", 300)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("CapturePaneHistoryContext error = %v, want context.Canceled", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("capture outlived its cancelled context by %v", elapsed)
	}
}
