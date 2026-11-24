package rail

import (
	"maps"
	"testing"
)

func TestPathsWithSessionsKeepsOnlyAncestorsOfGroupsWithSessions(t *testing.T) {
	paths := map[string]bool{"a": true, "a/b": true, "a/b/c": true, "ab": true, "d": true, "d/e": true}
	sessions := map[string][]Session{
		"a/b":  {{ID: "one"}},
		"ab/x": {{ID: "two"}},
		"":     {{ID: "root"}},
	}
	got := pathsWithSessions(paths, sessions)
	want := map[string]bool{"a": true, "a/b": true, "ab": true}
	if !maps.Equal(got, want) {
		t.Fatalf("kept = %v, want %v", got, want)
	}
	for path := range paths {
		inSubtree := false
		for group := range sessions {
			inSubtree = inSubtree || InGroupSubtree(group, path)
		}
		if got[path] != inSubtree {
			t.Fatalf("path %q kept=%v, but a session in its subtree is %v", path, got[path], inSubtree)
		}
	}
}
