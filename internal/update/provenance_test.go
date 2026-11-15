package update

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/golang/snappy"
	"github.com/sigstore/sigstore-go/pkg/root"
)

// The v0.49.0 linux/amd64 archive digest and the attestations GitHub served
// for it, with the Sigstore public-good trusted root of the same day.
const (
	fixtureTag    = "v0.49.0"
	fixtureDigest = "1bf8ea905742d0e49692990d4ab9d81c9c773fb0df1bd293949e6ea436cc2064"
	fixtureAsset  = "agent-manager_0.49.0_linux_amd64.tar.gz"
)

func serveAttestations(t *testing.T, digest string, body []byte) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/sha256:"+digest || body == nil {
			http.NotFound(w, r)
			return
		}
		w.Write(body)
	}))
	t.Cleanup(server.Close)
	origBase := attestationsBase
	attestationsBase = server.URL
	t.Cleanup(func() { attestationsBase = origBase })

	origTrusted := trustedMaterial
	trustedMaterial = func(context.Context) (root.TrustedMaterial, error) {
		return root.NewTrustedRootFromPath("testdata/sigstore-trusted-root.json")
	}
	t.Cleanup(func() { trustedMaterial = origTrusted })
}

func fixtureAttestations(t *testing.T) []byte {
	t.Helper()
	body, err := os.ReadFile("testdata/attestations-v0.49.0-linux-amd64.json")
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func TestVerifyProvenanceAcceptsTheReleaseWorkflow(t *testing.T) {
	serveAttestations(t, fixtureDigest, fixtureAttestations(t))
	if err := verifyProvenance(context.Background(), fixtureTag, fixtureAsset, fixtureDigest); err != nil {
		t.Fatalf("verify: %v", err)
	}
}

func TestVerifyProvenanceRejectsAnotherTag(t *testing.T) {
	serveAttestations(t, fixtureDigest, fixtureAttestations(t))
	err := verifyProvenance(context.Background(), "v0.48.0", fixtureAsset, fixtureDigest)
	if err == nil || !strings.Contains(err.Error(), "does not verify") {
		t.Fatalf("want an identity failure, got %v", err)
	}
}

func TestVerifyProvenanceRejectsAnotherDigest(t *testing.T) {
	other := strings.Repeat("ab", 32)
	serveAttestations(t, other, fixtureAttestations(t))
	err := verifyProvenance(context.Background(), fixtureTag, fixtureAsset, other)
	if err == nil || !strings.Contains(err.Error(), "does not verify") {
		t.Fatalf("want a digest failure, got %v", err)
	}
}

func TestVerifyProvenanceRejectsATamperedSignature(t *testing.T) {
	var list map[string][]map[string]any
	if err := json.Unmarshal(fixtureAttestations(t), &list); err != nil {
		t.Fatal(err)
	}
	for _, attestation := range list["attestations"] {
		envelope := attestation["bundle"].(map[string]any)["dsseEnvelope"].(map[string]any)
		signature := envelope["signatures"].([]any)[0].(map[string]any)
		sig := []byte(signature["sig"].(string))
		if sig[10] == 'A' {
			sig[10] = 'B'
		} else {
			sig[10] = 'A'
		}
		signature["sig"] = string(sig)
	}
	tampered, err := json.Marshal(list)
	if err != nil {
		t.Fatal(err)
	}
	serveAttestations(t, fixtureDigest, tampered)
	err = verifyProvenance(context.Background(), fixtureTag, fixtureAsset, fixtureDigest)
	if err == nil || !strings.Contains(err.Error(), "does not verify") {
		t.Fatalf("want a signature failure, got %v", err)
	}
}

func TestVerifyProvenanceRejectsAnEmptyList(t *testing.T) {
	serveAttestations(t, fixtureDigest, []byte(`{"attestations":[]}`))
	err := verifyProvenance(context.Background(), fixtureTag, fixtureAsset, fixtureDigest)
	if err == nil || !strings.Contains(err.Error(), "no build provenance published") {
		t.Fatalf("want a missing-provenance error, got %v", err)
	}
}

func TestVerifyProvenanceRejectsAMissingAttestation(t *testing.T) {
	serveAttestations(t, fixtureDigest, nil)
	err := verifyProvenance(context.Background(), fixtureTag, fixtureAsset, fixtureDigest)
	if err == nil || !strings.Contains(err.Error(), "no build provenance published") {
		t.Fatalf("want a missing-provenance error for the 404, got %v", err)
	}
	if errors.Is(err, errProvenanceUnavailable) {
		t.Fatalf("a 404 is an answer, not an outage: %v", err)
	}
}

func TestVerifyProvenanceFailsClosedWhenGitHubErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "rate limited", http.StatusForbidden)
	}))
	t.Cleanup(server.Close)
	origBase := attestationsBase
	attestationsBase = server.URL
	t.Cleanup(func() { attestationsBase = origBase })

	err := verifyProvenance(context.Background(), fixtureTag, fixtureAsset, fixtureDigest)
	assertUnavailable(t, err, "403")
}

func TestVerifyProvenanceFailsClosedWhenGitHubIsUnreachable(t *testing.T) {
	server := httptest.NewServer(http.NotFoundHandler())
	url := server.URL
	server.Close()
	origBase := attestationsBase
	attestationsBase = url
	t.Cleanup(func() { attestationsBase = origBase })

	err := verifyProvenance(context.Background(), fixtureTag, fixtureAsset, fixtureDigest)
	assertUnavailable(t, err, "")
}

func TestVerifyProvenanceFailsClosedWhenSigstoreIsUnreachable(t *testing.T) {
	serveAttestations(t, fixtureDigest, fixtureAttestations(t))
	trustedMaterial = func(context.Context) (root.TrustedMaterial, error) {
		return nil, errors.New("tuf-repo-cdn.sigstore.dev: connection refused")
	}

	err := verifyProvenance(context.Background(), fixtureTag, fixtureAsset, fixtureDigest)
	assertUnavailable(t, err, "Sigstore trusted root")
}

func assertUnavailable(t *testing.T, err error, cause string) {
	t.Helper()
	if !errors.Is(err, errProvenanceUnavailable) {
		t.Fatalf("want errProvenanceUnavailable, got %v", err)
	}
	msg := err.Error()
	for _, want := range []string{fixtureTag, "left unchanged", "try again", releasesPage, cause} {
		if !strings.Contains(msg, want) {
			t.Fatalf("error %q does not mention %q", msg, want)
		}
	}
}

// The signing identity is the workflow file path. If release.yml is renamed
// or stops attesting, every in-place update to the next release is refused,
// so the constant and the file must change together.
func TestReleaseWorkflowMatchesTheSigningIdentity(t *testing.T) {
	const repoURL = "https://github.com/wainmann/agent-manager/"
	if !strings.HasPrefix(releaseWorkflow, repoURL) {
		t.Fatalf("releaseWorkflow %q is not in %s", releaseWorkflow, repoURL)
	}
	rel := strings.TrimPrefix(releaseWorkflow, repoURL)
	if rel != ".github/workflows/release.yml" {
		t.Fatalf("releaseWorkflow points at %s; update this test only together with the workflow file", rel)
	}
	workflow, err := os.ReadFile(filepath.Join("..", "..", filepath.FromSlash(rel)))
	if err != nil {
		t.Fatalf("the workflow that signs releases is missing: %v", err)
	}
	for _, want := range []string{"actions/attest-build-provenance@", `tags: ["v*"]`} {
		if !strings.Contains(string(workflow), want) {
			t.Fatalf("%s no longer contains %s, so releases would not carry the provenance the updater requires", rel, want)
		}
	}
	if strings.Contains(string(workflow), "workflow_call") {
		t.Fatalf("%s is a reusable workflow; the signing identity would be the caller, not %s", rel, rel)
	}
}

func TestVerifyProvenanceRefusesARedirectToAnotherHost(t *testing.T) {
	elsewhere := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("followed the redirect to %s", r.Host)
	}))
	t.Cleanup(elsewhere.Close)
	redirect := strings.Replace(elsewhere.URL, "127.0.0.1", "localhost", 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, redirect+r.URL.Path, http.StatusFound)
	}))
	t.Cleanup(server.Close)
	origBase := attestationsBase
	attestationsBase = server.URL
	t.Cleanup(func() { attestationsBase = origBase })

	err := verifyProvenance(context.Background(), fixtureTag, fixtureAsset, fixtureDigest)
	assertUnavailable(t, err, "refusing redirect")
}

func TestContextClientStopsWhenTheUpdateIsCancelled(t *testing.T) {
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release
	}))
	t.Cleanup(func() { close(release); server.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	req, err := http.NewRequest(http.MethodGet, server.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = contextClient{ctx: ctx, next: &http.Client{}}.Do(req)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("want the deadline to stop the request, got %v", err)
	}
}

// fixtureEntries returns the two fixture attestations: release.yml's SLSA
// provenance (initiator user) and GitHub's own release attestation.
func fixtureEntries(t *testing.T) []map[string]any {
	t.Helper()
	var list struct {
		Attestations []map[string]any `json:"attestations"`
	}
	if err := json.Unmarshal(fixtureAttestations(t), &list); err != nil {
		t.Fatal(err)
	}
	return list.Attestations
}

func listBody(t *testing.T, entries []map[string]any) []byte {
	t.Helper()
	body, err := json.Marshal(map[string]any{"attestations": entries})
	if err != nil {
		t.Fatal(err)
	}
	return body
}

// serveCurrentShape serves the 2026-03-10 response, which has no inline
// bundle, and the snappy-compressed bundles at their bundle_url.
func serveCurrentShape(t *testing.T, bundleStatus int) {
	t.Helper()
	entries := fixtureEntries(t)
	bundles := map[string][]byte{}
	mux := http.NewServeMux()
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	for i, entry := range entries {
		raw, err := json.Marshal(entry["bundle"])
		if err != nil {
			t.Fatal(err)
		}
		path := "/bundles/" + strconv.Itoa(i) + ".json.sn"
		bundles[path] = snappy.Encode(nil, raw)
		delete(entry, "bundle")
		entry["bundle_url"] = server.URL + path
	}
	var gotVersion string
	mux.HandleFunc("/bundles/", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			t.Errorf("bundle download sent an Authorization header")
		}
		if bundleStatus == http.StatusFound {
			http.Redirect(w, r, strings.Replace(server.URL, "127.0.0.1", "localhost", 1)+r.URL.Path, http.StatusFound)
			return
		}
		if bundleStatus != http.StatusOK {
			http.Error(w, "gone", bundleStatus)
			return
		}
		w.Write(bundles[r.URL.Path])
	})
	list := listBody(t, entries)
	mux.HandleFunc("/sha256:"+fixtureDigest, func(w http.ResponseWriter, r *http.Request) {
		gotVersion = r.Header.Get("X-GitHub-Api-Version")
		w.Write(list)
	})
	t.Cleanup(func() {
		if gotVersion != attestationsAPIVersion {
			t.Errorf("X-GitHub-Api-Version = %q, want %q", gotVersion, attestationsAPIVersion)
		}
	})
	useLocalAPI(t, server.URL)
}

func useLocalAPI(t *testing.T, base string) {
	t.Helper()
	origBase := attestationsBase
	attestationsBase = base
	t.Cleanup(func() { attestationsBase = origBase })
	origTrusted := trustedMaterial
	trustedMaterial = func(context.Context) (root.TrustedMaterial, error) {
		return root.NewTrustedRootFromPath("testdata/sigstore-trusted-root.json")
	}
	t.Cleanup(func() { trustedMaterial = origTrusted })
}

func TestVerifyProvenanceDownloadsTheBundleFromItsURL(t *testing.T) {
	serveCurrentShape(t, http.StatusOK)
	if err := verifyProvenance(context.Background(), fixtureTag, fixtureAsset, fixtureDigest); err != nil {
		t.Fatalf("verify: %v", err)
	}
}

func TestVerifyProvenanceReportsAFailedBundleDownloadAsUnavailable(t *testing.T) {
	serveCurrentShape(t, http.StatusForbidden)
	err := verifyProvenance(context.Background(), fixtureTag, fixtureAsset, fixtureDigest)
	assertUnavailable(t, err, "attestation bundle")
}

func TestVerifyProvenanceRefusesABundleRedirectToAnotherHost(t *testing.T) {
	serveCurrentShape(t, http.StatusFound)
	err := verifyProvenance(context.Background(), fixtureTag, fixtureAsset, fixtureDigest)
	assertUnavailable(t, err, "refusing redirect")
}

func TestVerifyProvenanceTreatsANullBundleWithNoURLAsMissing(t *testing.T) {
	entries := fixtureEntries(t)
	for _, entry := range entries {
		entry["bundle"] = nil
		delete(entry, "bundle_url")
	}
	serveAttestations(t, fixtureDigest, listBody(t, entries))
	err := verifyProvenance(context.Background(), fixtureTag, fixtureAsset, fixtureDigest)
	if err == nil || !strings.Contains(err.Error(), "no build provenance published") {
		t.Fatalf("want a missing-provenance error, got %v", err)
	}
}

func TestVerifyProvenanceIgnoresGitHubsOwnAttestation(t *testing.T) {
	github := fixtureEntries(t)[1]
	if github["initiator"] != "github" {
		t.Fatalf("fixture order changed, entry 1 is %v", github["initiator"])
	}
	serveAttestations(t, fixtureDigest, listBody(t, []map[string]any{github}))
	err := verifyProvenance(context.Background(), fixtureTag, fixtureAsset, fixtureDigest)
	if err == nil || !strings.Contains(err.Error(), "no build provenance published") {
		t.Fatalf("want a missing-provenance error, got %v", err)
	}

	// The same correctly signed bundle relabelled as user-initiated is
	// still refused, because it is not SLSA provenance from release.yml.
	github["initiator"] = "user"
	serveAttestations(t, fixtureDigest, listBody(t, []map[string]any{github}))
	if err := verifyProvenance(context.Background(), fixtureTag, fixtureAsset, fixtureDigest); err == nil {
		t.Fatal("accepted GitHub's release attestation as build provenance")
	}
}

func TestVerifyProvenanceRequiresTheAssetName(t *testing.T) {
	serveAttestations(t, fixtureDigest, fixtureAttestations(t))
	err := verifyProvenance(context.Background(), fixtureTag, "agent-manager_0.49.0_linux_arm64.tar.gz", fixtureDigest)
	if err == nil || !strings.Contains(err.Error(), "does not name agent-manager_0.49.0_linux_arm64.tar.gz") {
		t.Fatalf("want an asset mismatch, got %v", err)
	}
}

func TestVerifyProvenanceKeepsAFailureOnOneLine(t *testing.T) {
	serveAttestations(t, fixtureDigest, fixtureAttestations(t))
	err := verifyProvenance(context.Background(), "v0.48.0", fixtureAsset, fixtureDigest)
	if err == nil || strings.Contains(err.Error(), "\n") || !strings.Contains(err.Error(), "left unchanged") {
		t.Fatalf("want a one-line failure that says the binary was kept, got %q", err)
	}
}

func TestVerifyProvenanceNamesTheRateLimit(t *testing.T) {
	reset := time.Date(2026, 11, 15, 17, 30, 0, 0, time.UTC)
	for _, status := range []int{http.StatusForbidden, http.StatusTooManyRequests} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("X-RateLimit-Remaining", "0")
			w.Header().Set("X-RateLimit-Reset", strconv.FormatInt(reset.Unix(), 10))
			http.Error(w, "API rate limit exceeded", status)
		}))
		useLocalAPI(t, server.URL)
		err := verifyProvenance(context.Background(), fixtureTag, fixtureAsset, fixtureDigest)
		server.Close()
		if !errors.Is(err, errRateLimited) || errors.Is(err, errProvenanceUnavailable) {
			t.Fatalf("%d: want a rate limit error, got %v", status, err)
		}
		want := "Try again after " + reset.Local().Format("15:04 MST")
		if !strings.Contains(err.Error(), want) || !strings.Contains(err.Error(), "left unchanged") {
			t.Fatalf("%d: error %q does not say %q", status, err, want)
		}
	}
}

func TestTUFCacheFallsBackToATempDirectory(t *testing.T) {
	t.Setenv("HOME", "")
	t.Setenv("XDG_CACHE_HOME", "")
	t.Setenv("LocalAppData", "")
	if _, err := os.UserCacheDir(); err == nil {
		t.Skip("this platform still has a user cache directory")
	}
	dir, cleanup, err := tufCachePath()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("temp cache %s: %v", dir, err)
	}
	cleanup()
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("temp cache %s was not removed", dir)
	}
}

func TestBundleDownloadErrorsHideTheSignedQuery(t *testing.T) {
	server := httptest.NewServer(http.NotFoundHandler())
	base := server.URL
	server.Close()
	_, err := downloadBundle(context.Background(), base+"/bundle.json.sn?sig=secret-signature&se=2026")
	if err == nil {
		t.Fatal("want a download error")
	}
	if strings.Contains(err.Error(), "secret-signature") || strings.Contains(err.Error(), "?") {
		t.Fatalf("error leaks the signed query: %q", err)
	}
}

func TestBundleDownloadStopsARedirectLoop(t *testing.T) {
	var hits int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		http.Redirect(w, r, r.URL.Path, http.StatusFound)
	}))
	t.Cleanup(server.Close)
	_, err := downloadBundle(context.Background(), server.URL+"/loop")
	if err == nil || !strings.Contains(err.Error(), "10 redirects") || hits > 10 {
		t.Fatalf("want the loop stopped after 10 redirects, got %v after %d requests", err, hits)
	}
}

func TestBundleDownloadHidesTheQueryOfABadRedirect(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", "/bad%zz?sig=secret-signature")
		w.WriteHeader(http.StatusFound)
	}))
	t.Cleanup(server.Close)
	_, err := downloadBundle(context.Background(), server.URL+"/bundle.json.sn?sig=first-signature")
	if err == nil {
		t.Fatal("want a redirect error")
	}
	if strings.Contains(err.Error(), "secret-signature") || strings.Contains(err.Error(), "first-signature") {
		t.Fatalf("error leaks a signed query: %q", err)
	}
}

func TestBundleDownloadRejectsADecodedBundleLargerThan8MiB(t *testing.T) {
	payload := snappy.Encode(nil, bytes.Repeat([]byte{'a'}, 64<<20))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(payload)
	}))
	t.Cleanup(server.Close)
	_, err := downloadBundle(context.Background(), server.URL)
	if err == nil || !strings.Contains(err.Error(), "larger than 8 MiB") {
		t.Fatalf("want a size cap error, got %v", err)
	}
}

func TestBundleDownloadRejectsABundleLargerThan8MiB(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(bytes.Repeat([]byte{'x'}, attestationLimit+1))
	}))
	t.Cleanup(server.Close)
	_, err := downloadBundle(context.Background(), server.URL)
	if err == nil || !strings.Contains(err.Error(), "larger than 8 MiB") {
		t.Fatalf("want a size cap error, got %v", err)
	}
}

func TestSameHostClientRefusesACleartextRedirect(t *testing.T) {
	req, err := http.NewRequest(http.MethodGet, "https://example.com/bundle.json.sn?sig=secret-signature", nil)
	if err != nil {
		t.Fatal(err)
	}
	next, err := http.NewRequest(http.MethodGet, "http://example.com/bundle.json.sn?sig=secret-signature", nil)
	if err != nil {
		t.Fatal(err)
	}
	err = sameHostClient(req).CheckRedirect(next, []*http.Request{req})
	if err == nil || !strings.Contains(err.Error(), "refusing redirect to http") {
		t.Fatalf("want a cleartext redirect refusal, got %v", err)
	}
}

func TestVerifyProvenanceTreatsASecondaryRateLimitAsARateLimit(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-RateLimit-Remaining", "12")
		w.Header().Set("Retry-After", "60")
		http.Error(w, "You have exceeded a secondary rate limit", http.StatusForbidden)
	}))
	useLocalAPI(t, server.URL)
	before := time.Now().Add(60 * time.Second)
	err := verifyProvenance(context.Background(), fixtureTag, fixtureAsset, fixtureDigest)
	after := time.Now().Add(60 * time.Second)
	server.Close()
	if !errors.Is(err, errRateLimited) || errors.Is(err, errProvenanceUnavailable) {
		t.Fatalf("want a rate limit error, got %v", err)
	}
	wantA := "Try again after " + before.Local().Format("15:04 MST")
	wantB := "Try again after " + after.Local().Format("15:04 MST")
	if (!strings.Contains(err.Error(), wantA) && !strings.Contains(err.Error(), wantB)) || !strings.Contains(err.Error(), "left unchanged") {
		t.Fatalf("error %q does not name the rate limit retry time", err)
	}
}
