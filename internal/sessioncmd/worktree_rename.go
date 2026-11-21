package sessioncmd

import (
	"database/sql"
	"errors"
	"fmt"

	"github.com/YoanWai/agent-manager/internal/git"
	"github.com/YoanWai/agent-manager/internal/store"
)

type worktreeRenameStore interface {
	ListSessions(bool) ([]store.Session, error)
	RenameSessionWorktreeBranch(string, string) error
}

// ApplyName names a session the way the manager does: its managed branch
// first, so a name git cannot give it leaves the session as it was, then its
// row. It starts from the row as stored, not a caller's earlier copy, since
// another rename or a group rename may have landed since, and returns it
// renamed. The caller relabels the pane.
func ApplyName(gitDrv *git.Driver, st *store.Store, id, name string) (store.Session, error) {
	sess, err := st.Get(id)
	if errors.Is(err, sql.ErrNoRows) {
		return store.Session{}, fmt.Errorf("session %s: %w", id, store.ErrSessionGone)
	}
	if err != nil || name == sess.Name {
		return sess, err
	}
	if err := RenameWorktreeBranch(gitDrv, st, &sess, name); err != nil {
		return store.Session{}, fmt.Errorf("worktree rename: %w", err)
	}
	if err := st.RenameSession(id, name); err != nil {
		return store.Session{}, err
	}
	sess.Name = name
	return sess, nil
}

// RenameWorktreeBranch keeps a managed branch aligned with its
// session's display name without moving the directory of a live process.
func RenameWorktreeBranch(gitDrv *git.Driver, st worktreeRenameStore, sess *store.Session, name string) error {
	if gitDrv == nil || sess.WorktreeRepo == "" || sess.WorktreeBranch == "" {
		return nil
	}
	sessions, err := st.ListSessions(true)
	if err != nil {
		return err
	}
	for _, other := range sessions {
		if other.ID != sess.ID && other.Cwd == sess.Cwd {
			return fmt.Errorf("worktree is shared with session %q", other.Name)
		}
	}
	branch, err := gitDrv.RenameWorktreeBranch(sess.WorktreeRepo, sess.Cwd, sess.WorktreeBranch, name)
	if err != nil {
		return err
	}
	if branch == sess.WorktreeBranch {
		return nil
	}
	if err := st.RenameSessionWorktreeBranch(sess.ID, branch); err != nil {
		rollbackBranch, rollbackErr := gitDrv.RenameWorktreeBranch(sess.WorktreeRepo, sess.Cwd, branch, sess.Name)
		if rollbackErr != nil {
			return fmt.Errorf("%w; could not restore git branch %s: %w", err, sess.WorktreeBranch, rollbackErr)
		}
		if rollbackBranch != sess.WorktreeBranch {
			return fmt.Errorf("%w; git branch rollback returned %s instead of %s", err, rollbackBranch, sess.WorktreeBranch)
		}
		return err
	}
	sess.WorktreeBranch = branch
	return nil
}
