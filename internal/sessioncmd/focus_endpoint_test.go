package sessioncmd

import (
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A Unix socket path stops near 104 bytes, past which t.TempDir's names run.
func focusSocketBase(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "amfocus")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return filepath.Join(dir, "agentmgr.focus")
}

func listenFocusForTest(t *testing.T, base string, pid int) <-chan FocusDelivery {
	t.Helper()
	deliveries := make(chan FocusDelivery, 1)
	stop, err := listenFocusAs(base, pid, func(delivery FocusDelivery) { deliveries <- delivery })
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(stop)
	return deliveries
}

// staleFocusSocket leaves a socket file nobody listens on, the way a
// manager that crashed does.
func staleFocusSocket(t *testing.T, path string) {
	t.Helper()
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	listener.(*net.UnixListener).SetUnlinkOnClose(false)
	listener.Close()
}

func askFocus(t *testing.T, base string, request FocusRequest) <-chan focusAnswer {
	t.Helper()
	answers := make(chan focusAnswer, 1)
	go func() {
		var answer focusAnswer
		conn, err := dialManager(base)
		if err != nil {
			answer.Error = "dial: " + err.Error()
			answers <- answer
			return
		}
		defer conn.Close()
		if err := json.NewEncoder(conn).Encode(request); err != nil {
			answer.Error = "send: " + err.Error()
		} else if err := json.NewDecoder(conn).Decode(&answer); err != nil {
			answer.Error = "read: " + err.Error()
		}
		answers <- answer
	}()
	return answers
}

// Each connection carries one request and reads back the outcome the
// manager handed back for that request, a refusal included.
func TestFocusEndpointAnswersEachRequestWithItsOutcome(t *testing.T) {
	base := focusSocketBase(t)
	deliveries := listenFocusForTest(t, base, 100)

	answers := askFocus(t, base, FocusRequest{ID: "a", Enter: true})
	delivery := <-deliveries
	if delivery.Request != (FocusRequest{ID: "a", Enter: true}) {
		t.Fatalf("delivered %+v, want a entered", delivery.Request)
	}
	delivery.Answer <- nil
	if answer := <-answers; answer.Error != "" {
		t.Fatalf("answered %q, want success", answer.Error)
	}

	answers = askFocus(t, base, FocusRequest{ID: "b"})
	delivery = <-deliveries
	delivery.Answer <- os.ErrNotExist
	if answer := <-answers; answer.Error != os.ErrNotExist.Error() {
		t.Fatalf("answered %q, want the refusal", answer.Error)
	}
}

// Sockets left by managers that exited, and files that are not a
// manager's, never stand in for the one manager that is answering.
func TestDialManagerReachesTheLiveManagerPastStaleSockets(t *testing.T) {
	base := focusSocketBase(t)
	if _, err := dialManager(base); !errors.Is(err, errNoManager) {
		t.Fatalf("dial with nothing there = %v, want no manager", err)
	}
	staleFocusSocket(t, base+".7")
	if err := os.WriteFile(base+".x", nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := dialManager(base); !errors.Is(err, errNoManager) {
		t.Fatalf("dial with only a stale socket = %v, want no manager", err)
	}
	deliveries := listenFocusForTest(t, base, 200)
	askFocus(t, base, FocusRequest{ID: "a"})
	if delivery := <-deliveries; delivery.Request.ID != "a" {
		t.Fatalf("request = %+v, want a", delivery.Request)
	}
}

// With two managers on one tmux server nothing says which window the
// caller means, so the request is refused naming both instead of guessing.
func TestDialManagerRefusesWhenSeveralManagersAnswer(t *testing.T) {
	base := focusSocketBase(t)
	listenFocusForTest(t, base, 301)
	listenFocusForTest(t, base, 302)
	_, err := dialManager(base)
	if err == nil || !strings.Contains(err.Error(), "2 Agent Manager windows") || !strings.Contains(err.Error(), "301, 302") {
		t.Fatalf("dial with two managers = %v, want a refusal naming both", err)
	}
}

// A manager starting up clears sockets crashed managers left long ago, and
// spares a fresh one another manager may be about to listen on. Windows
// cannot set times on a socket, so the sweep is run as if later.
func TestSweepRemovesOnlyOldStaleSockets(t *testing.T) {
	base := focusSocketBase(t)
	staleFocusSocket(t, base+".11")
	live := listenFocusForTest(t, base, 400)
	sweepStaleFocusEndpoints(base, time.Now())
	if _, err := os.Lstat(base + ".11"); err != nil {
		t.Fatalf("fresh socket was swept: %v", err)
	}
	sweepStaleFocusEndpoints(base, time.Now().Add(time.Hour))
	if _, err := os.Lstat(base + ".11"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("old stale socket still there: %v", err)
	}
	askFocus(t, base, FocusRequest{ID: "a"})
	if delivery := <-live; delivery.Request.ID != "a" {
		t.Fatalf("the sweep cut off the live manager: request %+v", delivery.Request)
	}
}
