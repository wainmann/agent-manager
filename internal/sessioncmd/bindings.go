package sessioncmd

import (
	"github.com/YoanWai/agent-manager/internal/launch"
	"github.com/YoanWai/agent-manager/internal/store"
	"github.com/YoanWai/agent-manager/internal/tmux"
)

// PromptHeadCommand is the hidden subcommand an attached ← runs. tmux
// passes it the attached session's name.
const PromptHeadCommand = "prompt-head"

// ApplyBindingSettings reads what the in-session bindings are built from.
// Every process that installs them reads it here, so none strips what
// another put on the server.
func ApplyBindingSettings(st *store.Store, driver *tmux.Driver) error {
	keys, err := st.SessionKeys()
	if err != nil {
		return err
	}
	attaches, err := st.ArrowStepAttaches()
	if err != nil {
		return err
	}
	driver.SetSessionKeys(keys)
	check := ""
	if attaches {
		check = tmux.ShellQuote(launch.Executable()) + " " + PromptHeadCommand
	}
	driver.SetPromptHeadCheck(check)
	return nil
}
