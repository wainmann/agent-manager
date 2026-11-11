package lifecyclehook

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
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

func (d *Dispatcher) record(ev Event, command, reason, output string) {
	if !d.allow(ev.Name) {
		return
	}
	d.writeFailure(ev, command, reason, output)
}

func (d *Dispatcher) allow(event string) bool {
	d.failMu.Lock()
	defer d.failMu.Unlock()
	now := time.Now()
	if last, ok := d.failAt[event]; ok && now.Sub(last) < FailureWindow {
		return false
	}
	d.failAt[event] = now
	return true
}

func (d *Dispatcher) writeFailure(ev Event, command, reason, output string) {
	_ = d.db.AddHookFailure(store.HookFailure{At: time.Now().UTC(), Event: ev.Name, Command: command, Reason: reason})
	line := fmt.Sprintf("%s %s %s: %s\n", time.Now().UTC().Format(time.RFC3339), ev.Name, command, reason)
	if output != "" {
		line += output
		if output[len(output)-1] != '\n' {
			line += "\n"
		}
	}
	_ = appendLog(filepath.Join(d.configDir, LogFile), line)
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
