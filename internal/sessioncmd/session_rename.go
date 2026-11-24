package sessioncmd

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/YoanWai/agent-manager/internal/git"
)

// refuseRenameCaller keeps renaming another row the user's: a session names
// itself once, on a placeholder, through the manager.
func refuseRenameCaller(sessionID string) error {
	if sessionID == "" {
		return nil
	}
	return fmt.Errorf("renaming is for the user's rename key on another machine; session %s names itself with agent-manager rename", sessionID)
}

// RenameSession names an agent or a terminal the way the rename key does,
// its managed branch included.
func (s *Sessions) RenameSession(sessionID, targetID, name string) (Session, error) {
	if err := refuseRenameCaller(sessionID); err != nil {
		return Session{}, err
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return Session{}, errors.New("name cannot be empty")
	}
	runtime, err := s.open()
	if err != nil {
		return Session{}, err
	}
	defer runtime.Close()
	targetID = strings.TrimSpace(targetID)
	target, err := runtime.store.Get(targetID)
	if errors.Is(err, sql.ErrNoRows) {
		return Session{}, fmt.Errorf("session %s does not exist; call %s for current ids", targetID, runtime.words.ListSessions)
	}
	if err != nil {
		return Session{}, err
	}
	var gitDriver *git.Driver
	if target.WorktreeRepo != "" {
		if gitDriver, err = s.newGit(); err != nil {
			return Session{}, fmt.Errorf("cannot rename %s's branch: %w", target.Name, err)
		}
	}
	renamed, err := ApplyName(gitDriver, runtime.store, target.ID, name)
	if err != nil {
		return Session{}, err
	}
	if err := runtime.relabel([]string{renamed.ID}); err != nil {
		return Session{}, fmt.Errorf("renamed %s, but could not update its status bar: %w", renamed.ID, err)
	}
	return runtime.sessionInfo(renamed, runtime.driver.Exists(renamed.ID), false), nil
}
