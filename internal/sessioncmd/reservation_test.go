package sessioncmd

import (
	"os"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"testing"
	"time"

	"github.com/YoanWai/agent-manager/internal/lifecyclehook"
	"github.com/YoanWai/agent-manager/internal/status"
	"github.com/YoanWai/agent-manager/internal/store"
	"github.com/YoanWai/agent-manager/internal/tmux"
	"github.com/google/uuid"
)

func TestReservationsSurfaceOverlapWithoutBlockingIt(t *testing.T) {
	h := newStoreSessionHarness(t)
	rivalID := h.addSessionRow(t, "rival")
	held, err := h.sessions.Reserve(h.caller.ID, []string{"internal/store/*.go"}, "", "adding the inbox table", 0)
	if err != nil {
		t.Fatalf("Reserve: %v", err)
	}
	if len(held.Reserved) != 1 || len(held.Conflicts) != 0 {
		t.Fatalf("first reservation = %+v", held)
	}
	if !held.Reserved[0].Mine || held.Reserved[0].Mode != "exclusive" {
		t.Fatalf("reservation = %+v", held.Reserved[0])
	}

	// A literal path under a reserved glob is the collision worktrees miss.
	clash, err := h.sessions.Reserve(rivalID, []string{"internal/store/store.go"}, "", "", 0)
	if err != nil {
		t.Fatalf("Reserve rival: %v", err)
	}
	if len(clash.Conflicts) != 1 {
		t.Fatalf("overlap was not reported: %+v", clash)
	}
	if clash.Conflicts[0].Holder != "calling-agent" || clash.Conflicts[0].Note != "adding the inbox table" {
		t.Fatalf("conflict = %+v", clash.Conflicts[0])
	}
	// Advisory: the second lease is still granted, so neither agent is stuck.
	if len(clash.Reserved) != 1 {
		t.Fatalf("an advisory lease must still be granted: %+v", clash)
	}

	listed, err := h.sessions.Reservations(h.caller.ID)
	if err != nil {
		t.Fatalf("Reservations: %v", err)
	}
	if len(listed) != 2 {
		t.Fatalf("listed = %+v", listed)
	}
}

func TestSharedLeasesOnlyClashWithExclusiveOnes(t *testing.T) {
	h := newStoreSessionHarness(t)
	rivalID := h.addSessionRow(t, "rival")
	if _, err := h.sessions.Reserve(h.caller.ID, []string{"docs/usage.md"}, "shared", "", 0); err != nil {
		t.Fatalf("Reserve shared: %v", err)
	}
	quiet, err := h.sessions.Reserve(rivalID, []string{"docs/usage.md"}, "shared", "", 0)
	if err != nil {
		t.Fatalf("Reserve second shared: %v", err)
	}
	if len(quiet.Conflicts) != 0 {
		t.Fatalf("two shared leases should not clash: %+v", quiet.Conflicts)
	}
	loud, err := h.sessions.Reserve(rivalID, []string{"docs/usage.md"}, "exclusive", "", 0)
	if err != nil {
		t.Fatalf("Reserve exclusive: %v", err)
	}
	if len(loud.Conflicts) != 1 {
		t.Fatalf("an exclusive lease over a shared one should clash: %+v", loud)
	}
	if _, err := h.sessions.Reserve(h.caller.ID, []string{"docs/usage.md"}, "sideways", "", 0); err == nil ||
		!strings.Contains(err.Error(), "unknown mode") {
		t.Fatalf("bad mode error = %v", err)
	}
	if _, err := h.sessions.Reserve(h.caller.ID, nil, "", "", 0); err == nil {
		t.Fatal("reserving nothing should be refused")
	}
	for _, ttl := range []time.Duration{-time.Minute, MaxReservationTTL + time.Minute} {
		if _, err := h.sessions.Reserve(h.caller.ID, []string{"docs/usage.md"}, "", "", ttl); err == nil ||
			!strings.Contains(err.Error(), "outside 0 to "+MaxReservationTTL.String()) {
			t.Fatalf("a %s lease = %v, want a refusal naming the bound", ttl, err)
		}
	}
}

func TestALapsedLeaseStopsBlockingAndReleaseClearsTheRest(t *testing.T) {
	h := newStoreSessionHarness(t)
	rivalID := h.addSessionRow(t, "rival")
	// A lease whose expiry is already behind us stands in for an agent that
	// died holding one, which is the case the whole advisory design rests on.
	lapsed := time.Now().Add(-time.Minute)
	if _, err := h.store.Reserve([]store.Reservation{{
		ID:         "lapsed01",
		SessionID:  rivalID,
		Pattern:    "internal/ui/*.go",
		Mode:       store.ReservationExclusive,
		AcquiredAt: lapsed.Add(-time.Hour),
		ExpiresAt:  lapsed,
	}}); err != nil {
		t.Fatalf("write a lapsed lease: %v", err)
	}
	fresh, err := h.sessions.Reserve(h.caller.ID, []string{"internal/ui/model.go"}, "", "", 0)
	if err != nil {
		t.Fatalf("Reserve after lapse: %v", err)
	}
	if len(fresh.Conflicts) != 0 {
		t.Fatalf("a lapsed lease still blocks: %+v", fresh.Conflicts)
	}

	if _, err := h.sessions.Reserve(h.caller.ID, []string{"a.go", "b.go"}, "", "", 0); err != nil {
		t.Fatalf("Reserve more: %v", err)
	}
	released, err := h.sessions.ReleaseFiles(h.caller.ID, []string{"a.go"})
	if err != nil || released != 1 {
		t.Fatalf("ReleaseFiles = %d, %v", released, err)
	}
	released, err = h.sessions.ReleaseFiles(h.caller.ID, nil)
	if err != nil || released != 2 {
		t.Fatalf("ReleaseFiles all = %d, %v", released, err)
	}
	listed, err := h.sessions.Reservations(h.caller.ID)
	if err != nil {
		t.Fatalf("Reservations: %v", err)
	}
	if len(listed) != 0 {
		t.Fatalf("leases survived a full release: %+v", listed)
	}
}

func TestReleasingBlankPathsDoesNotDropEveryLease(t *testing.T) {
	h := newStoreSessionHarness(t)
	if _, err := h.sessions.Reserve(h.caller.ID, []string{"a.go", "b.go"}, "", "", 0); err != nil {
		t.Fatalf("Reserve: %v", err)
	}
	if _, err := h.sessions.ReleaseFiles(h.caller.ID, []string{"  "}); err == nil ||
		!strings.Contains(err.Error(), "omit paths entirely") {
		t.Fatalf("blank release error = %v", err)
	}
	listed, err := h.sessions.Reservations(h.caller.ID)
	if err != nil {
		t.Fatalf("Reservations: %v", err)
	}
	if len(listed) != 2 {
		t.Fatalf("a blank path released real leases: %+v", listed)
	}
}

type reservationHarness struct {
	store    *store.Store
	sessions *Sessions
	events   *lifecyclehook.Recorder
}

// newReservationHarness backs Sessions with an open store directly, so
// reservation resolution tests run without requiring tmux.
func newReservationHarness(t *testing.T) *reservationHarness {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	t.Cleanup(func() {
		if err := st.Close(); err != nil {
			t.Errorf("close store: %v", err)
		}
	})
	rec := &lifecyclehook.Recorder{}
	backend := &Backend{
		opened: true,
		runtime: Runtime{
			Store:  st,
			Driver: &tmux.Driver{},
			Events: rec,
		},
	}
	return &reservationHarness{
		store:    st,
		sessions: NewSessionsWithBackend(backend, MCPVocabulary()),
		events:   rec,
	}
}

func (h *reservationHarness) addSession(t *testing.T, name, cwd string) string {
	t.Helper()
	id := uuid.NewString()[:8]
	if err := h.store.CreateSession(store.Session{
		ID:     id,
		Name:   name,
		Tool:   "echoer",
		Cwd:    cwd,
		Status: status.Idle,
	}); err != nil {
		t.Fatalf("create session %s: %v", name, err)
	}
	return id
}

func TestRelativeAndAbsolutePathsConflictInSameCheckout(t *testing.T) {
	h := newReservationHarness(t)
	repo := t.TempDir()
	first := h.addSession(t, "first", repo)
	second := h.addSession(t, "second", repo)
	third := h.addSession(t, "third", repo)

	absFile := filepath.Join(repo, "dir", "file.md")
	resolvedBase, err := resolveBaseDir(repo)
	if err != nil {
		t.Fatalf("resolveBaseDir: %v", err)
	}
	wantResolved, err := resolveReservationPattern(resolvedBase, absFile)
	if err != nil {
		t.Fatalf("resolveReservationPattern: %v", err)
	}
	wantPattern := wantResolved.pattern

	held, err := h.sessions.Reserve(first, []string{"dir/file.md"}, "exclusive", "relative claim", 0)
	if err != nil {
		t.Fatalf("Reserve first: %v", err)
	}
	if len(held.Reserved) != 1 || held.Reserved[0].Pattern != wantPattern {
		t.Fatalf("first reserved = %+v, want pattern %q", held.Reserved, wantPattern)
	}

	clash, err := h.sessions.Reserve(second, []string{absFile}, "exclusive", "absolute claim", 0)
	if err != nil {
		t.Fatalf("Reserve second: %v", err)
	}
	if len(clash.Conflicts) != 1 || clash.Conflicts[0].Holder != "first" {
		t.Fatalf("second conflicts = %+v, want first", clash.Conflicts)
	}

	both, err := h.sessions.Reserve(third, []string{absFile}, "exclusive", "", 0)
	if err != nil {
		t.Fatalf("Reserve third: %v", err)
	}
	if len(both.Conflicts) != 2 {
		t.Fatalf("third conflicts = %+v, want both first and second", both.Conflicts)
	}

	// Relative glob and absolute literal path overlap in both directions.
	globHolder := h.addSession(t, "glob-holder", repo)
	globClash, err := h.sessions.Reserve(globHolder, []string{"./dir/*.md"}, "exclusive", "", 0)
	if err != nil {
		t.Fatalf("Reserve glob: %v", err)
	}
	if len(globClash.Conflicts) != 3 {
		t.Fatalf("glob conflicts = %+v, want all three file holders", globClash.Conflicts)
	}
}

func TestReservationsResolveAgainstEachSessionsDirectory(t *testing.T) {
	h := newReservationHarness(t)
	repo := t.TempDir()
	subDir := filepath.Join(repo, "dir")
	otherSub := filepath.Join(repo, "other")
	otherRepo := t.TempDir()

	rootSess := h.addSession(t, "root", repo)
	subSess := h.addSession(t, "subdir", subDir)
	siblingSess := h.addSession(t, "sibling", otherSub)
	separateSess := h.addSession(t, "other-checkout", otherRepo)

	if _, err := h.sessions.Reserve(rootSess, []string{"dir/file.md"}, "exclusive", "", 0); err != nil {
		t.Fatalf("Reserve root: %v", err)
	}
	fromSub, err := h.sessions.Reserve(subSess, []string{"file.md"}, "exclusive", "", 0)
	if err != nil {
		t.Fatalf("Reserve subdir: %v", err)
	}
	if len(fromSub.Conflicts) != 1 || fromSub.Conflicts[0].Holder != "root" {
		t.Fatalf("subdir conflicts = %+v, want root", fromSub.Conflicts)
	}
	fromSibling, err := h.sessions.Reserve(siblingSess, []string{"../dir/file.md"}, "exclusive", "", 0)
	if err != nil {
		t.Fatalf("Reserve sibling: %v", err)
	}
	if len(fromSibling.Conflicts) != 2 {
		t.Fatalf("sibling conflicts = %+v, want root and subdir", fromSibling.Conflicts)
	}
	fromOtherRepo, err := h.sessions.Reserve(separateSess, []string{"dir/file.md"}, "exclusive", "", 0)
	if err != nil {
		t.Fatalf("Reserve other checkout: %v", err)
	}
	if len(fromOtherRepo.Conflicts) != 0 {
		t.Fatalf("separate checkout falsely conflicted: %+v", fromOtherRepo.Conflicts)
	}
}

func TestReleaseAndRenewalMatchAcrossRelativeAndAbsoluteForms(t *testing.T) {
	h := newReservationHarness(t)
	repo := t.TempDir()
	sess := h.addSession(t, "worker", repo)
	absFile := filepath.Join(repo, "dir", "file.md")

	h.events.Reset()
	first, err := h.sessions.Reserve(sess, []string{"dir/file.md", absFile}, "exclusive", "v1", 0)
	if err != nil {
		t.Fatalf("Reserve duplicate forms: %v", err)
	}
	if len(first.Reserved) != 1 {
		t.Fatalf("equivalent paths were not deduplicated: %+v", first.Reserved)
	}
	if got := h.events.Events(); len(got) != 1 {
		t.Fatalf("reserve events = %d, want 1", len(got))
	}

	// Renewing under the other spelling updates the same lease rather than adding a second row.
	if _, err := h.sessions.Reserve(sess, []string{absFile}, "exclusive", "v2", 0); err != nil {
		t.Fatalf("Reserve renew: %v", err)
	}
	listed, err := h.sessions.Reservations(sess)
	if err != nil {
		t.Fatalf("Reservations: %v", err)
	}
	if len(listed) != 1 || listed[0].Note != "v2" {
		t.Fatalf("listed after renew = %+v, want one updated lease", listed)
	}

	// Releasing by absolute path clears a lease taken by relative path.
	released, err := h.sessions.ReleaseFiles(sess, []string{absFile})
	if err != nil || released != 1 {
		t.Fatalf("ReleaseFiles absolute = %d, %v; want 1", released, err)
	}

	// Releasing by relative path clears a lease taken by absolute path.
	if _, err := h.sessions.Reserve(sess, []string{absFile}, "exclusive", "", 0); err != nil {
		t.Fatalf("Reserve absolute: %v", err)
	}
	released, err = h.sessions.ReleaseFiles(sess, []string{"./dir/file.md"})
	if err != nil || released != 1 {
		t.Fatalf("ReleaseFiles relative = %d, %v; want 1", released, err)
	}
	listed, err = h.sessions.Reservations(sess)
	if err != nil {
		t.Fatalf("Reservations final: %v", err)
	}
	if len(listed) != 0 {
		t.Fatalf("leases survived release: %+v", listed)
	}
}

func TestReservationsResolveSymlinkedDirectoriesAndGlobs(t *testing.T) {
	h := newReservationHarness(t)
	root := t.TempDir()
	realRepo := filepath.Join(root, "real-repo")
	if err := os.MkdirAll(filepath.Join(realRepo, "dir"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	linkRepo := filepath.Join(root, "link-repo")
	if err := os.Symlink(realRepo, linkRepo); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	viaLink := h.addSession(t, "via-link", linkRepo)
	viaReal := h.addSession(t, "via-real", realRepo)

	if _, err := h.sessions.Reserve(viaLink, []string{"dir/*.md"}, "exclusive", "", 0); err != nil {
		t.Fatalf("Reserve via symlink: %v", err)
	}
	clash, err := h.sessions.Reserve(viaReal, []string{filepath.Join(realRepo, "dir", "not-created-yet.md")}, "exclusive", "", 0)
	if err != nil {
		t.Fatalf("Reserve via real path: %v", err)
	}
	if len(clash.Conflicts) != 1 || clash.Conflicts[0].Holder != "via-link" {
		t.Fatalf("symlink vs real conflicts = %+v, want via-link", clash.Conflicts)
	}
}

func TestResolveReservationPatternEdgeCases(t *testing.T) {
	resolvedBase, err := resolveBaseDir(t.TempDir())
	if err != nil {
		t.Fatalf("resolveBaseDir: %v", err)
	}
	if home, err := os.UserHomeDir(); err == nil {
		want, err := resolveReservationPattern(resolvedBase, filepath.Join(home, "repo", "file.md"))
		if err != nil {
			t.Fatalf("resolve absolute home path: %v", err)
		}
		got, err := resolveReservationPattern(resolvedBase, "~/repo/file.md")
		if err != nil {
			t.Fatalf("resolve tilde path: %v", err)
		}
		if got.pattern != want.pattern {
			t.Fatalf("tilde path = %q, want %q", got.pattern, want.pattern)
		}
	}
	got, err := resolveReservationPattern(resolvedBase, "dir/./file.md")
	if err != nil {
		t.Fatalf("resolve relative path: %v", err)
	}
	if want := filepath.Join(resolvedBase, "dir", "file.md"); got.pattern != want {
		t.Fatalf("relative path = %q, want %q", got.pattern, want)
	}
}

func TestReservationsResolveSymlinksBeforeParentTraversal(t *testing.T) {
	h := newReservationHarness(t)
	root := t.TempDir()
	checkout := filepath.Join(root, "checkout")
	sharedSub := filepath.Join(root, "shared", "sub")
	if err := os.MkdirAll(checkout, 0o755); err != nil {
		t.Fatalf("mkdir checkout: %v", err)
	}
	if err := os.MkdirAll(sharedSub, 0o755); err != nil {
		t.Fatalf("mkdir shared: %v", err)
	}
	link := filepath.Join(checkout, "link")
	if err := os.Symlink(sharedSub, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	viaLink := h.addSession(t, "via-link", checkout)
	viaShared := h.addSession(t, "via-shared", filepath.Join(root, "shared"))

	held, err := h.sessions.Reserve(viaLink, []string{"link/../file.md"}, "exclusive", "", 0)
	if err != nil {
		t.Fatalf("Reserve link/../file.md: %v", err)
	}
	resolvedShared, err := resolveBaseDir(filepath.Join(root, "shared"))
	if err != nil {
		t.Fatalf("resolve shared: %v", err)
	}
	wantPattern := filepath.Join(resolvedShared, "file.md")
	if len(held.Reserved) != 1 || held.Reserved[0].Pattern != wantPattern {
		t.Fatalf("reserved = %+v, want %q", held.Reserved, wantPattern)
	}

	clash, err := h.sessions.Reserve(viaShared, []string{"file.md"}, "exclusive", "", 0)
	if err != nil {
		t.Fatalf("Reserve shared file.md: %v", err)
	}
	if len(clash.Conflicts) != 1 || clash.Conflicts[0].Holder != "via-link" {
		t.Fatalf("conflicts = %+v, want via-link", clash.Conflicts)
	}

	// Same behavior for absolute traversal and trailing glob after "..".
	globClash, err := h.sessions.Reserve(viaLink, []string{filepath.Join(checkout, "link", "..", "*.md")}, "exclusive", "", 0)
	if err != nil {
		t.Fatalf("Reserve link/../*.md: %v", err)
	}
	if len(globClash.Conflicts) != 1 || globClash.Conflicts[0].Holder != "via-shared" {
		t.Fatalf("glob traversal conflicts = %+v, want via-shared", globClash.Conflicts)
	}
}

func TestReservationsMatchWhenDirectoryContainsGlobMetacharacters(t *testing.T) {
	h := newReservationHarness(t)
	root := t.TempDir()
	bracketDir := filepath.Join(root, "repo[1]")
	plainDir := filepath.Join(root, "repo1")
	if err := os.MkdirAll(bracketDir, 0o755); err != nil {
		t.Fatalf("mkdir bracket: %v", err)
	}
	if err := os.MkdirAll(plainDir, 0o755); err != nil {
		t.Fatalf("mkdir plain: %v", err)
	}

	globSess := h.addSession(t, "glob-holder", bracketDir)
	fileSess := h.addSession(t, "file-holder", bracketDir)
	plainSess := h.addSession(t, "plain-holder", plainDir)

	held, err := h.sessions.Reserve(globSess, []string{"*.md"}, "exclusive", "", 0)
	if err != nil {
		t.Fatalf("Reserve *.md: %v", err)
	}
	resolvedBracket, err := resolveBaseDir(bracketDir)
	if err != nil {
		t.Fatalf("resolveBaseDir: %v", err)
	}
	if want := filepath.Join(resolvedBracket, "*.md"); held.Reserved[0].Pattern != want {
		t.Fatalf("stored pattern = %q, want %q", held.Reserved[0].Pattern, want)
	}

	clash, err := h.sessions.Reserve(fileSess, []string{"note.md"}, "exclusive", "", 0)
	if err != nil {
		t.Fatalf("Reserve note.md: %v", err)
	}
	if len(clash.Conflicts) != 1 || clash.Conflicts[0].Holder != "glob-holder" {
		t.Fatalf("conflicts = %+v, want glob-holder", clash.Conflicts)
	}

	// Reverse direction: reserving glob after literal file also conflicts.
	globSess2 := h.addSession(t, "glob-holder-2", bracketDir)
	reverse, err := h.sessions.Reserve(globSess2, []string{"*.md"}, "exclusive", "", 0)
	if err != nil {
		t.Fatalf("Reserve *.md second: %v", err)
	}
	if len(reverse.Conflicts) != 2 {
		t.Fatalf("reverse conflicts = %+v, want glob-holder and file-holder", reverse.Conflicts)
	}

	// Plain repo1 directory must stay isolated from repo[1].
	unrelated, err := h.sessions.Reserve(plainSess, []string{"note.md"}, "exclusive", "", 0)
	if err != nil {
		t.Fatalf("Reserve plain note.md: %v", err)
	}
	if len(unrelated.Conflicts) != 0 {
		t.Fatalf("repo1 falsely conflicted with repo[1]: %+v", unrelated.Conflicts)
	}
}

func TestReservationsPropagateHomeExpansionErrors(t *testing.T) {
	h := newReservationHarness(t)
	repo := t.TempDir()
	sess := h.addSession(t, "worker", repo)

	if _, err := h.sessions.Reserve(sess, []string{"existing.md"}, "exclusive", "", 0); err != nil {
		t.Fatalf("Reserve baseline: %v", err)
	}

	homeVar := "HOME"
	if goruntime.GOOS == "windows" {
		homeVar = "USERPROFILE"
	}
	t.Setenv(homeVar, "")

	if _, err := h.sessions.Reserve(sess, []string{"~/repo/file.md"}, "exclusive", "", 0); err == nil {
		t.Fatalf("Reserve with unset %s unexpectedly succeeded", homeVar)
	}
	if _, err := h.sessions.ReleaseFiles(sess, []string{"~/repo/file.md"}); err == nil {
		t.Fatalf("ReleaseFiles with unset %s unexpectedly succeeded", homeVar)
	}
	// Non-tilde paths remain unaffected and existing leases remain intact.
	if _, err := h.sessions.Reserve(sess, []string{"other.md"}, "exclusive", "", 0); err != nil {
		t.Fatalf("Reserve non-tilde with unset %s: %v", homeVar, err)
	}
	listed, err := h.sessions.Reservations(sess)
	if err != nil {
		t.Fatalf("Reservations: %v", err)
	}
	if len(listed) != 2 {
		t.Fatalf("listed = %+v, want 2 leases", listed)
	}
}

func TestReservationsPropagateSymlinkErrors(t *testing.T) {
	h := newReservationHarness(t)
	root := t.TempDir()
	sess := h.addSession(t, "worker", root)

	loopA := filepath.Join(root, "loop-a")
	loopB := filepath.Join(root, "loop-b")
	if err := os.Symlink(loopB, loopA); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := os.Symlink(loopA, loopB); err != nil {
		t.Fatalf("symlink loop-b: %v", err)
	}

	if _, err := h.sessions.Reserve(sess, []string{"loop-a/file.md"}, "exclusive", "", 0); err == nil {
		t.Fatalf("Reserve on symlink loop unexpectedly succeeded")
	}
	if _, err := h.sessions.ReleaseFiles(sess, []string{"loop-a/file.md"}); err == nil {
		t.Fatalf("ReleaseFiles on symlink loop unexpectedly succeeded")
	}
	listed, err := h.sessions.Reservations(sess)
	if err != nil {
		t.Fatalf("Reservations: %v", err)
	}
	if len(listed) != 0 {
		t.Fatalf("symlink loop stored lease unexpectedly: %+v", listed)
	}

	if goruntime.GOOS != "windows" && os.Geteuid() != 0 {
		locked := filepath.Join(root, "locked")
		if err := os.MkdirAll(filepath.Join(locked, "sub"), 0o755); err != nil {
			t.Fatalf("mkdir locked: %v", err)
		}
		if err := os.Chmod(locked, 0o000); err != nil {
			t.Fatalf("chmod: %v", err)
		}
		t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })

		if _, err := h.sessions.Reserve(sess, []string{"locked/sub/file.md"}, "exclusive", "", 0); err == nil {
			t.Fatalf("Reserve under unreadable dir unexpectedly succeeded")
		}
		if _, err := h.sessions.ReleaseFiles(sess, []string{"locked/sub/file.md"}); err == nil {
			t.Fatalf("ReleaseFiles under unreadable dir unexpectedly succeeded")
		}
	}
}

func TestReservationsReleaseAndRenewLegacyRelativeLeases(t *testing.T) {
	h := newReservationHarness(t)
	repo := t.TempDir()
	sess := h.addSession(t, "worker", repo)

	now := time.Now()
	// Seed a legacy active reservation stored with a relative pattern prior to normalization.
	if _, err := h.store.Reserve([]store.Reservation{{
		ID:         "legacy01",
		SessionID:  sess,
		Pattern:    "dir/file.md",
		Mode:       store.ReservationExclusive,
		Note:       "pre-upgrade",
		AcquiredAt: now,
		ExpiresAt:  now.Add(time.Hour),
	}}); err != nil {
		t.Fatalf("seed legacy reservation: %v", err)
	}

	// Renewing via resolved or relative spelling supersedes the legacy relative row cleanly.
	if _, err := h.sessions.Reserve(sess, []string{filepath.Join(repo, "dir", "file.md")}, "shared", "renewed", 0); err != nil {
		t.Fatalf("Reserve renew legacy: %v", err)
	}
	listed, err := h.sessions.Reservations(sess)
	if err != nil {
		t.Fatalf("Reservations after renew: %v", err)
	}
	if len(listed) != 1 || listed[0].Note != "renewed" || listed[0].Mode != store.ReservationShared || !filepath.IsAbs(listed[0].Pattern) {
		t.Fatalf("leases after legacy renew = %+v, want one canonical lease", listed)
	}

	// Seed a second legacy relative lease and verify ReleaseFiles clears it by relative or absolute path.
	if _, err := h.store.Reserve([]store.Reservation{{
		ID:         "legacy02",
		SessionID:  sess,
		Pattern:    "dir/other.md",
		Mode:       store.ReservationExclusive,
		AcquiredAt: now,
		ExpiresAt:  now.Add(time.Hour),
	}}); err != nil {
		t.Fatalf("seed legacy02: %v", err)
	}
	released, err := h.sessions.ReleaseFiles(sess, []string{filepath.Join(repo, "dir", "other.md")})
	if err != nil || released != 1 {
		t.Fatalf("ReleaseFiles legacy = %d, %v; want 1", released, err)
	}
}
