package update

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func channelServer(t *testing.T, status int, body string) (string, *int) {
	t.Helper()
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		requests++
		writer.WriteHeader(status)
		_, _ = writer.Write([]byte(body))
	}))
	t.Cleanup(server.Close)
	return server.URL, &requests
}

func catalogResult() Result {
	return Result{
		Latest: "v0.48.0",
		URL:    "https://github.com/x/releases/tag/v0.48.0",
		Releases: []Release{
			{Version: "v0.48.0", URL: "https://github.com/x/releases/tag/v0.48.0"},
			{Version: "v0.47.1", URL: "https://github.com/x/releases/tag/v0.47.1"},
			{Version: "v0.47.0", URL: "https://github.com/x/releases/tag/v0.47.0"},
		},
	}
}

func TestForInstallOffersTheNewestReleaseTheChannelHas(t *testing.T) {
	cases := []struct {
		label      string
		current    string
		channel    channel
		body       string
		wantLatest string
	}{
		{"formula has the release", "0.47.1", coreChannel, `"versions":{"stable":"0.48.0","head":"HEAD"}`, "v0.48.0"},
		{"formula is one behind and this build is older", "0.47.0", coreChannel, `"versions":{"stable":"0.47.1","head":"HEAD"}`, "v0.47.1"},
		{"formula has nothing newer than this build", "0.47.1", coreChannel, `"versions":{"stable":"0.47.1","head":"HEAD"}`, ""},
		{"cask has the release", "0.47.1", caskChannel, "  version \"0.48.0\"\n", "v0.48.0"},
		{"bucket is behind", "0.47.1", scoopChannel, "{\n    \"version\": \"0.47.1\",\n", ""},
		{"aur has the release", "0.47.1", aurChannel, `"Version":"0.48.0-1"`, "v0.48.0"},
	}
	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			url, _ := channelServer(t, http.StatusOK, tc.body)
			manager := Manager{Name: "channel", channel: channel{url: url, version: tc.channel.version}}
			got, err := ForInstall(context.Background(), t.TempDir(), tc.current, manager, false, catalogResult())
			if err != nil {
				t.Fatal(err)
			}
			if got.Latest != tc.wantLatest {
				t.Fatalf("Latest = %q, want %q", got.Latest, tc.wantLatest)
			}
			if len(got.Releases) != 3 {
				t.Fatalf("the catalog must stay whole, got %d releases", len(got.Releases))
			}
		})
	}
}

func TestForInstallLeavesADirectInstallAlone(t *testing.T) {
	got, err := ForInstall(context.Background(), t.TempDir(), "0.47.1", Manager{}, false, catalogResult())
	if err != nil {
		t.Fatal(err)
	}
	if got.Latest != "v0.48.0" {
		t.Fatalf("Latest = %q, want the release itself", got.Latest)
	}
}

func TestForInstallHidesTheUpdateWithoutAChannel(t *testing.T) {
	got, err := ForInstall(context.Background(), t.TempDir(), "0.47.1", Manager{Name: "Nix", Advice: "use Nix"}, false, catalogResult())
	if err != nil {
		t.Fatal(err)
	}
	if got.Latest != "" {
		t.Fatalf("Latest = %q, want none", got.Latest)
	}
}

func TestForInstallAsksMiseForTheVersion(t *testing.T) {
	origLatest := miseLatest
	t.Cleanup(func() { miseLatest = origLatest })
	var asked string
	miseLatest = func(_ context.Context, slug string) (string, error) {
		asked = slug
		return "0.47.1", nil
	}
	manager := Manager{Name: "mise", Command: []string{"mise", "upgrade", "--bump", "ubi:wainmann/agent-manager"}, channel: channel{miseSlug: "ubi:wainmann/agent-manager"}}
	got, err := ForInstall(context.Background(), t.TempDir(), "0.47.0", manager, false, catalogResult())
	if err != nil {
		t.Fatal(err)
	}
	if asked != "ubi:wainmann/agent-manager" || got.Latest != "v0.47.1" {
		t.Fatalf("asked %q, Latest = %q", asked, got.Latest)
	}

	miseLatest = func(context.Context, string) (string, error) { return "", errors.New("no mise") }
	got, err = ForInstall(context.Background(), t.TempDir(), "0.47.0", manager, false, catalogResult())
	if err == nil || got.Latest != "" {
		t.Fatalf("a failed probe must hide the update and report, got %q, %v", got.Latest, err)
	}
}

func TestForInstallCachesTheChannelVersion(t *testing.T) {
	url, requests := channelServer(t, http.StatusOK, `"stable":"0.48.0"`)
	manager := Manager{Name: "Homebrew", channel: channel{url: url, version: coreChannel.version}}
	dir := t.TempDir()
	for range 2 {
		if _, err := ForInstall(context.Background(), dir, "0.47.1", manager, false, catalogResult()); err != nil {
			t.Fatal(err)
		}
	}
	if *requests != 1 {
		t.Fatalf("requests = %d, want the second answered from the cache", *requests)
	}
	if _, err := ForInstall(context.Background(), dir, "0.47.1", manager, true, catalogResult()); err != nil {
		t.Fatal(err)
	}
	if *requests != 2 {
		t.Fatalf("requests = %d, want a forced check to refetch", *requests)
	}
}

func TestForInstallSkipsTheChannelWhenNothingIsNewer(t *testing.T) {
	url, requests := channelServer(t, http.StatusOK, `"stable":"0.48.0"`)
	manager := Manager{Name: "Homebrew", channel: channel{url: url, version: coreChannel.version}}
	if _, err := ForInstall(context.Background(), t.TempDir(), "0.48.0", manager, false, Result{Releases: catalogResult().Releases}); err != nil {
		t.Fatal(err)
	}
	if *requests != 0 {
		t.Fatalf("requests = %d, want none", *requests)
	}
}

func TestForInstallKeepsTheLastAnswerWhenTheChannelFails(t *testing.T) {
	origLatest := miseLatest
	t.Cleanup(func() { miseLatest = origLatest })
	manager := Manager{Name: "mise", Command: []string{"mise", "upgrade", "--bump", "ubi:wainmann/agent-manager"}, channel: channel{miseSlug: "ubi:wainmann/agent-manager"}}
	dir := t.TempDir()

	miseLatest = func(context.Context, string) (string, error) { return "0.48.0", nil }
	if _, err := ForInstall(context.Background(), dir, "0.47.1", manager, false, catalogResult()); err != nil {
		t.Fatal(err)
	}
	miseLatest = func(context.Context, string) (string, error) { return "", errors.New("offline") }
	got, err := ForInstall(context.Background(), dir, "0.47.1", manager, true, catalogResult())
	if err == nil {
		t.Fatal("a failed channel must report")
	}
	if got.Latest != "v0.48.0" {
		t.Fatalf("Latest = %q, want the last answer", got.Latest)
	}
}

func TestUpgradedComparesTheInstalledBinary(t *testing.T) {
	orig := installedVersion
	t.Cleanup(func() { installedVersion = orig })
	installedVersion = func(context.Context, string) (string, error) { return "0.48.0", nil }
	if _, upgraded, err := (Manager{}).Upgraded("/x", "0.47.1", ""); err != nil || !upgraded {
		t.Fatalf("newer binary: %v, %v", upgraded, err)
	}
	if _, upgraded, err := (Manager{}).Upgraded("/x", "0.48.0", ""); err != nil || upgraded {
		t.Fatalf("same binary: %v, %v", upgraded, err)
	}
}

func TestUpgradedAsksMiseWhichBinaryItServes(t *testing.T) {
	origVersion, origWhich := installedVersion, miseWhich
	t.Cleanup(func() { installedVersion, miseWhich = origVersion, origWhich })
	newPath := "/m/installs/ubi-wainmann-agent-manager/0.48.0/agent-manager"
	miseWhich = func(context.Context) (string, error) { return newPath, nil }
	installedVersion = func(_ context.Context, path string) (string, error) {
		if path != newPath {
			t.Fatalf("checked %q, want the path mise named", path)
		}
		return "0.48.0", nil
	}
	manager := Manager{Name: "mise", channel: channel{miseSlug: "ubi:wainmann/agent-manager"}}
	target, upgraded, err := manager.Upgraded("/m/installs/ubi-wainmann-agent-manager/0.47.1/agent-manager", "0.47.1", "")
	if err != nil || !upgraded || target != newPath {
		t.Fatalf("target %q, upgraded %v, err %v", target, upgraded, err)
	}
}

func TestUpgradedRejectsUnusableResults(t *testing.T) {
	orig := installedVersion
	t.Cleanup(func() { installedVersion = orig })
	for _, tc := range []struct{ installed, current, expected string }{
		{"0.47.1", "0.47.0", "v0.48.0"},
		{"0.47.0", "0.47.1", ""},
		{"dev", "0.47.1", ""},
		{"0.48.0", "dev", ""},
	} {
		installedVersion = func(context.Context, string) (string, error) { return tc.installed, nil }
		if _, upgraded, err := (Manager{}).Upgraded("/x", tc.current, tc.expected); err == nil || upgraded {
			t.Errorf("installed %s, current %s, expected %s got upgraded %v, err %v", tc.installed, tc.current, tc.expected, upgraded, err)
		}
	}
}
