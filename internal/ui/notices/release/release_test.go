package release

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/YoanWai/agent-manager/internal/feed"
	"github.com/YoanWai/agent-manager/internal/ui/notices"
	"github.com/YoanWai/agent-manager/internal/update"
)

func TestDelegatedResult(t *testing.T) {
	manager := update.Manager{Name: "Homebrew", Command: []string{"brew", "upgrade", "--cask", "yoanwai/tap/agent-manager"}}
	execPath := "/x/agent-manager-delegated-test"

	origUpgraded := Upgraded
	t.Cleanup(func() { Upgraded = origUpgraded })
	Upgraded = func(update.Manager, string, string, string) (string, bool, error) { return execPath, true, nil }

	msg := delegatedResult(manager, execPath, "0.47.1", "v0.48.0", nil)
	if msg.Err != nil {
		t.Fatalf("success reported %v", msg.Err)
	}
	if msg.Path != execPath {
		t.Fatalf("restart path = %q, want %q", msg.Path, execPath)
	}

	failed := delegatedResult(manager, execPath, "0.47.1", "v0.48.0", errors.New("exit status 1"))
	if failed.Err == nil || !strings.Contains(failed.Err.Error(), "brew upgrade --cask yoanwai/tap/agent-manager") {
		t.Fatalf("failure should name the command, got %v", failed.Err)
	}
	if failed.Path != "" {
		t.Fatal("a failed upgrade must not restart")
	}

	Upgraded = func(update.Manager, string, string, string) (string, bool, error) { return execPath, false, nil }
	unchanged := delegatedResult(manager, execPath, "0.47.1", "v0.48.0", nil)
	if unchanged.Path != "" {
		t.Fatal("an upgrade that changed nothing must not restart")
	}
	if unchanged.Err == nil || !strings.Contains(unchanged.Err.Error(), "Homebrew finished, still on 0.47.1") {
		t.Fatalf("an upgrade that changed nothing should say so, got %v", unchanged.Err)
	}
}

func TestUseCachedKeepsNotesWithoutDetectingAvailability(t *testing.T) {
	configDir := t.TempDir()
	catalog := `{"checked_at":"2026-11-07T00:00:00Z","parser":2,"releases":[{"version":"v0.48.0","url":"https://github.com/x/releases/tag/v0.48.0"}]}`
	if err := os.WriteFile(filepath.Join(configDir, "release-catalog.json"), []byte(catalog), 0o644); err != nil {
		t.Fatal(err)
	}
	origDetect := DetectManager
	t.Cleanup(func() { DetectManager = origDetect })
	DetectManager = func(string) update.Manager {
		t.Fatal("install detection must wait for the asynchronous check")
		return update.Manager{}
	}
	s := State{Version: "0.47.1"}
	s.UseCached(configDir)
	if s.Latest != "" || s.URL != "" || len(s.Releases) != 1 {
		t.Fatalf("cached notes must not advertise availability, got %+v", s)
	}
}

func TestSourcesMapsFeedKindsActionBannerAndExpiry(t *testing.T) {
	expires := time.Date(2026, time.November, 20, 0, 0, 0, 0, time.UTC)
	state := State{Feed: []feed.Message{
		{ID: "a", Kind: feed.KindHeadsUp, Banner: "Soon", Action: "Read it", Pinned: true, ExpiresAt: expires},
		{ID: "b", Kind: feed.KindTip},
		{ID: "c", Kind: feed.KindNews},
		{ID: "d", Kind: "mystery"},
	}}
	got := state.Sources().Feed
	wantKinds := []notices.Kind{notices.KindHeadsUp, notices.KindTip, notices.KindNews, notices.KindNews}
	for i, want := range wantKinds {
		if got[i].Kind != want {
			t.Fatalf("feed[%d].Kind = %v, want %v", i, got[i].Kind, want)
		}
	}
	if got[0].Banner != "Soon" || got[0].Action != "Read it" || !got[0].Pinned || !got[0].Expires.Equal(expires) {
		t.Fatalf("feed[0] = %+v", got[0])
	}
}

func TestApplyDefersInstallDetection(t *testing.T) {
	orig := DetectManager
	t.Cleanup(func() { DetectManager = orig })
	called := false
	DetectManager = func(string) update.Manager {
		called = true
		return update.Manager{Name: "Nix", Advice: "use Nix"}
	}
	s := State{Version: "0.47.1"}
	cmd := s.Apply()
	if called {
		t.Fatal("install detection ran on the event loop")
	}
	msg := cmd().(AppliedMsg)
	if !called || msg.Err == nil || msg.Err.Error() != "use Nix" {
		t.Fatalf("deferred detection got called %v, message %+v", called, msg)
	}
}

func TestDelegatedResultChecksTheOfferedVersion(t *testing.T) {
	orig := Upgraded
	t.Cleanup(func() { Upgraded = orig })
	Upgraded = func(_ update.Manager, _, current, expected string) (string, bool, error) {
		if current != "0.47.0" || expected != "v0.48.0" {
			t.Fatalf("checking current %q, expected %q", current, expected)
		}
		return "", false, errors.New("installed version 0.47.1 did not reach the offered version v0.48.0")
	}
	msg := delegatedResult(update.Manager{Name: "Homebrew"}, "/x", "0.47.0", "v0.48.0", nil)
	if msg.Path != "" || msg.Err == nil || !strings.Contains(msg.Err.Error(), "did not reach") {
		t.Fatalf("partial upgrade got %+v", msg)
	}
}

func TestDelegatedResultWithoutANoticeChecksAvailability(t *testing.T) {
	origUpgraded, origRefresh := Upgraded, RefreshForApply
	t.Cleanup(func() { Upgraded, RefreshForApply = origUpgraded, origRefresh })
	t.Setenv("AGENT_MANAGER_CONFIG_DIR", t.TempDir())
	Upgraded = func(update.Manager, string, string, string) (string, bool, error) { return "/x", false, nil }
	for _, tc := range []struct {
		name     string
		result   update.Result
		err      error
		upToDate bool
	}{
		{name: "current", upToDate: true},
		{name: "newer available", result: update.Result{Latest: "v0.48.0"}},
		{name: "cannot confirm", err: errors.New("offline")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			RefreshForApply = func(context.Context, string, string) (update.Result, error) { return tc.result, tc.err }
			msg := delegatedResult(update.Manager{}, "/x", "0.47.1", "", nil)
			if msg.UpToDate != tc.upToDate || (msg.Err == nil) != tc.upToDate || msg.Path != "" {
				t.Fatalf("got %+v", msg)
			}
		})
	}
}
