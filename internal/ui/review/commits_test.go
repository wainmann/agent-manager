package review

import (
	"fmt"
	"strings"
	"testing"

	git "github.com/YoanWai/agent-manager/internal/git/value"
	"github.com/YoanWai/agent-manager/internal/ui/presentation"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

func TestCommitRailOverflowLabelsKeepMouseRows(t *testing.T) {
	m := New(false)
	m.scope = git.ScopeCommit
	for i := range 15 {
		m.commits = append(m.commits, git.Commit{SHA: fmt.Sprint(i), Short: fmt.Sprint(i), Subject: "literal ← subject"})
	}
	m.commitPin.SHA = m.commits[7].SHA
	for _, tc := range []struct {
		mode         presentation.GlyphMode
		above, below string
	}{
		{presentation.GlyphModeBasic, "more above", "more below"},
		{presentation.GlyphModeAdvanced, "↑ ", "↓ "},
	} {
		view := ansi.Strip(m.CommitRail(RenderContext{GlyphMode: tc.mode}, 60, 5))
		rows := strings.Split(view, "\n")
		if !strings.Contains(rows[0], tc.above) || !strings.Contains(rows[len(rows)-1], tc.below) || !strings.Contains(view, "literal ← subject") {
			t.Fatalf("%s: %s", tc.mode, view)
		}
		for _, line := range []int{0, len(rows) - 1} {
			if _, marked := m.commitMarks.At(line); marked {
				t.Fatalf("overflow row %d marks a commit", line)
			}
			sha, ok := m.ClickCommit(line)
			if !ok || sha == "" {
				t.Fatalf("%s row %d lost mouse target", tc.mode, line)
			}
		}
	}
}

func TestCommitMarksFollowModeAndPreserveSubjects(t *testing.T) {
	for _, tc := range []struct {
		mode                    presentation.GlyphMode
		pending, done, selected string
	}{
		{presentation.GlyphModeBasic, ".", "+", "│"},
		{presentation.GlyphModeAdvanced, "○", "✔", "▎"},
		{"", "○", "✔", "▎"},
	} {
		m := New(false)
		m.scope = git.ScopeCommit
		m.commits = []git.Commit{
			{SHA: "aaaa", Short: "aaaa", Subject: "keep ○ ✔ ▎ ⇄", Files: []string{"pending.go"}},
			{SHA: "bbbb", Short: "bbbb", Subject: "reviewed subject", Files: []string{"done.go"}},
			{SHA: "cccc", Short: "cccc", Subject: "oldest subject", Files: []string{"old.go"}},
		}
		m.reviewed[m.reviewKey()] = map[string]uint64{commitScopePrefix + "bbbb\x00done.go": 1}
		m.commitPin.SHA = "bbbb"
		rows, current, errText := m.CommitPickerRows(tc.mode)
		if errText != "" || current != "bbbb" {
			t.Fatalf("%s: picker %q %q", tc.mode, current, errText)
		}
		for i, want := range []string{tc.pending + " aaaa keep ○ ✔ ▎ ⇄ (HEAD)", tc.done + " bbbb reviewed subject", tc.pending + " cccc oldest subject"} {
			if rows[i].Label != want || rows[i].Root != m.commits[i].SHA {
				t.Fatalf("%s row %d: %+v, want %q", tc.mode, i, rows[i], want)
			}
		}
		rail := strings.Split(ansi.Strip(m.CommitRail(RenderContext{GlyphMode: tc.mode}, 80, 5)), "\n")
		for i, prefix := range []string{" " + tc.pending + " cccc ", tc.selected + tc.done + " bbbb ", " " + tc.pending + " aaaa "} {
			if !strings.HasPrefix(rail[i], prefix) {
				t.Fatalf("%s row %d: %q, want prefix %q", tc.mode, i, rail[i], prefix)
			}
			left, _, _ := strings.Cut(rail[i], m.commits[2-i].Short)
			if ansi.StringWidth(left) != 3 {
				t.Fatalf("%s: glyph columns changed width", tc.mode)
			}
			if index, ok := m.commitMarks.At(i); !ok || index != 2-i {
				t.Fatalf("%s row %d: mark targets %d/%v", tc.mode, i, index, ok)
			}
		}
		if !strings.Contains(rail[2], "keep ○ ✔ ▎ ⇄") {
			t.Fatalf("literal subject changed: %s", rail[2])
		}
		if sha, ok := m.ClickCommit(0); !ok || sha != "cccc" {
			t.Fatalf("row click = %q/%v", sha, ok)
		}
		for _, filter := range []string{"reviewed subject", "bbbb", tc.done, "keep ○ ✔ ▎ ⇄"} {
			want := "bbbb"
			if filter == "keep ○ ✔ ▎ ⇄" {
				want = "aaaa"
			}
			for _, mouse := range []bool{false, true} {
				var picker Picker
				picker.Open(rows, "Review commit", PickCommit, current, PickerSource{}, "")
				picker.Key(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(filter)})
				filtered := picker.FilteredRows()
				// The literal subject contains the done mark too, so glyph filtering keeps both rows.
				if filter == tc.done && tc.mode != presentation.GlyphModeBasic {
					want = "aaaa"
				}
				if len(filtered) == 0 || filtered[0].Root != want {
					t.Fatalf("%s filter %q: %+v", tc.mode, filter, filtered)
				}
				picker.Body(RenderContext{GlyphMode: tc.mode}, 80, 25)
				var result PickerResult
				if mouse {
					result = picker.Click(2)
				} else {
					result = picker.Key(tea.KeyMsg{Type: tea.KeyEnter})
				}
				if result.Choice == nil || result.Choice.Value != want {
					t.Fatalf("%s filter %q mouse %v: %+v", tc.mode, filter, mouse, result)
				}
			}
		}
	}
}
