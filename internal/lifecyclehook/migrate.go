package lifecyclehook

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/YoanWai/agent-manager/internal/atomicfile"
	"github.com/YoanWai/agent-manager/internal/store"
)

// pendingPrefix marks a migration whose file write has not finished. It is
// not the command, so a crash before the write still retries, and a crash
// after the write resumes instead of looking done.
const pendingPrefix = "pending\x00"

// writeHooksFile is the atomic create. Tests replace it to fail the write
// before the migrated marker is set.
var writeHooksFile = atomicfile.CreateFile

type migratedFile struct {
	SchemaVersion int           `toml:"schema_version"`
	Hooks         migratedHooks `toml:"hooks"`
}

type migratedHooks struct {
	Notification []migratedHook `toml:"notification"`
}

type migratedHook struct {
	Command []string `toml:"command"`
	Timeout string   `toml:"timeout"`
}

// CarryNotifyCommand runs MigrateNotifyCommand and stores its error for the
// Messages panel, or clears it once a run succeeds. A store that cannot hold
// the error gets it in hooks.log.
func CarryNotifyCommand(dir string, st *store.Store) {
	reason := ""
	if err := MigrateNotifyCommand(dir, st); err != nil {
		reason = err.Error()
	}
	if _, err := st.SetSettingIfChanged(store.NotifyCommandFailedKey, reason); err != nil && reason != "" {
		line := fmt.Sprintf("%s notify_command carry-over: %s\n", time.Now().UTC().Format(time.RFC3339), reason)
		_ = appendLog(filepath.Join(dir, LogFile), line)
	}
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
		return finishPending(dir, st, command)
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
	if err := writeHooksFile(hooksPath(dir), body, 0o600); errors.Is(err, fs.ErrExist) {
		// Another start can write the same file first. finishPending keeps
		// any other file and raises the paste notice for it.
		return finishPending(dir, st, command)
	} else if err != nil {
		_, _ = st.SwapSetting(store.NotifyCommandMigratedKey, pending, "")
		return err
	}
	if err := keepShown(st, command, argv); err != nil {
		return err
	}
	_, err = st.SwapSetting(store.NotifyCommandMigratedKey, pending, command)
	return err
}

// keepShown marks the carried hook as already shown when the user saw the
// old notify_command's first-run banner, so it is not announced again.
func keepShown(st *store.Store, command string, argv []string) error {
	shown, err := st.Setting(store.NotifyCommandShownKey)
	if err != nil || shown != command {
		return err
	}
	_, err = st.ClaimHookShown(Fingerprint(EventNotification, argv))
	return err
}

// finishPending completes a migration a crash interrupted after its marker,
// when the file there is the one it writes. Any other file is the user's.
func finishPending(dir string, st *store.Store, command string) error {
	pending := pendingPrefix + command
	body, argv, err := migratedDocument(command)
	if err != nil {
		return err
	}
	existing, err := os.ReadFile(hooksPath(dir))
	if err != nil {
		return err
	}
	if !bytes.Equal(existing, body) {
		return noticeExisting(st, command, pending)
	}
	if err := keepShown(st, command, argv); err != nil {
		return err
	}
	_, err = st.SwapSetting(store.NotifyCommandMigratedKey, pending, command)
	return err
}

func noticeExisting(st *store.Store, command, marker string) error {
	argv, err := migratedArgv(command)
	if err != nil {
		return err
	}
	hook, err := toml.Marshal(migratedHook{Command: argv, Timeout: "15s"})
	if err != nil {
		return err
	}
	block := "# hooks.toml already exists, so this command was not written into it.\n# Paste the block below at the end of it if you still want that command.\n" +
		commentLines(command) + "[[hooks.notification]]\n" + string(hook)
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
		SchemaVersion: SchemaVersion,
		Hooks: migratedHooks{Notification: []migratedHook{{
			Command: argv,
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
	b.WriteString("# hooks.notification runs it on every banner trigger, now also with banners off.\n")
	for _, line := range strings.Split(command, "\n") {
		b.WriteString("# ")
		b.WriteString(line)
		b.WriteByte('\n')
	}
	return b.String()
}
