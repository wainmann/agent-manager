package ui

import (
	"testing"
	"time"

	"github.com/YoanWai/agent-manager/internal/store"
	uilifecycle "github.com/YoanWai/agent-manager/internal/ui/session/lifecycle"
)

func TestEqualStampListingCannotConfirmDeletion(t *testing.T) {
	t.Parallel()
	m := buildModel(t)
	createSession(t, m, "doomed", t.TempDir(), "")
	sess := m.sessionRows()[0]
	deleteSession(t, m, "doomed")
	at := m.effects.latestObservation
	m.ledger.lifecycle.Gone[sess.ID] = uilifecycle.GoneMark{At: at, Deleted: true}

	m.Update(refreshMsg{sessions: []store.Session{sess}, listedAt: at})
	if len(m.sessionRows()) != 0 || len(m.ledger.lifecycle.Gone) != 1 {
		t.Fatalf("equal-stamp listing confirmed a stale row, rows=%v markers=%v", m.sessionRows(), m.ledger.lifecycle.Gone)
	}
	m.Update(refreshMsg{listedAt: m.freshListingAt()})
	if len(m.ledger.lifecycle.Gone) != 0 {
		t.Fatalf("strictly newer listing left deletion markers=%v", m.ledger.lifecycle.Gone)
	}
}

func TestFreshListingAtFollowsEveryObservation(t *testing.T) {
	at := time.Unix(100, 0)
	for _, source := range []string{"listing", "effect", "launch", "session", "group"} {
		t.Run(source, func(t *testing.T) {
			m := &Model{}
			switch source {
			case "listing":
				m.workspace.lastListedAt = at
			case "effect":
				m.effects.latestObservation = at
			case "launch":
				m.ledger.lifecycle.RecordLaunch("row", at)
			case "session":
				m.ledger.lifecycle.Gone = map[string]uilifecycle.GoneMark{"row": {At: at}}
			case "group":
				m.ledger.lifecycle.GoneGroups = map[string]uilifecycle.GoneMark{"group": {At: at}}
			}
			if got := m.freshListingAt(); !got.Equal(at.Add(time.Nanosecond)) {
				t.Fatalf("listing time=%v, want one nanosecond after %s observation %v", got, source, at)
			}
		})
	}
}
