package help

import "github.com/YoanWai/agent-manager/internal/keybind"

// Tables are the copied key tables the key map lists, whether the ←→ pair
// is on, whether its → attaches, and whether ← comes back from an attach.
type Tables struct {
	Session      keybind.Table
	List         keybind.Table
	ArrowStep    bool
	ArrowAttach  bool
	AttachedBack bool
}

// Reads is what building the key map's context asks the root: the copied
// tables, the glyphs and styles it paints with, the fixed quick-prompt keys
// it names and Review's own rows.
type Reads interface {
	KeyTables() Tables
	Glyphs() Glyphs
	QuickKeys() QuickKeys
	ReviewRows() [][2]string
	CursorMarker() string
	Styles() Styles
}

func ContextFor(reads Reads, width int) Context {
	tables := reads.KeyTables()
	return Context{
		SessionKeys:  tables.Session,
		ListKeys:     tables.List,
		ArrowStep:    tables.ArrowStep,
		ArrowAttach:  tables.ArrowAttach,
		AttachedBack: tables.AttachedBack,
		Width:        width,
		Glyphs:       reads.Glyphs(),
		QuickKeys:    reads.QuickKeys(),
		Styles:       reads.Styles(),
		CursorMarker: reads.CursorMarker(),
		ReviewRows:   reads.ReviewRows(),
	}
}
