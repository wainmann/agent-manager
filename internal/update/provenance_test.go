package update

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sigstore/sigstore-go/pkg/root"
)

// The v0.49.0 linux/amd64 archive digest and the attestations GitHub served
// for it, with the Sigstore public-good trusted root of the same day.
const (
	fixtureTag    = "v0.49.0"
	fixtureDigest = "1bf8ea905742d0e49692990d4ab9d81c9c773fb0df1bd293949e6ea436cc2064"
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
	if err := verifyProvenance(context.Background(), fixtureTag, fixtureDigest); err != nil {
		t.Fatalf("verify: %v", err)
	}
}

func TestVerifyProvenanceRejectsAnotherTag(t *testing.T) {
	serveAttestations(t, fixtureDigest, fixtureAttestations(t))
	err := verifyProvenance(context.Background(), "v0.48.0", fixtureDigest)
	if err == nil || !strings.Contains(err.Error(), "does not verify") {
		t.Fatalf("want an identity failure, got %v", err)
	}
}

func TestVerifyProvenanceRejectsAnotherDigest(t *testing.T) {
	other := strings.Repeat("ab", 32)
	serveAttestations(t, other, fixtureAttestations(t))
	err := verifyProvenance(context.Background(), fixtureTag, other)
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
	err = verifyProvenance(context.Background(), fixtureTag, fixtureDigest)
	if err == nil || !strings.Contains(err.Error(), "does not verify") {
		t.Fatalf("want a signature failure, got %v", err)
	}
}

func TestVerifyProvenanceRejectsAnEmptyList(t *testing.T) {
	serveAttestations(t, fixtureDigest, []byte(`{"attestations":[]}`))
	err := verifyProvenance(context.Background(), fixtureTag, fixtureDigest)
	if err == nil || !strings.Contains(err.Error(), "no build provenance published") {
		t.Fatalf("want a missing-provenance error, got %v", err)
	}
}

func TestVerifyProvenanceRejectsAMissingAttestation(t *testing.T) {
	serveAttestations(t, fixtureDigest, nil)
	err := verifyProvenance(context.Background(), fixtureTag, fixtureDigest)
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

	err := verifyProvenance(context.Background(), fixtureTag, fixtureDigest)
	assertUnavailable(t, err, "403")
}

func TestVerifyProvenanceFailsClosedWhenGitHubIsUnreachable(t *testing.T) {
	server := httptest.NewServer(http.NotFoundHandler())
	url := server.URL
	server.Close()
	origBase := attestationsBase
	attestationsBase = url
	t.Cleanup(func() { attestationsBase = origBase })

	err := verifyProvenance(context.Background(), fixtureTag, fixtureDigest)
	assertUnavailable(t, err, "")
}

func TestVerifyProvenanceFailsClosedWhenSigstoreIsUnreachable(t *testing.T) {
	serveAttestations(t, fixtureDigest, fixtureAttestations(t))
	trustedMaterial = func(context.Context) (root.TrustedMaterial, error) {
		return nil, errors.New("tuf-repo-cdn.sigstore.dev: connection refused")
	}

	err := verifyProvenance(context.Background(), fixtureTag, fixtureDigest)
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
