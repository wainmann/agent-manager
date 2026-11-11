package execution

import (
	"time"

	"github.com/YoanWai/agent-manager/internal/launch"
	"github.com/YoanWai/agent-manager/internal/lifecyclehook"
	"github.com/YoanWai/agent-manager/internal/notify"

	"github.com/YoanWai/agent-manager/internal/status"
	"github.com/YoanWai/agent-manager/internal/store"
)

// notifyTransition fires a desktop notification when a session's status
// flips into one the user has asked to hear about. It runs only from the
// transition branch of refreshOnce, so a status that persists across polls
// never re-fires. Waiting and errored notify unless silenced in Settings;
// finished stays quiet unless opted in, since most turn ends are routine.
// There is no focus gate: the poll cannot tell whether the user is looking
// at the manager, and the session they are watching is precisely the one
// whose ping they must not miss. The banner quotes what the row shows:
// a waiting session's question, a finished session's prompt.
func (p *Runner) notifyTransition(sess store.Session, newStatus, prompt, quote string) {
	event, ok := p.attention(sess, newStatus, prompt, quote)
	if !ok || p.notifyFn == nil {
		return
	}
	// Delivery can wait on an external process, so it must never run
	// inside refreshOnce, which holds runMu.
	go p.notifyFn(event)
}

// emitSessionState reports one stored status change. A notify hook runs
// for the change itself. NotifyEnv is set only when the banner would
// also fire, and that is the only time the hook receives the banner text.
func (p *Runner) emitSessionState(sess store.Session, newStatus, previous, prompt, quote string) {
	if p.shellTools[sess.Tool] {
		return
	}
	sess.Status = newStatus
	ev := lifecyclehook.Event{
		Name:           lifecyclehook.EventSessionState,
		At:             time.Now().UTC(),
		Session:        sess,
		PreviousStatus: previous,
	}
	if event, ok := p.attention(sess, newStatus, prompt, quote); ok {
		ev.NotifyEnv = notify.CommandEnv(event)
	}
	lifecyclehook.Or(p.events).Emit(ev)
}

func (p *Runner) attention(sess store.Session, newStatus, prompt, quote string) (notify.Event, bool) {
	var kind notify.Kind
	var text string
	switch newStatus {
	case status.Waiting:
		kind, text = notify.Waiting, quote
	case status.Errored:
		kind = notify.Errored
	case status.Finished:
		if !p.notifyFinished() {
			return notify.Event{}, false
		}
		kind, text = notify.Finished, sessionPrompt(sess, prompt)
	default:
		return notify.Event{}, false
	}
	if !p.notificationsOn() {
		return notify.Event{}, false
	}
	return notify.Event{
		ID: sess.ID, Session: sess.Name, Tool: sess.Tool, Kind: kind,
		Dir: sess.Cwd, Branch: sess.WorktreeBranch, Repo: sess.WorktreeRepo, Text: text,
	}, true
}

func (p *Runner) notificationsOn() bool {
	chosen, err := p.store.Setting(notificationsSetting)
	return err != nil || chosen != "off"
}

// sessionPrompt is the line a finished banner quotes, the same fallback
// the session row uses: the prompt echoed in the pane, else the last one
// delivered through the manager, else the one the session launched with.
func sessionPrompt(sess store.Session, echoed string) string {
	if echoed != "" {
		return echoed
	}
	if sess.LastPrompt != "" {
		return sess.LastPrompt
	}
	return launch.DeliveredPrompt(sess.LaunchPrompt)
}

func (p *Runner) notifyFinished() bool {
	chosen, err := p.store.Setting(notifyFinishedSetting)
	return err == nil && chosen == "on"
}
