package sessioncmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// FocusDelivery is one request handed to the manager, with the channel the
// outcome goes back on. A delivery taken after Expires is dropped: its
// caller has already been told it timed out.
type FocusDelivery struct {
	Request FocusRequest
	Answer  chan<- error
	Expires time.Time
}

// staleEndpointAge spares a socket another manager may have bound but not
// yet started listening on.
const staleEndpointAge = time.Minute

// ListenFocus serves focus requests until the returned stop runs, on an
// endpoint of this process's own beside base, so every manager on one
// server stays reachable and a caller can tell when several are. Each
// connection carries one request and gets the outcome back on itself, so
// no request can be mistaken for another.
func ListenFocus(base string, deliver func(FocusDelivery)) (stop func(), err error) {
	return listenFocusAs(base, os.Getpid(), deliver)
}

func listenFocusAs(base string, pid int, deliver func(FocusDelivery)) (stop func(), err error) {
	sweepStaleFocusEndpoints(base, time.Now())
	path := base + "." + strconv.Itoa(pid)
	// tmux makes this directory with these permissions when its server
	// starts, which may come after the manager does.
	if err := os.MkdirAll(filepath.Dir(base), 0o700); err != nil {
		return nil, err
	}
	// A process that held this id before and crashed left its socket here.
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	listener, err := net.Listen("unix", path)
	if err != nil {
		return nil, err
	}
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go answerFocus(conn, deliver)
		}
	}()
	// Closing a listener Go created also unlinks its socket.
	return func() { listener.Close() }, nil
}

// focusEndpoints lists the sockets managers created beside base, live or
// left behind by one that crashed.
func focusEndpoints(base string) ([]string, error) {
	dir, prefix := filepath.Dir(base), filepath.Base(base)+"."
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var paths []string
	for _, entry := range entries {
		pid, ok := strings.CutPrefix(entry.Name(), prefix)
		if !ok {
			continue
		}
		if _, err := strconv.Atoi(pid); err != nil {
			continue
		}
		paths = append(paths, filepath.Join(dir, entry.Name()))
	}
	return paths, nil
}

func sweepStaleFocusEndpoints(base string, now time.Time) {
	paths, err := focusEndpoints(base)
	if err != nil {
		return
	}
	for _, path := range paths {
		info, err := os.Lstat(path)
		if err != nil || now.Sub(info.ModTime()) < staleEndpointAge {
			continue
		}
		if conn, err := net.Dial("unix", path); err == nil {
			conn.Close()
			continue
		}
		os.Remove(path)
	}
}

// dialManager connects to the one manager answering beside base. A socket
// that refuses the connection belongs to a manager that exited. Several
// live ones leave no way to tell which window the caller is looking at.
func dialManager(base string) (net.Conn, error) {
	paths, err := focusEndpoints(base)
	if err != nil {
		return nil, err
	}
	var live []net.Conn
	var pids []string
	for _, path := range paths {
		conn, err := net.DialTimeout("unix", path, time.Second)
		if err != nil {
			continue
		}
		live = append(live, conn)
		pids = append(pids, strings.TrimPrefix(filepath.Base(path), filepath.Base(base)+"."))
	}
	switch len(live) {
	case 0:
		return nil, errNoManager
	case 1:
		return live[0], nil
	}
	for _, conn := range live {
		conn.Close()
	}
	return nil, fmt.Errorf("%d Agent Manager windows are running on this tmux server (processes %s), so it cannot tell which one to use (quit all but one)", len(live), strings.Join(pids, ", "))
}

func answerFocus(conn net.Conn, deliver func(FocusDelivery)) {
	defer conn.Close()
	expires := time.Now().Add(focusAnswerWait)
	if err := conn.SetDeadline(expires.Add(time.Second)); err != nil {
		return
	}
	var request FocusRequest
	if err := json.NewDecoder(conn).Decode(&request); err != nil {
		return
	}
	answer := make(chan error, 1)
	// The program stops taking messages while it is suspended in a full
	// screen attach, so the hand-over must not hold up the reply.
	go deliver(FocusDelivery{Request: request, Answer: answer, Expires: expires})
	var reply focusAnswer
	select {
	case err := <-answer:
		if err != nil {
			reply.Error = err.Error()
		}
	case <-time.After(time.Until(expires)):
		reply.Error = "Agent Manager did not act on the request in time; it does while a session is attached full screen only once you detach, and then drops it"
	}
	_ = json.NewEncoder(conn).Encode(reply)
}
