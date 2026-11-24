package app

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/YoanWai/agent-manager/internal/config"
	"github.com/YoanWai/agent-manager/internal/sessioncmd"
	"github.com/YoanWai/agent-manager/internal/status"
	"github.com/YoanWai/agent-manager/internal/store"
	"github.com/YoanWai/agent-manager/internal/tmux"
	uifocus "github.com/YoanWai/agent-manager/internal/ui/focus"
)

// ErrNotAtPromptHead is a prompt-head answer of no: ← belongs to the agent.
var ErrNotAtPromptHead = errors.New("the caret is not at the head of the prompt")

// PromptHead answers whether the agent in the named tmux session has its
// caret at the head of its prompt, the same question focus asks of ←. Any
// error is a no, so ← reaches the agent.
func PromptHead(profileDir string, driver *tmux.Driver, args []string) error {
	if len(args) != 1 {
		return errors.New("usage: agent-manager " + sessioncmd.PromptHeadCommand + " <tmux session name>")
	}
	id, managed := strings.CutPrefix(args[0], "am_")
	if !managed {
		return ErrNotAtPromptHead
	}
	st, err := store.Open(filepath.Join(profileDir, "state.db"))
	if err != nil {
		return err
	}
	defer st.Close()
	if attaches, err := st.ArrowStepAttaches(); err != nil || !attaches {
		return errors.Join(err, ErrNotAtPromptHead)
	}
	sess, err := st.Get(id)
	if err != nil {
		return err
	}
	cfg, err := config.Default()
	if err != nil {
		return err
	}
	engine, err := status.NewEngine(cfg)
	if err != nil {
		return err
	}
	state, pane, err := driver.DisplayAndCapture(id, "#{cursor_x},#{cursor_y},#{cursor_flag}")
	if err != nil {
		return err
	}
	cursor := uifocus.Cursor{PositionKnown: true}
	var shown int
	if _, err := fmt.Sscanf(state, "%d,%d,%d", &cursor.X, &cursor.Y, &shown); err != nil {
		return fmt.Errorf("tmux reported no cursor: %q", state)
	}
	cursor.Visible = shown == 1
	if !uifocus.CaretAtPromptHead(cursor, engine.Plain(sess.Tool, pane), status.PromptRules{Engine: engine, Tool: sess.Tool}) {
		return ErrNotAtPromptHead
	}
	return nil
}
