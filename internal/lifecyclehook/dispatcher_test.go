package lifecyclehook

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/YoanWai/agent-manager/internal/status"
	"github.com/YoanWai/agent-manager/internal/store"
	"github.com/YoanWai/agent-manager/internal/testenv"
)

func TestDispatcherRunsFourSessionsAndHoldsTheFifth(t *testing.T) {
	dir := t.TempDir()
	release := filepath.Join(dir, "release")
	started := filepath.Join(dir, "started")
	hook := holdScript(t, started, release)
	writeHooks(t, dir, EventSessionState, []string{hook}, "")
	d, _ := openDispatcher(t, dir)
	for i := range 5 {
		d.Emit(sessionEvent("s"+strconv.Itoa(i), status.Working))
	}
	waitUntil(t, func() bool { return countLines(started) >= 4 })
	if strings.Contains(readFile(t, started), "s4") {
		t.Fatalf("the fifth session started before a worker was free:\n%s", readFile(t, started))
	}
	if err := os.WriteFile(release, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, func() bool { return countLines(started) >= 5 })
}

func TestDispatcherDropsWhenTheQueueIsFull(t *testing.T) {
	dir := t.TempDir()
	release := filepath.Join(dir, "release")
	started := filepath.Join(dir, "started")
	hook := holdScript(t, started, release)
	writeHooks(t, dir, EventSessionState, []string{hook}, "")
	d, st := openDispatcher(t, dir)
	for i := range Workers {
		d.Emit(sessionEvent("b"+strconv.Itoa(i), status.Working))
	}
	waitUntil(t, func() bool { return countLines(started) >= Workers })
	for i := range QueueCap {
		d.Emit(sessionEvent(fmt.Sprintf("q%d", i), status.Working))
	}
	before := time.Now()
	d.Emit(sessionEvent("dropped", status.Working))
	if time.Since(before) > time.Second {
		t.Fatalf("a full queue blocked Emit for %s", time.Since(before))
	}
	waitUntil(t, func() bool {
		failures, err := st.HookFailures()
		return err == nil && len(failures) > 0
	})
	failures, err := st.HookFailures()
	if err != nil {
		t.Fatal(err)
	}
	if len(failures) != 1 || failures[0].Event != EventSessionState || failures[0].Command != "" || !strings.Contains(failures[0].Reason, "queue full, dropped session_state") {
		t.Fatalf("failures = %+v", failures)
	}
	if err := os.WriteFile(release, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, func() bool { return countLines(started) >= Workers+QueueCap })
	if strings.Contains(readFile(t, started), "dropped") {
		t.Fatal("the dropped event ran")
	}
}

func TestDispatcherKeepsOneSessionInOrder(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "log")
	held := filepath.Join(dir, "held")
	release := filepath.Join(dir, "release")
	hook := script(t, `
printf '%s\n' "start $AM_SESSION_ID" >> `+quote(logPath)+`
if [ "$AM_SESSION_ID" = "A" ]; then
  count=$(grep -c '^start A$' `+quote(logPath)+` || true)
  if [ "$count" -eq 1 ]; then
    printf 'held\n' > `+quote(held)+`
    while [ ! -f `+quote(release)+` ]; do sleep 0.02; done
  fi
fi
printf '%s\n' "end $AM_SESSION_ID" >> `+quote(logPath)+`
`)
	writeHooks(t, dir, EventSessionState, []string{hook}, "")
	d, _ := openDispatcher(t, dir)
	d.Emit(sessionEvent("A", status.Working))
	waitUntil(t, func() bool { return fileExists(held) })
	d.Emit(sessionEvent("A", status.Working))
	d.Emit(sessionEvent("B", status.Working))
	waitUntil(t, func() bool { return strings.Contains(readFile(t, logPath), "start B") })
	if strings.Count(readFile(t, logPath), "start A") != 1 {
		t.Fatalf("the second event for A started early:\n%s", readFile(t, logPath))
	}
	if err := os.WriteFile(release, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, func() bool { return strings.Count(readFile(t, logPath), "end A") >= 2 })
	lines := strings.Split(strings.TrimSpace(readFile(t, logPath)), "\n")
	firstEnd, secondStart, seenStart := -1, -1, 0
	for i, line := range lines {
		if line == "start A" {
			seenStart++
			if seenStart == 2 {
				secondStart = i
			}
		}
		if line == "end A" && firstEnd < 0 {
			firstEnd = i
		}
	}
	if seenStart != 2 || secondStart < firstEnd {
		t.Fatalf("log order:\n%s", strings.Join(lines, "\n"))
	}
}

func TestDispatcherCloseStopsALongHookAndDropsTheQueue(t *testing.T) {
	dir := t.TempDir()
	ready := filepath.Join(dir, "ready")
	done := filepath.Join(dir, "done")
	started := filepath.Join(dir, "started")
	hook := script(t, `
printf '%s\n' "$AM_SESSION_ID" >> `+quote(started)+`
printf 'ready\n' > `+quote(ready)+`
sleep 30
printf 'done\n' > `+quote(done)+`
`)
	writeHooks(t, dir, EventSessionState, []string{hook}, "timeout = \"30s\"\n")
	d, _ := openDispatcher(t, dir)
	d.Emit(sessionEvent("long", status.Working))
	waitUntil(t, func() bool { return fileExists(ready) })
	for range 3 {
		d.Emit(sessionEvent("long", status.Working))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	start := time.Now()
	d.Close(ctx)
	cancel()
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("close took %s", elapsed)
	}
	if fileExists(done) {
		t.Fatal("the long hook finished after close")
	}
	if countLines(started) != 1 {
		t.Fatalf("a queued hook started after close:\n%s", readFile(t, started))
	}
}

func TestDispatcherCloseWaitsForAShortHook(t *testing.T) {
	dir := t.TempDir()
	done := filepath.Join(dir, "done")
	hook := script(t, "sleep 0.2\nprintf 'done\\n' > "+quote(done)+"\n")
	writeHooks(t, dir, EventSessionState, []string{hook}, "")
	d, _ := openDispatcher(t, dir)
	d.Emit(sessionEvent("s", status.Finished))
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	d.Close(ctx)
	cancel()
	if !fileExists(done) {
		t.Fatal("close returned before the hook finished")
	}
}

func TestDispatcherFailureIsOnceAndPrivate(t *testing.T) {
	dir := t.TempDir()
	count := filepath.Join(dir, "count")
	hook := script(t, "printf 'x\\n' >> "+quote(count)+"\necho secret-token >&2\nexit 7\n")
	writeHooks(t, dir, EventSessionState, []string{hook}, "")
	d, st := openDispatcher(t, dir)
	d.Emit(sessionEvent("s", status.Errored))
	d.Emit(sessionEvent("s", status.Errored))
	closeDispatcher(d, 3*time.Second)
	if countLines(count) != 2 {
		t.Fatalf("the hook ran %d times, want one run per event", countLines(count))
	}
	failures, err := st.HookFailures()
	if err != nil {
		t.Fatal(err)
	}
	if len(failures) != 1 || failures[0].Command != hook || failures[0].Reason != "exit status 7" || strings.Contains(failures[0].Reason, "secret-token") {
		t.Fatalf("failures = %+v", failures)
	}
	log, err := os.ReadFile(filepath.Join(dir, LogFile))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(log), "secret-token") || !strings.Contains(string(log), "exit status 7") {
		t.Fatalf("log = %q", log)
	}
	info, err := os.Stat(filepath.Join(dir, LogFile))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("log mode = %o", info.Mode().Perm())
	}
}

func TestDispatcherGatesNotifyAndStatus(t *testing.T) {
	dir := t.TempDir()
	gate := filepath.Join(dir, "gate")
	notified := filepath.Join(dir, "notified")
	changes := filepath.Join(dir, "changes")
	barrier := filepath.Join(dir, "barrier")
	gateHook := script(t, "printf 'gate\\n' > "+quote(gate)+"\n")
	notifyHook := script(t, "printf '%s\\n' \"$AM_BODY\" >> "+quote(notified)+"\n")
	changeHook := script(t, "printf '%s\\n' \"$AM_BODY\" >> "+quote(changes)+"\n")
	barrierHook := script(t, "printf 'barrier\\n' > "+quote(barrier)+"\n")
	text := "schema_version = 1\n" +
		"[[hooks.session_state]]\ncommand = [" + strconv.Quote(gateHook) + "]\nevents = [\"waiting\"]\n" +
		"[[hooks.session_state]]\ncommand = [" + strconv.Quote(notifyHook) + "]\nnotify = true\n" +
		"[[hooks.session_state]]\ncommand = [" + strconv.Quote(changeHook) + "]\n" +
		"[hooks.session_kill]\ncommand = [" + strconv.Quote(barrierHook) + "]\n"
	if err := os.WriteFile(filepath.Join(dir, HooksFile), []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	d, _ := openDispatcher(t, dir)
	d.Emit(sessionEvent("s", status.Working))
	d.Emit(sessionEvent("s", status.Waiting))
	ev := sessionEvent("s", status.Waiting)
	ev.NotifyEnv = []string{"AM_NOTIFY_KIND=waiting", "AM_TITLE=Hello", "AM_BODY=Need you"}
	d.Emit(ev)
	d.Emit(Event{Name: EventSessionKill, Session: store.Session{ID: "s", Status: status.Dead}})
	waitUntil(t, func() bool { return readFile(t, barrier) != "" })
	if !fileExists(gate) {
		t.Fatal("the events filter dropped a waiting transition")
	}
	if got := readFile(t, notified); got != "Need you\n" {
		t.Fatalf("a notify hook runs only on a banner trigger, got %q", got)
	}
	if got := readFile(t, changes); got != "\n\n\n" {
		t.Fatalf("a hook without notify runs on every change with no banner text, got %q", got)
	}
}

func TestDispatcherPassesLiteralArgumentsAndStdin(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "out")
	envFile := filepath.Join(dir, "env")
	pwned := filepath.Join(dir, "pwned")
	hook := script(t, "printf '%s\\n' \"$1\" > "+quote(out)+"\ncat > "+quote(out)+".json\nenv | grep '^AM_' | sort > "+quote(envFile+".tmp")+" && mv "+quote(envFile+".tmp")+" "+quote(envFile)+"\n")
	payload := "$(touch " + pwned + ")"
	writeHooks(t, dir, EventSessionStart, []string{hook, payload}, "")
	d, _ := openDispatcher(t, dir)
	d.Emit(Event{
		Name: EventSessionStart, Reason: ReasonSpawn,
		Session: store.Session{ID: "s1", Name: "deploy", Tool: "claude", Cwd: dir, Status: status.Starting, LaunchPrompt: "HIDDEN-PROMPT"},
	})
	waitUntil(t, func() bool { return fileExists(envFile) })
	if readFile(t, out) != payload+"\n" {
		t.Fatalf("argument = %q", readFile(t, out))
	}
	if _, err := os.Stat(pwned); err == nil {
		t.Fatal("the argument was executed")
	}
	raw := readFile(t, out+".json")
	if strings.Count(raw, "\n") != 1 || strings.Contains(raw, "HIDDEN-PROMPT") || !strings.Contains(raw, `"event":"session_start"`) {
		t.Fatalf("stdin = %q", raw)
	}
	env := readFile(t, envFile)
	for _, want := range []string{"AM_EVENT=session_start", "AM_SESSION_ID=s1", "AM_CWD=" + dir, "AM_SESSION_NAME=deploy"} {
		if !strings.Contains(env, want) {
			t.Fatalf("env missing %s:\n%s", want, env)
		}
	}
	if strings.Contains(env, "AM_BODY") || strings.Contains(env, "HIDDEN-PROMPT") {
		t.Fatalf("env leaked agent text:\n%s", env)
	}
}

func TestDispatcherRereadsAChangedFile(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "out")
	first := script(t, "printf 'one\\n' >> "+quote(out)+"\n")
	second := script(t, "printf 'two-and-more\\n' >> "+quote(out)+"\n")
	writeHooks(t, dir, EventSessionState, []string{first}, "")
	d, _ := openDispatcher(t, dir)
	d.Emit(sessionEvent("s", status.Idle))
	waitUntil(t, func() bool { return strings.Contains(readFile(t, out), "one") })
	writeHooks(t, dir, EventSessionState, []string{second}, "# a longer file so the size changes\n")
	d.Emit(sessionEvent("s", status.Idle))
	waitUntil(t, func() bool { return strings.Contains(readFile(t, out), "two-and-more") })
}

func TestDispatcherShowsAHookOnce(t *testing.T) {
	dir := t.TempDir()
	hook := script(t, "exit 0\n")
	writeHooks(t, dir, EventSessionState, []string{hook}, "")
	var mu sync.Mutex
	var shown []string
	st := openStore(t)
	d := New(Options{ConfigDir: dir, Store: st, Version: "test", Show: func(text string) {
		mu.Lock()
		shown = append(shown, text)
		mu.Unlock()
	}})
	t.Cleanup(func() { closeDispatcher(d, 2*time.Second) })
	d.Emit(sessionEvent("s", status.Waiting))
	d.Emit(sessionEvent("s", status.Waiting))
	waitUntil(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(shown) >= 1
	})
	other := script(t, "exit 0\n")
	writeHooks(t, dir, EventSessionState, []string{other}, "# changed\n")
	d.Emit(sessionEvent("s", status.Waiting))
	closeDispatcher(d, 2*time.Second)
	want := []string{EventSessionState + ": " + hook, EventSessionState + ": " + other}
	if !slices.Equal(shown, want) {
		t.Fatalf("shown = %q, want %q", shown, want)
	}
}

func TestTwoDispatchersEachRecordOneFailure(t *testing.T) {
	dir := t.TempDir()
	hook := script(t, "exit 1\n")
	writeHooks(t, dir, EventSessionKill, []string{hook}, "")
	st := openStore(t)
	var dispatchers [2]*Dispatcher
	for i := range dispatchers {
		dispatchers[i] = New(Options{ConfigDir: dir, Store: st, Version: "test"})
	}
	t.Cleanup(func() {
		for _, d := range dispatchers {
			closeDispatcher(d, 2*time.Second)
		}
	})
	dispatchers[0].Emit(Event{Name: EventSessionKill, Session: store.Session{ID: "a"}})
	dispatchers[1].Emit(Event{Name: EventSessionKill, Session: store.Session{ID: "b"}})
	waitUntil(t, func() bool {
		failures, err := st.HookFailures()
		return err == nil && len(failures) >= 2
	})
}

func openDispatcher(t *testing.T, dir string) (*Dispatcher, *store.Store) {
	t.Helper()
	st := openStore(t)
	d := New(Options{ConfigDir: dir, Store: st, Version: "test"})
	t.Cleanup(func() { closeDispatcher(d, 3*time.Second) })
	return d, st
}

func closeDispatcher(d *Dispatcher, limit time.Duration) {
	ctx, cancel := context.WithTimeout(context.Background(), limit)
	defer cancel()
	d.Close(ctx)
}

func openStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func holdScript(t *testing.T, started, release string) string {
	t.Helper()
	return script(t, "printf '%s\\n' \"$AM_SESSION_ID\" >> "+quote(started)+"\nwhile [ ! -f "+quote(release)+" ]; do sleep 0.02; done\n")
}

func script(t *testing.T, body string) string {
	t.Helper()
	testenv.SkipPOSIXShell(t)
	path := filepath.Join(t.TempDir(), "hook.sh")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body), 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}

func writeHooks(t *testing.T, dir, event string, argv []string, extra string) {
	t.Helper()
	var b strings.Builder
	b.WriteString("schema_version = 1\n[hooks.")
	b.WriteString(event)
	b.WriteString("]\ncommand = [")
	for i, arg := range argv {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(strconv.Quote(arg))
	}
	b.WriteString("]\n")
	b.WriteString(extra)
	if err := os.WriteFile(filepath.Join(dir, HooksFile), []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
}

func sessionEvent(id, sessionStatus string) Event {
	return Event{Name: EventSessionState, Session: store.Session{ID: id, Status: sessionStatus}}
}

func quote(path string) string {
	return "'" + strings.ReplaceAll(path, "'", `'\''`) + "'"
}

func waitUntil(t *testing.T, done func() bool) {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		if done() {
			return
		}
		time.Sleep(15 * time.Millisecond)
	}
	t.Fatal("timed out")
}

func countLines(path string) int {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	return strings.Count(string(data), "\n")
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(data)
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
