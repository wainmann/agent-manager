package lifecyclehook

import (
	"context"
	"errors"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/YoanWai/agent-manager/internal/store"
)

const (
	QueueCap    = 64
	Workers     = 4
	DrainCall   = 12 * time.Second
	DrainCreate = 45 * time.Second
	// DrainQuit bounds the drain when a long-lived process exits. It is
	// one hook's longest timeout, past which running trees are cancelled.
	DrainQuit = 30 * time.Second
)

// Options configures the one dispatcher a process owns.
type Options struct {
	ConfigDir string
	Store     *store.Store
	Version   string
	Show      func(event, command string)
}

// Dispatcher runs hooks off the caller's goroutine. The queue is a slice
// rather than a channel so a worker can skip an event whose session is
// already running and start a different session's event. Events that have
// not started are capped at QueueCap; the ones a worker has claimed are
// not part of that count.
type Dispatcher struct {
	configDir string
	show      func(event, command string)

	mu       sync.Mutex
	cond     *sync.Cond
	db       *store.Store
	ver      string
	waiting  []Event
	running  map[string]struct{}
	active   int
	started  bool
	stopping bool
	shutdown bool
	root     context.Context
	cancel   context.CancelFunc
	wg       sync.WaitGroup
	// cache serves the last load while size and mtime are unchanged. A
	// rewrite in the same second that keeps the same size leaves both
	// unchanged, so that rewrite is served on the next event. An atomic
	// rename usually changes both.
	cache hookCache

	failMu    sync.Mutex
	failWG    sync.WaitGroup
	failAt    map[string]time.Time
	claimed   map[string]bool
	shownErr  map[string]bool
	configErr string
	configSet bool
}

type hookCache struct {
	set  bool
	mod  time.Time
	size int64
	file File
	err  error
}

func New(opts Options) *Dispatcher {
	d := &Dispatcher{
		configDir: opts.ConfigDir,
		db:        opts.Store,
		ver:       opts.Version,
		show:      opts.Show,
		running:   map[string]struct{}{},
		claimed:   map[string]bool{},
		shownErr:  map[string]bool{},
	}
	d.cond = sync.NewCond(&d.mu)
	return d
}

func (d *Dispatcher) SetStore(st *store.Store) {
	d.mu.Lock()
	d.db = st
	d.mu.Unlock()
}

func (d *Dispatcher) SetVersion(version string) {
	d.mu.Lock()
	d.ver = version
	d.mu.Unlock()
}

func (d *Dispatcher) Emit(ev Event) {
	d.mu.Lock()
	if d.shutdown {
		d.mu.Unlock()
		return
	}
	d.startLocked()
	if len(d.waiting) >= QueueCap {
		if !d.allow(ev.Name) {
			d.mu.Unlock()
			return
		}
		d.failWG.Add(1)
		d.mu.Unlock()
		go func() {
			defer d.failWG.Done()
			d.writeFailure(ev, "", "queue full, dropped "+ev.Name, "")
		}()
		return
	}
	d.waiting = append(d.waiting, ev)
	d.cond.Signal()
	d.mu.Unlock()
}

func (d *Dispatcher) startLocked() {
	if d.started {
		return
	}
	d.root, d.cancel = context.WithCancel(context.Background())
	d.started = true
	for range Workers {
		d.wg.Add(1)
		go d.worker()
	}
}

// Close runs queued hooks until they finish or ctx ends. On the deadline
// it cancels hooks that have started and drops the rest.
func (d *Dispatcher) Close(ctx context.Context) {
	if ctx == nil {
		ctx = context.Background()
	}
	d.mu.Lock()
	if d.shutdown {
		d.mu.Unlock()
		return
	}
	d.shutdown = true
	started := d.started
	d.mu.Unlock()
	defer d.failWG.Wait()
	if !started {
		return
	}
	idle := make(chan struct{})
	go func() {
		d.mu.Lock()
		for len(d.waiting) > 0 || d.active > 0 {
			d.cond.Wait()
		}
		d.mu.Unlock()
		close(idle)
	}()
	select {
	case <-idle:
		d.mu.Lock()
		d.stopping = true
		d.mu.Unlock()
		d.cond.Broadcast()
		d.wg.Wait()
		d.cancel()
	case <-ctx.Done():
		d.mu.Lock()
		dropped := d.waiting
		d.waiting = nil
		d.stopping = true
		d.mu.Unlock()
		d.cancel()
		d.cond.Broadcast()
		d.wg.Wait()
		for _, ev := range dropped {
			d.record(ev, "", "dropped on close", "")
		}
	}
}

func (d *Dispatcher) worker() {
	defer d.wg.Done()
	for {
		ev, ok := d.take()
		if !ok {
			return
		}
		d.dispatch(ev)
		d.finish(ev.Session.ID)
	}
}

func (d *Dispatcher) take() (Event, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	for {
		if d.stopping {
			return Event{}, false
		}
		if i, ok := d.next(); ok {
			ev := d.waiting[i]
			d.waiting = append(d.waiting[:i], d.waiting[i+1:]...)
			d.running[ev.Session.ID] = struct{}{}
			d.active++
			return ev, true
		}
		d.cond.Wait()
	}
}

func (d *Dispatcher) next() (int, bool) {
	if d.active >= Workers {
		return 0, false
	}
	for i, ev := range d.waiting {
		if _, busy := d.running[ev.Session.ID]; busy {
			continue
		}
		return i, true
	}
	return 0, false
}

func (d *Dispatcher) finish(id string) {
	d.mu.Lock()
	delete(d.running, id)
	d.active--
	d.cond.Broadcast()
	d.mu.Unlock()
}

func (d *Dispatcher) dispatch(ev Event) {
	file, err := d.load()
	if err != nil || !file.Found {
		return
	}
	line := Line(ev, d.version())
	base := Env(ev)
	for _, hook := range file.Hooks {
		if !match(hook, ev) {
			continue
		}
		d.announce(hook)
		env := slices.Clone(base)
		if hook.Notify {
			env = append(env, ev.NotifyEnv...)
		}
		ctx, cancel := context.WithTimeout(d.rootContext(), hook.Timeout)
		output, runErr := run(ctx, hook.Command, env, line)
		timedOut := errors.Is(ctx.Err(), context.DeadlineExceeded)
		cancel()
		if runErr != nil {
			d.record(ev, firstArg(hook.Command), failureReason(hook.Timeout, runErr, timedOut), output)
		}
	}
}

func (d *Dispatcher) rootContext() context.Context {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.root == nil {
		return context.Background()
	}
	return d.root
}

func (d *Dispatcher) version() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.ver
}

func (d *Dispatcher) currentStore() *store.Store {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.db
}

func (d *Dispatcher) load() (File, error) {
	path := hooksPath(d.configDir)
	info, statErr := os.Stat(path)
	var mod time.Time
	var size int64
	missing := errors.Is(statErr, os.ErrNotExist)
	if statErr != nil && !missing {
		d.noteConfig(statErr.Error())
		return File{Path: path, Found: true}, statErr
	}
	if statErr == nil {
		mod, size = info.ModTime(), info.Size()
	}
	d.mu.Lock()
	if d.cache.set && d.cache.mod.Equal(mod) && d.cache.size == size && d.cache.file.Found == !missing {
		file, loadErr := d.cache.file, d.cache.err
		d.mu.Unlock()
		return file, loadErr
	}
	d.mu.Unlock()
	file, loadErr := Load(d.configDir)
	d.mu.Lock()
	d.cache = hookCache{set: true, mod: mod, size: size, file: file, err: loadErr}
	d.mu.Unlock()
	if loadErr != nil {
		d.noteConfig(loadErr.Error())
	} else {
		d.noteConfig("")
	}
	return file, loadErr
}

func (d *Dispatcher) announce(hook Hook) {
	fp := Fingerprint(hook.Event, hook.Command)
	d.failMu.Lock()
	if d.claimed[fp] || d.shownErr[fp] {
		d.failMu.Unlock()
		return
	}
	d.failMu.Unlock()
	st := d.currentStore()
	if st == nil {
		d.failMu.Lock()
		if d.claimed[fp] {
			d.failMu.Unlock()
			return
		}
		d.claimed[fp] = true
		d.failMu.Unlock()
		d.showHook(hook)
		return
	}
	fresh, err := st.ClaimHookShown(fp)
	d.failMu.Lock()
	if err != nil {
		if d.shownErr[fp] {
			d.failMu.Unlock()
			return
		}
		d.shownErr[fp] = true
		d.failMu.Unlock()
		d.showHook(hook)
		return
	}
	d.claimed[fp] = true
	d.failMu.Unlock()
	if fresh {
		d.showHook(hook)
	}
}

func (d *Dispatcher) showHook(hook Hook) {
	if d.show == nil {
		return
	}
	text := hook.Event + ": " + strings.Join(hook.Command, " ")
	if runes := []rune(text); len(runes) > bannerRunes {
		text = string(runes[:bannerRunes-1]) + "…"
	}
	d.show(hook.Event, text)
}

func firstArg(argv []string) string {
	if len(argv) == 0 {
		return ""
	}
	return argv[0]
}

func failureReason(timeout time.Duration, err error, timedOut bool) string {
	if timedOut {
		return "timed out after " + timeout.String()
	}
	if err == nil {
		return ""
	}
	return err.Error()
}
