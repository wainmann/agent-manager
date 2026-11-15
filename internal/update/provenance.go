package update

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"

	"github.com/sigstore/sigstore-go/pkg/bundle"
	"github.com/sigstore/sigstore-go/pkg/root"
	"github.com/sigstore/sigstore-go/pkg/tuf"
	"github.com/sigstore/sigstore-go/pkg/util"
	"github.com/sigstore/sigstore-go/pkg/verify"
	"github.com/sigstore/sigstore/pkg/httpretry"
	"github.com/theupdateframework/go-tuf/v2/metadata/fetcher"
)

const (
	provenanceIssuer    = "https://token.actions.githubusercontent.com"
	provenancePredicate = "https://slsa.dev/provenance/v1"
	// releaseWorkflow must name the workflow file that runs
	// actions/attest-build-provenance for releases. Renaming or moving
	// .github/workflows/release.yml, calling it as a reusable workflow, or
	// renaming the repository changes the signing identity, and every update
	// to a release built that way is refused. Change this constant in the
	// same PR. TestReleaseWorkflowMatchesTheSigningIdentity fails when the
	// file and this constant drift apart.
	releaseWorkflow  = "https://github.com/wainmann/agent-manager/.github/workflows/release.yml"
	attestationLimit = 8 << 20
	releasesPage     = "https://github.com/wainmann/agent-manager/releases"
)

// errProvenanceUnavailable marks a provenance check that could not run
// because GitHub or Sigstore did not answer. The update stops either way,
// so a network failure never installs an unverified binary.
var errProvenanceUnavailable = errors.New("could not reach GitHub or Sigstore to verify the build provenance")

func provenanceUnavailable(tag string, cause error) error {
	return fmt.Errorf("update: %w of %s, so the installed binary was left unchanged. "+
		"Check the network and try again later, or reinstall from %s (%w)",
		errProvenanceUnavailable, tag, releasesPage, cause)
}

// attestationsBase is a var so tests can serve the attestations API locally.
var attestationsBase = "https://api.github.com/repos/wainmann/agent-manager/attestations"

// trustedMaterial is a var so tests can load a fixed Sigstore trusted root.
var trustedMaterial = publicGoodTrustedRoot

// checkProvenance is a var so the Apply tests, whose archives are built
// in the test, can stand in for the attestation check.
var checkProvenance = verifyProvenance

type attestationList struct {
	Attestations []struct {
		Bundle json.RawMessage `json:"bundle"`
	} `json:"attestations"`
}

// verifyProvenance accepts the archive only when a Sigstore-signed SLSA
// provenance attestation names its digest and was signed by release.yml
// running on tag.
func verifyProvenance(ctx context.Context, tag, sha256Hex string) error {
	digest, err := hex.DecodeString(sha256Hex)
	if err != nil {
		return fmt.Errorf("update: bad checksum %q: %w", sha256Hex, err)
	}
	bundles, err := fetchAttestations(ctx, sha256Hex)
	if err != nil {
		return provenanceUnavailable(tag, err)
	}
	if len(bundles) == 0 {
		return fmt.Errorf("update: no build provenance published for %s, so the installed binary was left unchanged", tag)
	}
	trusted, err := trustedMaterial(ctx)
	if err != nil {
		return provenanceUnavailable(tag, fmt.Errorf("load the Sigstore trusted root: %w", err))
	}
	verifier, err := verify.NewVerifier(trusted,
		verify.WithSignedCertificateTimestamps(1),
		verify.WithTransparencyLog(1),
		verify.WithObserverTimestamps(1))
	if err != nil {
		return err
	}
	identity, err := verify.NewShortCertificateIdentity(provenanceIssuer, "", releaseWorkflow+"@refs/tags/"+tag, "")
	if err != nil {
		return err
	}
	policy := verify.NewPolicy(verify.WithArtifactDigest("sha256", digest), verify.WithCertificateIdentity(identity))

	var failures []error
	for _, raw := range bundles {
		var entity bundle.Bundle
		if err := entity.UnmarshalJSON(raw); err != nil {
			failures = append(failures, err)
			continue
		}
		result, err := verifier.Verify(&entity, policy)
		if err != nil {
			failures = append(failures, err)
			continue
		}
		if got := result.Statement.GetPredicateType(); got != provenancePredicate {
			failures = append(failures, fmt.Errorf("predicate type %s", got))
			continue
		}
		return nil
	}
	return fmt.Errorf("update: build provenance for %s does not verify, so the installed binary was left unchanged: %w", tag, errors.Join(failures...))
}

// fetchAttestations returns the inline bundles GitHub publishes for the
// digest. A 404 means GitHub has no attestation for it and comes back as an
// empty list. Any other failure is returned for the caller to report as
// unavailable.
func fetchAttestations(ctx context.Context, sha256Hex string) ([]json.RawMessage, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, attestationsBase+"/sha256:"+sha256Hex, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	client := &http.Client{
		// The attestations API answers directly. A redirect to another host
		// is refused rather than followed.
		CheckRedirect: func(next *http.Request, _ []*http.Request) error {
			if next.URL.Host != req.URL.Host {
				return fmt.Errorf("refusing redirect to %s", next.URL.Host)
			}
			return nil
		},
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound:
		return nil, nil
	default:
		return nil, fmt.Errorf("GitHub attestations API returned %s", resp.Status)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, attestationLimit))
	if err != nil {
		return nil, err
	}
	var list attestationList
	if err := json.Unmarshal(raw, &list); err != nil {
		return nil, fmt.Errorf("read attestations: %w", err)
	}
	bundles := make([]json.RawMessage, 0, len(list.Attestations))
	for _, attestation := range list.Attestations {
		if len(attestation.Bundle) > 0 {
			bundles = append(bundles, attestation.Bundle)
		}
	}
	return bundles, nil
}

// publicGoodTrustedRoot reads the Sigstore public-good trusted root through
// its TUF repository, so key rotations reach installed binaries.
func publicGoodTrustedRoot(ctx context.Context) (root.TrustedMaterial, error) {
	cacheDir, err := os.UserCacheDir()
	if err != nil {
		return nil, err
	}
	// The TUF fetcher builds its requests without a context, so each one is
	// bound to ctx here and the update deadline also bounds a stalled CDN.
	tufFetcher := fetcher.NewDefaultFetcher()
	tufFetcher.SetHTTPUserAgent(util.ConstructUserAgent())
	tufFetcher.SetHTTPClient(contextClient{ctx: ctx, next: httpretry.NewClient()})
	options := tuf.DefaultOptions().
		WithContext(ctx).
		WithFetcher(tufFetcher).
		WithCachePath(filepath.Join(cacheDir, "agent-manager", "sigstore-tuf"))
	return root.FetchTrustedRootWithOptions(options)
}

// contextClient sends every request with ctx, so cancelling ctx stops it.
type contextClient struct {
	ctx  context.Context
	next *http.Client
}

func (c contextClient) Do(req *http.Request) (*http.Response, error) {
	return c.next.Do(req.WithContext(c.ctx))
}
