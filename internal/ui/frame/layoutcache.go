package frame

import (
	"reflect"
	"slices"

	"github.com/YoanWai/agent-manager/internal/ui/presentation"
	"github.com/charmbracelet/lipgloss"
)

type Measurement struct {
	Mode LegendMode
	Rows int
}

// LayoutCache retains geometry, not painted text: theme changes still paint
// with current styles, while animation frames reuse the same measurements.
type LayoutCache struct {
	valid           bool
	refs            [][]Section
	width, room     int
	glyph           presentation.GlyphMode
	measured        Measurement
	list            []Section
	listMeasurement Measurement
	listRows        int
	listValid       bool
	listWidth       int
	listGlyph       presentation.GlyphMode
}

func (c *LayoutCache) Measure(refs [][]Section, width, room int, glyph presentation.GlyphMode) Measurement {
	if c.valid && c.width == width && c.room == room && c.glyph == glyph && reflect.DeepEqual(c.refs, refs) {
		return c.measured
	}
	badge, quiet := TitleStyles()
	mode, rows := LegendLayout(refs, width, room, LiveCap(glyph), badge, quiet, Separator())
	snapshot := make([][]Section, len(refs))
	for i, sections := range refs {
		snapshot[i] = cloneSections(sections)
	}
	*c = LayoutCache{valid: true, refs: snapshot, width: width, room: room, glyph: glyph, measured: Measurement{Mode: mode, Rows: rows}}
	return c.measured
}

// ListHeight keeps the selected row's actual height separate from the maximum
// budget measured across all reference tiers. Transient footers need that
// height without painting the list legend again on each animation frame.
func (c *LayoutCache) ListHeight(f Footer) int {
	if c.listValid && c.listWidth == f.Width && c.listGlyph == f.Mode && c.listMeasurement == f.Measurement && reflect.DeepEqual(c.list, f.List) {
		return c.listRows
	}
	c.list = cloneSections(f.List)
	c.listWidth, c.listGlyph = f.Width, f.Mode
	c.listMeasurement = f.Measurement
	c.listRows = lipgloss.Height(f.ListLegend().Text)
	c.listValid = true
	return c.listRows
}

func cloneSections(sections []Section) []Section {
	snapshot := slices.Clone(sections)
	for i := range snapshot {
		section := &snapshot[i]
		section.Leads = slices.Clone(section.Leads)
		section.Alerts = slices.Clone(section.Alerts)
		section.Pairs = slices.Clone(section.Pairs)
		section.ActionPairs = slices.Clone(section.ActionPairs)
	}
	return snapshot
}
