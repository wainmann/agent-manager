package watch

import (
	"sync"
	"testing"
	"testing/synctest"
	"time"
)

type captureLoopTest struct {
	t        *testing.T
	stop     chan struct{}
	input    chan struct{}
	events   chan struct{}
	mu       sync.Mutex
	pane     string
	arrivals chan focusArrival
	observed []focusArrival
}

func startCaptureLoop(t *testing.T) *captureLoopTest {
	loop := &captureLoopTest{
		t: t, stop: make(chan struct{}), input: make(chan struct{}, 1),
		events: make(chan struct{}, 1), pane: "seed", arrivals: make(chan focusArrival, 1024),
	}
	go captureLoop(loop.stop, loop.input, loop.events, nil, func() (string, bool) {
		pane := loop.currentPane()
		loop.arrivals <- focusArrival{time.Now(), pane}
		return pane, true
	})
	synctest.Wait()
	loop.wantFrames(1)
	return loop
}

func (loop *captureLoopTest) close() {
	close(loop.stop)
	synctest.Wait()
}

func (loop *captureLoopTest) output(pane string) {
	loop.mu.Lock()
	loop.pane = pane
	loop.mu.Unlock()
	select {
	case loop.events <- struct{}{}:
	default:
	}
	synctest.Wait()
}

func (loop *captureLoopTest) wantFrames(want int) {
	loop.t.Helper()
	if got := len(loop.frames()); got != want {
		loop.t.Fatalf("captures = %d, want %d", got, want)
	}
}

func (loop *captureLoopTest) currentPane() string {
	loop.mu.Lock()
	defer loop.mu.Unlock()
	return loop.pane
}

func (loop *captureLoopTest) frames() []focusArrival {
	for {
		select {
		case frame := <-loop.arrivals:
			loop.observed = append(loop.observed, frame)
		default:
			return loop.observed
		}
	}
}
