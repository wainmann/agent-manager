package review

import (
	"testing"

	diff "github.com/YoanWai/agent-manager/internal/diff/model"
	git "github.com/YoanWai/agent-manager/internal/git/value"
	tea "github.com/charmbracelet/bubbletea"
)

type carryRecorder struct {
	forgot  []string
	refresh int
	errs    []string
	asks    Requests
}

func (r *carryRecorder) ForgetPreferredRepo(repo string) { r.forgot = append(r.forgot, repo) }
func (r *carryRecorder) ReportErr(text string)           { r.errs = append(r.errs, text) }
func (r *carryRecorder) RequestRefresh()                 { r.refresh++ }

func (r *carryRecorder) ReviewCommands(reqs Requests) tea.Cmd {
	r.asks = reqs
	return func() tea.Msg { return reqs }
}

// A decision nobody accepted carries nothing: no complaint reaches the root and
// no work goes back through the command builder.
func TestCarryDropsAnUnacceptedDecision(t *testing.T) {
	root := &carryRecorder{}
	if cmd := Carry(root, ApplyResult{Error: "stale load", Refresh: true}); cmd != nil {
		t.Fatal("an unaccepted decision produced work")
	}
	if len(root.errs) != 0 || root.refresh != 0 {
		t.Fatalf("an unaccepted decision reached the root: %+v", root)
	}
}

// An accepted decision hands its error and its forgotten repo to the root, and
// its requests to the command builder.
func TestCarryReportsAndForwardsAnAcceptedDecision(t *testing.T) {
	root := &carryRecorder{}
	cmd := Carry(root, ApplyResult{
		Accepted:            true,
		Error:               "picked repo is gone",
		ForgetPreferredRepo: "/repo/gone",
		Refresh:             true,
		Requests:            Requests{StartupTick: true},
	})
	if cmd == nil {
		t.Fatal("an accepted decision produced no work")
	}
	if len(root.errs) != 1 || root.errs[0] != "picked repo is gone" {
		t.Fatalf("errors = %v", root.errs)
	}
	if len(root.forgot) != 1 || root.forgot[0] != "/repo/gone" {
		t.Fatalf("forgot = %v", root.forgot)
	}
	if root.refresh != 1 {
		t.Fatalf("refresh = %d", root.refresh)
	}
	if !root.asks.StartupTick {
		t.Fatalf("requests = %+v", root.asks)
	}
}

// A review with no resolved repo has nothing for a picker to list, and says so
// before a read starts; one that has a repo hands out the identity its picker is
// checked against.
func TestPickerReadNamesTheReviewItListsFor(t *testing.T) {
	var none Model
	if _, err := none.PickerRead(); err == nil {
		t.Fatal("a review with no repo should refuse a picker read")
	}

	var m Model
	m.active = true
	m.target = Target{ID: "s1"}
	m.repoSel = "/w/one"
	m.gen = 7
	source, err := m.PickerRead()
	if err != nil {
		t.Fatalf("PickerRead: %v", err)
	}
	if source.TargetID != "s1" || source.RepoRoot != "/w/one" || source.Generation != 7 {
		t.Fatalf("source = %+v", source)
	}
	if !m.PickerSourceCurrent(source) {
		t.Fatal("the identity handed out should name the open review")
	}
}

// A pick for a review that moved on is refused in the words the status bar
// repeats, instead of retargeting whatever is showing now; a base with no
// repository to save it against is refused the same way.
func TestDecidePickRefusesAStaleOrUnsaveablePick(t *testing.T) {
	var m Model
	m.active = true
	m.target = Target{ID: "s1"}
	m.repoSel = "/w/one"
	m.gen = 3

	stale := PickedMsg{Choice: PickerChoice{Kind: PickRepo, Value: "/w/two"}, Source: PickerSource{Generation: 1, TargetID: "s1", RepoRoot: "/w/one"}}
	if got := m.DecidePick(stale); got.Refused != "review changed; reopen the picker" {
		t.Fatalf("stale pick = %+v", got)
	}

	fresh := PickerSource{Generation: 3, TargetID: "s1", RepoRoot: "/w/one"}
	if got := m.DecidePick(PickedMsg{Choice: PickerChoice{Kind: PickBase, Value: "main"}, Source: fresh}); got.Refused != "review repository is gone" {
		t.Fatalf("base pick with no store root = %+v", got)
	}

	repo := m.DecidePick(PickedMsg{Choice: PickerChoice{Kind: PickRepo, Value: "/w/two"}, Source: fresh})
	if repo.Refused != "" || repo.Repo != "/w/two" || repo.Base != nil {
		t.Fatalf("repo pick = %+v", repo)
	}

	base := m.DecidePick(PickedMsg{Choice: PickerChoice{Kind: PickBase, Value: "main"}, Source: fresh, StoreRoot: "/w/one"})
	if base.Refused != "" || base.Base == nil || base.Base.Ref != "main" || base.Base.StoreRoot != "/w/one" || base.Base.TargetID != "s1" {
		t.Fatalf("base pick = %+v", base)
	}
}

// reanchorAnnotations looks its file up without a nil check, so ApplyFile
// must refuse a result whose index and path do not name a file in the set,
// even when a comment on that path is waiting to re-anchor.
func TestApplyFileRefusesAPathOutsideTheSetBeforeReanchoring(t *testing.T) {
	m := New(false)
	request := m.Open(Target{ID: "s1"}, git.ScopeUncommitted, "/repo")
	set := diff.Set{Repo: git.Repo{Root: "/repo"}, Files: []diff.FileDiff{{File: git.ChangedFile{Path: "a.go"}}}}
	m.ApplyLoad(LoadResult{
		TargetID: "s1", Scope: request.Scope, Generation: request.Generation, Set: set,
		RepoRoots: []string{"/repo"}, RepoRoot: "/repo", SavedLoaded: true,
		Saved: SavedState{Comments: []Comment{
			{ID: "aaaaaaaaaaaaaaa1", File: "a.go", Line: 1, Excerpt: "alpha()", Text: "stays"},
			{ID: "aaaaaaaaaaaaaaa2", File: "gone.go", Line: 1, Excerpt: "beta()", Text: "file left the set"},
		}},
	})
	loaded := diff.BuildFile(nil, []byte("x\nalpha()\n"), git.ChangedFile{Path: "a.go"}, git.FileStat{})
	result := func(index int, path string) FileResult {
		return FileResult{TargetID: "s1", Scope: request.Scope, Generation: request.Generation,
			RepoRoot: "/repo", Index: index, Path: path, File: loaded}
	}
	for _, refused := range []FileResult{result(0, "gone.go"), result(1, "gone.go"), result(-1, "a.go")} {
		if got := m.ApplyFile(refused); got.Accepted {
			t.Fatalf("accepted index %d path %q", refused.Index, refused.Path)
		}
	}
	if got := m.ApplyFile(result(0, "a.go")); !got.Accepted {
		t.Fatal("refused the file the set names")
	}
	if notes := m.Annotations(); notes[0].Line != 2 || notes[1].Line != 1 {
		t.Fatalf("lines = %d, %d, want 2, 1", notes[0].Line, notes[1].Line)
	}
}
