package link

import (
	"context"

	"github.com/YoanWai/agent-manager/internal/remote"
	"github.com/YoanWai/agent-manager/internal/sessioncmd"
	uiframe "github.com/YoanWai/agent-manager/internal/ui/frame"
	uirail "github.com/YoanWai/agent-manager/internal/ui/rail"
	"github.com/YoanWai/agent-manager/internal/ui/spawn"
	tea "github.com/charmbracelet/bubbletea"
)

type Op uint8

const (
	Type Op = iota + 1
	Spawn
	NewTerminal
	Kill
	CloseTerminal
	Revive
	Archive
	Restore
	CreateGroup
	Delete
	DeleteGroup
	Rename
	RenameGroup
	Move
)

type Call struct {
	Op       Op
	Host     string
	Ref      remote.Ref
	Name     string
	Text     string
	Spawn    sessioncmd.CreateSessionOptions
	Terminal sessioncmd.CreateTerminalOptions
	// GroupDir is the directory a new group on the host defaults to; Name
	// carries its path.
	GroupDir string
	// Sessions holds the ids of the sessions a group delete confirmed it
	// takes.
	Sessions []string
	// Group is the host's group a move files the session under, empty for
	// its root.
	Group string
	// ComposerGen and Submission let a send or spawn from the quick bar, or
	// a spawn from the New Session form, settle what asked for it.
	ComposerGen int
	Submission  spawn.Submission
	FromForm    bool
	Dead        bool
}

// FromQuick is whether the call is a send or spawn the quick bar accepted.
func (c Call) FromQuick() bool { return c.Op == Type || c.Op == Spawn && !c.FromForm }

// Result is what a call returned: the session or terminal it made or
// changed, and why a deleted session's worktree stayed on the host.
type Result struct {
	Session sessioncmd.Session
	Notice  string
}

func Run(ctx context.Context, client *remote.Client, openers remote.Openers, call Call) (Result, error) {
	var result Result
	var err error
	switch call.Op {
	case Type:
		result.Session, err = client.Type(ctx, call.Ref, call.Text)
	case Spawn:
		result.Session, err = client.Spawn(ctx, call.Host, call.Spawn)
	case NewTerminal:
		var term sessioncmd.Terminal
		term, err = client.CreateTerminal(ctx, call.Host, call.Terminal)
		result.Session = sessioncmd.Session{ID: term.ID, Name: term.Name}
	case Kill:
		result.Session, err = client.Kill(ctx, call.Ref)
	case CloseTerminal:
		err = client.Terminals(openers).CloseForUser(ctx, call.Ref)
	case Delete:
		var deleted sessioncmd.Deletion
		deleted, err = client.Delete(ctx, call.Ref)
		result.Session, result.Notice = deleted.Session, deleted.Notice
	case Revive:
		result.Session, err = client.Revive(ctx, call.Ref)
	case Archive, Restore:
		result.Session, err = client.Archive(ctx, call.Ref, call.Op == Restore)
	case CreateGroup:
		_, err = client.CreateGroup(ctx, call.Host, call.Name, call.GroupDir)
	case DeleteGroup:
		var deleted sessioncmd.GroupDeletion
		deleted, err = client.DeleteGroupWithSessions(ctx, call.Host, call.Name, call.Sessions)
		result.Notice = deleted.Notice
	case Rename:
		result.Session, err = client.Rename(ctx, call.Ref, call.Text)
	case RenameGroup:
		_, err = client.RenameGroup(ctx, call.Host, call.Name, call.Text)
	case Move:
		result.Session, err = client.Move(ctx, call.Ref, call.Group)
	}
	return result, err
}

func (s *State) ApplyCall(root Root, call Call, result Result, err error) tea.Cmd {
	if err != nil {
		// As on this machine, a prompt that may sit in the pane stays off
		// the bar, so the next enter cannot type it a second time.
		if call.FromQuick() && !remote.MayHaveReached(err) {
			root.RestoreRefused(call.ComposerGen, call.Submission)
		}
		root.ReportErr(err.Error())
		return s.PollHost(call.Host)
	}
	on := " on " + call.Host
	switch call.Op {
	case Type:
		root.ReportDone("sent to " + call.Name + on)
	case Spawn:
		if call.FromForm {
			root.CloseAsker(call.ComposerGen, FormAsker)
		}
		root.ReportDone("started " + result.Session.Name + on)
	case NewTerminal:
		root.ReportDone("opened terminal " + result.Session.Name + on)
	case Kill, CloseTerminal:
		root.ReportDone("killed " + call.Name + on)
	case Delete, DeleteGroup:
		deleted := "deleted " + call.Name + on
		if call.Op == DeleteGroup {
			deleted = "deleted group " + call.Name + on
		}
		if result.Notice != "" {
			root.ReportErr(deleted + "; " + result.Notice)
		} else {
			root.ReportDone(deleted)
		}
	case Revive:
		root.ReportDone("revived " + call.Name + on)
	case Archive:
		root.ReportDone("archived " + call.Name + on)
	case Restore:
		root.ReportDone("restored " + call.Name + on)
	case CreateGroup:
		root.CloseAsker(call.ComposerGen, GroupFormAsker)
		root.ReportDone("created group " + call.Name + on)
	case Rename:
		root.CloseRename(call.ComposerGen, call.Text)
		root.ReportDone("renamed " + call.Name + " to " + call.Text + on)
	case RenameGroup:
		root.CloseRename(call.ComposerGen, call.Text)
		renamed := call.Text
		if parent := uirail.ParentGroup(call.Name); parent != "" {
			renamed = parent + "/" + call.Text
		}
		root.RailConnection(RailChange{GroupHost: call.Host, Group: call.Name, ToGroup: renamed})
		root.ReportDone("renamed group " + call.Name + " to " + renamed + on)
	case Move:
		root.ReportDone("moved " + call.Name + " to " + uiframe.DisplayGroup(call.Group) + on)
	}
	return s.PollHost(call.Host)
}
