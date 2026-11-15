package update

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/golang/snappy"
	in_toto "github.com/in-toto/attestation/go/v1"

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

// attestationsAPIVersion selects the response shape that omits inline
// bundles and links each one through bundle_url. The older shape still
// parses, so both are handled.
const attestationsAPIVersion = "2026-03-10"

// errProvenanceUnavailable marks a provenance check that could not run
// because GitHub or Sigstore did not answer. The update stops either way,
// so a network failure never installs an unverified binary.
var errProvenanceUnavailable = errors.New("could not reach GitHub or Sigstore to verify the build provenance")

func provenanceUnavailable(tag string, cause error) error {
	return fmt.Errorf("update: %w of %s, so the installed binary was left unchanged. "+
		"Check the network and try again later, or reinstall from %s (%w)",
		errProvenanceUnavailable, tag, releasesPage, cause)
}

// errRateLimited marks an attestations request GitHub refused because the
// anonymous API limit for this address is used up.
var errRateLimited = errors.New("GitHub's API rate limit for unauthenticated requests is used up")

type rateLimitError struct {
	reset time.Time
}

func (e rateLimitError) Error() string { return errRateLimited.Error() }

func (e rateLimitError) Unwrap() error { return errRateLimited }

func provenanceRateLimited(tag string, limit rateLimitError) error {
	when := "in an hour"
	if !limit.reset.IsZero() {
		when = "after " + limit.reset.Local().Format("15:04 MST")
	}
	return fmt.Errorf("update: %w, so the build provenance of %s could not be checked and the installed binary was left unchanged. Try again %s",
		limit, tag, when)
}

func provenanceFailed(tag, what string, causes []string) error {
	return fmt.Errorf("update: %s for %s, so the installed binary was left unchanged (%s)",
		what, tag, strings.Join(causes, "; "))
}

// attestationsBase is a var so tests can serve the attestations API locally.
var attestationsBase = "https://api.github.com/repos/wainmann/agent-manager/attestations"

// trustedMaterial is a var so tests can load a fixed Sigstore trusted root.
var trustedMaterial = publicGoodTrustedRoot

// checkProvenance is a var so the Apply tests, whose archives are built
// in the test, can stand in for the attestation check.
var checkProvenance = verifyProvenance

type attestation struct {
	Bundle    json.RawMessage `json:"bundle"`
	BundleURL string          `json:"bundle_url"`
	Initiator string          `json:"initiator"`
}

type attestationList struct {
	Attestations []attestation `json:"attestations"`
}

// verifyProvenance accepts the archive only when a Sigstore-signed SLSA
// provenance attestation names asset with its digest and was signed by
// release.yml running on tag.
func verifyProvenance(ctx context.Context, tag, asset, sha256Hex string) error {
	digest, err := hex.DecodeString(sha256Hex)
	if err != nil {
		return fmt.Errorf("update: bad checksum %q: %w", sha256Hex, err)
	}
	listed, err := fetchAttestations(ctx, sha256Hex)
	var limit rateLimitError
	if errors.As(err, &limit) {
		return provenanceRateLimited(tag, limit)
	}
	if err != nil {
		return provenanceUnavailable(tag, err)
	}
	// GitHub also attests its own release record (initiator github). It is
	// never signed by release.yml, so it is not worth a download or a check.
	var candidates []attestation
	for _, entry := range listed {
		if entry.Initiator != "github" {
			candidates = append(candidates, entry)
		}
	}
	if len(candidates) == 0 {
		return noProvenance(tag)
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
		return provenanceFailed(tag, "could not set up the build provenance check", []string{err.Error()})
	}
	identity, err := verify.NewShortCertificateIdentity(provenanceIssuer, "", releaseWorkflow+"@refs/tags/"+tag, "")
	if err != nil {
		return provenanceFailed(tag, "could not set up the build provenance check", []string{err.Error()})
	}
	policy := verify.NewPolicy(verify.WithArtifactDigest("sha256", digest), verify.WithCertificateIdentity(identity))

	var failures []string
	var downloadErr error
	for _, entry := range candidates {
		raw, err := entry.load(ctx)
		if err != nil {
			downloadErr = err
			continue
		}
		if raw == nil {
			continue
		}
		var entity bundle.Bundle
		if err := entity.UnmarshalJSON(raw); err != nil {
			failures = append(failures, err.Error())
			continue
		}
		if !isProvenance(&entity) {
			continue
		}
		result, err := verifier.Verify(&entity, policy)
		if err != nil {
			failures = append(failures, err.Error())
			continue
		}
		if got := result.Statement.GetPredicateType(); got != provenancePredicate {
			failures = append(failures, "predicate type "+got)
			continue
		}
		if !namesAsset(result.Statement.GetSubject(), asset, sha256Hex) {
			failures = append(failures, "the attestation does not name "+asset)
			continue
		}
		return nil
	}
	if len(failures) > 0 {
		return provenanceFailed(tag, "build provenance does not verify", failures)
	}
	if downloadErr != nil {
		return provenanceUnavailable(tag, fmt.Errorf("download the attestation bundle: %w", downloadErr))
	}
	return noProvenance(tag)
}

func noProvenance(tag string) error {
	return fmt.Errorf("update: no build provenance published for %s, so the installed binary was left unchanged", tag)
}

// isProvenance reports whether the bundle claims an SLSA provenance
// predicate, so other attestations on the same digest are skipped rather
// than reported as failures. The claim is checked again after verification.
func isProvenance(entity *bundle.Bundle) bool {
	envelope, err := entity.Envelope()
	if err != nil {
		return true
	}
	statement, err := envelope.Statement()
	if err != nil {
		return true
	}
	return statement.GetPredicateType() == provenancePredicate
}

func namesAsset(subjects []*in_toto.ResourceDescriptor, asset, sha256Hex string) bool {
	for _, subject := range subjects {
		if subject.GetName() == asset && strings.EqualFold(subject.GetDigest()["sha256"], sha256Hex) {
			return true
		}
	}
	return false
}

// fetchAttestations lists the attestations GitHub publishes for the
// digest. A 404 means GitHub has none and comes back as an empty list. A
// spent rate limit comes back as rateLimitError. Any other failure is
// returned for the caller to report as unavailable.
func fetchAttestations(ctx context.Context, sha256Hex string) ([]attestation, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, attestationsBase+"/sha256:"+sha256Hex, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", attestationsAPIVersion)
	resp, err := sameHostClient(req).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusOK:
	case resp.StatusCode == http.StatusNotFound:
		return nil, nil
	case resp.StatusCode == http.StatusTooManyRequests,
		resp.StatusCode == http.StatusForbidden && (resp.Header.Get("X-RateLimit-Remaining") == "0" || resp.Header.Get("Retry-After") != ""):
		return nil, rateLimitError{reset: rateLimitReset(resp.Header)}
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
	return list.Attestations, nil
}

// redactURL rewrites a transport error without any URL query. bundle_url
// carries a signed access token in its query, which must not reach the
// status bar or a log, and a bad redirect can echo it in the cause too.
func redactURL(err error) error {
	var urlErr *url.Error
	if !errors.As(err, &urlErr) {
		return errors.New(stripQueries(err.Error()))
	}
	where := "attestation bundle URL"
	if parsed, parseErr := url.Parse(urlErr.URL); parseErr == nil {
		where = parsed.Scheme + "://" + parsed.Host + parsed.Path
	}
	if urlErr.Timeout() {
		return fmt.Errorf("%s %s: timed out", urlErr.Op, where)
	}
	return fmt.Errorf("%s %s: %s", urlErr.Op, where, stripQueries(urlErr.Err.Error()))
}

var urlQuery = regexp.MustCompile(`\?[^\s"']*`)

func stripQueries(text string) string {
	return urlQuery.ReplaceAllString(text, "?<redacted>")
}

// sameHostClient sends no credentials, keeps Go's limit of 10 redirects
// and refuses a redirect to a host other than req's. An https request
// also refuses a redirect that is not https, so a signed URL is never
// sent in the clear. Neither the attestations API nor the bundle store
// redirects across hosts.
func sameHostClient(req *http.Request) *http.Client {
	return &http.Client{
		CheckRedirect: func(next *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return errors.New("stopped after 10 redirects")
			}
			if next.URL.Host != req.URL.Host {
				return fmt.Errorf("refusing redirect to %s", next.URL.Host)
			}
			if next.URL.Scheme != "https" && req.URL.Scheme != "http" {
				return fmt.Errorf("refusing redirect to %s", next.URL.Scheme)
			}
			return nil
		},
	}
}

func rateLimitReset(header http.Header) time.Time {
	if epoch, err := strconv.ParseInt(header.Get("X-RateLimit-Reset"), 10, 64); err == nil && epoch > 0 {
		return time.Unix(epoch, 0)
	}
	if seconds, err := strconv.Atoi(header.Get("Retry-After")); err == nil && seconds > 0 {
		return time.Now().Add(time.Duration(seconds) * time.Second)
	}
	return time.Time{}
}

// load returns the bundle JSON, inline when GitHub still sends it and
// otherwise from bundle_url. It returns nil when the entry has neither.
func (a attestation) load(ctx context.Context) (json.RawMessage, error) {
	inline := bytes.TrimSpace(a.Bundle)
	if len(inline) > 0 && !bytes.Equal(inline, []byte("null")) {
		return inline, nil
	}
	if a.BundleURL == "" {
		return nil, nil
	}
	return downloadBundle(ctx, a.BundleURL)
}

// downloadBundle reads a bundle from its short-lived signed URL. The URL
// carries its own access, so the request has no auth header. GitHub serves
// the bundle snappy-compressed. A body that is already JSON is used as is.
func downloadBundle(ctx context.Context, url string) (json.RawMessage, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := sameHostClient(req).Do(req)
	if err != nil {
		return nil, redactURL(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("attestation bundle download returned %s", resp.Status)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, attestationLimit+1))
	if err != nil {
		return nil, err
	}
	if len(raw) > attestationLimit {
		return nil, errors.New("attestation bundle is larger than 8 MiB")
	}
	if json.Valid(raw) {
		return raw, nil
	}
	size, err := snappy.DecodedLen(raw)
	if err != nil {
		return nil, fmt.Errorf("decompress the attestation bundle: %w", err)
	}
	if size > attestationLimit {
		return nil, errors.New("attestation bundle is larger than 8 MiB")
	}
	decoded, err := snappy.Decode(nil, raw)
	if err != nil {
		return nil, fmt.Errorf("decompress the attestation bundle: %w", err)
	}
	return decoded, nil
}

// publicGoodTrustedRoot reads the Sigstore public-good trusted root through
// its TUF repository, so key rotations reach installed binaries.
func publicGoodTrustedRoot(ctx context.Context) (root.TrustedMaterial, error) {
	cachePath, cleanup, err := tufCachePath()
	if err != nil {
		return nil, err
	}
	defer cleanup()
	// The TUF fetcher builds its requests without a context, so each one is
	// bound to ctx here and the update deadline also bounds a stalled CDN.
	tufFetcher := fetcher.NewDefaultFetcher()
	tufFetcher.SetHTTPUserAgent(util.ConstructUserAgent())
	tufFetcher.SetHTTPClient(contextClient{ctx: ctx, next: httpretry.NewClient()})
	options := tuf.DefaultOptions().
		WithContext(ctx).
		WithFetcher(tufFetcher).
		WithCachePath(cachePath)
	return root.FetchTrustedRootWithOptions(options)
}

// tufCachePath keeps the TUF metadata under the user cache directory. When
// there is none (HOME unset, as in some service or cron runs), it uses a
// private temp directory for this one check and removes it afterwards.
func tufCachePath() (string, func(), error) {
	if cacheDir, err := os.UserCacheDir(); err == nil {
		return filepath.Join(cacheDir, "agent-manager", "sigstore-tuf"), func() {}, nil
	}
	dir, err := os.MkdirTemp("", "agent-manager-sigstore-tuf-")
	if err != nil {
		return "", nil, fmt.Errorf("no user cache directory and no temp directory for the Sigstore trusted root: %w", err)
	}
	return dir, func() { os.RemoveAll(dir) }, nil
}

// contextClient sends every request with ctx, so cancelling ctx stops it.
type contextClient struct {
	ctx  context.Context
	next *http.Client
}

func (c contextClient) Do(req *http.Request) (*http.Response, error) {
	return c.next.Do(req.WithContext(c.ctx))
}
