package tmux

import (
	"context"
	"testing"
	"time"
)

type probeResult struct {
	writes bool
	err    error
}

// startProbe greets a parser-only client and runs its write probe, returning
// once both probe lines reached stdin.
func startProbe(t *testing.T) (*fakeServer, <-chan probeResult) {
	t.Helper()
	server := newFakeServer()
	server.send("%begin 1 0 0", "%end 1 0 0")
	got := make(chan probeResult, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		writes, err := server.control.probeWrites(ctx, "am_x:@1.0")
		got <- probeResult{writes, err}
	}()
	waitWritten(t, server, "send-keys -t am_x:@1.0 -H\ndisplay-message -p "+probeMarker+"\n")
	return server, got
}

func probeAnswer(t *testing.T, got <-chan probeResult) probeResult {
	t.Helper()
	select {
	case result := <-got:
		return result
	case <-time.After(2 * time.Second):
		t.Fatal("probe never resolved")
		return probeResult{}
	}
}

// A server that answers writes replies to the empty send-keys, then to the
// marker.
func TestControlProbeFindsAServerThatAnswersWrites(t *testing.T) {
	server, got := startProbe(t)
	server.send("%begin 2 1 1", "%end 2 1 1", "%begin 3 1 1", probeMarker, "%end 3 1 1")
	if result := probeAnswer(t, got); result.err != nil || !result.writes {
		t.Fatalf("probe = %+v, want writes", result)
	}
}

// psmux 3.4.0 gives the empty send-keys no block, so only the marker answers.
// The waiter nobody answers must not take the next command's reply.
func TestControlProbeFindsAServerThatAnswersNoWrites(t *testing.T) {
	server, got := startProbe(t)
	server.send("%begin 2 1 1", probeMarker, "%end 2 1 1")
	if result := probeAnswer(t, got); result.err != nil || result.writes {
		t.Fatalf("probe = %+v, want no writes", result)
	}
	reply := make(chan string, 1)
	go func() {
		text, err := server.control.Command("display-message -p next")
		if err != nil {
			text = "error: " + err.Error()
		}
		reply <- text
	}()
	waitWritten(t, server, "display-message -p next\n")
	server.send("%begin 3 1 1", "next", "%end 3 1 1")
	select {
	case text := <-reply:
		if text != "next" {
			t.Fatalf("command after the probe = %q, want next", text)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the command after the probe never got its reply")
	}
}

// An error answer to the write is no answer a write can be trusted to.
func TestControlProbeTakesAnErroredWriteForNoWrites(t *testing.T) {
	server, got := startProbe(t)
	server.send("%begin 2 1 1", "command timed out", "%error 2 1 1", "%begin 3 1 1", probeMarker, "%end 3 1 1")
	if result := probeAnswer(t, got); result.err != nil || result.writes {
		t.Fatalf("probe = %+v, want no writes", result)
	}
}

// A parser-only client, like tmux's, carries writes until a probe says not.
func TestControlCarriesWritesByDefault(t *testing.T) {
	if !newFakeServer().control.CarriesWrites() {
		t.Fatal("a client nobody probed refuses writes")
	}
}
