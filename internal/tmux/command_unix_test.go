//go:build darwin || linux

package tmux

import (
	"syscall"
	"testing"
	"testing/synctest"
)

func TestKillProcessGroupWaitsForLeaderBeforeTrustingEPERM(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const leader = 123
		groupSignals, leaderProbes := 0, 0
		err := killProcessGroup(leader, func(pid int, signal syscall.Signal) error {
			switch {
			case pid == -leader && signal == syscall.SIGKILL:
				groupSignals++
				if groupSignals == 2 || groupSignals == 4 {
					return syscall.EPERM
				}
				return nil
			case pid == leader && signal == 0:
				leaderProbes++
				if leaderProbes == 2 {
					return syscall.ESRCH
				}
				return nil
			default:
				t.Fatalf("unexpected signal %d to %d", signal, pid)
				return nil
			}
		})
		if err != nil {
			t.Fatal(err)
		}
		if groupSignals != 4 || leaderProbes != 2 {
			t.Fatalf("stopped before the leader was reaped and its last child signaled, group signals %d, leader probes %d", groupSignals, leaderProbes)
		}
	})
}
