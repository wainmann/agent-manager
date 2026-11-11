package lifecyclehook

import (
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/BurntSushi/toml"
)

const (
	HooksFile      = "hooks.toml"
	DefaultTimeout = 5 * time.Second
	MinTimeout     = time.Second
	MaxTimeout     = 30 * time.Second
)

// Hook is one command bound to an event, in file order within that event.
type Hook struct {
	Event   string
	Command []string
	Events  []string
	Notify  bool
	Timeout time.Duration
}

// File is a loaded hooks.toml. Found is false when the path is absent,
// which is the normal state and not an error.
type File struct {
	Path          string
	Found         bool
	SchemaVersion int
	Hooks         []Hook
}

type document struct {
	SchemaVersion int                       `toml:"schema_version"`
	Hooks         map[string]toml.Primitive `toml:"hooks"`
}

type rawHook struct {
	Command toml.Primitive `toml:"command"`
	Timeout string         `toml:"timeout"`
	Events  []string       `toml:"events"`
	Notify  bool           `toml:"notify"`
}

// Load reads hooks.toml. A missing file returns a zero File and a nil
// error. Any other problem refuses the whole file.
func Load(dir string) (File, error) {
	path := hooksPath(dir)
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return File{Path: path}, nil
		}
		return File{Path: path, Found: true}, fmt.Errorf("hooks.toml: %w", err)
	}
	file := File{Path: path, Found: true}
	var doc document
	meta, err := toml.Decode(string(data), &doc)
	if err != nil {
		return file, fmt.Errorf("hooks.toml: %w", err)
	}
	if doc.SchemaVersion == 0 {
		return file, errors.New("hooks.toml: schema_version is required")
	}
	if doc.SchemaVersion != EnvelopeVersion {
		return file, fmt.Errorf("hooks.toml: schema_version %d is not supported; this manager reads %d", doc.SchemaVersion, EnvelopeVersion)
	}
	file.SchemaVersion = doc.SchemaVersion
	hooks, err := decodeHooks(&meta, doc.Hooks)
	if err != nil {
		return file, err
	}
	if unknown := meta.Undecoded(); len(unknown) > 0 {
		return file, fmt.Errorf("hooks.toml: unknown key %q", unknown[0].String())
	}
	file.Hooks = hooks
	return file, nil
}

func hooksPath(dir string) string {
	return filepath.Join(dir, HooksFile)
}

func decodeHooks(meta *toml.MetaData, raw map[string]toml.Primitive) ([]Hook, error) {
	for _, name := range slices.Sorted(maps.Keys(raw)) {
		if !slices.Contains(EventNames(), name) {
			return nil, fmt.Errorf("hooks.toml: unknown event %q", name)
		}
	}
	var hooks []Hook
	for _, name := range EventNames() {
		prim, ok := raw[name]
		if !ok {
			continue
		}
		decoded, err := decodeEvent(meta, name, prim)
		if err != nil {
			return nil, err
		}
		hooks = append(hooks, decoded...)
	}
	return hooks, nil
}

func decodeEvent(meta *toml.MetaData, event string, prim toml.Primitive) ([]Hook, error) {
	var many []rawHook
	if err := meta.PrimitiveDecode(prim, &many); err != nil {
		var one rawHook
		if err := meta.PrimitiveDecode(prim, &one); err != nil {
			return nil, fmt.Errorf("hooks.toml: hooks.%s: %w", event, err)
		}
		many = []rawHook{one}
	}
	hooks := make([]Hook, 0, len(many))
	for _, raw := range many {
		hook, err := raw.toHook(meta, event)
		if err != nil {
			return nil, err
		}
		hooks = append(hooks, hook)
	}
	return hooks, nil
}

func (raw rawHook) toHook(meta *toml.MetaData, event string) (Hook, error) {
	command, err := decodeCommand(meta, event, raw.Command)
	if err != nil {
		return Hook{}, err
	}
	if event != EventSessionState && (len(raw.Events) > 0 || raw.Notify) {
		return Hook{}, fmt.Errorf("hooks.toml: hooks.%s: events and notify are only valid on session_state", event)
	}
	for _, name := range raw.Events {
		if !slices.Contains(StatusNames(), name) {
			return Hook{}, fmt.Errorf("hooks.toml: hooks.%s: unknown status %q", event, name)
		}
	}
	timeout := DefaultTimeout
	if raw.Timeout != "" {
		parsed, err := time.ParseDuration(raw.Timeout)
		if err != nil {
			return Hook{}, fmt.Errorf("hooks.toml: hooks.%s: timeout: %w", event, err)
		}
		if parsed < MinTimeout || parsed > MaxTimeout {
			return Hook{}, fmt.Errorf("hooks.toml: hooks.%s: timeout %s is outside %s to %s", event, parsed, MinTimeout, MaxTimeout)
		}
		timeout = parsed
	}
	return Hook{Event: event, Command: command, Events: raw.Events, Notify: raw.Notify, Timeout: timeout}, nil
}

func decodeCommand(meta *toml.MetaData, event string, prim toml.Primitive) ([]string, error) {
	var argv []string
	if err := meta.PrimitiveDecode(prim, &argv); err != nil {
		var text string
		if err := meta.PrimitiveDecode(prim, &text); err == nil {
			return nil, fmt.Errorf("hooks.toml: hooks.%s.command must be an array of strings, not a shell line; use command = [\"sh\", \"-c\", %q] when the command needs a shell", event, text)
		}
		return nil, fmt.Errorf("hooks.toml: hooks.%s.command: %w", event, err)
	}
	if len(argv) == 0 || argv[0] == "" {
		return nil, fmt.Errorf("hooks.toml: hooks.%s.command is empty", event)
	}
	return argv, nil
}

func match(hook Hook, ev Event) bool {
	if hook.Event != ev.Name {
		return false
	}
	if len(hook.Events) > 0 && !slices.Contains(hook.Events, ev.Session.Status) {
		return false
	}
	return true
}
