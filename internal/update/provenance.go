package update

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/sigstore/sigstore-go/pkg/bundle"
	"github.com/sigstore/sigstore-go/pkg/root"
	"github.com/sigstore/sigstore-go/pkg/tuf"
	"github.com/sigstore/sigstore-go/pkg/verify"
)

const (
	provenanceIssuer    = "https://token.actions.githubusercontent.com"
	provenancePredicate = "https://slsa.dev/provenance/v1"
	releaseWorkflow     = "https://github.com/wainmann/agent-manager/.github/workflows/release.yml"
	attestationLimit    = 8 << 20
)

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
		return err
	}
	trusted, err := trustedMaterial(ctx)
	if err != nil {
		return fmt.Errorf("update: load the Sigstore trusted root: %w", err)
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
	if len(failures) == 0 {
		return fmt.Errorf("update: no build provenance published for %s", tag)
	}
	return fmt.Errorf("update: build provenance for %s does not verify: %w", tag, errors.Join(failures...))
}

func fetchAttestations(ctx context.Context, sha256Hex string) ([]json.RawMessage, error) {
	body, err := fetch(ctx, attestationsBase+"/sha256:"+sha256Hex)
	if err != nil {
		return nil, err
	}
	defer body.Close()
	raw, err := io.ReadAll(io.LimitReader(body, attestationLimit))
	if err != nil {
		return nil, err
	}
	var list attestationList
	if err := json.Unmarshal(raw, &list); err != nil {
		return nil, fmt.Errorf("update: read attestations: %w", err)
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
	options := tuf.DefaultOptions().
		WithContext(ctx).
		WithCachePath(filepath.Join(cacheDir, "agent-manager", "sigstore-tuf"))
	return root.FetchTrustedRootWithOptions(options)
}
