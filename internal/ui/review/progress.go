package review

import (
	"strings"

	diff "github.com/YoanWai/agent-manager/internal/diff/model"
)

func (m *Model) ToggleReviewed() Requests {
	fd := m.currentFile()
	if fd == nil || !fd.Loaded() || m.fileHidden(fd) {
		return Requests{}
	}
	requests := m.toggleFileMark(fd)
	if !m.FileReviewed(fd.File.Path) {
		return requests
	}
	marks := m.reviewed[m.reviewKey()]
	for step := 1; step < len(m.set.Files); step++ {
		next := (m.fileIdx + step) % len(m.set.Files)
		if marks[m.markKey(m.set.Files[next].File.Path)] == 0 && !m.fileHidden(&m.set.Files[next]) {
			return mergeRequests(requests, m.SwitchFile(next-m.fileIdx))
		}
	}
	return requests
}

// ClickFileMark toggles the reviewed mark of the file whose glyph row line
// of the file list painted, leaving the file in view where it is. A file not
// loaded yet loads first, since its mark hashes the rendering.
func (m *Model) ClickFileMark(line int) Requests {
	index, ok := m.fileMarks.At(line)
	if !ok || m.paused() {
		return Requests{}
	}
	fd := &m.set.Files[index]
	if fd.Loaded() {
		return m.toggleFileMark(fd)
	}
	request, ok := m.requestFile(index)
	if !ok {
		return Requests{}
	}
	m.markFile = pendingMark{generation: m.gen, path: fd.File.Path}
	return Requests{Files: []FileRequest{request}}
}

// toggleFileMark marks a loaded file reviewed, or clears its mark.
func (m *Model) toggleFileMark(fd *diff.FileDiff) Requests {
	key := m.reviewKey()
	marks := m.reviewed[key]
	if marks == nil {
		marks = make(map[string]uint64)
		m.reviewed[key] = marks
	}
	markKey := m.markKey(fd.File.Path)
	if marks[markKey] != 0 {
		delete(marks, markKey)
	} else {
		marks[markKey] = ContentHash(fd)
	}
	return Requests{Save: m.saveRequest()}
}

func (m Model) FileReviewed(path string) bool {
	return m.reviewed[m.reviewKey()][m.markKey(path)] != 0
}

func (m *Model) clearStaleMarks() bool {
	marks := m.reviewed[m.reviewKey()]
	if len(marks) == 0 {
		return false
	}
	files := make(map[string]*diff.FileDiff, len(m.set.Files))
	for i := range m.set.Files {
		files[m.set.Files[i].File.Path] = &m.set.Files[i]
	}
	scopeKey := m.scopeKey()
	changed := false
	for markKey, stored := range marks {
		if stored == 0 {
			continue
		}
		scope, path, _ := strings.Cut(markKey, "\x00")
		if scope != scopeKey {
			continue
		}
		fd := files[path]
		if fd != nil && fd.Loaded() && ContentHash(fd) != stored {
			delete(marks, markKey)
			changed = true
		}
	}
	return changed
}

func (m *Model) clearStaleMark(path string) bool {
	marks := m.reviewed[m.reviewKey()]
	key := m.markKey(path)
	stored := marks[key]
	if stored == 0 {
		return false
	}
	fd := m.fileByPath(path)
	if fd != nil && fd.Loaded() && ContentHash(fd) != stored {
		delete(marks, key)
		return true
	}
	return false
}

func (m *Model) markMissingCommentsOutdated() bool {
	fallback := m.rounds[m.reviewKey()].Scope
	paths := make(map[string]bool, len(m.set.Files))
	for i := range m.set.Files {
		paths[m.set.Files[i].File.Path] = true
	}
	notes := m.annotations[m.reviewKey()]
	changed := false
	for i := range notes {
		if notes[i].round == 0 || notes[i].outdated || paths[notes[i].file] {
			continue
		}
		scope := notes[i].scope
		if scope == "" {
			scope = fallback
		}
		if !m.inScope(scope) {
			continue
		}
		notes[i].outdated = true
		changed = true
	}
	return changed
}

func (m *Model) reanchorAnnotations(path string) bool {
	fd := m.fileByPath(path)
	notes := m.annotations[m.reviewKey()]
	currentHash := ContentHash(fd)
	var index map[anchor]anchorMatch
	occupied := map[slot]int{}
	for i := range notes {
		if notes[i].file == path {
			occupied[noteSlot(&notes[i])]++
		}
	}
	changed := false
	for i := range notes {
		note := &notes[i]
		if note.file != path {
			continue
		}
		scope := note.scope
		if scope == "" && note.round > 0 {
			scope = m.rounds[m.reviewKey()].Scope
		}
		if !m.inScope(scope) {
			continue
		}
		if note.hash != 0 && note.hash == currentHash {
			if note.outdated {
				note.outdated = false
				changed = true
			}
			continue
		}
		if note.excerpt == "" {
			if note.round > 0 && !note.outdated {
				note.outdated = true
				changed = true
			}
			continue
		}
		if index == nil {
			index = anchorIndex(fd)
		}
		match := index[anchor{excerpt: note.excerpt, deleted: note.deleted}]
		target := slot{line: match.line, deleted: note.deleted}
		own := noteSlot(note)
		taken := occupied[target]
		if own == target {
			taken--
		}
		if match.count == 1 && taken == 0 {
			if note.line != match.line || note.hash != currentHash || note.outdated {
				occupied[own]--
				occupied[target]++
				note.line = match.line
				note.hash = currentHash
				note.outdated = false
				changed = true
			}
		} else if note.round > 0 && !note.outdated {
			note.outdated = true
			changed = true
		}
	}
	return changed
}

// slot is the line a comment sits on within the file: two comments never
// share one.
type slot struct {
	line    int
	deleted bool
}

func noteSlot(note *annotation) slot {
	return slot{line: note.line, deleted: note.deleted}
}

type anchor struct {
	excerpt string
	deleted bool
}

// anchorMatch counts the lines of a file an excerpt matches, with the
// number of the last one.
type anchorMatch struct {
	count int
	line  int
}

func anchorIndex(fd *diff.FileDiff) map[anchor]anchorMatch {
	index := make(map[anchor]anchorMatch, len(fd.Lines))
	for _, line := range fd.Lines {
		if line.Kind == diff.Gap {
			continue
		}
		num, deleted := annotationLine(line)
		key := anchor{excerpt: excerptOf(line.Text), deleted: deleted}
		match := index[key]
		index[key] = anchorMatch{count: match.count + 1, line: num}
	}
	return index
}

func (m Model) fileByPath(path string) *diff.FileDiff {
	for i := range m.set.Files {
		if m.set.Files[i].File.Path == path {
			return &m.set.Files[i]
		}
	}
	return nil
}

func (m *Model) saveRequest() *SaveRequest {
	if m.target.ID == "" || m.repoSel == "" {
		return nil
	}
	return &SaveRequest{TargetID: m.target.ID, RepoRoot: m.repoSel, State: m.savedState(m.reviewKey())}
}

func mergeRequests(left, right Requests) Requests {
	if right.Load != nil {
		left.Load = right.Load
	}
	left.Files = append(left.Files, right.Files...)
	if right.Highlight != nil {
		left.Highlight = right.Highlight
	}
	if right.Save != nil {
		left.Save = right.Save
	}
	if right.Status != nil {
		left.Status = right.Status
	}
	if right.Handle != nil {
		left.Handle = right.Handle
	}
	if right.Send != nil {
		left.Send = right.Send
	}
	if right.FileCheck != nil {
		left.FileCheck = right.FileCheck
	}
	if right.WidgetCmd != nil {
		left.WidgetCmd = right.WidgetCmd
	}
	left.StartupTick = left.StartupTick || right.StartupTick
	return left
}

func annotationLine(line diff.Line) (int, bool) {
	if line.Kind == diff.Del {
		return line.OldNum, true
	}
	return line.NewNum, false
}

func AnnotationLine(line diff.Line) (int, bool) { return annotationLine(line) }

func excerptOf(text string) string {
	excerpt := strings.TrimSpace(stripControls(text))
	if runes := []rune(excerpt); len(runes) > 60 {
		return string(runes[:60])
	}
	return excerpt
}

func Excerpt(text string) string { return excerptOf(text) }
