// Package lifecyclehook runs the commands in hooks.toml after a lifecycle
// event is stored. It is not internal/hooks, which reads Claude Code status
// files.
package lifecyclehook

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/YoanWai/agent-manager/internal/status"
	"github.com/YoanWai/agent-manager/internal/store"
)

const (
	EventSessionStart   = "session_start"
	EventSessionState   = "session_state"
	EventSessionArchive = "session_archive"
	EventSessionRestore = "session_restore"
	EventSessionKill    = "session_kill"
	EventTaskClaim      = "task_claim"
	EventTaskFinish     = "task_finish"
	EventReviewRound    = "review_round"
	EventReservationSet = "reservation_set"
	ReasonSpawn         = "spawn"
	ReasonFork          = "fork"
	ReasonRevive        = "revive"
	ReasonRestart       = "restart"
	ReasonRestore       = "restore"
	ReasonKill          = "kill"
	ReasonArchive       = "archive"
	ReasonAfterTurn     = "after_turn"
	ReasonDelete        = "delete"
	ReasonRollback      = "rollback"
	ReasonAgent         = "agent"
	ReasonUser          = "user"
	ReservationReserve  = "reserve"
	ReservationRelease  = "release"
	EnvelopeVersion     = 1
)

// EventNames is the event catalog, in the order hooks --json prints it.
func EventNames() []string {
	return []string{
		EventSessionStart,
		EventSessionState,
		EventSessionArchive,
		EventSessionRestore,
		EventSessionKill,
		EventTaskClaim,
		EventTaskFinish,
		EventReviewRound,
		EventReservationSet,
	}
}

// StatusNames are the session statuses an events filter may name.
func StatusNames() []string {
	return []string{
		status.Working,
		status.Waiting,
		status.Finished,
		status.Errored,
		status.Idle,
		status.Dead,
		status.Starting,
	}
}

// TaskInfo is the claim or finish payload. The task body stays off it.
type TaskInfo struct {
	ID    string
	Title string
	Owner string
}

// ReservationInfo is one lease grant or release. The note stays off it.
type ReservationInfo struct {
	Action    string
	Pattern   string
	Mode      string
	ExpiresAt time.Time
	Released  bool
}

// ReviewInfo is one confirmed review send.
type ReviewInfo struct {
	CommentCount int
	Round        int
	Repo         string
}

// Event is one stored lifecycle change. Emit copies what it needs; the
// caller can reuse the session afterward.
type Event struct {
	Name            string
	At              time.Time
	Session         store.Session
	PreviousStatus  string
	Reason          string
	Model           string
	Effort          string
	Conversation    string
	UseConversation bool
	// NotifyEnv is AM_NOTIFY_KIND, AM_TITLE and AM_BODY when the banner
	// fired. A notify hook runs only when it is set.
	NotifyEnv   []string
	Task        *TaskInfo
	Reservation *ReservationInfo
	Review      *ReviewInfo
}

// Emitter accepts one event. Emit must not block on the hook itself.
type Emitter interface {
	Emit(Event)
}

// Nop drops every event. Constructors install it so call sites never check.
type Nop struct{}

func (Nop) Emit(Event) {}

// Or returns events, or Nop when it is nil.
func Or(events Emitter) Emitter {
	if events == nil {
		return Nop{}
	}
	return events
}

// Recorder stores events for tests.
type Recorder struct {
	mu  sync.Mutex
	got []Event
}

func (r *Recorder) Emit(ev Event) {
	r.mu.Lock()
	r.got = append(r.got, ev)
	r.mu.Unlock()
}

func (r *Recorder) Reset() {
	r.mu.Lock()
	r.got = nil
	r.mu.Unlock()
}

func (r *Recorder) Events() []Event {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]Event, len(r.got))
	copy(out, r.got)
	return out
}

// Fingerprint identifies one hook command. A NUL separates the event and
// each argument so two different argv lists cannot share a hash.
func Fingerprint(event string, argv []string) string {
	sum := sha256.New()
	sum.Write([]byte(event))
	sum.Write([]byte{0})
	for _, arg := range argv {
		sum.Write([]byte(arg))
		sum.Write([]byte{0})
	}
	return hex.EncodeToString(sum.Sum(nil))
}

// Env is the AM_* variables set for every hook, on top of the process
// environment. AM_CWD is the session checkout, which is the worktree path
// when the session has one. The JSON directory field is the main checkout
// in that case.
func Env(ev Event) []string {
	return []string{
		"AM_EVENT=" + ev.Name,
		"AM_SESSION_ID=" + ev.Session.ID,
		"AM_MANAGER_PID=" + strconv.Itoa(os.Getpid()),
		"AM_GROUP=" + ev.Session.Group,
		"AM_SPAWNED_BY=" + ev.Session.SpawnedBy,
		"AM_SESSION_NAME=" + ev.Session.Name,
		"AM_TOOL=" + ev.Session.Tool,
		"AM_CWD=" + ev.Session.Cwd,
		"AM_BRANCH=" + ev.Session.WorktreeBranch,
	}
}

// Line is one JSON object and a newline, then the caller closes stdin.
func Line(ev Event, version string) []byte {
	at := ev.At.UTC()
	if ev.At.IsZero() {
		at = time.Now().UTC()
	}
	directory, worktree := ev.Session.Cwd, ""
	if ev.Session.WorktreeRepo != "" {
		directory = ev.Session.WorktreeRepo
		worktree = ev.Session.Cwd
	}
	conversation := ev.Session.AgentSessionID
	if ev.UseConversation {
		conversation = ev.Conversation
	}
	payload := envelope{
		V:     EnvelopeVersion,
		Event: ev.Name,
		At:    at.Format(time.RFC3339),
		Session: sessionJSON{
			ID:             ev.Session.ID,
			Name:           ev.Session.Name,
			Tool:           ev.Session.Tool,
			Group:          ev.Session.Group,
			Directory:      directory,
			Worktree:       worktree,
			Branch:         ev.Session.WorktreeBranch,
			Status:         ev.Session.Status,
			PreviousStatus: ev.PreviousStatus,
			Conversation:   conversation,
			SpawnedBy:      ev.Session.SpawnedBy,
		},
		Manager: managerJSON{PID: os.Getpid(), Version: version},
		Reason:  ev.Reason,
		Model:   ev.Model,
		Effort:  ev.Effort,
	}
	if ev.Task != nil {
		payload.Task = &taskJSON{ID: ev.Task.ID, Title: ev.Task.Title, Owner: ev.Task.Owner}
	}
	if ev.Reservation != nil {
		item := reservationJSON{Action: ev.Reservation.Action, Pattern: ev.Reservation.Pattern, Mode: ev.Reservation.Mode, Released: ev.Reservation.Released}
		if !ev.Reservation.ExpiresAt.IsZero() {
			item.ExpiresAt = ev.Reservation.ExpiresAt.UTC().Format(time.RFC3339)
		}
		payload.Reservation = &item
	}
	if ev.Review != nil {
		payload.Review = &reviewJSON{CommentCount: ev.Review.CommentCount, Round: ev.Review.Round, Repo: ev.Review.Repo}
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(payload)
	return buf.Bytes()
}

type envelope struct {
	V           int              `json:"v"`
	Event       string           `json:"event"`
	At          string           `json:"ts"`
	Session     sessionJSON      `json:"session"`
	Manager     managerJSON      `json:"manager"`
	Reason      string           `json:"reason,omitempty"`
	Model       string           `json:"model,omitempty"`
	Effort      string           `json:"effort,omitempty"`
	Task        *taskJSON        `json:"task,omitempty"`
	Reservation *reservationJSON `json:"reservation,omitempty"`
	Review      *reviewJSON      `json:"review,omitempty"`
}

type sessionJSON struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	Tool           string `json:"tool"`
	Group          string `json:"group"`
	Directory      string `json:"directory"`
	Worktree       string `json:"worktree"`
	Branch         string `json:"branch"`
	Status         string `json:"status"`
	PreviousStatus string `json:"previous_status,omitempty"`
	Conversation   string `json:"conversation"`
	SpawnedBy      string `json:"spawned_by"`
}

type managerJSON struct {
	PID     int    `json:"pid"`
	Version string `json:"version"`
}

type taskJSON struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Owner string `json:"owner"`
}

type reservationJSON struct {
	Action    string `json:"action"`
	Pattern   string `json:"pattern"`
	Mode      string `json:"mode,omitempty"`
	ExpiresAt string `json:"expires_at,omitempty"`
	Released  bool   `json:"released,omitempty"`
}

type reviewJSON struct {
	CommentCount int    `json:"comment_count"`
	Round        int    `json:"round"`
	Repo         string `json:"repo"`
}
