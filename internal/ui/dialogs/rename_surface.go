package dialogs

import (
	"github.com/YoanWai/agent-manager/internal/ui/pathcomplete"
	"github.com/YoanWai/agent-manager/internal/ui/surface"
	tea "github.com/charmbracelet/bubbletea"
)

// RenameSurface is the session rename typed into its row. It paints no
// card: the rail draws the name field in place, and the list holds still
// under it.
type RenameSurface struct {
	Rename
	root RenameRoot
}

// RenameRoot is what the row reaches on the root beyond surface.Host. The
// request a submit validated stays the root's, so it builds one from the
// RenameRequest the dialog hands it.
type RenameRoot interface {
	ReportErr(text string)
	QueueRename(request RenameRequest) tea.Cmd
}

func NewRenameSurface(root RenameRoot, rename Rename) *RenameSurface {
	return &RenameSurface{Rename: rename, root: root}
}

// dialogPaths hands the group form its completer as the PathSuggestions the
// fields read.
type dialogPaths struct{ *pathcomplete.State }

func (p dialogPaths) Active() bool        { return p.State.Active() }
func (p dialogPaths) Chosen() bool        { return p.State.Chosen }
func (p dialogPaths) Move(delta int) bool { return p.State.Move(delta) }
func (p dialogPaths) Reset()              { p.State.Reset() }

func (s *RenameSurface) Key(h surface.Host, msg tea.KeyMsg) (bool, tea.Cmd) {
	key := s.Rename.Key(msg)
	switch key.Action {
	case RenameCancel:
		h.Pop()
		return true, nil
	case RenameInvalid:
		s.root.ReportErr(key.Err)
		return true, nil
	case RenameSubmit:
		return true, s.root.QueueRename(key.Request)
	}
	return true, key.Cmd
}

func (s *RenameSurface) Click(surface.Host, int) tea.Cmd { return nil }

func (s *RenameSurface) Wheel(surface.Host, tea.KeyMsg) tea.Cmd { return nil }

// Mouse holds the screen still while a rename is typed.
func (s *RenameSurface) Mouse(surface.Host, tea.MouseMsg) tea.Cmd { return nil }

// View leaves the frame as it is: the row draws the field itself.
func (s *RenameSurface) View(_ surface.Host, below string) string { return below }

func (s *RenameSurface) Footer() [][2]string { return s.Hints() }

func (s *RenameSurface) Owner() surface.Owner { return surface.Manager }

// Dismissible: the name is a draft, so a stray click or ctrl+c keeps it;
// esc cancels. The row also holds the list still, so the mouse treats it as
// no card at all.
func (s *RenameSurface) Dismissible() bool { return false }

func (s *RenameSurface) Leave(surface.Host) {}
