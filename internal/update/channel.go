package update

import (
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

	"github.com/YoanWai/agent-manager/internal/atomicfile"
)

const (
	channelCacheFile   = "channel-version.json"
	upgradeCheckBudget = 30 * time.Second
	processGrace       = 2 * time.Second
)

// channel says where a package manager's newest version can be read: a
// published file, or mise itself.
type channel struct {
	url      string
	version  *regexp.Regexp
	miseSlug string
}

func (c channel) key() string { return c.url + c.miseSlug }

var (
	coreChannel = channel{
		url:     "https://formulae.brew.sh/api/formula/agent-manager.json",
		version: regexp.MustCompile(`"stable":"(\d+\.\d+\.\d+)"`),
	}
	caskChannel = channel{
		url:     "https://raw.githubusercontent.com/YoanWai/homebrew-tap/main/Casks/agent-manager.rb",
		version: regexp.MustCompile(`version "(\d+\.\d+\.\d+)"`),
	}
	scoopChannel = channel{
		url:     "https://raw.githubusercontent.com/YoanWai/scoop-bucket/main/bucket/agent-manager.json",
		version: regexp.MustCompile(`"version":\s*"(\d+\.\d+\.\d+)"`),
	}
	aurChannel = channel{
		url:     "https://aur.archlinux.org/rpc/v5/info?arg[]=agent-manager-bin",
		version: regexp.MustCompile(`"Version":"(\d+\.\d+\.\d+)`),
	}
)

// miseAnswer runs mise from the home directory: a project's mise.toml at the
// manager's working directory can fail the trust check or change the answer.
func miseAnswer(ctx context.Context, args ...string) (string, error) {
	command := exec.CommandContext(ctx, "mise", args...)
	command.WaitDelay = processGrace
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	command.Dir = home
	output, err := command.Output()
	return strings.TrimSpace(string(output)), err
}

// miseLatest asks mise itself, so its release-age cooldown and the user's
// settings decide what counts as available.
var miseLatest = func(ctx context.Context, slug string) (string, error) {
	return miseAnswer(ctx, "latest", slug)
}

// miseWhich is the seam for finding the binary mise now serves.
var miseWhich = func(ctx context.Context) (string, error) {
	return miseAnswer(ctx, "which", "agent-manager")
}

// installedVersion is a seam so tests never exec a real binary.
var installedVersion = func(ctx context.Context, path string) (string, error) {
	command := exec.CommandContext(ctx, path, "--version")
	command.WaitDelay = processGrace
	output, err := command.Output()
	if err != nil {
		return "", err
	}
	fields := strings.Fields(string(output))
	if len(fields) == 0 {
		return "", fmt.Errorf("update: %s printed no version", path)
	}
	return fields[len(fields)-1], nil
}

// Upgraded verifies that the restart binary advanced and reached the offered version, if any.
func (m Manager) Upgraded(execPath, current, expected string) (string, bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), upgradeCheckBudget)
	defer cancel()
	target := RestartTarget(execPath)
	if m.channel.miseSlug != "" {
		var err error
		if target, err = miseWhich(ctx); err != nil {
			return "", false, fmt.Errorf("asking mise for the installed path: %w", err)
		}
	}
	version, err := installedVersion(ctx, target)
	if err != nil {
		return target, false, err
	}
	if _, ok := parseVersion(version); !ok {
		return target, false, fmt.Errorf("%s reported an invalid version %q", target, version)
	}
	if _, ok := parseVersion(current); !ok {
		return target, false, fmt.Errorf("cannot compare installed version %s with running version %q", version, current)
	}
	if Newer(current, version) {
		return target, false, fmt.Errorf("installed version %s is older than running version %s", version, current)
	}
	if Newer(expected, version) {
		return target, false, fmt.Errorf("installed version %s did not reach the offered version %s", version, expected)
	}
	return target, Newer(version, current), nil
}

type channelCache struct {
	Key       string    `json:"key"`
	Version   string    `json:"version"`
	CheckedAt time.Time `json:"checked_at"`
}

// ForInstall narrows result to the newest release this install's package
// manager can deliver, so the update message never offers one it cannot. A
// direct install downloads from the release itself and keeps result as is.
func ForInstall(ctx context.Context, configDir, current string, manager Manager, force bool, result Result) (Result, error) {
	if result.Latest == "" || manager.Name == "" {
		return result, nil
	}
	available, err := manager.availableVersion(ctx, configDir, force)
	return narrowToAvailable(result, current, available), err
}

func narrowToAvailable(result Result, current, available string) Result {
	result.Latest, result.URL = "", ""
	if available == "" {
		return result
	}
	for _, release := range result.Releases {
		if Newer(release.Version, current) && !Newer(release.Version, available) {
			result.Latest, result.URL = release.Version, release.URL
			return result
		}
	}
	return result
}

// availableVersion keeps the last answer on failure so a passing outage does
// not hide a release this install can already receive. A Manager with no
// channel (Nix) has no knowable version and answers "".
func (m Manager) availableVersion(ctx context.Context, configDir string, force bool) (string, error) {
	key := m.channel.key()
	if key == "" {
		return "", nil
	}
	cachePath := filepath.Join(configDir, channelCacheFile)
	cached, ok := atomicfile.ReadJSON[channelCache](cachePath)
	known := ok && cached.Key == key
	if known && !force && cacheFresh(cached.CheckedAt, time.Now()) {
		return cached.Version, nil
	}

	budget := requestBudget
	if force || m.channel.miseSlug != "" {
		budget = refreshBudget
	}
	ctx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()
	version, err := m.fetchAvailableVersion(ctx)
	if err != nil {
		if known {
			return cached.Version, err
		}
		return "", err
	}
	_ = atomicfile.WriteJSON(cachePath, channelCache{Key: key, Version: version, CheckedAt: time.Now()}, 0o644)
	return version, nil
}

func (m Manager) fetchAvailableVersion(ctx context.Context) (string, error) {
	if m.channel.miseSlug != "" {
		version, err := miseLatest(ctx, m.channel.miseSlug)
		if err != nil {
			return "", fmt.Errorf("update: asking mise for the latest version: %w", err)
		}
		if _, ok := parseVersion(version); !ok {
			return "", fmt.Errorf("update: mise answered %q", version)
		}
		return version, nil
	}
	body, err := fetch(ctx, m.channel.url)
	if err != nil {
		return "", err
	}
	defer body.Close()
	published, err := io.ReadAll(io.LimitReader(body, maxPayload))
	if err != nil {
		return "", err
	}
	match := m.channel.version.FindSubmatch(published)
	if match == nil {
		return "", errors.New("update: no version in " + m.channel.url)
	}
	return string(match[1]), nil
}
