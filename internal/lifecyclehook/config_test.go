package lifecyclehook

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLoadMissingFile(t *testing.T) {
	file, err := Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if file.Found {
		t.Fatal("a missing hooks.toml is not an error")
	}
}

func TestLoadRejectsAnEmptyFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, HooksFile), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Load(dir)
	if err == nil || !strings.Contains(err.Error(), "schema_version") {
		t.Fatalf("empty file error = %v", err)
	}
}

func TestLoadRejectsAMissingSchemaVersion(t *testing.T) {
	_, err := loadText(t, "[hooks.session_start]\ncommand = [\"echo\", \"hi\"]\n")
	if err == nil || !strings.Contains(err.Error(), "schema_version") {
		t.Fatalf("error = %v", err)
	}
}

func TestLoadReadsATableAndAnArrayInFileOrder(t *testing.T) {
	file, err := loadText(t, `
schema_version = 1

[hooks.session_kill]
command = ["echo", "gone"]

[[hooks.session_start]]
command = ["echo", "one"]

[[hooks.session_start]]
command = ["echo", "two"]
timeout = "2s"
`)
	if err != nil {
		t.Fatal(err)
	}
	if file.SchemaVersion != 1 || len(file.Hooks) != 3 {
		t.Fatalf("hooks = %+v", file.Hooks)
	}
	if file.Hooks[0].Event != EventSessionStart || file.Hooks[0].Command[1] != "one" || file.Hooks[1].Command[1] != "two" {
		t.Fatalf("start hooks = %+v", file.Hooks[:2])
	}
	if file.Hooks[1].Timeout != 2*time.Second || file.Hooks[2].Event != EventSessionKill {
		t.Fatalf("hooks = %+v", file.Hooks)
	}
}

func TestLoadRejectsAShellString(t *testing.T) {
	_, err := loadText(t, "schema_version = 1\n[hooks.session_start]\ncommand = \"echo hi\"\n")
	if err == nil || !strings.Contains(err.Error(), "array of strings") {
		t.Fatalf("error = %v", err)
	}
}

func TestLoadRejectsUnknownKeysEventsAndTimeouts(t *testing.T) {
	cases := []string{
		"schema_version = 1\nextra = true\n",
		"schema_version = 1\n[hooks.nope]\ncommand = [\"echo\"]\n",
		"schema_version = 1\n[hooks.session_start]\ncommand = [\"echo\"]\nextra = true\n",
		"schema_version = 1\n[hooks.session_start]\ncommand = []\n",
		"schema_version = 1\n[hooks.session_start]\ncommand = [\"echo\"]\ntimeout = \"50ms\"\n",
		"schema_version = 1\n[hooks.session_start]\ncommand = [\"echo\"]\ntimeout = \"1m\"\n",
		"schema_version = 1\n[hooks.session_start]\ncommand = [\"echo\"]\nevents = [\"waiting\"]\n",
		"schema_version = 1\n[hooks.session_start]\ncommand = [\"echo\"]\nnotify = true\n",
		"schema_version = 1\n[hooks.session_state]\ncommand = [\"echo\"]\nevents = [\"asleep\"]\n",
		"schema_version = 2\n",
	}
	for _, text := range cases {
		if _, err := loadText(t, text); err == nil {
			t.Fatalf("accepted %q", text)
		}
	}
}

func TestLoadAcceptsSessionStateFilters(t *testing.T) {
	file, err := loadText(t, `
schema_version = 1
[hooks.session_state]
command = ["echo", "hi"]
events = ["waiting", "errored", "finished"]
notify = true
timeout = "15s"
`)
	if err != nil {
		t.Fatal(err)
	}
	hook := file.Hooks[0]
	if !hook.Notify || hook.Timeout != 15*time.Second || len(hook.Events) != 3 {
		t.Fatalf("hook = %+v", hook)
	}
}

func loadText(t *testing.T, text string) (File, error) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, HooksFile), []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	return Load(dir)
}
