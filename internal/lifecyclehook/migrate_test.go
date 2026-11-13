package lifecyclehook

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/YoanWai/agent-manager/internal/status"
	"github.com/YoanWai/agent-manager/internal/store"
)

func TestMigrateWritesAPrivateFileAndSetsTheMarkerAfter(t *testing.T) {
	dir := t.TempDir()
	st := openStore(t)
	command := "curl -d \"$AM_BODY\" ntfy.sh/topic ']]'"
	if err := st.SetSetting(store.NotifyCommandKey, command); err != nil {
		t.Fatal(err)
	}
	if err := st.SetSetting(store.NotifyCommandShownKey, command); err != nil {
		t.Fatal(err)
	}
	if err := MigrateNotifyCommand(dir, st); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, HooksFile)
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	// Windows reports 0666 for any writable file, so only POSIX can check it.
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %o, want 600", info.Mode().Perm())
	}
	file, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	hook := file.Hooks[0]
	argv, err := migratedArgv(command)
	if err != nil {
		t.Fatal(err)
	}
	if !hook.Notify || hook.Timeout.String() != "15s" || !slices.Equal(hook.Command, argv) {
		t.Fatalf("hook = %+v", hook)
	}
	if joined := strings.Join(hook.Events, ","); joined != status.Waiting+","+status.Errored+","+status.Finished {
		t.Fatalf("events = %q", joined)
	}
	marker, err := st.Setting(store.NotifyCommandMigratedKey)
	if err != nil || marker != command {
		t.Fatalf("marker = %q, %v", marker, err)
	}
	shown, err := st.HooksShown()
	if err != nil || len(shown) != 1 || shown[0] != Fingerprint(EventSessionState, hook.Command) {
		t.Fatalf("shown = %v, %v", shown, err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := MigrateNotifyCommand(dir, st); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("a second start rewrote hooks.toml")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := MigrateNotifyCommand(dir, st); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("deleting hooks.toml restored the command")
	}
}

func TestMigrateLeavesTheMarkerUnsetWhenTheWriteFails(t *testing.T) {
	dir := t.TempDir()
	st := openStore(t)
	if err := st.SetSetting(store.NotifyCommandKey, "echo hi"); err != nil {
		t.Fatal(err)
	}
	previous := writeHooksFile
	writeHooksFile = func(string, []byte, os.FileMode) error { return os.ErrPermission }
	t.Cleanup(func() { writeHooksFile = previous })
	if err := MigrateNotifyCommand(dir, st); err == nil {
		t.Fatal("a failed write reported success")
	}
	marker, err := st.Setting(store.NotifyCommandMigratedKey)
	if err != nil || marker != "" {
		t.Fatalf("marker = %q, %v", marker, err)
	}
	if _, err := os.Stat(filepath.Join(dir, HooksFile)); !os.IsNotExist(err) {
		t.Fatal("hooks.toml exists after a failed write")
	}
	writeHooksFile = previous
	if err := MigrateNotifyCommand(dir, st); err != nil {
		t.Fatal(err)
	}
	marker, err = st.Setting(store.NotifyCommandMigratedKey)
	if err != nil || marker != "echo hi" {
		t.Fatalf("marker after retry = %q, %v", marker, err)
	}
}

func TestMigrateNoticesAnExistingFile(t *testing.T) {
	dir := t.TempDir()
	st := openStore(t)
	path := filepath.Join(dir, HooksFile)
	original := []byte("schema_version = 1\n# keep me\n")
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatal(err)
	}
	command := "echo keep"
	if err := st.SetSetting(store.NotifyCommandKey, command); err != nil {
		t.Fatal(err)
	}
	if err := MigrateNotifyCommand(dir, st); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(original) {
		t.Fatalf("file changed to %q", got)
	}
	notice, err := st.Setting(store.NotifyCommandNotCarriedKey)
	if err != nil || !strings.Contains(notice, command) || !strings.Contains(notice, "already exists") {
		t.Fatalf("notice = %q, %v", notice, err)
	}
	marker, err := st.Setting(store.NotifyCommandMigratedKey)
	if err != nil || marker != command {
		t.Fatalf("marker = %q, %v", marker, err)
	}
}

func TestMigrateResumeAfterAPendingMarker(t *testing.T) {
	dir := t.TempDir()
	st := openStore(t)
	command := "echo resume"
	if err := st.SetSetting(store.NotifyCommandKey, command); err != nil {
		t.Fatal(err)
	}
	if err := st.SetSetting(store.NotifyCommandMigratedKey, pendingPrefix+command); err != nil {
		t.Fatal(err)
	}
	if err := MigrateNotifyCommand(dir, st); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(dir); err != nil {
		t.Fatal(err)
	}
	marker, err := st.Setting(store.NotifyCommandMigratedKey)
	if err != nil || marker != command {
		t.Fatalf("marker = %q, %v", marker, err)
	}
}

func TestMigrateFinishesAPendingWriteWithoutANotice(t *testing.T) {
	dir := t.TempDir()
	st := openStore(t)
	command := "echo written"
	if err := st.SetSetting(store.NotifyCommandKey, command); err != nil {
		t.Fatal(err)
	}
	if err := st.SetSetting(store.NotifyCommandMigratedKey, pendingPrefix+command); err != nil {
		t.Fatal(err)
	}
	body, _, err := migratedDocument(command)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, HooksFile), body, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := MigrateNotifyCommand(dir, st); err != nil {
		t.Fatal(err)
	}
	marker, err := st.Setting(store.NotifyCommandMigratedKey)
	if err != nil || marker != command {
		t.Fatalf("marker = %q, %v", marker, err)
	}
	notice, err := st.Setting(store.NotifyCommandNotCarriedKey)
	if err != nil || notice != "" {
		t.Fatalf("a file this migration wrote raised the not-carried notice: %q, %v", notice, err)
	}
}

func TestTwoStoresMigrateOneFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.db")
	st1, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	st2, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st1.Close(); st2.Close() })
	if err := st1.SetSetting(store.NotifyCommandKey, "echo once"); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for _, st := range []*store.Store{st1, st2} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- MigrateNotifyCommand(dir, st)
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if _, err := Load(dir); err != nil {
		t.Fatal(err)
	}
	marker, err := st1.Setting(store.NotifyCommandMigratedKey)
	if err != nil || marker != "echo once" {
		t.Fatalf("marker = %q, %v", marker, err)
	}
	if notice, err := st1.Setting(store.NotifyCommandNotCarriedKey); err != nil || notice != "" {
		t.Fatalf("racing migrations raised the not-carried notice: %q, %v", notice, err)
	}
}
