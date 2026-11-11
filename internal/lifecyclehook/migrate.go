package lifecyclehook

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/YoanWai/agent-manager/internal/atomicfile"
	"github.com/YoanWai/agent-manager/internal/status"
	"github.com/YoanWai/agent-manager/internal/store"
)

// pendingPrefix marks a migration whose file write has not finished. It is
// not the command, so a crash before the write still retries, and a crash
// after the write resumes instead of looking done.
const pendingPrefix = "pending\x00"

// writeHooksFile is the atomic write. Tests replace it to fail the write
// before the migrated marker is set.
var writeHooksFile = atomicfile.WriteFile

type migratedFile struct {
	SchemaVersion int           `toml:"schema_version"`
	Hooks         migratedHooks `toml:"hooks"`
}

type migratedHooks struct {
	SessionState []migratedHook `toml:"session_state"`
}

type migratedHook struct {
	Command []string `toml:"command"`
	Events  []string `toml:"events"`
	Notify  bool     `toml:"notify"`
	Timeout string   `toml:"timeout"`
}

// MigrateNotifyCommand carries a stored notify_command into hooks.toml.
// The marker is set only after the file write or the already-exists notice
// succeeds. An empty command, or a command that already matches the marker,
// is left alone, so deleting hooks.toml does not restore the command.
func MigrateNotifyCommand(dir string, st *store.Store) error {
	command, err := st.Setting(store.NotifyCommandKey)
	if err != nil {
		return err
	}
	command = strings.TrimSpace(command)
	if command == "" {
		return nil
	}
	marker, err := st.Setting(store.NotifyCommandMigratedKey)
	if err != nil {
		return err
	}
	if marker == command {
		return nil
	}
	pending := pendingPrefix + command
	info, statErr := os.Stat(hooksPath(dir))
	if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
		return statErr
	}
	exists := statErr == nil
	if exists && info.IsDir() {
		return fmt.Errorf("hooks.toml: %s is a directory", hooksPath(dir))
	}
	if exists && marker == pending {
		_, err := st.SwapSetting(store.NotifyCommandMigratedKey, pending, command)
		return err
	}
	if exists {
		return noticeExisting(st, command, marker)
	}
	if marker != pending {
		won, err := st.SwapSetting(store.NotifyCommandMigratedKey, marker, pending)
		if err != nil || !won {
			return err
		}
	}
	body, argv, err := migratedDocument(command)
	if err != nil {
		_, _ = st.SwapSetting(store.NotifyCommandMigratedKey, pending, "")
		return err
	}
	if err := writeHooksFile(hooksPath(dir), body, 0o600); err != nil {
		_, _ = st.SwapSetting(store.NotifyCommandMigratedKey, pending, "")
		return err
	}
	shown, err := st.Setting(store.NotifyCommandShownKey)
	if err != nil {
		return err
	}
	if shown == command {
		if _, err := st.ClaimHookShown(Fingerprint(EventSessionState, argv)); err != nil {
			return err
		}
	}
	_, err = st.SwapSetting(store.NotifyCommandMigratedKey, pending, command)
	return err
}

func noticeExisting(st *store.Store, command, marker string) error {
	body, _, err := migratedDocument(command)
	if err != nil {
		return err
	}
	block := "# hooks.toml already exists, so this command was not written into it.\n# Paste the block below into it if you still want that command.\n" + string(body)
	if err := st.SetSetting(store.NotifyCommandNotCarriedKey, block); err != nil {
		return err
	}
	_, err = st.SwapSetting(store.NotifyCommandMigratedKey, marker, command)
	return err
}

func migratedDocument(command string) ([]byte, []string, error) {
	argv, err := migratedArgv(command)
	if err != nil {
		return nil, nil, err
	}
	doc := migratedFile{
		SchemaVersion: EnvelopeVersion,
		Hooks: migratedHooks{SessionState: []migratedHook{{
			Command: argv,
			Events:  []string{status.Waiting, status.Errored, status.Finished},
			Notify:  true,
			Timeout: "15s",
		}}},
	}
	encoded, err := toml.Marshal(doc)
	if err != nil {
		return nil, nil, err
	}
	return []byte(commentLines(command) + string(encoded)), argv, nil
}

func commentLines(command string) string {
	var b strings.Builder
	b.WriteString("# Carried over from notify_command by agent-manager.\n")
	b.WriteString("# notify = true runs it only when the banner fires, as before.\n")
	for _, line := range strings.Split(command, "\n") {
		b.WriteString("# ")
		b.WriteString(line)
		b.WriteByte('\n')
	}
	return b.String()
}
