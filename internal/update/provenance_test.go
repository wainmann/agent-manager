package update

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
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
	if err == nil || !strings.Contains(err.Error(), "404") {
		t.Fatalf("want the 404, got %v", err)
	}
}
