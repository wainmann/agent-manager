package ui

import (
	"testing"
	"time"

	"github.com/YoanWai/agent-manager/internal/execution"
	"github.com/YoanWai/agent-manager/internal/store"
)

func TestPollMessageCopiesHookNotices(t *testing.T) {
	at := time.Date(2026, 11, 11, 14, 2, 0, 0, time.UTC)
	msg := pollMessage(execution.Result{Snapshot: execution.Snapshot{
		HookFailures:     []store.HookFailure{{At: at, Event: "session_state", Command: "sh", Reason: "exit status 1"}},
		HooksConfigError: "hooks.toml: schema_version is required",
		NotifyNotCarried: "paste me",
	}})
	refresh, ok := msg.(refreshMsg)
	if !ok {
		t.Fatalf("message = %T", msg)
	}
	if refresh.hooksConfigError == "" || refresh.notifyNotCarried != "paste me" || len(refresh.hookFailures) != 1 || refresh.hookFailures[0].Command != "sh" {
		t.Fatalf("refresh = %+v", refresh)
	}
}

func TestRefreshStoresHookNoticesBeforeItDropsAStaleListing(t *testing.T) {
	m := buildModel(t)
	at := time.Date(2026, 11, 11, 14, 2, 0, 0, time.UTC)
	m.routeRefreshMsg(refreshMsg{
		listedAt:         time.Unix(1, 0),
		hookFailures:     []store.HookFailure{{At: at, Event: "session_kill", Command: "stop", Reason: "killed"}},
		hooksConfigError: "hooks.toml: refused",
		notifyNotCarried: "paste me",
	})
	if m.notices.HooksConfigError != "hooks.toml: refused" || m.notices.NotifyNotCarried != "paste me" || len(m.notices.HookFailures) != 1 {
		t.Fatalf("log = %+v", m.notices.Log)
	}
	if m.notices.HooksLog == "" {
		t.Fatal("the log path was not recorded")
	}
	sources := m.noticeSources()
	if sources.HooksConfigError != m.notices.HooksConfigError || len(sources.HookFailures) != 1 || sources.HooksLog != m.notices.HooksLog {
		t.Fatalf("sources = %+v", sources)
	}
}
