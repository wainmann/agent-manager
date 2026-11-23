package tmux

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"
)

// Temporary measurement of multiplexer invocations on the CI runners. It
// comes off this branch before the pull request leaves draft.
var (
	timingFile = os.Getenv("AM_PSMUX_TIMING")
	timingMu   sync.Mutex
)

func recordTiming(kind string, index, count int, begun time.Time, startLatency, total time.Duration, err error, args []string) {
	if timingFile == "" {
		return
	}
	failed := "ok"
	if err != nil {
		failed = strings.NewReplacer("\t", " ", "\n", " ", "\r", " ").Replace(err.Error())
	}
	text := strings.NewReplacer("\t", " ", "\n", " ", "\r", " ").Replace(strings.Join(args, " "))
	if kind != "note" && len(text) > 200 {
		text = text[:200]
	}
	line := fmt.Sprintf("%d\t%d\t%s\t%d\t%d\t%d\t%d\t%s\t%s\n",
		begun.UnixMilli(), os.Getpid(), kind, index, count, startLatency.Milliseconds(), total.Milliseconds(), failed, text)
	timingMu.Lock()
	defer timingMu.Unlock()
	file, openErr := os.OpenFile(timingFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if openErr != nil {
		return
	}
	defer file.Close()
	_, _ = file.WriteString(line)
}

// StartTimingProbes times a bare process start and a multiplexer start that
// talks to no server, twice a second, so a slow command can be read against
// what the machine was doing at that moment.
func StartTimingProbes() (stop func()) {
	if timingFile == "" {
		return func() {}
	}
	done := make(chan struct{})
	finished := make(chan struct{})
	probe := func(kind, bin string, args ...string) {
		cmd := exec.Command(bin, args...)
		cmd.Env = commandEnv()
		begun := time.Now()
		err := cmd.Start()
		started := time.Since(begun)
		if err == nil {
			err = cmd.Wait()
		}
		recordTiming(kind, 0, 1, begun, started, time.Since(begun), err, append([]string{bin}, args...))
	}
	go func() {
		defer close(finished)
		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
			}
			if runtime.GOOS == "windows" {
				probe("probe-process", "cmd.exe", "/c", "exit 0")
			} else {
				probe("probe-process", "/bin/sh", "-c", "exit 0")
			}
			probe("probe-mux", Binary, "-V")
		}
	}()
	return func() {
		close(done)
		<-finished
	}
}
