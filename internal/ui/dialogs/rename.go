package dialogs

import (
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
)

// Rename edits the selected session's name and tool. It owns the field,
// keys and validation. The root seeds it from the selected row, runs the
// rename on the effect lane and closes it from the fenced completion.
type Rename struct {
	sessionID string
	input     textinput.Model
	tools     []string
	toolIndex int
}

func nameField(name string) textinput.Model {
	input := textinput.New()
	input.Prompt = ""
	input.Focus()
	input.SetValue(name)
	return input
}

// RenameSession opens a rename of one session, its tool picked from tools.
// A tool missing from tools (a removed config block) stays selectable, so a
// save does not silently reassign the session to the first configured tool.
func RenameSession(id, name, tool string, tools []string) Rename {
	toolIndex := 0
	for i, candidate := range tools {
		if candidate == tool {
			toolIndex = i
			break
		}
	}
	if len(tools) == 0 || tools[toolIndex] != tool {
		tools = append([]string{tool}, tools...)
		toolIndex = 0
	}
	return Rename{
		sessionID: id,
		input:     nameField(name),
		tools:     tools,
		toolIndex: toolIndex,
	}
}

func (d Rename) SessionID() string { return d.sessionID }

func (d Rename) Name() string { return cleanName(d.input.Value()) }

func (d *Rename) SetName(name string) { d.input.SetValue(name) }

func (d Rename) Tools() []string { return d.tools }

func (d *Rename) NameField(width int) textinput.Model {
	if width >= 5 {
		d.input.Width = width
	}
	return d.input
}

// Tool is the tool the rename would save.
func (d Rename) Tool() string {
	if len(d.tools) == 0 {
		return ""
	}
	return d.tools[d.toolIndex]
}

func (d *Rename) cycleTool(delta int) {
	if len(d.tools) == 0 {
		return
	}
	n := len(d.tools)
	d.toolIndex = (d.toolIndex + delta + n) % n
}

type RenameAction uint8

const (
	RenameStay RenameAction = iota
	RenameCancel
	// RenameInvalid carries the error the root reports.
	RenameInvalid
	RenameSubmit
)

// RenameRequest is a validated session rename.
type RenameRequest struct {
	SessionID string
	Tool      string
	Name      string
}

// RenameKey is a key's outcome. Cmd is the name field's own command.
type RenameKey struct {
	Action  RenameAction
	Err     string
	Request RenameRequest
	Cmd     tea.Cmd
}

func (d *Rename) Key(msg tea.KeyMsg) RenameKey {
	switch msg.String() {
	case "esc":
		return RenameKey{Action: RenameCancel}
	case "tab":
		d.cycleTool(1)
		return RenameKey{}
	case "shift+tab":
		d.cycleTool(-1)
		return RenameKey{}
	case "enter":
		return d.submit()
	}
	var cmd tea.Cmd
	d.input, cmd = d.input.Update(msg)
	return RenameKey{Cmd: cmd}
}

func (d *Rename) submit() RenameKey {
	name := d.Name()
	if name == "" {
		return RenameKey{Action: RenameInvalid, Err: "name cannot be empty"}
	}
	return RenameKey{Action: RenameSubmit, Request: RenameRequest{
		SessionID: d.sessionID,
		Name:      name,
		Tool:      d.Tool(),
	}}
}

func (d Rename) Hints() [][2]string {
	if tool := d.Tool(); tool != "" {
		return [][2]string{{"tab", "tool: " + tool}, {"↵", "save"}, {"esc", "cancel"}}
	}
	return [][2]string{{"↵", "save"}, {"esc", "cancel"}}
}
