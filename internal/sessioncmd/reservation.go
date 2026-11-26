package sessioncmd

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/YoanWai/agent-manager/internal/lifecyclehook"
	"github.com/YoanWai/agent-manager/internal/store"
	"github.com/google/uuid"
)

const (
	DefaultReservationTTL = 30 * time.Minute
	MaxReservationTTL     = 4 * time.Hour
)

type Reservation struct {
	Pattern   string `json:"pattern" jsonschema:"path or glob the lease covers"`
	Mode      string `json:"mode" jsonschema:"exclusive (nobody else edits these paths) or shared (others may read or also edit)"`
	SessionID string `json:"session_id" jsonschema:"session holding the lease"`
	Holder    string `json:"holder" jsonschema:"name of the session holding the lease"`
	Note      string `json:"note,omitempty" jsonschema:"what the holder is doing there"`
	ExpiresIn string `json:"expires_in" jsonschema:"time left before the lease lapses on its own"`
	Mine      bool   `json:"mine" jsonschema:"whether the calling session holds it"`
}

type ReserveResult struct {
	Reserved  []Reservation `json:"reserved" jsonschema:"leases this session now holds"`
	Conflicts []Reservation `json:"conflicts,omitempty" jsonschema:"live leases held by other sessions that overlap what was asked for; these are advisory, so nothing is blocked, but the holders should be messaged before editing"`
}

func normalizeMode(mode string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "", store.ReservationExclusive:
		return store.ReservationExclusive, nil
	case store.ReservationShared:
		return store.ReservationShared, nil
	}
	return "", fmt.Errorf("unknown mode %q; use exclusive or shared", mode)
}

func (r *runtime) callerDir(caller store.Session) string {
	if current, err := r.driver.PaneCurrentPath(caller.ID); err == nil {
		return current
	}
	return caller.Cwd
}

type resolvedPattern struct {
	pattern     string
	globPattern string
}

var sqliteGlobEscaper = strings.NewReplacer(
	"[", "[[]",
	"*", "[*]",
	"?", "[?]",
)

func escapeSQLiteGlob(literal string) string {
	return sqliteGlobEscaper.Replace(literal)
}

func resolveReservationPatternsFromBase(resolvedBase string, patterns []string) ([]resolvedPattern, error) {
	seen := make(map[string]bool, len(patterns))
	resolved := make([]resolvedPattern, 0, len(patterns))
	for _, pattern := range patterns {
		item, err := resolveReservationPattern(resolvedBase, pattern)
		if err != nil {
			return nil, err
		}
		if seen[item.pattern] {
			continue
		}
		seen[item.pattern] = true
		resolved = append(resolved, item)
	}
	return resolved, nil
}

func resolveBaseDir(baseDir string) (string, error) {
	baseDir = strings.TrimSpace(baseDir)
	home, rest, hasHome, err := splitHomePrefix(baseDir)
	if err != nil {
		return "", err
	}
	if hasHome {
		resolvedHome, err := resolveLiteralPath(home)
		if err != nil {
			return "", err
		}
		return walkLiteralSegments(resolvedHome, nil, splitPathSegments(filepath.FromSlash(rest)))
	}
	return resolveLiteralPath(baseDir)
}

// resolveReservationPattern anchors a relative path or glob at resolvedBase
// and resolves symlinks segment-by-segment before collapsing "..", while
// keeping literal directory metacharacters escaped in globPattern.
func resolveReservationPattern(resolvedBase, pattern string) (resolvedPattern, error) {
	home, rest, hasHome, err := splitHomePrefix(pattern)
	if err != nil {
		return resolvedPattern{}, err
	}
	if hasHome {
		resolvedHome, err := resolveLiteralPath(home)
		if err != nil {
			return resolvedPattern{}, err
		}
		return walkPatternSegments(resolvedHome, nil, splitPathSegments(filepath.FromSlash(rest)))
	}
	native := filepath.FromSlash(pattern)
	if filepath.IsAbs(native) {
		vol := filepath.VolumeName(native)
		cur := vol + string(filepath.Separator)
		return walkPatternSegments(cur, nil, splitPathSegments(native[len(vol):]))
	}
	return walkPatternSegments(resolvedBase, nil, splitPathSegments(native))
}

func splitHomePrefix(path string) (home, rest string, hasHome bool, err error) {
	nativeHome := "~" + string(filepath.Separator)
	if path != "~" && !strings.HasPrefix(path, "~/") && !strings.HasPrefix(path, nativeHome) {
		return "", path, false, nil
	}
	home, err = os.UserHomeDir()
	if err != nil {
		return "", "", true, err
	}
	if path == "~" {
		return home, "", true, nil
	}
	return home, path[2:], true, nil
}

func resolveLiteralPath(raw string) (string, error) {
	native := filepath.FromSlash(raw)
	if !filepath.IsAbs(native) {
		abs, err := filepath.Abs(native)
		if err != nil {
			return "", err
		}
		native = abs
	}
	vol := filepath.VolumeName(native)
	cur := vol + string(filepath.Separator)
	return walkLiteralSegments(cur, nil, splitPathSegments(native[len(vol):]))
}

func splitPathSegments(path string) []string {
	raw := strings.Split(path, string(filepath.Separator))
	segments := make([]string, 0, len(raw))
	for _, seg := range raw {
		if seg == "" || seg == "." {
			continue
		}
		segments = append(segments, seg)
	}
	return segments
}

func walkLiteralSegments(cur string, missing []string, segments []string) (string, error) {
	for _, seg := range segments {
		if seg == ".." {
			if len(missing) > 0 {
				missing = missing[:len(missing)-1]
			} else {
				cur = filepath.Dir(cur)
			}
			continue
		}
		if len(missing) > 0 {
			missing = append(missing, seg)
			continue
		}
		resolved, err := filepath.EvalSymlinks(filepath.Join(cur, seg))
		if err == nil {
			cur = resolved
			continue
		}
		if errors.Is(err, os.ErrNotExist) {
			missing = append(missing, seg)
			continue
		}
		return "", err
	}
	return joinResolvedPath(cur, missing), nil
}

func walkPatternSegments(cur string, missing []string, segments []string) (resolvedPattern, error) {
	for i, seg := range segments {
		if seg == ".." {
			if len(missing) > 0 {
				missing = missing[:len(missing)-1]
			} else {
				cur = filepath.Dir(cur)
			}
			continue
		}
		if strings.ContainsAny(seg, "*?[") {
			// A segment with '[' (e.g. "repo[1]") that exists on disk is a
			// literal path entry rather than a glob character class.
			if len(missing) == 0 && !strings.ContainsAny(seg, "*?") {
				resolved, err := filepath.EvalSymlinks(filepath.Join(cur, seg))
				if err == nil {
					cur = resolved
					continue
				}
				if !errors.Is(err, os.ErrNotExist) {
					return resolvedPattern{}, err
				}
			}
			prefix := joinResolvedPath(cur, missing)
			tail := filepath.Join(segments[i:]...)
			return resolvedPattern{
				pattern:     filepath.Join(prefix, tail),
				globPattern: filepath.Join(escapeSQLiteGlob(prefix), tail),
			}, nil
		}
		if len(missing) > 0 {
			missing = append(missing, seg)
			continue
		}
		resolved, err := filepath.EvalSymlinks(filepath.Join(cur, seg))
		if err == nil {
			cur = resolved
			continue
		}
		if errors.Is(err, os.ErrNotExist) {
			missing = append(missing, seg)
			continue
		}
		return resolvedPattern{}, err
	}
	full := joinResolvedPath(cur, missing)
	return resolvedPattern{
		pattern:     full,
		globPattern: escapeSQLiteGlob(full),
	}, nil
}

func joinResolvedPath(cur string, missing []string) string {
	if len(missing) == 0 {
		return cur
	}
	parts := make([]string, 0, len(missing)+1)
	parts = append(parts, cur)
	parts = append(parts, missing...)
	return filepath.Join(parts...)
}

func releaseLegacyRelativeMatches(st *store.Store, sessionID, resolvedBase string, patterns []resolvedPattern, now time.Time) (int64, error) {
	active, err := st.Reservations(now)
	if err != nil {
		return 0, err
	}
	targets := make(map[string]bool, len(patterns))
	for _, item := range patterns {
		targets[item.pattern] = true
	}
	total := int64(0)
	for _, existing := range active {
		if existing.SessionID != sessionID || filepath.IsAbs(existing.Pattern) {
			continue
		}
		legacy, err := resolveReservationPattern(resolvedBase, existing.Pattern)
		if err != nil || !targets[legacy.pattern] {
			continue
		}
		released, err := st.Release(sessionID, existing.Pattern)
		if err != nil {
			return total, err
		}
		total += released
	}
	return total, nil
}

func (r *runtime) reservationInfo(reservation store.Reservation, names map[string]string, sessionID string, now time.Time) Reservation {
	return Reservation{
		Pattern:   reservation.Pattern,
		Mode:      reservation.Mode,
		SessionID: reservation.SessionID,
		Holder:    names[reservation.SessionID],
		Note:      reservation.Note,
		ExpiresIn: reservation.ExpiresAt.Sub(now).Round(time.Minute).String(),
		Mine:      reservation.SessionID == sessionID,
	}
}

// Reserve declares that this session is working on a set of paths. The
// lease is advisory and expires on its own, so an agent that dies never
// holds the repo hostage. Overlapping leases are reported rather than
// refused: the point is that both agents find out in time to talk.
func (s *Sessions) Reserve(sessionID string, patterns []string, mode, note string, ttl time.Duration) (ReserveResult, error) {
	mode, err := normalizeMode(mode)
	if err != nil {
		return ReserveResult{}, err
	}
	cleaned := make([]string, 0, len(patterns))
	for _, pattern := range patterns {
		pattern = strings.TrimSpace(pattern)
		if pattern != "" {
			cleaned = append(cleaned, pattern)
		}
	}
	if len(cleaned) == 0 {
		return ReserveResult{}, errors.New("no paths given; pass the files or globs you are about to edit")
	}
	switch {
	case ttl < 0 || ttl > MaxReservationTTL:
		return ReserveResult{}, fmt.Errorf("ttl %s is outside 0 to %s; reserve again when the lease lapses", ttl, MaxReservationTTL)
	case ttl == 0:
		ttl = DefaultReservationTTL
	}
	runtime, err := s.open()
	if err != nil {
		return ReserveResult{}, err
	}
	defer runtime.Close()
	caller, err := runtime.caller(sessionID)
	if err != nil {
		return ReserveResult{}, err
	}
	names, err := runtime.sessionNames()
	if err != nil {
		return ReserveResult{}, err
	}
	resolvedBase, err := resolveBaseDir(runtime.callerDir(caller))
	if err != nil {
		return ReserveResult{}, err
	}
	resolved, err := resolveReservationPatternsFromBase(resolvedBase, cleaned)
	if err != nil {
		return ReserveResult{}, err
	}
	now := time.Now()
	reservations := make([]store.Reservation, 0, len(resolved))
	for _, item := range resolved {
		reservations = append(reservations, store.Reservation{
			ID:          uuid.NewString()[:8],
			SessionID:   caller.ID,
			Pattern:     item.pattern,
			GlobPattern: item.globPattern,
			Mode:        mode,
			Note:        strings.TrimSpace(note),
			AcquiredAt:  now,
			ExpiresAt:   now.Add(ttl),
		})
	}
	// One store call leases the whole set, so a failure grants nothing
	// rather than leaving leases the caller was never told it holds.
	clashes, err := runtime.store.Reserve(reservations)
	if err != nil {
		return ReserveResult{}, err
	}
	if _, err := releaseLegacyRelativeMatches(runtime.store, caller.ID, resolvedBase, resolved, now); err != nil {
		return ReserveResult{}, err
	}
	result := ReserveResult{Reserved: make([]Reservation, 0, len(reservations))}
	for _, clash := range clashes {
		result.Conflicts = append(result.Conflicts, runtime.reservationInfo(clash, names, caller.ID, now))
	}
	for _, reservation := range reservations {
		result.Reserved = append(result.Reserved, runtime.reservationInfo(reservation, names, caller.ID, now))
		runtime.emitReservation(caller, lifecyclehook.ReservationInfo{
			Action:    lifecyclehook.ReservationReserve,
			Pattern:   reservation.Pattern,
			Mode:      reservation.Mode,
			ExpiresAt: reservation.ExpiresAt,
		})
	}
	return result, nil
}

// ReleaseFiles drops this session's leases once it is done editing.
func (s *Sessions) ReleaseFiles(sessionID string, patterns []string) (int, error) {
	runtime, err := s.open()
	if err != nil {
		return 0, err
	}
	defer runtime.Close()
	caller, err := runtime.caller(sessionID)
	if err != nil {
		return 0, err
	}
	cleaned := make([]string, 0, len(patterns))
	for _, pattern := range patterns {
		if pattern = strings.TrimSpace(pattern); pattern != "" {
			cleaned = append(cleaned, pattern)
		}
	}
	// Releasing everything is what an omitted list means, never what a list
	// that turned out to hold only blanks means.
	if len(patterns) > 0 && len(cleaned) == 0 {
		return 0, errors.New("no usable paths given; omit paths entirely to release every lease this session holds")
	}
	if len(cleaned) == 0 {
		released, err := runtime.store.Release(caller.ID, "")
		if err != nil {
			return 0, err
		}
		runtime.emitReservation(caller, lifecyclehook.ReservationInfo{Action: lifecyclehook.ReservationRelease})
		return int(released), nil
	}
	resolvedBase, err := resolveBaseDir(runtime.callerDir(caller))
	if err != nil {
		return 0, err
	}
	resolved, err := resolveReservationPatternsFromBase(resolvedBase, cleaned)
	if err != nil {
		return 0, err
	}
	total := int64(0)
	for _, item := range resolved {
		released, err := runtime.store.Release(caller.ID, item.pattern)
		if err != nil {
			return int(total), err
		}
		total += released
		runtime.emitReservation(caller, lifecyclehook.ReservationInfo{
			Action:  lifecyclehook.ReservationRelease,
			Pattern: item.pattern,
		})
	}
	legacyReleased, err := releaseLegacyRelativeMatches(runtime.store, caller.ID, resolvedBase, resolved, time.Now())
	if err != nil {
		return int(total), err
	}
	total += legacyReleased
	return int(total), nil
}

// Reservations lists every live lease, so an agent can see what the rest
// of the fleet is editing before it starts.
func (s *Sessions) Reservations(sessionID string) ([]Reservation, error) {
	runtime, err := s.open()
	if err != nil {
		return nil, err
	}
	defer runtime.Close()
	caller, err := runtime.caller(sessionID)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	if err := runtime.store.PruneReservations(now); err != nil {
		return nil, err
	}
	stored, err := runtime.store.Reservations(now)
	if err != nil {
		return nil, err
	}
	names, err := runtime.sessionNames()
	if err != nil {
		return nil, err
	}
	reservations := make([]Reservation, 0, len(stored))
	for _, reservation := range stored {
		reservations = append(reservations, runtime.reservationInfo(reservation, names, caller.ID, now))
	}
	return reservations, nil
}
