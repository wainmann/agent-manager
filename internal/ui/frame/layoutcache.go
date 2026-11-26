package frame

import (
	"slices"

	"github.com/YoanWai/agent-manager/internal/ui/presentation"
)

type Measurement struct {
	Mode LegendMode
	Rows int
}

// LayoutCache retains geometry, not painted text: theme changes still paint
// with current styles, while animation frames reuse the same measurements.
type LayoutCache struct {
	valid       bool
	refs        [][]Section
	width, room int
	glyph       presentation.GlyphMode
	measured    Measurement

	listValid  bool
	listRefs   []Section
	listWidth  int
	listMode   LegendMode
	listRows   int
	listGlyph  presentation.GlyphMode
	listHeight int
}

// ListHeight returns the painted row count of the list footer for sections,
// reusing the last measurement when the sections and geometry are unchanged.
func (c *LayoutCache) ListHeight(sections []Section, width int, mode LegendMode, rows int, glyph presentation.GlyphMode, measure func() int) int {
	if mode != Titled {
		return rows
	}
	if c.listValid && c.listWidth == width && c.listMode == mode && c.listRows == rows && c.listGlyph == glyph && sectionsSliceEqual(c.listRefs, sections) {
		return c.listHeight
	}
	height := measure()
	c.listValid = true
	c.listRefs = cloneSections(sections)
	c.listWidth = width
	c.listMode = mode
	c.listRows = rows
	c.listGlyph = glyph
	c.listHeight = height
	return height
}

func cloneSections(sections []Section) []Section {
	out := slices.Clone(sections)
	for i := range out {
		section := &out[i]
		section.Leads = slices.Clone(section.Leads)
		section.Alerts = slices.Clone(section.Alerts)
		section.Pairs = slices.Clone(section.Pairs)
		section.ActionPairs = slices.Clone(section.ActionPairs)
	}
	return out
}

func sectionsSliceEqual(a, b []Section) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !sectionEqual(a[i], b[i]) {
			return false
		}
	}
	return true
}

func (c *LayoutCache) Measure(refs [][]Section, width, room int, glyph presentation.GlyphMode) Measurement {
	if c.valid && c.width == width && c.room == room && c.glyph == glyph && sectionRefsEqual(c.refs, refs) {
		return c.measured
	}
	badge, quiet := TitleStyles()
	mode, rows := LegendLayout(refs, width, room, LiveCap(glyph), badge, quiet, Separator())
	snapshot := make([][]Section, len(refs))
	for i, sections := range refs {
		snapshot[i] = slices.Clone(sections)
		for j := range snapshot[i] {
			section := &snapshot[i][j]
			section.Leads = slices.Clone(section.Leads)
			section.Alerts = slices.Clone(section.Alerts)
			section.Pairs = slices.Clone(section.Pairs)
			section.ActionPairs = slices.Clone(section.ActionPairs)
		}
	}
	*c = LayoutCache{valid: true, refs: snapshot, width: width, room: room, glyph: glyph, measured: Measurement{Mode: mode, Rows: rows}}
	return c.measured
}

func sectionRefsEqual(a, b [][]Section) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if len(a[i]) != len(b[i]) {
			return false
		}
		for j := range a[i] {
			if !sectionEqual(a[i][j], b[i][j]) {
				return false
			}
		}
	}
	return true
}

func sectionEqual(a, b Section) bool {
	return a.Title == b.Title &&
		a.Hint == b.Hint &&
		a.Quiet == b.Quiet &&
		slices.Equal(a.Leads, b.Leads) &&
		slices.Equal(a.Alerts, b.Alerts) &&
		slices.Equal(a.Pairs, b.Pairs) &&
		slices.Equal(a.ActionPairs, b.ActionPairs)
}
