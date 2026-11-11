package sessioncmd

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/YoanWai/agent-manager/internal/config"
	"github.com/YoanWai/agent-manager/internal/git"
	"github.com/YoanWai/agent-manager/internal/hooks"
	"github.com/YoanWai/agent-manager/internal/lifecyclehook"
	"github.com/YoanWai/agent-manager/internal/notify"
	"github.com/YoanWai/agent-manager/internal/store"
	"github.com/YoanWai/agent-manager/internal/tmux"
)

// Runtime is the already-open local workspace used by a long-lived owner.
// The caller retains ownership of borrowed dependencies.
type Runtime struct {
	Config   config.Config
	Store    *store.Store
	Driver   *tmux.Driver
	Hooks    *hooks.Manager
	Git      *git.Driver
	Snapshot func(string, string) error
	Events   lifecyclehook.Emitter
}

// Backend binds command services to exactly one local runtime. OpenBackend
// owns a lazily opened store; BorrowBackend uses the caller's runtime and
// never closes it.
type Backend struct {
	mu        sync.Mutex
	configDir string
	runtime   Runtime
	opened    bool
	owned     bool
	closed    bool
	version   string
	drain     time.Duration
}

func OpenBackend(configDir string) *Backend {
	return &Backend{configDir: configDir, owned: true, drain: lifecyclehook.DrainCall}
}

// SetVersion is the version written into hook envelopes. Set it before the
// first command opens the runtime.
func (b *Backend) SetVersion(version string) {
	b.mu.Lock()
	b.version = version
	b.mu.Unlock()
}

// SetDrain is how long Close waits for hooks. A short command stays under
// the remote call timeout. Spawn stays under the create timeout.
func (b *Backend) SetDrain(limit time.Duration) {
	b.mu.Lock()
	b.drain = limit
	b.mu.Unlock()
}

func BorrowBackend(runtime Runtime) (*Backend, error) {
	if err := validateRuntime(runtime); err != nil {
		return nil, err
	}
	if runtime.Hooks == nil {
		return nil, errors.New("session command runtime requires a hook manager")
	}
	return &Backend{runtime: runtime, opened: true}, nil
}

func validateRuntime(runtime Runtime) error {
	switch {
	case runtime.Store == nil:
		return errors.New("session command runtime requires a store")
	case runtime.Driver == nil:
		return errors.New("session command runtime requires a tmux driver")
	case runtime.Snapshot == nil:
		return errors.New("session command runtime requires a snapshot writer")
	default:
		return nil
	}
}

func (b *Backend) resolve() (Runtime, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return Runtime{}, errors.New("session command backend is closed")
	}
	if !b.opened {
		runtime, err := openRuntime(b.configDir, b.version)
		if err != nil {
			return Runtime{}, err
		}
		b.runtime = runtime
		b.opened = true
	} else if b.owned {
		// Settings can change between commands of a long-lived owner, so the
		// session keys are reread from the store each time.
		keys, err := b.runtime.Store.SessionKeys()
		if err != nil {
			return Runtime{}, err
		}
		b.runtime.Driver.SetSessionKeys(keys)
	}
	return b.runtime, nil
}

func openRuntime(configDir, version string) (Runtime, error) {
	cfg, err := config.Default()
	if err != nil {
		return Runtime{}, err
	}
	driver, err := tmux.New()
	if err != nil {
		return Runtime{}, err
	}
	st, err := openStore(configDir)
	if err != nil {
		return Runtime{}, err
	}
	keys, err := st.SessionKeys()
	if err != nil {
		st.Close()
		return Runtime{}, err
	}
	driver.SetSessionKeys(keys)
	gitDriver, _ := git.New()
	events := lifecyclehook.New(lifecyclehook.Options{
		ConfigDir: configDir,
		Store:     st,
		Version:   version,
		Show:      notify.ShowCommand,
	})
	return Runtime{
		Config:   cfg,
		Store:    st,
		Driver:   driver,
		Hooks:    hooks.NewManager(configDir),
		Git:      gitDriver,
		Snapshot: st.SetSnapshot,
		Events:   events,
	}, nil
}

// Close releases only a runtime opened by this backend. It is safe to call
// more than once and is a no-op for borrowed runtimes.
func (b *Backend) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return nil
	}
	b.closed = true
	if !b.owned || !b.opened || b.runtime.Store == nil {
		return nil
	}
	if dispatcher, ok := b.runtime.Events.(*lifecyclehook.Dispatcher); ok {
		ctx, cancel := context.WithTimeout(context.Background(), b.drain)
		dispatcher.Close(ctx)
		cancel()
	}
	return b.runtime.Store.Close()
}

func (b *Backend) Lifecycle() (*Lifecycle, error) {
	runtime, err := b.resolve()
	if err != nil {
		return nil, err
	}
	return NewLifecycle(runtime)
}

// ProactiveCoordination reads the coordination mode from this backend's
// already-open store. Long-lived MCP owners must not open a second store to
// decide which instructions they serve.
func (b *Backend) ProactiveCoordination() (bool, error) {
	runtime, err := b.resolve()
	if err != nil {
		return false, err
	}
	return runtime.Store.ProactiveCoordination()
}

// Connections reads the SSH connections from this backend's store, for the
// same reason as ProactiveCoordination.
func (b *Backend) Connections() ([]store.Connection, error) {
	runtime, err := b.resolve()
	if err != nil {
		return nil, err
	}
	return runtime.Store.Connections()
}

// WithStore runs use on this backend's store, for the same reason as
// ProactiveCoordination.
func (b *Backend) WithStore(use func(*store.Store) error) error {
	runtime, err := b.resolve()
	if err != nil {
		return err
	}
	return use(runtime.Store)
}

func (b *Backend) commands(words Vocabulary) (*runtime, error) {
	bound, err := b.resolve()
	if err != nil {
		return nil, err
	}
	return &runtime{
		cfg:    bound.Config,
		words:  words,
		store:  bound.Store,
		driver: bound.Driver,
		events: bound.Events,
	}, nil
}

func (b *Backend) gitDriver() (*git.Driver, error) {
	bound, err := b.resolve()
	if err != nil {
		return nil, err
	}
	if bound.Git != nil {
		return bound.Git, nil
	}
	return git.New()
}
