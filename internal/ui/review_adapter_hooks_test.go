package ui

import (
	"testing"

	"github.com/YoanWai/agent-manager/internal/config"
	"github.com/YoanWai/agent-manager/internal/lifecyclehook"
	"github.com/YoanWai/agent-manager/internal/store"
	uireview "github.com/YoanWai/agent-manager/internal/ui/review"
)

func TestConfirmedReviewSendEmitsARound(t *testing.T) {
	m := buildModel(t)
	rec := &lifecyclehook.Recorder{}
	m.services.events = rec
	m.workspace.sessions = []store.Session{{ID: "s1", Name: "rev", Tool: "echoer", Cwd: t.TempDir()}}
	m.handleReviewSend(uireview.SendResult{
		TargetID: "s1", RepoRoot: "/repo", Round: 2, Count: 3, TargetName: "rev", Outcome: uireview.SendConfirmed,
	})
	got := rec.Events()
	if len(got) != 1 || got[0].Name != lifecyclehook.EventReviewRound || got[0].Review == nil || got[0].Review.CommentCount != 3 || got[0].Review.Round != 2 || got[0].Review.Repo != "/repo" || got[0].Session.ID != "s1" {
		t.Fatalf("review = %+v", got)
	}
	m.handleReviewSend(uireview.SendResult{TargetID: "s1", Outcome: uireview.SendRefused})
	m.handleReviewSend(uireview.SendResult{TargetID: "s1", Outcome: uireview.SendUncertain})
	if extra := rec.Events(); len(extra) != 1 {
		t.Fatalf("a refused send emitted %+v", extra)
	}
	m.services.cfg.Tools["shell"] = config.Tool{Shell: true}
	m.workspace.sessions[0].Tool = "shell"
	m.handleReviewSend(uireview.SendResult{TargetID: "s1", Outcome: uireview.SendConfirmed, Count: 1, Round: 1})
	if extra := rec.Events(); len(extra) != 1 {
		t.Fatalf("a shell review emitted %+v", extra)
	}
}
