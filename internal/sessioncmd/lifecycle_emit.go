package sessioncmd

import (
	"strings"
	"time"

	"github.com/YoanWai/agent-manager/internal/lifecyclehook"
	"github.com/YoanWai/agent-manager/internal/store"
)

func (l *Lifecycle) emit(ev lifecyclehook.Event) {
	if l.shell(ev.Session) {
		return
	}
	if ev.At.IsZero() {
		ev.At = time.Now().UTC()
	}
	lifecyclehook.Or(l.runtime.Events).Emit(ev)
}

func (l *Lifecycle) shell(sess store.Session) bool {
	tool, ok := l.runtime.Config.Tools[sess.Tool]
	return ok && tool.Shell
}

func (l *Lifecycle) emitStart(sess store.Session, reason, conversation string, replaceConversation bool) {
	l.emit(lifecyclehook.Event{
		Name:            lifecyclehook.EventSessionStart,
		Session:         sess,
		Reason:          reason,
		Model:           sess.Choice.Model,
		Effort:          sess.Choice.Effort,
		Conversation:    conversation,
		UseConversation: replaceConversation,
	})
}

// emitKilled reports the stop twice, once as session_kill and once as a
// dead session_state. A subscriber that stops a container has to treat
// the pair as one stop, because the poller can also write dead.
func (l *Lifecycle) emitKilled(sess store.Session, reason, previous string) {
	l.emit(lifecyclehook.Event{
		Name: lifecyclehook.EventSessionKill, Session: sess, Reason: reason, PreviousStatus: previous,
	})
	l.emit(lifecyclehook.Event{
		Name: lifecyclehook.EventSessionState, Session: sess, PreviousStatus: previous,
	})
}

func (l *Lifecycle) emitRollback(sess store.Session, previous string) {
	l.emit(lifecyclehook.Event{
		Name: lifecyclehook.EventSessionKill, Session: sess, Reason: lifecyclehook.ReasonRollback, PreviousStatus: previous,
	})
}

func (l *Lifecycle) emitArchived(sess store.Session, reason string, archived bool) {
	name := lifecyclehook.EventSessionArchive
	if !archived {
		name = lifecyclehook.EventSessionRestore
	}
	sess.Archived = archived
	l.emit(lifecyclehook.Event{Name: name, Session: sess, Reason: reason})
}

func (l *Lifecycle) emitDeleted(sess store.Session) {
	l.emit(lifecyclehook.Event{
		Name: lifecyclehook.EventSessionKill, Session: sess, Reason: lifecyclehook.ReasonDelete,
	})
}

// emitGroupChange reports the sessions whose archived flag the group write
// moved. A session created between the earlier list and that write is
// archived with the group and gets no event. The caller emits the selection
// it already held when either list failed.
func (l *Lifecycle) emitGroupChange(path string, before []store.Session, beforeErr error, archived bool, reason string, fallback []store.Session) {
	if beforeErr != nil {
		l.emitArchiveSelection(fallback, archived, reason)
		return
	}
	after, err := l.runtime.Store.ListSessions(true)
	if err != nil {
		l.emitArchiveSelection(fallback, archived, reason)
		return
	}
	was := map[string]bool{}
	known := map[string]bool{}
	for _, sess := range sessionsUnder(before, path) {
		was[sess.ID] = sess.Archived
		known[sess.ID] = true
	}
	for _, sess := range sessionsUnder(after, path) {
		if !known[sess.ID] || was[sess.ID] == archived {
			continue
		}
		l.emitArchived(sess, reason, archived)
	}
}

func (l *Lifecycle) emitArchiveSelection(sessions []store.Session, archived bool, reason string) {
	for _, sess := range sessions {
		l.emitArchived(sess, reason, archived)
	}
}

func sessionsUnder(sessions []store.Session, path string) []store.Session {
	if path == "" {
		return nil
	}
	var members []store.Session
	prefix := path + "/"
	for _, sess := range sessions {
		if sess.Group == path || strings.HasPrefix(sess.Group, prefix) {
			members = append(members, sess)
		}
	}
	return members
}

func (r *runtime) emit(ev lifecyclehook.Event) {
	if tool, ok := r.cfg.Tools[ev.Session.Tool]; ok && tool.Shell {
		return
	}
	if ev.At.IsZero() {
		ev.At = time.Now().UTC()
	}
	lifecyclehook.Or(r.events).Emit(ev)
}

func (r *runtime) emitTask(caller store.Session, name, id, title, owner string) {
	if owner == "" {
		owner = caller.ID
	}
	r.emit(lifecyclehook.Event{
		Name:    name,
		Session: caller,
		Task:    &lifecyclehook.TaskInfo{ID: id, Title: title, Owner: owner},
	})
}

func (r *runtime) emitReservation(caller store.Session, info lifecyclehook.ReservationInfo) {
	r.emit(lifecyclehook.Event{
		Name:        lifecyclehook.EventReservationSet,
		Session:     caller,
		Reservation: &info,
	})
}
