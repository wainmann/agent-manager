package git

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/YoanWai/agent-manager/internal/git/value"
)

type Worktree = value.Worktree

func (d *Driver) Worktrees(root string) ([]Worktree, error) {
	out, err := d.run(root, "worktree", "list", "--porcelain")
	if err != nil {
		return nil, err
	}
	var worktrees []Worktree
	var current Worktree
	for _, line := range strings.Split(out, "\n") {
		switch {
		case strings.HasPrefix(line, "worktree "):
			current = Worktree{Root: filepath.FromSlash(strings.TrimPrefix(line, "worktree "))}
		case strings.HasPrefix(line, "HEAD "):
			sha := strings.TrimPrefix(line, "HEAD ")
			if len(sha) > 7 {
				sha = sha[:7]
			}
			current.Branch = sha
		case strings.HasPrefix(line, "branch "):
			current.Branch = strings.TrimPrefix(strings.TrimPrefix(line, "branch "), "refs/heads/")
		case line == "":
			if current.Root != "" {
				worktrees = append(worktrees, current)
				current = Worktree{}
			}
		}
	}
	if current.Root != "" {
		worktrees = append(worktrees, current)
	}
	return worktrees, nil
}

func (d *Driver) RepoRoot(dir string) (string, error) {
	top, err := d.toplevel(dir)
	if err != nil {
		return "", fmt.Errorf("not inside a git repository: %s", dir)
	}
	return top, nil
}

var worktreeNamePattern = regexp.MustCompile(`[^a-zA-Z0-9._-]+`)

func sanitizeWorktreeName(name string) string {
	return strings.Trim(worktreeNamePattern.ReplaceAllString(name, "-"), "-.")
}

// worktreeBase picks the ref a session worktree branches from and is
// measured against before removal: the override when one is set, else the
// repo's default base, else HEAD. "" means the repo has no commits.
func (d *Driver) worktreeBase(dir, override string) (string, error) {
	if override != "" {
		if err := d.ResolveRef(dir, override); err != nil {
			return "", fmt.Errorf("base %w", err)
		}
		return override, nil
	}
	base, err := d.DefaultBase(dir)
	if err != nil {
		return "", err
	}
	if base != "" {
		return base, nil
	}
	if d.ResolveRef(dir, "HEAD") == nil {
		return "HEAD", nil
	}
	return "", nil
}

// fetchTimeout bounds a fetch stuck on a dead connection; a working one
// lands well inside it.
const fetchTimeout = 30 * time.Second

// FetchBase updates the remote-tracking branch a worktree would branch
// from, and only that branch, so a session starts from the remote's tip
// rather than from whatever the last fetch left behind.
func (d *Driver) FetchBase(dir, override string) error {
	base, err := d.worktreeBase(dir, override)
	if err != nil || base == "" {
		return err
	}
	remote, branch, err := d.remoteBranch(dir, base)
	if err != nil || remote == "" {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), fetchTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, d.bin, "fetch", "--quiet", remote, branch)
	cmd.Dir = dir
	// Nobody can answer a background fetch, so git's, askpass's and ssh's tty prompts are all shut off.
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "SSH_ASKPASS_REQUIRE=never")
	killFetchTree(cmd)
	cmd.WaitDelay = time.Second
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git fetch %s %s: %w: %s", remote, branch, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// remoteBranch splits a remote-tracking ref into its remote and branch,
// both "" for any other ref.
func (d *Driver) remoteBranch(dir, ref string) (remote, branch string, err error) {
	full, err := d.run(dir, "rev-parse", "--symbolic-full-name", ref)
	if err != nil {
		return "", "", err
	}
	remotes, err := d.run(dir, "remote")
	if err != nil {
		return "", "", err
	}
	for _, candidate := range strings.Fields(remotes) {
		prefix := "refs/remotes/" + candidate + "/"
		if strings.HasPrefix(full, prefix) && len(candidate) > len(remote) {
			remote, branch = candidate, strings.TrimPrefix(full, prefix)
		}
	}
	return remote, branch, nil
}

// pushToOrigin sends git push to origin for a branch that tracks another
// remote: in a fork layout that remote is the parent, which the user cannot
// push to. A remote.pushDefault the user set already decides this.
func (d *Driver) pushToOrigin(root, branch string) error {
	tracked, err := d.configValue(root, "branch."+branch+".remote")
	if err != nil || tracked == "" || tracked == "origin" {
		return err
	}
	pushDefault, err := d.configValue(root, "remote.pushDefault")
	if err != nil || pushDefault != "" {
		return err
	}
	origin, err := d.configValue(root, "remote.origin.url")
	if err != nil || origin == "" {
		return err
	}
	_, err = d.run(root, "config", "branch."+branch+".pushRemote", "origin")
	return err
}

// configValue reads a git config key, "" when it is unset.
func (d *Driver) configValue(dir, key string) (string, error) {
	value, err := d.run(dir, "config", "--get", key)
	// --get exits 1 for a missing key; anything else is a real failure.
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
		return "", nil
	}
	return value, err
}

// worktreePlacement is where a session of this name keeps its worktree:
// a directory beside the repo and the am/ branch checked out in it.
func worktreePlacement(root, name string) (string, string) {
	return filepath.Join(filepath.Dir(root), filepath.Base(root)+"-worktrees", name), "am/" + name
}

func (d *Driver) AddWorktree(root, sessionName, baseOverride string) (string, string, error) {
	name := sanitizeWorktreeName(sessionName)
	if name == "" {
		return "", "", fmt.Errorf("session name %q leaves nothing usable for a worktree directory", sessionName)
	}
	path, branch := worktreePlacement(root, name)
	if _, err := os.Stat(path); err == nil {
		return "", "", fmt.Errorf("worktree path already exists: %s", path)
	}
	base, err := d.worktreeBase(root, baseOverride)
	if err != nil {
		return "", "", err
	}
	if base == "" {
		return "", "", fmt.Errorf("no base ref for a worktree in %s: repository has no commits", root)
	}
	if _, err := d.run(root, "worktree", "add", "-b", branch, path, base); err != nil {
		return "", "", err
	}
	if err := d.pushToOrigin(root, branch); err != nil {
		_, removeErr := d.run(root, "worktree", "remove", "--force", path)
		_, branchErr := d.run(root, "branch", "-D", branch)
		return "", "", errors.Join(err, removeErr, branchErr)
	}
	return path, branch, nil
}

// RenameWorktreeBranch gives a managed worktree the branch a session of
// newName would have received at spawn without moving its live directory.
// The recorded branch is returned untouched when the worktree directory or
// branch no longer exists, or the worktree has left the recorded branch
// (switched to another, or detached mid-rebase), which is what a worktree
// renamed, removed or repointed by hand looks like.
func (d *Driver) RenameWorktreeBranch(root, path, branch, newName string) (string, error) {
	name := sanitizeWorktreeName(newName)
	if name == "" {
		return "", fmt.Errorf("session name %q leaves nothing usable for a worktree branch", newName)
	}
	newBranch := "am/" + name
	if newBranch == branch {
		return branch, nil
	}
	if _, err := os.Stat(path); err != nil {
		if !os.IsNotExist(err) {
			return "", err
		}
		return branch, nil
	}
	if _, err := d.run(root, "rev-parse", "--verify", "--quiet", "refs/heads/"+branch); err != nil {
		// --verify --quiet exits 1 when the ref is absent; anything else is a real failure.
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) || exitErr.ExitCode() != 1 {
			return "", err
		}
		return branch, nil
	}
	currentBranch, err := d.run(path, "symbolic-ref", "--short", "-q", "HEAD")
	if err != nil {
		// -q exits 1 on a detached HEAD; anything else is a real failure.
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) || exitErr.ExitCode() != 1 {
			return "", fmt.Errorf("read worktree branch: %w", err)
		}
		return branch, nil
	}
	if currentBranch != branch {
		return branch, nil
	}
	if _, err := d.run(root, "rev-parse", "--verify", "--quiet", "refs/heads/"+newBranch); err == nil {
		return "", fmt.Errorf("branch already exists: %s", newBranch)
	}
	if err := d.moveCheckedOutBranch(root, path, branch, newBranch); err != nil {
		return "", err
	}
	return newBranch, nil
}

// moveCheckedOutBranch renames the branch checked out at path while git
// commands keep running there. `git branch -m` deletes the old ref before
// it repoints HEAD, so a reader in that gap finds HEAD naming a missing
// branch and fails. Copying first, then repointing HEAD, then deleting the
// old ref keeps HEAD on a branch that exists at every step. The old ref
// stays locked at the copied tip while HEAD moves, so a commit racing the
// rename lands on one branch or fails to lock, and is never left behind.
func (d *Driver) moveCheckedOutBranch(root, path, branch, newBranch string) error {
	oldRef, newRef := "refs/heads/"+branch, "refs/heads/"+newBranch
	// -c carries the branch config and reflog, as -m does.
	if _, err := d.run(root, "branch", "-c", branch, newBranch); err != nil {
		return err
	}
	tip, err := d.run(root, "rev-parse", "--verify", newRef)
	if err != nil {
		return errors.Join(err, d.dropCopy(root, newBranch, ""))
	}
	release, err := d.holdRef(root, oldRef, tip)
	if err != nil {
		return errors.Join(fmt.Errorf("branch %s changed or is in use by another git command: %w", branch, err), d.dropCopy(root, newBranch, tip))
	}
	_, err = d.run(path, "symbolic-ref", "-m", "branch: renamed "+oldRef+" to "+newRef, "HEAD", newRef)
	releaseErr := release()
	if err != nil {
		return errors.Join(err, releaseErr, d.dropCopy(root, newBranch, tip))
	}
	// HEAD is on the new branch, so the rename has happened and the caller
	// must record it. An old ref that cannot be deleted at the copied tip
	// was moved by something outside this worktree; it stays as a plain
	// branch holding that work.
	if _, err := d.run(root, "update-ref", "-d", oldRef, tip); err == nil {
		_ = d.dropBranchConfig(root, branch)
	}
	return nil
}

// dropCopy undoes the copy of a rename that could not switch HEAD.
func (d *Driver) dropCopy(root, branch, tip string) error {
	args := []string{"update-ref", "-d", "refs/heads/" + branch}
	if tip != "" {
		args = append(args, tip)
	}
	_, err := d.run(root, args...)
	return errors.Join(err, d.dropBranchConfig(root, branch))
}

// holdRef locks ref, which must point at tip, until release is called.
func (d *Driver) holdRef(root, ref, tip string) (func() error, error) {
	cmd := exec.Command(d.bin, "update-ref", "--stdin")
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "SSH_ASKPASS_REQUIRE=never")
	detachTerminal(cmd)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	finish := func() error {
		_ = stdin.Close()
		if err := cmd.Wait(); err != nil {
			return fmt.Errorf("git update-ref: %w: %s", err, strings.TrimSpace(stderr.String()))
		}
		return nil
	}
	if _, err := io.WriteString(stdin, "start\nverify "+ref+" "+tip+"\nprepare\n"); err != nil {
		return nil, errors.Join(err, finish())
	}
	replies := bufio.NewScanner(stdout)
	for _, want := range []string{"start: ok", "prepare: ok"} {
		if !replies.Scan() || replies.Text() != want {
			return nil, errors.Join(fmt.Errorf("could not lock %s at %s", ref, tip), finish())
		}
	}
	return func() error {
		if _, err := io.WriteString(stdin, "abort\n"); err != nil {
			return errors.Join(err, finish())
		}
		return finish()
	}, nil
}

// dropBranchConfig removes branch.<name>.* when it exists, as
// `git branch -D` would.
func (d *Driver) dropBranchConfig(root, branch string) error {
	if _, err := d.run(root, "config", "--get-regexp", `^branch\.`+regexp.QuoteMeta(branch)+`\.`); err != nil {
		// --get-regexp exits 1 when nothing matches; anything else is a real failure.
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
			return nil
		}
		return err
	}
	_, err := d.run(root, "config", "--remove-section", "branch."+branch)
	return err
}

// RemoveWorktreeIfClean removes a session's worktree and its am/ branch
// only when nothing would be lost: no uncommitted or untracked files, and
// no commits that exist nowhere else. A kept worktree is not an error.
func (d *Driver) RemoveWorktreeIfClean(root, path, branch, baseOverride string) (bool, error) {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return false, nil
	}
	porcelain, err := d.run(path, "status", "--porcelain")
	if err != nil {
		return false, err
	}
	if porcelain != "" {
		return false, nil
	}
	base, err := d.worktreeBase(root, baseOverride)
	if err != nil {
		return false, err
	}
	if base == "" {
		return false, fmt.Errorf("no base ref in %s to compare %s against", root, branch)
	}
	// Only HEAD is read in the worktree: a base of HEAD means the root's checkout.
	head, err := d.run(path, "rev-parse", "HEAD")
	if err != nil {
		return false, err
	}
	branchTip, err := d.localBranchTip(root, branch)
	if err != nil {
		return false, err
	}
	// A worktree can leave its branch, so its HEAD and the branch each hold work.
	tips := []string{head}
	if branchTip != "" && branchTip != head {
		tips = append(tips, branchTip)
	}
	branchMerged := false
	for _, tip := range tips {
		merged, saved, err := d.tipSaved(root, base, tip)
		if err != nil || !saved {
			return false, err
		}
		if tip == branchTip {
			branchMerged = merged
		}
	}
	if _, err := d.run(root, "worktree", "remove", path); err != nil {
		return false, err
	}
	// Remote-tracking refs are a local cache, so a branch deleted or
	// force-pushed elsewhere can read as saved until the next fetch. The
	// branch ref costs nothing and keeps those commits reachable, so only
	// a branch the base already holds earns deleting it. A branch that is
	// itself the base holds its commits for every session built on it.
	if !branchMerged || base == branch {
		return true, nil
	}
	if _, err := d.run(root, "branch", "-D", branch); err != nil {
		return false, err
	}
	return true, nil
}

// localBranchTip resolves a local branch, or returns "" when it no longer
// exists, as after a rename by hand.
func (d *Driver) localBranchTip(root, branch string) (string, error) {
	tip, err := d.run(root, "rev-parse", "--verify", "--quiet", "refs/heads/"+branch)
	if err != nil {
		// --verify --quiet exits 1 when the ref is absent; anything else is a real failure.
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) || exitErr.ExitCode() != 1 {
			return "", err
		}
		return "", nil
	}
	return tip, nil
}

// tipSaved reports whether tip's commits outlive the worktree: merged when
// the base holds them, saved when the base or a remote does.
func (d *Driver) tipSaved(root, base, tip string) (merged, saved bool, err error) {
	merged, err = d.mergedInto(root, base, tip)
	if err != nil || merged {
		return merged, merged, err
	}
	// The base ref only moves on fetch, so a branch that is pushed, and
	// often already merged, still counts as ahead. Commits that exist on
	// a remote are not work this would lose.
	unpushed, err := d.run(root, "rev-list", "--count", tip, "--not", "--remotes")
	if err != nil {
		return false, false, err
	}
	return false, unpushed == "0", nil
}

// mergedInto reports whether base already holds tip's changes, as the same
// commits or, after a squash or rebase merge, as the same content.
func (d *Driver) mergedInto(root, base, tip string) (bool, error) {
	ahead, err := d.run(root, "rev-list", "--count", base+".."+tip)
	if err != nil {
		return false, err
	}
	if ahead == "0" {
		return true, nil
	}
	// Merging tip into base leaves base's tree untouched when tip adds nothing.
	mergedTree, err := d.run(root, "merge-tree", "--write-tree", base, tip)
	if err != nil {
		// Exit 1 is a conflict, so base lacks the change; git before 2.38
		// has no --write-tree and cannot tell.
		var exitErr *exec.ExitError
		conflicted := errors.As(err, &exitErr) && exitErr.ExitCode() == 1
		if conflicted || strings.Contains(err.Error(), "unknown rev --write-tree") {
			return false, nil
		}
		return false, err
	}
	baseTree, err := d.run(root, "rev-parse", base+"^{tree}")
	if err != nil {
		return false, err
	}
	return mergedTree == baseTree, nil
}
