package sessioncmd

import (
	"os"
	"testing"
	"time"

	"github.com/YoanWai/agent-manager/internal/lifecyclehook"
	"github.com/YoanWai/agent-manager/internal/status"
	"github.com/YoanWai/agent-manager/internal/store"
)

func recordingLifecycle(t *testing.T, h *sessionHarness) (*Lifecycle, *lifecyclehook.Recorder) {
	t.Helper()
	rec := &lifecyclehook.Recorder{}
	runtime := harnessRuntime(t, h)
	runtime.Events = rec
	lifecycle, err := NewLifecycle(runtime)
	if err != nil {
		t.Fatal(err)
	}
	return lifecycle, rec
}

func eventNames(events []lifecyclehook.Event) []string {
	names := make([]string, len(events))
	for i, ev := range events {
		names[i] = ev.Name + ":" + ev.Reason
	}
	return names
}

func TestLifecycleHooksCoverSpawnKillForkAndAFailedLaunch(t *testing.T) {
	h := newSessionHarness(t)
	lifecycle, rec := recordingLifecycle(t, h)
	runtime := harnessRuntime(t, h)
	tool := runtime.Config.Tools["echoer"]
	sess := store.Session{ID: "hooked", Name: "hooked", Tool: "echoer", Cwd: t.TempDir(), Status: status.Starting}
	launched, err := lifecycle.Launch(LaunchRequest{
		Session: sess, Tool: tool, BaseCommand: "sleep 30", Pane: PaneSize{Width: 80, Height: 24}, Reason: lifecyclehook.ReasonSpawn,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := eventNames(rec.Events()); len(got) != 1 || got[0] != lifecyclehook.EventSessionStart+":"+lifecyclehook.ReasonSpawn {
		t.Fatalf("spawn events = %v", got)
	}
	stored, err := h.store.Get(sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.SpawnedBy != "" {
		t.Fatalf("a picker launch set spawned_by to %q", stored.SpawnedBy)
	}

	rec.Reset()
	forked := store.Session{ID: "forked", Name: "forked", Tool: "echoer", Cwd: t.TempDir(), Status: status.Starting}
	if _, err := lifecycle.Launch(LaunchRequest{
		Session: forked, Tool: tool, BaseCommand: "sleep 30", Pane: PaneSize{Width: 80, Height: 24}, Reason: lifecyclehook.ReasonFork,
	}); err != nil {
		t.Fatal(err)
	}
	if got := rec.Events(); len(got) != 1 || got[0].Reason != lifecyclehook.ReasonFork {
		t.Fatalf("fork events = %+v", got)
	}
	if row, err := h.store.Get(forked.ID); err != nil || row.SpawnedBy != "" {
		t.Fatalf("fork spawned_by = %q, %v", row.SpawnedBy, err)
	}

	rec.Reset()
	killed, err := lifecycle.Kill(launched.Session)
	if err != nil {
		t.Fatal(err)
	}
	got := rec.Events()
	if len(got) != 2 || got[0].Name != lifecyclehook.EventSessionKill || got[0].Reason != lifecyclehook.ReasonKill || got[1].Name != lifecyclehook.EventSessionState || got[1].Session.Status != status.Dead {
		t.Fatalf("kill events = %+v", got)
	}
	if got[1].PreviousStatus != launched.Session.Status || killed.Status != status.Dead {
		t.Fatalf("previous %q status %q", got[1].PreviousStatus, killed.Status)
	}

	rec.Reset()
	if _, err := lifecycle.Launch(LaunchRequest{
		Session: sess, Tool: tool, BaseCommand: "sleep 30", Pane: PaneSize{Width: 80, Height: 24}, Reason: lifecyclehook.ReasonSpawn,
	}); err == nil {
		t.Fatal("duplicate launch succeeded")
	}
	if extra := rec.Events(); len(extra) != 0 {
		t.Fatalf("a launch that never stored a row emitted %+v", extra)
	}

	rec.Reset()
	shell := store.Session{ID: "shell-hook", Name: "shell", Tool: "terminal", Cwd: t.TempDir(), Status: status.Starting}
	if _, err := lifecycle.Launch(LaunchRequest{
		Session: shell, Tool: runtime.Config.Tools["terminal"], BaseCommand: "sleep 30", Pane: PaneSize{Width: 80, Height: 24}, Reason: lifecyclehook.ReasonSpawn,
	}); err != nil {
		t.Fatal(err)
	}
	if extra := rec.Events(); len(extra) != 0 {
		t.Fatalf("a shell emitted %+v", extra)
	}
}

func TestLifecycleHooksCoverReviveRestartArchiveRestoreDeleteAndAfterTurn(t *testing.T) {
	h := newSessionHarness(t)
	lifecycle, rec := recordingLifecycle(t, h)
	runtime := harnessRuntime(t, h)
	tool := runtime.Config.Tools["echoer"]
	tool.SessionIDFlag = "--session"
	runtime.Config.Tools["echoer"] = tool
	lifecycle.runtime.Config.Tools["echoer"] = tool

	sess := store.Session{ID: "again", Name: "again", Tool: "echoer", Cwd: t.TempDir(), Status: status.Dead, AgentSessionID: "old-conv"}
	if err := h.store.CreateSession(sess); err != nil {
		t.Fatal(err)
	}
	if _, err := lifecycle.Revive(sess, PaneSize{Width: 80, Height: 24}); err != nil {
		t.Fatal(err)
	}
	if got := rec.Events(); len(got) != 1 || got[0].Name != lifecyclehook.EventSessionStart || got[0].Reason != lifecyclehook.ReasonRevive {
		t.Fatalf("revive events = %+v", got)
	}

	rec.Reset()
	restarted, err := lifecycle.Restart(sess, PaneSize{Width: 80, Height: 24})
	if err != nil {
		t.Fatal(err)
	}
	got := rec.Events()
	if len(got) != 3 || got[0].Reason != lifecyclehook.ReasonRestart || got[0].Name != lifecyclehook.EventSessionKill || got[1].Name != lifecyclehook.EventSessionState || got[2].Name != lifecyclehook.EventSessionStart || got[2].Reason != lifecyclehook.ReasonRestart {
		t.Fatalf("restart events = %v", eventNames(got))
	}
	if !got[2].UseConversation || got[2].Conversation == "" || got[2].Conversation == "old-conv" || got[2].Conversation != restarted.Conversation {
		t.Fatalf("restart conversation = %q, result %q", got[2].Conversation, restarted.Conversation)
	}

	rec.Reset()
	if err := h.store.UpdateStatus(sess.ID, status.Idle); err != nil {
		t.Fatal(err)
	}
	if _, err := lifecycle.ArchiveForHuman(ArchiveSelection{Sessions: []store.Session{sess}, Reason: lifecyclehook.ReasonUser}); err != nil {
		t.Fatal(err)
	}
	got = rec.Events()
	if len(got) != 3 || got[0].Name != lifecyclehook.EventSessionKill || got[0].Reason != lifecyclehook.ReasonArchive || got[2].Name != lifecyclehook.EventSessionArchive || got[2].Reason != lifecyclehook.ReasonUser {
		t.Fatalf("archive events = %v", eventNames(got))
	}

	rec.Reset()
	if _, err := lifecycle.RestoreForHuman(ArchiveSelection{Sessions: []store.Session{sess}}, PaneSize{Width: 80, Height: 24}); err != nil {
		t.Fatal(err)
	}
	got = rec.Events()
	if len(got) < 2 || got[0].Name != lifecyclehook.EventSessionStart || got[0].Reason != lifecyclehook.ReasonRestore || got[len(got)-1].Name != lifecyclehook.EventSessionRestore || got[len(got)-1].Reason != lifecyclehook.ReasonUser {
		t.Fatalf("restore events = %v", eventNames(got))
	}

	rec.Reset()
	if err := h.store.RequestAfterTurn(sess.ID, store.AfterTurnArchive, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := h.store.UpdateStatus(sess.ID, status.Idle); err != nil {
		t.Fatal(err)
	}
	if _, err := lifecycle.EndAfterTurn(sess.ID); err != nil {
		t.Fatal(err)
	}
	got = rec.Events()
	if len(got) == 0 || got[0].Reason != lifecyclehook.ReasonAfterTurn || got[len(got)-1].Name != lifecyclehook.EventSessionArchive || got[len(got)-1].Reason != lifecyclehook.ReasonAfterTurn {
		t.Fatalf("after_turn events = %v", eventNames(got))
	}

	rec.Reset()
	if _, err := lifecycle.SetArchivedForSession(h.caller.ID, sess.ID, false, MCPVocabulary()); err != nil {
		t.Fatal(err)
	}
	if got := rec.Events(); len(got) != 1 || got[0].Name != lifecyclehook.EventSessionRestore || got[0].Reason != lifecyclehook.ReasonAgent {
		t.Fatalf("agent restore = %v", eventNames(got))
	}

	rec.Reset()
	if _, err := lifecycle.DeleteForHuman(DeleteSelection{Sessions: []store.Session{sess}}); err != nil {
		t.Fatal(err)
	}
	got = rec.Events()
	if len(got) != 1 || got[0].Name != lifecyclehook.EventSessionKill || got[0].Reason != lifecyclehook.ReasonDelete {
		t.Fatalf("delete events = %v", eventNames(got))
	}
	for _, ev := range got {
		if ev.Name == lifecyclehook.EventSessionState {
			t.Fatal("delete also emitted session_state")
		}
	}
}

func TestLifecycleHooksArchiveAGroup(t *testing.T) {
	h := newSessionHarness(t)
	if err := h.store.CreateGroup("hooks", t.TempDir()); err != nil {
		t.Fatal(err)
	}
	lifecycle, rec := recordingLifecycle(t, h)
	var selected []store.Session
	for _, id := range []string{"g1", "g2"} {
		sess := store.Session{ID: id, Name: id, Tool: "echoer", Cwd: t.TempDir(), Status: status.Dead, Group: "hooks"}
		if err := h.store.CreateSession(sess); err != nil {
			t.Fatal(err)
		}
		selected = append(selected, sess)
	}
	if _, err := lifecycle.ArchiveForHuman(ArchiveSelection{Sessions: selected, GroupPath: "hooks", Reason: lifecyclehook.ReasonUser}); err != nil {
		t.Fatal(err)
	}
	var archived int
	for _, ev := range rec.Events() {
		if ev.Name != lifecyclehook.EventSessionArchive || ev.Reason != lifecyclehook.ReasonUser {
			t.Fatalf("group archive emitted %+v", ev)
		}
		archived++
	}
	if archived != 2 {
		t.Fatalf("archived %d sessions, want 2", archived)
	}
}

func TestLifecycleHooksRollBackAnAnnouncedGroupRestore(t *testing.T) {
	h := newSessionHarness(t)
	lifecycle, rec := recordingLifecycle(t, h)
	gone := t.TempDir()
	first := store.Session{ID: "keep", Name: "keep", Tool: "echoer", Cwd: t.TempDir(), Status: status.Dead, Archived: true, Group: "backend"}
	second := store.Session{ID: "gone", Name: "gone", Tool: "echoer", Cwd: gone, Status: status.Dead, Archived: true, Group: "backend"}
	for _, sess := range []store.Session{first, second} {
		if err := h.store.CreateSession(sess); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.RemoveAll(gone); err != nil {
		t.Fatal(err)
	}
	if _, err := lifecycle.RestoreForHuman(ArchiveSelection{Sessions: []store.Session{first, second}, GroupPath: "backend"}, PaneSize{Width: 80, Height: 24}); err == nil {
		t.Fatal("restore of a missing directory succeeded")
	}
	var rollback, start int
	for _, ev := range rec.Events() {
		switch {
		case ev.Name == lifecyclehook.EventSessionStart && ev.Session.ID == first.ID && ev.Reason == lifecyclehook.ReasonRestore:
			start++
		case ev.Name == lifecyclehook.EventSessionKill && ev.Session.ID == first.ID && ev.Reason == lifecyclehook.ReasonRollback:
			rollback++
		case ev.Session.ID == second.ID:
			t.Fatalf("the unannounced session emitted %+v", ev)
		}
	}
	if start != 1 || rollback != 1 {
		t.Fatalf("start %d rollback %d in %+v", start, rollback, eventNames(rec.Events()))
	}
}

func TestSessionCreateRecordsTheCallerAndAnEmptyCallerRecordsNone(t *testing.T) {
	h := newSessionHarness(t)
	rec := &lifecyclehook.Recorder{}
	h.sessions.events = rec
	created, err := h.sessions.Create(h.caller.ID, CreateSessionOptions{Name: "child", Directory: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	stored, err := h.store.Get(created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.SpawnedBy != h.caller.ID {
		t.Fatalf("spawned_by = %q, want %s", stored.SpawnedBy, h.caller.ID)
	}
	if got := rec.Events(); len(got) != 1 || got[0].Reason != lifecyclehook.ReasonSpawn || got[0].Session.SpawnedBy != h.caller.ID {
		t.Fatalf("create events = %+v", got)
	}

	rec.Reset()
	scripted, err := h.sessions.Create("", CreateSessionOptions{Name: "scripted", Directory: t.TempDir(), Tool: "echoer"})
	if err != nil {
		t.Fatal(err)
	}
	stored, err = h.store.Get(scripted.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.SpawnedBy != "" {
		t.Fatalf("a script spawn set spawned_by to %q", stored.SpawnedBy)
	}
}

func TestTaskAndReservationHooksFollowTheWrite(t *testing.T) {
	h := newSessionHarness(t)
	rec := &lifecyclehook.Recorder{}
	h.sessions.events = rec
	created, err := h.sessions.CreateTask(h.caller.ID, "ship it", "the body stays out of the event", nil)
	if err != nil {
		t.Fatal(err)
	}
	if extra := rec.Events(); len(extra) != 0 {
		t.Fatalf("creating a task emitted %+v", extra)
	}
	claimed, err := h.sessions.ClaimTask(h.caller.ID, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	got := rec.Events()
	if len(got) != 1 || got[0].Name != lifecyclehook.EventTaskClaim || got[0].Task == nil || got[0].Task.ID != created.ID || got[0].Task.Title != "ship it" || got[0].Task.Owner != h.caller.ID {
		t.Fatalf("claim = %+v", got)
	}
	rec.Reset()
	if _, err := h.sessions.ClaimTask(h.caller.ID, created.ID); err == nil {
		t.Fatal("a second claim succeeded")
	}
	if extra := rec.Events(); len(extra) != 0 {
		t.Fatalf("a refused claim emitted %+v", extra)
	}
	rec.Reset()
	if _, err := h.sessions.ReleaseTask(h.caller.ID, created.ID); err != nil {
		t.Fatal(err)
	}
	if extra := rec.Events(); len(extra) != 0 {
		t.Fatalf("releasing a task emitted %+v", extra)
	}
	if _, err := h.sessions.ClaimTask(h.caller.ID, created.ID); err != nil {
		t.Fatal(err)
	}
	rec.Reset()
	if _, err := h.sessions.FinishTask(h.caller.ID, claimed.ID); err != nil {
		t.Fatal(err)
	}
	got = rec.Events()
	if len(got) != 1 || got[0].Name != lifecyclehook.EventTaskFinish || got[0].Task == nil || got[0].Task.Title != "ship it" {
		t.Fatalf("finish = %+v", got)
	}

	rec.Reset()
	if _, err := h.sessions.Reserve(h.caller.ID, []string{"internal/ui/*.go"}, "exclusive", "editing", 0); err != nil {
		t.Fatal(err)
	}
	if _, err := h.sessions.Reserve(h.caller.ID, []string{"internal/ui/*.go"}, "exclusive", "editing", 0); err != nil {
		t.Fatal(err)
	}
	got = rec.Events()
	if len(got) != 2 || got[0].Reservation == nil || got[1].Reservation == nil || got[0].Reservation.Action != lifecyclehook.ReservationReserve || got[1].Reservation.ExpiresAt.Before(got[0].Reservation.ExpiresAt) {
		t.Fatalf("renew = %+v", got)
	}
	rec.Reset()
	if _, err := h.sessions.ReleaseFiles(h.caller.ID, []string{"internal/ui/*.go"}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.sessions.ReleaseFiles(h.caller.ID, nil); err != nil {
		t.Fatal(err)
	}
	got = rec.Events()
	if len(got) != 2 || got[0].Reservation.Action != lifecyclehook.ReservationRelease || got[0].Reservation.Pattern != "internal/ui/*.go" || got[1].Reservation.Pattern != "" || !got[1].Reservation.Released {
		t.Fatalf("release = %+v %+v", got[0].Reservation, got[1].Reservation)
	}
}

func TestTerminalCreateAndCloseEmitNothing(t *testing.T) {
	h := newSessionHarness(t)
	rec := &lifecyclehook.Recorder{}
	h.terminals.events = rec
	created, err := h.terminals.Create(h.caller.ID, CreateTerminalOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := h.terminals.Close(h.caller.ID, created.ID); err != nil {
		t.Fatal(err)
	}
	if extra := rec.Events(); len(extra) != 0 {
		t.Fatalf("a terminal emitted %+v", extra)
	}
}
