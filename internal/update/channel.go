package update

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/YoanWai/agent-manager/internal/atomicfile"
)

const channelCacheFile = "channel-version.json"

// channel is the published file a package manager reads new versions from.
type channel struct {
	url     string
	version *regexp.Regexp
}

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

// miseLatest asks mise itself, so its release-age cooldown and the user's
// settings decide what counts as available.
var miseLatest = func(ctx context.Context, slug string) (string, error) {
	output, err := exec.CommandContext(ctx, "mise", "latest", slug).Output()
	return strings.TrimSpace(string(output)), err
}

// installedVersion is a seam so tests never exec a real binary.
var installedVersion = func(path string) (string, error) {
	output, err := exec.Command(path, "--version").Output()
	if err != nil {
		return "", err
	}
	fields := strings.Fields(string(output))
	if len(fields) == 0 {
		return "", fmt.Errorf("update: %s printed no version", path)
	}
	return fields[len(fields)-1], nil
}

// Upgraded reports whether the binary at path is newer than current. A package
// manager with nothing newer still exits 0, so its exit status cannot say.
func Upgraded(path, current string) (bool, error) {
	version, err := installedVersion(path)
	if err != nil {
		return false, err
	}
	return Newer(version, current), nil
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

func (m Manager) channelKey() string {
	if m.Name == "mise" {
		return m.Command[len(m.Command)-1]
	}
	return m.channel.url
}

// availableVersion keeps the last answer on failure so a passing outage does
// not hide a release this install can already receive. A Manager with no
// channel (Nix) has no knowable version and answers "".
func (m Manager) availableVersion(ctx context.Context, configDir string, force bool) (string, error) {
	key := m.channelKey()
	if key == "" {
		return "", nil
	}
	cachePath := filepath.Join(configDir, channelCacheFile)
	cached, ok := atomicfile.ReadJSON[channelCache](cachePath)
	known := ok && cached.Key == key
	if known && !force && cacheFresh(cache{CheckedAt: cached.CheckedAt}, time.Now()) {
		return cached.Version, nil
	}

	budget := requestBudget
	if force {
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
	if m.Name == "mise" {
		version, err := miseLatest(ctx, m.channelKey())
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
