package lifecyclehook

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/YoanWai/agent-manager/internal/store"
)

const (
	LogFile       = "hooks.log"
	LogLimit      = 256 << 10
	FailureWindow = time.Minute
)

var logMu sync.Mutex

// record throttles one command failing over and over for one session, so
// a different command or another session's failure is still written.
func (d *Dispatcher) record(ev Event, argv []string, reason, output string) {
	key := strings.Join(append([]string{ev.Name, ev.Session.ID}, argv...), "\x00")
	if !d.allow(key) {
		return
	}
	d.writeAllowed(key, ev, argv[0], reason, output)
}

// writeAllowed writes a failure allow let through. One that did not
// reach both state.db and hooks.log leaves the window open for the next.
func (d *Dispatcher) writeAllowed(key string, ev Event, command, reason, output string) {
	if err := d.writeFailure(ev, command, reason, output); err != nil {
		d.failMu.Lock()
		delete(d.failAt, key)
		d.failMu.Unlock()
	}
}

func (d *Dispatcher) allow(key string) bool {
	d.failMu.Lock()
	defer d.failMu.Unlock()
	now := time.Now()
	if last, ok := d.failAt[key]; ok && now.Sub(last) < FailureWindow {
		return false
	}
	for seen, last := range d.failAt {
		if now.Sub(last) >= FailureWindow {
			delete(d.failAt, seen)
		}
	}
	d.failAt[key] = now
	return true
}

func (d *Dispatcher) writeFailure(ev Event, command, reason, output string) error {
	stored := d.db.AddHookFailure(store.HookFailure{At: time.Now().UTC(), Event: ev.Name, Session: ev.Session.ID, Command: command, Reason: reason})
	line := fmt.Sprintf("%s %s session=%s %s: %s\n", time.Now().UTC().Format(time.RFC3339), ev.Name, ev.Session.ID, command, reason)
	if stored != nil {
		line += "not saved to state.db: " + stored.Error() + "\n"
	}
	if output != "" {
		line += output
		if output[len(output)-1] != '\n' {
			line += "\n"
		}
	}
	return errors.Join(stored, appendLog(filepath.Join(d.configDir, LogFile), line))
}

func (d *Dispatcher) noteConfig(msg string) {
	d.failMu.Lock()
	unchanged := d.configSet && d.configErr == msg
	d.failMu.Unlock()
	if unchanged {
		return
	}
	if err := d.db.SetHooksConfigError(msg); err != nil {
		return
	}
	d.failMu.Lock()
	d.configErr = msg
	d.configSet = true
	d.failMu.Unlock()
}

func appendLog(path, line string) error {
	logMu.Lock()
	defer logMu.Unlock()
	if info, err := os.Stat(path); err == nil && info.Size() > LogLimit {
		if err := os.Truncate(path, 0); err != nil {
			return err
		}
	}
	file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	_, err = file.WriteString(line)
	return errors.Join(err, file.Close())
}
