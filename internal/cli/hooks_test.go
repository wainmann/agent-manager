package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/YoanWai/agent-manager/internal/lifecyclehook"
	"github.com/YoanWai/agent-manager/internal/store"
)

func TestHooksJSONReportsTheFileAndAMissingFile(t *testing.T) {
	dir := t.TempDir()
	var out bytes.Buffer
	if err := runHooks(&out, []string{"--json"}, dir); err != nil {
		t.Fatal(err)
	}
	var missing hooksReport
	if err := json.Unmarshal(out.Bytes(), &missing); err != nil {
		t.Fatal(err)
	}
	if missing.Found || missing.Error != "" || !strings.HasSuffix(missing.Path, "hooks.toml") || len(missing.Events) == 0 {
		t.Fatalf("missing file = %+v", missing)
	}

	body := "schema_version = 1\n\n[[hooks.session_state]]\ncommand = [\"sh\", \"-c\", \"true\"]\ntimeout = \"15s\"\nnotify = true\n"
	if err := os.WriteFile(filepath.Join(dir, "hooks.toml"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(filepath.Join(dir, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	fingerprint := lifecyclehook.Fingerprint("session_state", []string{"sh", "-c", "true"})
	if _, err := st.ClaimHookShown(fingerprint); err != nil {
		t.Fatal(err)
	}
	if err := st.AddHookFailure(store.HookFailure{At: time.Date(2026, 11, 11, 12, 2, 11, 0, time.UTC), Event: "session_state", Command: "sh", Reason: "exit status 22"}); err != nil {
		t.Fatal(err)
	}
	st.Close()

	out.Reset()
	if err := runHooks(&out, []string{"--json"}, dir); err != nil {
		t.Fatal(err)
	}
	var report hooksReport
	if err := json.Unmarshal(out.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if !report.Found || report.SchemaVersion != 1 || len(report.Hooks) != 1 || !report.Hooks[0].Shown || !report.Hooks[0].Notify || report.Hooks[0].Timeout != "15s" {
		t.Fatalf("report = %+v", report)
	}
	if len(report.Failures) != 1 || report.Failures[0].Command != "sh" || report.Failures[0].Reason != "exit status 22" {
		t.Fatalf("failures = %+v", report.Failures)
	}

	out.Reset()
	if err := runHooks(&out, nil, dir); err != nil {
		t.Fatal(err)
	}
	failedAt := time.Date(2026, 11, 11, 12, 2, 11, 0, time.UTC).Local().Format(time.RFC3339)
	for _, want := range []string{"found", "true", "session_state", "shown", "exit status 22", failedAt} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("text lacks %q:\n%s", want, out.String())
		}
	}

	if err := os.WriteFile(filepath.Join(dir, "hooks.toml"), []byte("schema_version = 9\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	err = runHooks(&out, []string{"--json"}, dir)
	if err == nil {
		t.Fatal("a refused file exited 0")
	}
	var refused hooksReport
	if jsonErr := json.Unmarshal(out.Bytes(), &refused); jsonErr != nil || refused.Error == "" || !strings.Contains(refused.Error, "schema_version") {
		t.Fatalf("refused = %+v, %v", refused, jsonErr)
	}
}
