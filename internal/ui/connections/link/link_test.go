package link

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/YoanWai/agent-manager/internal/remote"
	"github.com/YoanWai/agent-manager/internal/remote/remotetest"
	"github.com/YoanWai/agent-manager/internal/sessioncmd"
	"github.com/YoanWai/agent-manager/internal/store"
	uiconnections "github.com/YoanWai/agent-manager/internal/ui/connections"
	uirail "github.com/YoanWai/agent-manager/internal/ui/rail"
	tea "github.com/charmbracelet/bubbletea"
)

// ssh stands in for ssh: it answers as the remote CLI would and records the
// words each call carried.
type ssh struct {
	mu       sync.Mutex
	calls    [][]string
	snapshot sessioncmd.Snapshot
	screen   string
	err      error
	// stuck names the sessions whose read never answers: it ends only when
	// its context does, as an ssh that is killed. holding hears each one
	// start, which is when it has the host's one call.
	stuck   []string
	holding chan string
}

func (f *ssh) run(ctx context.Context, argv []string) ([]byte, []byte, error) {
	all, err := remotetest.Words(argv)
	if err != nil {
		return nil, nil, err
	}
	words := all[1:]
	f.mu.Lock()
	f.calls = append(f.calls, words)
	snapshot, screen, fail := f.snapshot, f.screen, f.err
	f.mu.Unlock()
	if fail != nil {
		return nil, nil, fail
	}
	if words[0] == "read" && slices.Contains(f.stuck, words[len(words)-1]) {
		f.holding <- words[len(words)-1]
		<-ctx.Done()
		return nil, nil, ctx.Err()
	}
	var reply any
	switch {
	case words[0] == "snapshot":
		reply = snapshot
	case words[0] == "read":
		reply = sessioncmd.SessionScreen{Session: sessioncmd.Session{ID: words[len(words)-1]}, Output: screen}
	case words[0] == "terminal" && words[1] == "read":
		reply = sessioncmd.TerminalScreen{Terminal: sessioncmd.Terminal{ID: words[len(words)-1]}, Output: screen}
	case words[0] == "terminal":
		reply = sessioncmd.Terminal{ID: "t9", Name: "shell"}
	default:
		reply = sessioncmd.Session{ID: "s9", Name: "fresh"}
	}
	out, err := json.Marshal(reply)
	return remotetest.Answer(out), nil, err
}

func (f *ssh) taken() [][]string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.calls)
}

func newClient(t *testing.T, fake *ssh) *remote.Client {
	t.Helper()
	return remote.New(t.TempDir(), remote.WithRunner(fake.run))
}

func listedState(t *testing.T, fake *ssh) State {
	t.Helper()
	return New(newClient(t, fake), []store.Connection{{Name: "box", Destination: "me@box"}})
}

// recorder is the root as a routed result reaches it.
type recorder struct {
	rebuilt  int
	synced   int
	rail     []RailChange
	answered []bool
	closed   []Asker
	renamed  []string
	cleared  int
	errs     []string
	done     []string
}

func (r *recorder) RebuildRows() { r.rebuilt++ }

func (r *recorder) SyncPreview() tea.Cmd { r.synced++; return nil }

func (r *recorder) RailConnection(change RailChange) { r.rail = append(r.rail, change) }

func (r *recorder) AnswerSave(gen uint64, failed bool) { r.answered = append(r.answered, failed) }

func (r *recorder) CloseAsker(gen int, asker Asker) { r.closed = append(r.closed, asker) }

func (r *recorder) CloseRename(gen int, name string) { r.renamed = append(r.renamed, name) }

func (r *recorder) ClearAccepted(composerGen int, draft string, closeAfterSend bool) { r.cleared++ }

func (r *recorder) ReportErr(text string) { r.errs = append(r.errs, text) }

func (r *recorder) ReportDone(text string) { r.done = append(r.done, text) }

func TestPollHostRunsOffTheUpdatePathAndLandsTheRefresh(t *testing.T) {
	fake := &ssh{snapshot: sessioncmd.Snapshot{
		Version: sessioncmd.SnapshotVersion, ManagerAwake: true,
		Sessions: []sessioncmd.Session{{ID: "s1", Name: "api", Tool: "claude", Group: "web", Status: "idle"}},
	}}
	state := listedState(t, fake)
	cmd := state.PollHost("box")
	if cmd == nil || len(fake.taken()) != 0 {
		t.Fatal("the refresh ran on the update path")
	}
	if again := state.PollHost("box"); again != nil {
		t.Fatal("a second refresh started while the first was in flight")
	}
	root := &recorder{}
	if _, ok := state.Route(root, cmd()); !ok {
		t.Fatal("a refresh result went unrouted")
	}
	if root.rebuilt != 1 || root.synced != 1 {
		t.Fatalf("rebuilt %d, synced %d, want one each", root.rebuilt, root.synced)
	}
	if hosts := state.RailHosts(); len(hosts) != 1 || hosts[0].State != uirail.HostOnline || len(hosts[0].Sessions) != 1 {
		t.Fatalf("rail hosts = %+v", hosts)
	}
}

func TestARefreshOfAReplacedListIsDropped(t *testing.T) {
	fake := &ssh{}
	state := listedState(t, fake)
	stale := state.PollHost("box")
	state.SetList([]store.Connection{{Name: "box", Destination: "me@other"}})
	root := &recorder{}
	state.Route(root, stale())
	if _, seen := state.Host("box"); seen {
		t.Fatal("a refresh of the old destination landed on the new one")
	}
	if root.rebuilt != 0 {
		t.Fatal("the rail was rebuilt for a refresh that did not land")
	}
}

// A host that failed says why agent-manager there is unusable rather than
// reading as merely down.
func TestRailHostsNameTheTrouble(t *testing.T) {
	state := listedState(t, &ssh{})
	state.Refreshed("box", state.Gen(), Facts(remote.HostState{Failures: 1, Err: context.DeadlineExceeded}))
	host := state.RailHosts()[0]
	if host.State != uirail.HostOffline || host.Trouble != "unreachable" {
		t.Fatalf("host = %+v", host)
	}
	if host.Err == "" {
		t.Fatal("the host's error was dropped")
	}
}

func TestPreviewReadsTheTrackedRowAndDropsAStaleRead(t *testing.T) {
	fake := &ssh{screen: "building api"}
	state := listedState(t, fake)
	state.Refreshed("box", state.Gen(), Facts(remote.HostState{OK: true, Snapshot: sessioncmd.Snapshot{
		Sessions: []sessioncmd.Session{{ID: "s1", Name: "api", Status: "idle"}},
	}}))
	row, ok := state.Row(uirail.Selection{Kind: uirail.SessionRow, Host: "box", SessionID: "s1"})
	if !ok {
		t.Fatal("the session has no row")
	}
	root := &recorder{}
	cmd := state.Follow(row, true)
	if cmd == nil {
		t.Fatal("the selected session got no read")
	}
	if state.Follow(row, true) != nil {
		t.Fatal("a second read started for the same row")
	}
	state.Route(root, cmd())
	if lines := state.Preview.Screen(40, 4, screenView()); !slices.Equal(lines, []string{"building api"}) {
		t.Fatalf("screen = %q", lines)
	}
	stale, ok := state.StartRead()
	if !ok {
		t.Fatal("the tracked row refused a read")
	}
	state.Preview.Follow(row, false)
	state.Route(root, PreviewMsg{Read: stale, Text: "old screen"})
	if lines := state.Preview.Screen(40, 4, screenView()); slices.Contains(lines, "old screen") {
		t.Fatalf("a read for the row before the selection moved landed: %q", lines)
	}
}

func TestAReadForARowTheSelectionLeftStopsHoldingTheHost(t *testing.T) {
	fake := &ssh{screen: "building api", stuck: []string{"s1", "s2", "s3"}, holding: make(chan string, 3)}
	state := listedState(t, fake)
	var sessions []sessioncmd.Session
	for _, id := range []string{"s1", "s2", "s3", "s4"} {
		sessions = append(sessions, sessioncmd.Session{ID: id, Name: id, Status: "idle"})
	}
	state.Refreshed("box", state.Gen(), Facts(remote.HostState{OK: true, Snapshot: sessioncmd.Snapshot{Sessions: sessions}}))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	// The selection crosses three rows whose reads never answer and rests on
	// the fourth, as a cursor on its way down a slow host's list. The first
	// read has the host before the selection moves on, so the spawn below
	// can only go out once that read gives the host back.
	landed := make(chan tea.Msg, len(sessions))
	for i, session := range sessions {
		row, ok := state.Row(uirail.Selection{Kind: uirail.SessionRow, Host: "box", SessionID: session.ID})
		if !ok {
			t.Fatalf("%s has no row", session.ID)
		}
		cmd := state.Follow(row, true)
		if cmd == nil {
			t.Fatalf("%s got no read", session.ID)
		}
		go func() { landed <- cmd() }()
		if i > 0 {
			continue
		}
		select {
		case <-fake.holding:
		case <-ctx.Done():
			t.Fatal("the first read never reached the host")
		}
	}
	if _, err := Run(ctx, state.Client, nil, Call{Op: Spawn, Host: "box"}); err != nil {
		t.Fatalf("a spawn behind the reads of rows the selection left = %v", err)
	}
	root := &recorder{}
	for range sessions {
		select {
		case msg := <-landed:
			state.Route(root, msg)
		case <-ctx.Done():
			t.Fatal("a read for a row the selection left is still out")
		}
	}
	if len(root.errs) != 0 {
		t.Fatalf("a dropped read reported %q", root.errs)
	}
	if lines := state.Preview.Screen(40, 4, screenView()); !slices.Equal(lines, []string{"building api"}) {
		t.Fatalf("screen = %q, want the row the selection rests on", lines)
	}
	if refusal, offline := state.Offline("box"); offline {
		t.Fatalf("a dropped read took the host offline: %s", refusal)
	}
}

func TestAttachBuildsTheCommandOffTheUpdatePath(t *testing.T) {
	state := listedState(t, &ssh{})
	row := uiconnections.Row{Host: "box", Ref: remote.Ref{Host: "box", ID: "s1"}}
	msg, ok := state.Attach(row)().(AttachReadyMsg)
	if !ok || msg.Err != nil || msg.Host != "box" || msg.Cmd == nil {
		t.Fatalf("attach = %+v", msg)
	}
	root := &recorder{}
	if cmd, routed := state.Route(root, msg); !routed || cmd == nil {
		t.Fatal("the attach was not handed to the terminal")
	}
	if len(root.errs) != 0 {
		t.Fatalf("errors %q", root.errs)
	}
}

func TestAttachEndingNamesWhy(t *testing.T) {
	reports, request := errors.New("turn reports on"), errors.New("ask for the scheme")
	review := "review opens only for sessions on this machine, not yet for one on box"
	editor := "the editor opens only for sessions on this machine, not yet for one on box"
	failures := "turn reports on\nask for the scheme"
	// Exec joins the terminal's failures to the child's status in this shape.
	withFailures := func(status exitStatus) error { return errors.Join(errors.Join(status, reports), request) }
	for _, tc := range []struct {
		name string
		err  error
		want []string
	}{
		{"detached", nil, nil},
		{"review key", exitStatus(120), []string{review}},
		{"editor key", exitStatus(121), []string{editor}},
		{"review key and terminal failures", withFailures(120), []string{review + "; " + failures}},
		{"editor key and terminal failures", withFailures(121), []string{editor + "; " + failures}},
		{"attach failed", withFailures(1), []string{"box: attach ended: exit status\n" + failures}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			state := listedState(t, &ssh{})
			root := &recorder{}
			state.Route(root, AttachDoneMsg{Host: "box", Err: tc.err})
			if !slices.Equal(root.errs, tc.want) {
				t.Fatalf("errors = %q, want %q", root.errs, tc.want)
			}
		})
	}
}

// screenView is the preview's painter with the tab expansion the root
// supplies.
func screenView() uiconnections.ScreenView {
	return uiconnections.ScreenView{ExpandTabs: func(line string, width int) string { return line }}
}

type exitStatus int

func (e exitStatus) Error() string { return "exit status" }
func (e exitStatus) ExitCode() int { return int(e) }
