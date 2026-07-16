// Package registry is the HTTP client for the Genero Package Registry.
//
// All operations (search, resolve, install, publish) talk the v1 "registry"
// protocol (paths under /registry/...) against a single backend at
// service.generointelligence.ai. The base URL can be overridden via the
// FGLPKG_REGISTRY environment variable.
package registry

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/4js-mikefolcher/fglpkg/internal/manifest"
	"github.com/4js-mikefolcher/fglpkg/internal/semver"
	slugutil "github.com/4js-mikefolcher/fglpkg/internal/slug"
)

// ErrNotFound is returned (via %w wrapping) when the registry responds 404
// to a GET. Callers can detect first-publish or missing-package conditions
// with errors.Is(err, registry.ErrNotFound).
var ErrNotFound = errors.New("package not found in registry")

const (
	defaultRegistryBase = "https://service.generointelligence.ai"
)

// Bearer is the function the registry HTTP client calls to obtain the
// current bearer token for consumer-side authenticated requests. CLI swaps
// this for credentials.ActiveBearer(...) at startup so OAuth refresh +
// stored PAT lookup are transparent. Default reads env only.
var Bearer = func() string {
	return strings.TrimSpace(os.Getenv("FGLPKG_TOKEN"))
}

// TryRefresh is called by the registry HTTP client on a 401 to attempt a
// silent OAuth refresh before retrying the request once. Returns true if a
// fresh bearer is now available via Bearer(). CLI swaps this for a
// credentials/oauth-aware closure. Default no-op.
var TryRefresh = func() bool { return false }

// PackageInfo is the resolved metadata for a specific package version.
// FGLDeps / JavaDeps / GeneroConstraint / License / Author / Readme are
// populated from the registry's per-version `dependencies` and rich-metadata
// fields when present. Pre-rich-metadata versions (published before that flow
// shipped) return empty values for those fields and must be republished to
// expose them.
type PackageInfo struct {
	Name             string            `json:"name"`
	Version          string            `json:"version"`
	Description      string            `json:"description"`
	Author           string            `json:"author,omitempty"`
	License          string            `json:"license,omitempty"`
	PublishedAt      string            `json:"publishedAt,omitempty"`
	DownloadURL      string            `json:"downloadUrl"`
	Checksum         string            `json:"checksum"`
	GeneroConstraint string            `json:"genero,omitempty"`
	FGLDeps          map[string]string `json:"fglDeps,omitempty"`
	// FGLDepPins carries the per-dependency repository pin this package's own
	// manifest declared (dep name → registry name), e.g. {"qrcode":"acme"}.
	// The multi-provider resolver honours these so a package's transitive deps
	// resolve from the repository the author pinned, even when the name also
	// exists in another repository. Populated from an Artifactory sidecar's
	// object-form deps; the GI registry does not carry pins yet (see resolver).
	FGLDepPins map[string]string         `json:"fglDepPins,omitempty"`
	JavaDeps   []manifest.JavaDependency `json:"javaDeps,omitempty"`
	// Variant is the artifact variant tag selected by the registry client
	// when fetching this version — "genero<N>" for BDL packages or
	// "webcomponent" for webcomponent packages. The installer uses it to
	// route the artifact to the right install directory.
	Variant  string        `json:"variant,omitempty"`
	Variants []VariantInfo `json:"variants,omitempty"`
	Readme   string        `json:"readme,omitempty"`
	// Source is the logical name of the repository this info was resolved
	// from ("gi", "acme-internal", …). Set by the multi-provider routing
	// layer; empty means the default GI registry. Threaded into the lockfile
	// as the dependency-confusion pin. See
	// specs/artifactory-secondary-repository.md §9.
	Source string `json:"source,omitempty"`
}

// VariantInfo describes a Genero-major-version-specific build.
type VariantInfo struct {
	GeneroMajor string `json:"generoMajor"`
	DownloadURL string `json:"downloadUrl"`
	Checksum    string `json:"checksum"`
}

// VersionEntry pairs a version string with its declared Genero compatibility.
type VersionEntry struct {
	Version          string   `json:"version"`
	GeneroConstraint string   `json:"genero,omitempty"`
	Variants         []string `json:"variants,omitempty"`
}

// VersionList lists all published versions of a package.
type VersionList struct {
	Name           string         `json:"name"`
	Versions       []string       `json:"versions"`
	VersionEntries []VersionEntry `json:"versionEntries"`
}

// SearchResult is one entry returned by Search.
type SearchResult struct {
	Name          string `json:"name"`
	LatestVersion string `json:"latestVersion"`
	Description   string `json:"description"`
	Author        string `json:"author"`
	// Source is the logical repository this result came from, set by the
	// multi-provider search fan-out. Empty for single-registry results.
	Source string `json:"source,omitempty"`
}

// RegistryConfig is returned by the publisher registry's /config endpoint.
type RegistryConfig struct {
	GitHubRepos []GitHubRepo `json:"githubRepos"`
}

// GitHubRepo identifies a GitHub repository used for package storage.
type GitHubRepo struct {
	Owner string `json:"owner"`
	Repo  string `json:"repo"`
}

// ─── Consumer API (new /registry/... protocol) ───────────────────────────────

// FetchVersionList returns all published versions of name.
func FetchVersionList(name string) (*VersionList, error) {
	d, err := fetchPackageDetail(name)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch version list for %q: %w", name, err)
	}
	out := &VersionList{Name: d.Slug}
	for _, v := range d.Versions {
		out.Versions = append(out.Versions, v.Version)
		variants := make([]string, 0, len(v.Artifacts))
		for _, a := range v.Artifacts {
			variants = append(variants, a.Variant)
		}
		out.VersionEntries = append(out.VersionEntries, VersionEntry{
			Version:  v.Version,
			Variants: variants,
		})
	}
	return out, nil
}

// FetchInfo retrieves full package metadata for name@version.
func FetchInfo(name, version string) (*PackageInfo, error) {
	return FetchInfoForGenero(name, version, "")
}

// FetchInfoForGenero retrieves package metadata, picking the artifact whose
// variant matches generoMajor (e.g. "6" → "genero6"). Empty generoMajor or
// no matching variant falls back to "default", then to the first artifact.
func FetchInfoForGenero(name, version, generoMajor string) (*PackageInfo, error) {
	d, err := fetchPackageDetail(name)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch package info for %s@%s: %w", name, version, err)
	}
	var v *apiVersionSummary
	for i := range d.Versions {
		if d.Versions[i].Version == version {
			v = &d.Versions[i]
			break
		}
	}
	if v == nil {
		return nil, fmt.Errorf("version %q not found for package %q: %w", version, name, ErrNotFound)
	}
	art := pickArtifact(v.Artifacts, generoMajor)
	if art == nil {
		return nil, fmt.Errorf("no artifact available for %s@%s", name, version)
	}
	author := v.Author
	if author == "" {
		author = d.Owner.Name
	}
	info := &PackageInfo{
		Name:             d.Slug,
		Version:          v.Version,
		Description:      d.Description,
		Author:           author,
		License:          v.License,
		PublishedAt:      v.PublishedAt,
		DownloadURL:      AbsoluteDownloadURL(art.DownloadURL),
		Checksum:         art.SHA256,
		GeneroConstraint: v.Genero,
		FGLDeps:          v.Dependencies.FGL,
		JavaDeps:         v.Dependencies.Java,
		Variant:          art.Variant,
		Readme:           v.Readme,
	}
	for _, a := range v.Artifacts {
		info.Variants = append(info.Variants, VariantInfo{
			GeneroMajor: strings.TrimPrefix(a.Variant, "genero"),
			DownloadURL: AbsoluteDownloadURL(a.DownloadURL),
			Checksum:    a.SHA256,
		})
	}
	return info, nil
}

// AbsoluteDownloadURL turns a possibly site-relative download_url returned by
// the registry (e.g. "/registry/packages/x/versions/1.0.0/artifacts/genero6")
// into an absolute URL against the consumer base, so an HTTP GET has a scheme
// + host. Already-absolute URLs (an R2/CDN redirect target that carries its
// own scheme) are returned unchanged. Idempotent — safe to call on a URL that
// is already absolute, which is how the installer normalizes URLs read from an
// older lock file that persisted the relative form.
func AbsoluteDownloadURL(raw string) string {
	if raw == "" {
		return raw
	}
	if strings.HasPrefix(raw, "http://") || strings.HasPrefix(raw, "https://") {
		return raw
	}
	return registryBase() + "/" + strings.TrimPrefix(raw, "/")
}

// Resolve fetches the best matching version of name for the given constraint.
// constraint may be "latest", "*", or any semver constraint string (e.g.
// "^1.2.0"). generoMajor selects the variant; "" picks the default.
func Resolve(name, constraint, generoMajor string) (*PackageInfo, error) {
	vl, err := FetchVersionList(name)
	if err != nil {
		return nil, err
	}
	candidates := make([]semver.Version, 0, len(vl.Versions))
	for _, vs := range vl.Versions {
		v, err := semver.Parse(vs)
		if err != nil {
			continue
		}
		candidates = append(candidates, v)
	}
	c, err := semver.ParseConstraint(constraint)
	if err != nil {
		return nil, fmt.Errorf("invalid version constraint %q: %w", constraint, err)
	}
	best, err := c.Latest(candidates)
	if err != nil {
		return nil, fmt.Errorf("no version of %q satisfies constraint %q", name, constraint)
	}
	return FetchInfoForGenero(name, best.String(), generoMajor)
}

// Search queries the consumer registry for packages matching term.
func Search(term string) ([]SearchResult, error) {
	u := fmt.Sprintf("%s/registry/packages?q=%s", registryBase(), url.QueryEscape(term))
	data, err := httpGetAuthed(u)
	if err != nil {
		return nil, fmt.Errorf("search failed: %w", err)
	}
	var br apiBrowseResponse
	if err := json.Unmarshal(data, &br); err != nil {
		return nil, fmt.Errorf("invalid registry response: %w", err)
	}
	results := make([]SearchResult, 0, len(br.Packages))
	for _, p := range br.Packages {
		results = append(results, SearchResult{
			Name:          p.Slug,
			LatestVersion: p.LatestVersion,
			Description:   p.Description,
			Author:        p.Owner.Name,
		})
	}
	return results, nil
}

// ─── Publisher API (new /registry/... protocol) ──────────────────────────────

// PublishCreatePackage creates the slug on the registry if it doesn't exist.
// Returns nil on both 201 (created) and 409 (already exists) — callers don't
// need to differentiate, they only care that the slug is now claimable.
func PublishCreatePackage(slug, name, description, visibility string) error {
	if visibility == "" {
		visibility = "public"
	}
	body, _ := json.Marshal(map[string]string{
		"slug":        slug,
		"name":        name,
		"description": description,
		"visibility":  visibility,
	})
	status, respBody, err := publishJSON(http.MethodPost, registryBase()+"/registry/packages", body)
	if err != nil {
		return fmt.Errorf("create package %q: %w", slug, err)
	}
	if status == http.StatusCreated || status == http.StatusConflict {
		return nil
	}
	return fmt.Errorf("create package %q: HTTP %d: %s", slug, status, string(respBody))
}

// PublishCreateVersion adds version under slug. Returns nil on 201; returns
// ErrVersionExists (still wrapped) on 409 so callers can choose to upload a
// new variant against an existing version.
var ErrVersionExists = errors.New("version already exists")

// VersionMeta carries the optional rich metadata pushed alongside a new
// version on create — repository, author, license, genero constraint,
// production dependencies, and the README / USERGUIDE markdown bodies. All
// fields are optional; empty ones are omitted from the payload and the
// registry defaults them, so older registries (and the empty case) are
// unaffected. The registry stores this at version-create time only; it is
// never mutated by the artifact upload or a re-submit.
type VersionMeta struct {
	Repository   string
	Author       string
	License      string
	Genero       string
	Dependencies manifest.Dependencies
	Readme       string
	Userguide    string
}

func PublishCreateVersion(slug, version, changelog string, tags map[string][]string, meta VersionMeta) error {
	payload := map[string]any{
		"version":   version,
		"changelog": changelog,
	}
	if len(tags) > 0 {
		payload["tags"] = tags
	}
	if meta.Repository != "" {
		payload["repository"] = meta.Repository
	}
	if meta.Author != "" {
		payload["author"] = meta.Author
	}
	if meta.License != "" {
		payload["license"] = meta.License
	}
	if meta.Genero != "" {
		payload["genero"] = meta.Genero
	}
	if len(meta.Dependencies.FGL) > 0 || len(meta.Dependencies.Java) > 0 {
		payload["dependencies"] = meta.Dependencies
	}
	if meta.Readme != "" {
		payload["readme"] = meta.Readme
	}
	if meta.Userguide != "" {
		payload["userguide"] = meta.Userguide
	}
	body, _ := json.Marshal(payload)
	status, respBody, err := publishJSON(http.MethodPost,
		fmt.Sprintf("%s/registry/packages/%s/versions",
			registryBase(), url.PathEscape(slug)), body)
	if err != nil {
		return fmt.Errorf("create version %s@%s: %w", slug, version, err)
	}
	if status == http.StatusCreated {
		return nil
	}
	if status == http.StatusConflict {
		return fmt.Errorf("create version %s@%s: %w", slug, version, ErrVersionExists)
	}
	return fmt.Errorf("create version %s@%s: HTTP %d: %s", slug, version, status, string(respBody))
}

// PublishUploadArtifact streams the zip body for (slug, version, variant)
// to the registry. The server computes size_bytes + sha256 and stores the
// blob in R2. Allowed only while the version is pending or rejected; on an
// approved version the server returns 409. filename is what the registry
// records for download Content-Disposition.
func PublishUploadArtifact(slug, version, variant, filename string, zip io.Reader) error {
	u := fmt.Sprintf("%s/registry/packages/%s/versions/%s/artifacts/%s?filename=%s",
		registryBase(),
		url.PathEscape(slug), url.PathEscape(version), url.PathEscape(variant),
		url.QueryEscape(filename))
	bearer := Bearer()
	status, respBody, err := putBytes(u, "application/zip", bearer, zip)
	if err != nil {
		return fmt.Errorf("upload artifact %s@%s/%s: %w", slug, version, variant, err)
	}
	if status >= 200 && status < 300 {
		return nil
	}
	return fmt.Errorf("upload artifact %s@%s/%s: HTTP %d: %s",
		slug, version, variant, status, string(respBody))
}

// PublishSubmit marks a pending version for admin review. Idempotent — a
// no-op response on 200 if the version is already pending.
func PublishSubmit(slug, version string) error {
	u := fmt.Sprintf("%s/registry/packages/%s/versions/%s/submit",
		registryBase(), url.PathEscape(slug), url.PathEscape(version))
	status, respBody, err := publishJSON(http.MethodPost, u, nil)
	if err != nil {
		return fmt.Errorf("submit %s@%s: %w", slug, version, err)
	}
	if status >= 200 && status < 300 {
		return nil
	}
	return fmt.Errorf("submit %s@%s: HTTP %d: %s", slug, version, status, string(respBody))
}

// VariantsFor reports which variants are already published for (slug, version).
// Returns ErrNotFound wrapped if the package or version doesn't exist yet,
// which the publish flow treats as "nothing to clobber". On the new protocol
// this uses the same package detail endpoint the consumer side does, so the
// caller doesn't need a separate auth path.
func VariantsFor(slug, version string) ([]string, error) {
	d, err := fetchPackageDetail(slug)
	if err != nil {
		return nil, err
	}
	for _, v := range d.Versions {
		if v.Version != version {
			continue
		}
		out := make([]string, 0, len(v.Artifacts))
		for _, a := range v.Artifacts {
			out = append(out, a.Variant)
		}
		return out, nil
	}
	return nil, fmt.Errorf("version %s of %s not found on registry: %w",
		version, slug, ErrNotFound)
}

// ─── Publisher API (LEGACY /packages/... protocol) ───────────────────────────
// Only used by cmdUnpublish, cmdOwner, cmdToken, cmdConfig — operations that
// have no equivalent on the new /registry/* protocol. Hardcoded against the
// fly.dev URL via the helpers below.

// ─── Internal: new-protocol types ────────────────────────────────────────────

type apiArtifact struct {
	Variant     string `json:"variant"`
	Filename    string `json:"filename"`
	SizeBytes   int64  `json:"size_bytes"`
	SHA256      string `json:"sha256"`
	DownloadURL string `json:"download_url"`
}

type apiVersionSummary struct {
	Version       string              `json:"version"`
	Status        string              `json:"status"`
	Changelog     string              `json:"changelog"`
	Tags          map[string][]string `json:"tags"`
	Artifacts     []apiArtifact       `json:"artifacts"`
	SubmittedAt   string              `json:"submitted_at"`
	PublishedAt   string              `json:"published_at"`
	ReviewComment string              `json:"review_comment"`
	Repository    string              `json:"repository"`
	Author        string              `json:"author"`
	License       string              `json:"license"`
	Genero        string              `json:"genero"`
	Dependencies  apiVersionDeps      `json:"dependencies"`
	Readme        string              `json:"readme"`
	Userguide     string              `json:"userguide"`
}

type apiVersionDeps struct {
	FGL  map[string]string         `json:"fgl"`
	Java []manifest.JavaDependency `json:"java"`
}

type apiOwner struct {
	PartnerID string `json:"partner_id"`
	Name      string `json:"name"`
}

type apiListedPackage struct {
	Slug          string              `json:"slug"`
	Name          string              `json:"name"`
	Description   string              `json:"description"`
	Visibility    string              `json:"visibility"`
	Owner         apiOwner            `json:"owner"`
	Status        string              `json:"status"`
	LatestVersion string              `json:"latest_version"`
	Downloads     int64               `json:"downloads"`
	Tags          map[string][]string `json:"tags"`
}

type apiPackageDetail struct {
	apiListedPackage
	Versions []apiVersionSummary `json:"versions"`
}

type apiBrowseResponse struct {
	Packages []apiListedPackage `json:"packages"`
	Page     int                `json:"page"`
	PageSize int                `json:"pageSize"`
	Total    int                `json:"total"`
}

func fetchPackageDetail(slug string) (*apiPackageDetail, error) {
	// Canonicalize the name to its slug so any spelling (fgl_ai_sdk, Fgl.AI.SDK,
	// fgl-ai-sdk) resolves to the same /registry/packages/<slug> record (GIS-271).
	slug = slugutil.Canonical(slug)
	u := fmt.Sprintf("%s/registry/packages/%s", registryBase(), url.PathEscape(slug))
	data, err := httpGetAuthed(u)
	if err != nil {
		return nil, err
	}
	var d apiPackageDetail
	if err := json.Unmarshal(data, &d); err != nil {
		return nil, fmt.Errorf("invalid package detail response: %w", err)
	}
	if d.Slug == "" {
		d.Slug = slug
	}
	return &d, nil
}

// pickArtifact selects the best matching artifact for generoMajor.
// Order of preference:
//  1. "webcomponent" — kind-discriminating variant, matches any genero version
//  2. exact "genero<N>" match
//  3. "default"
//  4. first listed
//
// The webcomponent check is first because a webcomponent-only version has
// exactly one artifact (the "webcomponent" one), and the BDL fallbacks
// would otherwise miss it.
func pickArtifact(arts []apiArtifact, generoMajor string) *apiArtifact {
	if len(arts) == 0 {
		return nil
	}
	for i := range arts {
		if arts[i].Variant == "webcomponent" {
			return &arts[i]
		}
	}
	if generoMajor != "" {
		want := "genero" + generoMajor
		for i := range arts {
			if arts[i].Variant == want {
				return &arts[i]
			}
		}
	}
	for i := range arts {
		if arts[i].Variant == "default" {
			return &arts[i]
		}
	}
	return &arts[0]
}

// ─── Internal: HTTP ──────────────────────────────────────────────────────────

func registryBase() string {
	if r := os.Getenv("FGLPKG_REGISTRY"); r != "" {
		return strings.TrimRight(r, "/")
	}
	return defaultRegistryBase
}

// httpGetAuthed performs a GET against the consumer registry, sending the
// current Bearer() as an Authorization header. On a 401, calls TryRefresh()
// once and retries.
func httpGetAuthed(u string) ([]byte, error) {
	body, status, err := authedGet(u, Bearer())
	if err != nil {
		return nil, err
	}
	if status == http.StatusUnauthorized && TryRefresh() {
		body, status, err = authedGet(u, Bearer())
		if err != nil {
			return nil, err
		}
	}
	return finalise(body, status)
}

// httpGetPublisher performs an unauthenticated GET against the publisher
// registry. The current publisher commands keep their PAT-based auth in cli;
// this helper exists for the few endpoints (e.g. /config, /packages/<name>/versions)
// that are world-readable today.
func httpGetPublisher(u string) ([]byte, error) {
	resp, err := http.Get(u)
	if err != nil {
		return nil, fmt.Errorf("registry request failed: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read registry response: %w", err)
	}
	return finalise(body, resp.StatusCode)
}

// publishJSON does method u with optional JSON body, authenticated via
// Bearer(). One-shot 401-retry via TryRefresh, same pattern as httpGetAuthed.
// Returns (status, body, err); the caller inspects status because publish
// endpoints use 201/200/409 to mean distinct things.
func publishJSON(method, u string, body []byte) (int, []byte, error) {
	doOnce := func() (int, []byte, error) {
		var reader io.Reader
		if body != nil {
			reader = bytes.NewReader(body)
		}
		req, err := http.NewRequest(method, u, reader)
		if err != nil {
			return 0, nil, err
		}
		req.Header.Set("Accept", "application/json")
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		if b := Bearer(); b != "" {
			req.Header.Set("Authorization", "Bearer "+b)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return 0, nil, fmt.Errorf("registry request failed: %w", err)
		}
		defer resp.Body.Close()
		buf, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, buf, nil
	}
	status, respBody, err := doOnce()
	if err != nil {
		return 0, nil, err
	}
	if status == http.StatusUnauthorized && TryRefresh() {
		return doOnce()
	}
	return status, respBody, nil
}

// putBytes streams body to u with the given content type. Used for the zip
// upload step of publish. No 401 retry on streaming PUT — the body has been
// consumed, so retry would require buffering. Caller can retry at the
// fglpkg-publish level if a 401 surfaces.
func putBytes(u, contentType, bearer string, body io.Reader) (int, []byte, error) {
	req, err := http.NewRequest(http.MethodPut, u, body)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("Accept", "application/json")
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("registry upload failed: %w", err)
	}
	defer resp.Body.Close()
	buf, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, buf, nil
}

func authedGet(u, bearer string) ([]byte, int, error) {
	req, err := http.NewRequest(http.MethodGet, u, nil)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Accept", "application/json")
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("registry request failed: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to read registry response: %w", err)
	}
	return body, resp.StatusCode, nil
}

func finalise(body []byte, status int) ([]byte, error) {
	if status == http.StatusNotFound {
		return nil, ErrNotFound
	}
	if status < 200 || status >= 300 {
		return nil, fmt.Errorf("registry returned HTTP %d: %s", status, string(body))
	}
	return body, nil
}
