package execution

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"strings"

	"github.com/YoanWai/agent-manager/internal/agentsession"
	"github.com/YoanWai/agent-manager/internal/launch"
	"github.com/YoanWai/agent-manager/internal/lifecyclehook"
	"github.com/YoanWai/agent-manager/internal/notify"
	"github.com/YoanWai/agent-manager/internal/status"
	"github.com/YoanWai/agent-manager/internal/store"
)

// waitForCalls blocks until n notifications have landed. Delivery runs off
// the refresh path, so assertions have to give it a moment.
func waitForCalls(t *testing.T, rec *notifyRecorder, n int) []notify.Event {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		calls := rec.all()
		if len(calls) >= n {
			return calls
		}
		if time.Now().After(deadline) {
			t.Fatalf("want %d notifications, got %v", n, calls)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func newNotifyTestPoller(t *testing.T) (*Runner, store.Session, *notifyRecorder) {
	t.Helper()
	p, sess := newTestPollerWithSession(t)
	rec := &notifyRecorder{}
	p.notifyFn = rec.fn()
	return p, sess, rec
}

func envValue(env []string, name string) string {
	prefix := name + "="
	for _, item := range env {
		if strings.HasPrefix(item, prefix) {
			return strings.TrimPrefix(item, prefix)
		}
	}
	return ""
}

// A status change always emits. The banner text rides along only when the
// banner itself would fire.
func TestEmitSessionStateSetsNotifyEnvOnlyForABanner(t *testing.T) {
	p, sess, _ := newNotifyTestPoller(t)
	rec := &lifecyclehook.Recorder{}
	p.events = rec
	sess.WorktreeBranch = "am/fix-login"
	p.emitSessionState(sess, status.Waiting, status.Idle, "", "Do you want to proceed?")
	got := rec.Events()
	if len(got) != 1 || got[0].Name != lifecyclehook.EventSessionState || got[0].PreviousStatus != status.Idle || got[0].Session.Status != status.Waiting {
		t.Fatalf("event = %+v", got)
	}
	if envValue(got[0].NotifyEnv, "AM_NOTIFY_KIND") != "waiting" || !strings.Contains(envValue(got[0].NotifyEnv, "AM_BODY"), "Do you want to proceed?") {
		t.Fatalf("notify env = %q", got[0].NotifyEnv)
	}
	if envValue(got[0].NotifyEnv, "AM_CWD") != "" {
		t.Fatalf("notify env should carry only the banner variables, got %q", got[0].NotifyEnv)
	}

	p.emitSessionState(sess, status.Finished, status.Working, "write a haiku", "")
	if later := rec.Events(); len(later) != 2 || later[1].NotifyEnv != nil {
		t.Fatalf("finished without the opt-in should emit with no banner text, got %+v", later)
	}
	if err := p.store.SetSetting(notifyFinishedSetting, "on"); err != nil {
		t.Fatal(err)
	}
	p.emitSessionState(sess, status.Finished, status.Working, "write a haiku", "")
	if later := rec.Events(); envValue(later[2].NotifyEnv, "AM_NOTIFY_KIND") != "finished" {
		t.Fatalf("opted-in finished env = %q", later[2].NotifyEnv)
	}
	if err := p.store.SetSetting(notificationsSetting, "off"); err != nil {
		t.Fatal(err)
	}
	p.emitSessionState(sess, status.Waiting, status.Idle, "", "still there")
	p.emitSessionState(sess, status.Working, status.Waiting, "", "")
	later := rec.Events()
	if len(later) != 5 || later[3].NotifyEnv != nil || later[4].NotifyEnv != nil {
		t.Fatalf("a silenced banner and a working change carry no banner text, got %+v", later[3:])
	}
}

func TestEmitSessionStateSkipsAShell(t *testing.T) {
	p, sess, _ := newNotifyTestPoller(t)
	rec := &lifecyclehook.Recorder{}
	p.events = rec
	p.shellTools = map[string]bool{sess.Tool: true}
	p.emitSessionState(sess, status.Waiting, status.Idle, "", "hello")
	if got := rec.Events(); len(got) != 0 {
		t.Fatalf("a shell emits nothing, got %+v", got)
	}
}

func TestNotifyTransitionFiresOnWaitingAndErrored(t *testing.T) {
	p, sess, rec := newNotifyTestPoller(t)
	p.notifyTransition(sess, status.Waiting, "", "")
	p.notifyTransition(sess, status.Errored, "", "")
	calls := waitForCalls(t, rec, 2)
	want := map[notify.Event]bool{
		{ID: sess.ID, Session: sess.Name, Tool: sess.Tool, Kind: notify.Waiting, Dir: sess.Cwd}: true,
		{ID: sess.ID, Session: sess.Name, Tool: sess.Tool, Kind: notify.Errored, Dir: sess.Cwd}: true,
	}
	for _, call := range calls {
		delete(want, call)
	}
	if len(want) != 0 {
		t.Fatalf("missing notifications %v, got %v", want, calls)
	}
}

func TestNotifyTransitionCarriesCustomToolName(t *testing.T) {
	p, sess, rec := newNotifyTestPoller(t)
	sess.Tool = "my-custom-agent"
	p.notifyTransition(sess, status.Waiting, "", "")
	calls := waitForCalls(t, rec, 1)
	if calls[0] != (notify.Event{ID: sess.ID, Session: sess.Name, Tool: "my-custom-agent", Kind: notify.Waiting, Dir: sess.Cwd}) {
		t.Fatalf("configured tool identity should reach the backend, got %v", calls)
	}
}

func TestNotifyTransitionSkipsUnattentionStatuses(t *testing.T) {
	p, sess, rec := newNotifyTestPoller(t)
	for _, st := range []string{status.Working, status.Idle, status.Dead, status.Starting} {
		p.notifyTransition(sess, st, "", "")
	}
	settle()
	if calls := rec.all(); len(calls) != 0 {
		t.Fatalf("no notification should fire for working/idle/dead/starting, got %v", calls)
	}
}

// Finished is routine for most turn ends, so it only pings when the user
// opted in.
func TestNotifyTransitionFinishedOptIn(t *testing.T) {
	p, sess, rec := newNotifyTestPoller(t)
	p.notifyTransition(sess, status.Finished, "", "")
	settle()
	if calls := rec.all(); len(calls) != 0 {
		t.Fatalf("finished should stay quiet by default, got %v", calls)
	}
	if err := p.store.SetSetting(notifyFinishedSetting, "on"); err != nil {
		t.Fatal(err)
	}
	p.notifyTransition(sess, status.Finished, "", "")
	calls := waitForCalls(t, rec, 1)
	if calls[0] != (notify.Event{ID: sess.ID, Session: sess.Name, Tool: sess.Tool, Kind: notify.Finished, Dir: sess.Cwd}) {
		t.Fatalf("want one finished notification after opt-in, got %v", calls)
	}
}

func TestNotifyTransitionSilencedBySetting(t *testing.T) {
	p, sess, rec := newNotifyTestPoller(t)
	if err := p.store.SetSetting(notificationsSetting, "off"); err != nil {
		t.Fatal(err)
	}
	p.notifyTransition(sess, status.Waiting, "", "")
	p.notifyTransition(sess, status.Errored, "", "")
	settle()
	if calls := rec.all(); len(calls) != 0 {
		t.Fatalf("notifications off should silence everything, got %v", calls)
	}
}

// The full path a waiting transition travels in the running app: hooks
// file → refreshOnce → store update → notification. Fires once; a status
// that stays waiting across polls never re-fires.
func TestRefreshNotifiesWaitingTransitionOnce(t *testing.T) {
	m := buildModel(t)
	hooked := m.cfg.Tools["claude-hooked"]
	// Windows' stand-in already ignores the --settings the launch appends.
	if runtime.GOOS != "windows" {
		hooked.Command = `sh -c 'exec cat' --`
	}
	m.cfg.Tools["claude-hooked"] = hooked
	if err := m.spawnSession("claude-hooked", "needy", t.TempDir(), "", "boot-marker", false, false); err != nil {
		t.Fatal(err)
	}
	m.applyCmd(t, nil)
	sess := m.sessionRows()[0]
	waitForPane(t, m, sess.ID, "boot-marker")

	// Pin the pre-state: whatever earlier refreshes derived, the
	// transition into waiting is observed by the next refresh and only
	// there.
	if err := m.store.UpdateStatus(sess.ID, status.Idle); err != nil {
		t.Fatal(err)
	}

	rec := &notifyRecorder{}
	m.poller.notifyFn = rec.fn()

	statusFile := m.poller.hooks.StatusFile(sess.ID)
	if err := os.MkdirAll(filepath.Dir(statusFile), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(statusFile, []byte(status.Waiting), 0o644); err != nil {
		t.Fatal(err)
	}

	m.applyCmd(t, m.refreshCmd())
	calls := waitForCalls(t, rec, 1)
	if calls[0] != (notify.Event{ID: sess.ID, Session: "needy", Tool: "claude-hooked", Kind: notify.Waiting, Dir: sess.Cwd, Text: "boot-marker"}) {
		t.Fatalf("want one waiting notification titled with the session name and quoting the pane, got %v", calls)
	}

	m.applyCmd(t, m.refreshCmd())
	settle()
	if calls := rec.all(); len(calls) != 1 {
		t.Fatalf("a steady waiting status should not re-fire, got %v", calls)
	}
}

// Every manager on the server derives the same transition from the same
// pane. The event is behind the same check on whether this manager's
// write moved the stored status, so a second manager polling the same
// pane emits nothing. The race where both read the old status is the
// store's to settle, and its tests cover it.
func TestSessionStateEmitsOnceAcrossManagers(t *testing.T) {
	m := buildModel(t)
	hooked := m.cfg.Tools["claude-hooked"]
	hooked.Command = `sh -c 'exec cat' --`
	m.cfg.Tools["claude-hooked"] = hooked
	createSessionOn(t, m, "needy", "claude-hooked", t.TempDir())
	sess := m.sessionRows()[0]
	waitForPane(t, m, sess.ID, "boot-marker")
	if err := m.store.UpdateStatus(sess.ID, status.Idle); err != nil {
		t.Fatal(err)
	}
	second := New(Dependencies{Store: m.store, TMux: m.tmux, Engine: m.poller.engine, Hooks: m.poller.hooks, Git: m.poller.gitDrv}, OptionsFromConfig(m.cfg))
	rec := &lifecyclehook.Recorder{}
	for _, p := range []*Runner{m.poller, second} {
		p.notifyFn = func(notify.Event) {}
		p.events = rec
	}
	statusFile := m.poller.hooks.StatusFile(sess.ID)
	if err := os.MkdirAll(filepath.Dir(statusFile), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(statusFile, []byte(status.Waiting), 0o644); err != nil {
		t.Fatal(err)
	}
	m.poller.Step()
	second.Step()
	got := rec.Events()
	if len(got) != 1 || got[0].Name != lifecyclehook.EventSessionState || got[0].Session.Status != status.Waiting || envValue(got[0].NotifyEnv, "AM_NOTIFY_KIND") != "waiting" {
		t.Fatalf("two managers emitted %+v", got)
	}
}

// The banner quotes what the row would: a waiting session's question, a
// finished session's prompt. The prompt falls back the way the row does,
// to the last one the manager sent and then to the launch prompt.
func TestNotifyTransitionCarriesTheRowsLine(t *testing.T) {
	p, sess, rec := newNotifyTestPoller(t)
	if err := p.store.SetSetting(notifyFinishedSetting, "on"); err != nil {
		t.Fatal(err)
	}
	p.notifyTransition(sess, status.Waiting, "", "Do you want to proceed?")
	p.notifyTransition(sess, status.Finished, "write a haiku", "Rivers run.")
	sess.LastPrompt = "sent through the manager"
	p.notifyTransition(sess, status.Finished, "", "Rivers run.")
	sess.LastPrompt = ""
	sess.LaunchPrompt = launch.RenameDirective + "\n\nthe launch prompt"
	p.notifyTransition(sess, status.Finished, "", "Rivers run.")
	p.notifyTransition(sess, status.Errored, "write a haiku", "boom")
	calls := waitForCalls(t, rec, 5)
	want := map[notify.Event]bool{
		{ID: sess.ID, Session: sess.Name, Tool: sess.Tool, Dir: sess.Cwd, Kind: notify.Waiting, Text: "Do you want to proceed?"}:   true,
		{ID: sess.ID, Session: sess.Name, Tool: sess.Tool, Dir: sess.Cwd, Kind: notify.Finished, Text: "write a haiku"}:            true,
		{ID: sess.ID, Session: sess.Name, Tool: sess.Tool, Dir: sess.Cwd, Kind: notify.Finished, Text: "sent through the manager"}: true,
		{ID: sess.ID, Session: sess.Name, Tool: sess.Tool, Dir: sess.Cwd, Kind: notify.Finished, Text: "the launch prompt"}:        true,
		{ID: sess.ID, Session: sess.Name, Tool: sess.Tool, Dir: sess.Cwd, Kind: notify.Errored}:                                    true,
	}
	for _, call := range calls {
		delete(want, call)
	}
	if len(want) != 0 {
		t.Fatalf("missing notifications %v, got %v", want, calls)
	}
}

// followConversation can move a row onto another conversation in the same
// pass that notices the turn ended. The banner quotes that conversation,
// not the one the pass listed.
func TestRefreshQuotesTheConversationFollowedThisPass(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	m := buildModel(t)
	m.poller.reportStyles["claude-hooked"] = "claude"
	if err := m.store.SetSetting(notifyFinishedSetting, "on"); err != nil {
		t.Fatal(err)
	}
	hooked := m.cfg.Tools["claude-hooked"]
	if runtime.GOOS != "windows" {
		hooked.Command = `sh -c 'exec cat' --`
	}
	m.cfg.Tools["claude-hooked"] = hooked
	if err := m.spawnSession("claude-hooked", "needy", t.TempDir(), "", "boot-marker", false, false); err != nil {
		t.Fatal(err)
	}
	m.applyCmd(t, nil)
	sess := m.sessionRows()[0]
	waitForPane(t, m, sess.ID, "boot-marker")
	if err := m.store.UpdateStatus(sess.ID, status.Idle); err != nil {
		t.Fatal(err)
	}
	if err := m.store.SetAgentSessionID(sess.ID, "old-conv"); err != nil {
		t.Fatal(err)
	}
	write := func(id, prompt string) {
		t.Helper()
		path, err := agentsession.ClaudeTranscriptPath(sess.Cwd, id)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		line := `{"type":"user","message":{"content":[{"type":"text","text":"` + prompt + `"}]}}` + "\n"
		if err := os.WriteFile(path, []byte(line), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("old-conv", "previous conversation")
	write("new-conv", "current conversation")
	prev := followDuringPoll
	t.Cleanup(func() { followDuringPoll = prev })
	followDuringPoll = func(p *Runner, row *store.Session, agentAlive, paneGone bool) error {
		if row.ID == sess.ID {
			row.AgentSessionID = "new-conv"
		}
		return nil
	}
	statusFile := m.poller.hooks.StatusFile(sess.ID)
	if err := os.MkdirAll(filepath.Dir(statusFile), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(statusFile, []byte(status.Finished), 0o644); err != nil {
		t.Fatal(err)
	}
	rec := &notifyRecorder{}
	m.poller.notifyFn = rec.fn()
	m.applyCmd(t, m.refreshCmd())
	calls := waitForCalls(t, rec, 1)
	if calls[0].Text != "current conversation" {
		t.Fatalf("notification quoted %q, want the conversation followed this pass", calls[0].Text)
	}
}

// A worktree session's banner names the repository it was cut from. The
// working directory on the event stays the worktree, which is what a
// notify command receives as AM_CWD.
func TestNotifyTransitionNamesTheRepoOfAWorktreeSession(t *testing.T) {
	p, sess, rec := newNotifyTestPoller(t)
	sess.Cwd = "/src/api-worktrees/deploy"
	sess.WorktreeRepo = "/src/api"
	sess.WorktreeBranch = "am/deploy"
	p.notifyTransition(sess, status.Waiting, "", "")
	calls := waitForCalls(t, rec, 1)
	want := notify.Event{
		ID: sess.ID, Session: sess.Name, Tool: sess.Tool, Kind: notify.Waiting,
		Dir: sess.Cwd, Repo: "/src/api", Branch: "am/deploy",
	}
	if calls[0] != want {
		t.Fatalf("want the repository named beside the working directory, got %v", calls)
	}
}
