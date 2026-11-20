//go:build unix

package inputguard

import (
	"errors"
	"time"

	"golang.org/x/sys/unix"
)

func waitInput(fd int, timeout time.Duration) (bool, error) {
	ms := int((timeout + time.Millisecond - 1) / time.Millisecond)
	for {
		fds := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}
		ready, err := unix.Poll(fds, ms)
		if errors.Is(err, unix.EINTR) {
			continue
		}
		if err != nil {
			return false, err
		}
		return ready > 0, nil
	}
}
