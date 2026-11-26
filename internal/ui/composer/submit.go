package composer

import (
	"slices"

	"github.com/YoanWai/agent-manager/internal/clipboard"
	"github.com/YoanWai/agent-manager/internal/ui/spawn"
	tea "github.com/charmbracelet/bubbletea"
)

func (c *Composer) Submission() spawn.Submission {
	return spawn.Submission{ID: c.submitted + 1, Draft: c.Input.Value(), Saved: slices.Clone(c.Saved)}
}

// Accepted images belong to the delivery request.
func (c *Composer) Accept(submission spawn.Submission) {
	c.submitted = submission.ID
	c.Input.SetValue("")
	c.Saved = nil
	c.Attachments = nil
}

func (c *Composer) Untouched(id uint64) bool {
	return c.submitted == id && c.Input.Value() == "" && len(c.Saved) == 0 && len(c.Attachments) == 0
}

func (c *Composer) Restore(submission spawn.Submission) bool {
	if !c.Untouched(submission.ID) {
		return false
	}
	c.Input.SetValue(submission.Draft)
	c.Saved = submission.Saved
	return true
}

// KeepImages puts a sent prompt's images back on its chips under names of
// their own, since the delivery deletes the names it took if it is refused.
func (c *Composer) KeepImages(h Host, images []Attachment) tea.Cmd {
	var cmds []tea.Cmd
	for _, image := range images {
		path, err := clipboard.LinkPaste(image.Path)
		image.Path = path
		c.Attachments = append(c.Attachments, image)
		if err != nil {
			h.ReportErr("could not keep the pasted image: " + err.Error())
			cmds = append(cmds, c.removeImage(image.ID))
		}
	}
	return tea.Batch(cmds...)
}
