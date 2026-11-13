package lifecyclehook

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/YoanWai/agent-manager/internal/store"
)

func TestLineKeepsThePromptOutAndSplitsWorktreePaths(t *testing.T) {
	sentinel := "PROMPT-SENTINEL-do-not-leak"
	ev := Event{
		Name:   EventSessionStart,
		At:     time.Date(2026, 11, 11, 8, 0, 0, 0, time.FixedZone("IST", 2*60*60)),
		Reason: ReasonSpawn,
		Model:  "opus",
		Effort: "high",
		Session: store.Session{
			ID: "s1", Name: "deploy", Tool: "claude", Group: "api", Status: "starting",
			Cwd: "/src/api-worktrees/am-x", WorktreeRepo: "/src/api", WorktreeBranch: "am/fix",
			SpawnedBy: "", AgentSessionID: "conv-1", LaunchPrompt: sentinel, LastPrompt: sentinel,
		},
	}
	raw := string(Line(ev, "v0.1.0"))
	if strings.Contains(raw, sentinel) || strings.Count(raw, "\n") != 1 {
		t.Fatalf("line = %s", raw)
	}
	var payload envelope
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Session.Directory != "/src/api" || payload.Session.Worktree != "/src/api-worktrees/am-x" || payload.Session.SpawnedBy != "" || payload.Session.Conversation != "conv-1" {
		t.Fatalf("session = %+v", payload.Session)
	}
	if payload.At != "2026-11-11T06:00:00Z" || payload.Model != "opus" || payload.Reason != ReasonSpawn {
		t.Fatalf("payload = %+v", payload)
	}
	env := Env(ev)
	joined := strings.Join(env, "\n")
	if !strings.Contains(joined, "AM_CWD=/src/api-worktrees/am-x") || strings.Contains(joined, sentinel) {
		t.Fatalf("env = %q", env)
	}
}

func TestLineUsesTheReplacementConversation(t *testing.T) {
	ev := Event{
		Name: EventSessionStart, UseConversation: true, Conversation: "conv-new",
		Session: store.Session{ID: "s1", AgentSessionID: "conv-old"},
	}
	if !strings.Contains(string(Line(ev, "")), `"conversation":"conv-new"`) {
		t.Fatalf("line = %s", Line(ev, ""))
	}
}

func TestLineOmitsTaskBodyAndReservationNote(t *testing.T) {
	ev := Event{
		Name:        EventTaskClaim,
		Session:     store.Session{ID: "s1"},
		Task:        &TaskInfo{ID: "t1", Title: "write it", Owner: "s1"},
		Reservation: &ReservationInfo{Action: ReservationRelease, Pattern: "", Released: true},
	}
	raw := string(Line(ev, "dev"))
	if strings.Contains(raw, "body") || strings.Contains(raw, "note") {
		t.Fatalf("line = %s", raw)
	}
	if !strings.Contains(raw, `"pattern":""`) || !strings.Contains(raw, `"released":true`) {
		t.Fatalf("line = %s", raw)
	}
}

func TestFingerprintSeparatesArguments(t *testing.T) {
	if Fingerprint("session_start", []string{"ab", "c"}) == Fingerprint("session_start", []string{"a", "bc"}) {
		t.Fatal("different argv lists hashed the same")
	}
}
