package provider

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/4js-mikefolcher/fglpkg/internal/config"
	"github.com/4js-mikefolcher/fglpkg/internal/manifest"
	"github.com/4js-mikefolcher/fglpkg/internal/registry"
	"github.com/4js-mikefolcher/fglpkg/internal/resolver"
	"github.com/4js-mikefolcher/fglpkg/internal/semver"
)

// AuthApplier applies a repository's configured auth scheme to an outbound
// request (e.g. sets Authorization or X-JFrog-Art-Api). May be nil for
// anonymous repositories. Built from credentials + the descriptor's scheme by
// the CLI; injected here so this package stays credential-free and testable.
type AuthApplier func(*http.Request)

// ArtifactoryProvider resolves FGL packages from a JFrog Artifactory generic
// repository using the storage API. Layout (see
// specs/artifactory-secondary-repository.md §7.1):
//
//	{url}/{repoKey}/{name}/{version}/{name}-{version}-{variant}.zip
//	{url}/{repoKey}/{name}/{version}/fglpkg.json      (sidecar manifest)
type ArtifactoryProvider struct {
	reg    config.Registry
	client *http.Client
	auth   AuthApplier
}

// NewArtifactoryProvider builds a provider for the given artifactory descriptor.
// If client is nil, http.DefaultClient is used.
func NewArtifactoryProvider(reg config.Registry, client *http.Client, auth AuthApplier) *ArtifactoryProvider {
	if client == nil {
		client = http.DefaultClient
	}
	return &ArtifactoryProvider{reg: reg, client: client, auth: auth}
}

func (a *ArtifactoryProvider) Name() string { return a.reg.Name }

// ── storage API response shapes (verified live against JFrog Cloud) ──────────

type folderInfo struct {
	Children []struct {
		URI    string `json:"uri"`
		Folder bool   `json:"folder"`
	} `json:"children"`
}

type fileInfo struct {
	DownloadURI string `json:"downloadUri"`
	Checksums   struct {
		SHA256 string `json:"sha256"`
	} `json:"checksums"`
}

// storageURL builds an /api/storage URL for a path under the repo.
func (a *ArtifactoryProvider) storageURL(elem ...string) string {
	parts := append([]string{a.reg.URL, "api", "storage", a.reg.RepoKey}, elem...)
	return strings.Join(parts, "/")
}

// contentURL builds a direct (non-storage) URL for a path under the repo, used
// for the sidecar manifest.
func (a *ArtifactoryProvider) contentURL(elem ...string) string {
	parts := append([]string{a.reg.URL, a.reg.RepoKey}, elem...)
	return strings.Join(parts, "/")
}

// getJSON GETs url and decodes JSON into out. It maps 404 to ErrNotFound and
// 401/403 to a distinct auth error — never conflating "absent" with
// "unauthorised" (spec §7.2), which the collision guard depends on.
func (a *ArtifactoryProvider) getJSON(url string, out interface{}) error {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	if a.auth != nil {
		a.auth(req)
	}
	resp, err := a.client.Do(req)
	if err != nil {
		return fmt.Errorf("artifactory %s: %w", a.reg.Name, err)
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
		if out == nil {
			return nil
		}
		return json.NewDecoder(resp.Body).Decode(out)
	case http.StatusNotFound:
		return ErrNotFound
	case http.StatusUnauthorized, http.StatusForbidden:
		return fmt.Errorf(
			"artifactory %s: authentication failed (%d) for %s — check credentials for this repository",
			a.reg.Name, resp.StatusCode, url,
		)
	default:
		return fmt.Errorf("artifactory %s: unexpected status %d for %s", a.reg.Name, resp.StatusCode, url)
	}
}

// FetchVersions lists the version folders under the package name. A 404 means
// the package is absent (ErrNotFound); non-semver folders are skipped with a
// warning. GeneroConstraint is left empty here and filled lazily by FetchInfo
// from the per-version sidecar (avoids a metadata read per version).
func (a *ArtifactoryProvider) FetchVersions(name string) ([]resolver.CandidateVersion, error) {
	var fi folderInfo
	if err := a.getJSON(a.storageURL(name), &fi); err != nil {
		return nil, err
	}
	var out []resolver.CandidateVersion
	for _, c := range fi.Children {
		if !c.Folder {
			continue
		}
		raw := strings.TrimPrefix(c.URI, "/")
		v, err := semver.Parse(raw)
		if err != nil {
			fmt.Fprintf(os.Stderr, "warning: %s: skipping non-semver version folder %q in %s\n", a.reg.Name, raw, name)
			continue
		}
		out = append(out, resolver.CandidateVersion{Version: v})
	}
	if len(out) == 0 {
		return nil, ErrNotFound
	}
	return out, nil
}

// FetchInfo resolves name@version: lists the version folder to discover
// variants, selects one for generoMajor, reads the file's SHA-256 + download
// URL, and reads the sidecar manifest for dependencies + genero constraint.
func (a *ArtifactoryProvider) FetchInfo(name, version, generoMajor string) (*registry.PackageInfo, error) {
	var fi folderInfo
	if err := a.getJSON(a.storageURL(name, version), &fi); err != nil {
		return nil, err
	}

	tags := make([]string, 0, len(fi.Children))
	for _, c := range fi.Children {
		if c.Folder {
			continue
		}
		zipName := strings.TrimPrefix(c.URI, "/")
		if tag, ok := variantTag(name, version, zipName); ok {
			tags = append(tags, tag)
		}
	}
	variant := pickVariant(tags, generoMajor)
	if variant == "" {
		return nil, fmt.Errorf("artifactory %s: no installable variant for %s@%s (found: %s)",
			a.reg.Name, name, version, strings.Join(tags, ", "))
	}
	zipName := fmt.Sprintf("%s-%s-%s.zip", name, version, variant)

	var file fileInfo
	if err := a.getJSON(a.storageURL(name, version, zipName), &file); err != nil {
		return nil, err
	}

	info := &registry.PackageInfo{
		Name:        name,
		Version:     version,
		DownloadURL: file.DownloadURI,
		Checksum:    file.Checksums.SHA256,
		Variant:     variant,
		Source:      a.reg.Name,
	}

	// Sidecar manifest — best effort. Supplies deps + genero constraint.
	side, err := a.fetchSidecar(name, version)
	if err == nil {
		info.Description = side.Description
		info.Author = side.Author
		info.License = side.License
		info.GeneroConstraint = side.GeneroConstraint
		info.FGLDeps = side.Dependencies.FGL
		info.FGLDepPins = side.Dependencies.FGLPins
		info.JavaDeps = side.Dependencies.Java
	} else if err != ErrNotFound {
		return nil, err
	} else {
		fmt.Fprintf(os.Stderr, "warning: %s: no sidecar %s for %s@%s; dependencies unknown\n",
			a.reg.Name, manifest.Filename, name, version)
	}

	return info, nil
}

// fetchSidecar reads the per-version sidecar fglpkg.json. It returns ErrNotFound
// (unwrapped) when the sidecar is absent so callers can treat that as
// non-fatal; any other transport/auth error is returned as-is.
func (a *ArtifactoryProvider) fetchSidecar(name, version string) (*manifest.Manifest, error) {
	var side manifest.Manifest
	if err := a.getJSON(a.contentURL(name, version, manifest.Filename), &side); err != nil {
		return nil, err
	}
	return &side, nil
}

// Search enumerates the repository's top-level package folders and returns
// those whose name contains term (case-insensitive), with the highest version
// as LatestVersion. No AQL is used (a Phase-3 option for very large repos).
func (a *ArtifactoryProvider) Search(term string) ([]registry.SearchResult, error) {
	var root folderInfo
	if err := a.getJSON(a.storageURL(), &root); err != nil {
		if err == ErrNotFound {
			return nil, nil
		}
		return nil, err
	}
	term = strings.ToLower(term)
	var results []registry.SearchResult
	for _, c := range root.Children {
		if !c.Folder {
			continue
		}
		name := strings.TrimPrefix(c.URI, "/")
		if term != "" && !strings.Contains(strings.ToLower(name), term) {
			continue
		}
		if !a.reg.Admits(name) {
			continue
		}
		res := registry.SearchResult{Name: name, Source: a.reg.Name}
		if versions, err := a.FetchVersions(name); err == nil {
			latest := highestVersion(versions).String()
			res.LatestVersion = latest
			// Best-effort: read the latest version's sidecar so the result carries
			// a description/author like the GI path does (spec ISSUE-E). A missing
			// or unparseable sidecar leaves the fields blank rather than failing
			// the whole search.
			if side, err := a.fetchSidecar(name, latest); err == nil {
				res.Description = side.Description
				res.Author = side.Author
			}
		}
		results = append(results, res)
	}
	return results, nil
}

// variantTag extracts the variant from "{pkg}-{version}-{variant}.zip".
func variantTag(pkg, version, zipName string) (string, bool) {
	prefix := pkg + "-" + version + "-"
	if strings.HasPrefix(zipName, prefix) && strings.HasSuffix(zipName, ".zip") {
		return zipName[len(prefix) : len(zipName)-len(".zip")], true
	}
	return "", false
}

// pickVariant mirrors registry.pickArtifact's preference order:
// webcomponent -> genero{major} -> default -> first.
func pickVariant(tags []string, generoMajor string) string {
	for _, t := range tags {
		if t == "webcomponent" {
			return t
		}
	}
	if generoMajor != "" {
		want := "genero" + generoMajor
		for _, t := range tags {
			if t == want {
				return t
			}
		}
	}
	for _, t := range tags {
		if t == "default" {
			return t
		}
	}
	if len(tags) > 0 {
		return tags[0]
	}
	return ""
}

// PublishRequest describes one package variant to deploy to Artifactory.
type PublishRequest struct {
	Name     string
	Version  string
	Variant  string // e.g. "genero6" / "webcomponent"
	Zip      []byte
	Checksum string // SHA-256 hex; sent as X-Checksum-Sha256 and verified on receipt
	Manifest []byte // sidecar fglpkg.json bytes
	Force    bool   // overwrite an existing variant
	DryRun   bool
}

// Publish deploys a built package variant and its sidecar manifest to the
// generic repository (spec §10): PUT the zip with X-Checksum-Sha256 (Artifactory
// verifies it on receipt), then PUT the sidecar. An overwrite guard refuses to
// clobber an existing variant unless Force is set. There is no pending/approval
// step and visibility is not sent (both are GI-specific).
func (a *ArtifactoryProvider) Publish(req PublishRequest) error {
	zipName := fmt.Sprintf("%s-%s-%s.zip", req.Name, req.Version, req.Variant)
	zipURL := a.contentURL(req.Name, req.Version, zipName)
	sideURL := a.contentURL(req.Name, req.Version, manifest.Filename)

	if req.DryRun {
		fmt.Printf("  PUT %s  (X-Checksum-Sha256: %s)\n", zipURL, req.Checksum)
		fmt.Printf("  PUT %s  (sidecar manifest)\n", sideURL)
		return nil
	}

	// Overwrite guard: refuse to clobber an existing variant unless forced.
	if !req.Force {
		var existing fileInfo
		err := a.getJSON(a.storageURL(req.Name, req.Version, zipName), &existing)
		switch {
		case err == nil:
			return fmt.Errorf(
				"variant already exists: %s\n  re-run with --force to overwrite, or bump the version",
				zipURL)
		case errors.Is(err, ErrNotFound):
			// expected — proceed
		default:
			return err // auth or other hard error
		}
	}

	if err := a.put(zipURL, req.Zip, map[string]string{
		"X-Checksum-Sha256": req.Checksum,
		"Content-Type":      "application/zip",
	}); err != nil {
		return err
	}
	fmt.Printf("  deployed %s\n", zipURL)

	if err := a.put(sideURL, req.Manifest, map[string]string{"Content-Type": "application/json"}); err != nil {
		return err
	}
	fmt.Printf("  deployed %s\n", sideURL)
	return nil
}

// put uploads body to url with the given headers plus the configured auth.
func (a *ArtifactoryProvider) put(url string, body []byte, headers map[string]string) error {
	req, err := http.NewRequest(http.MethodPut, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	if a.auth != nil {
		a.auth(req)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := a.client.Do(req)
	if err != nil {
		return fmt.Errorf("artifactory %s: %w", a.reg.Name, err)
	}
	defer func() { io.Copy(io.Discard, resp.Body); resp.Body.Close() }()
	switch resp.StatusCode {
	case http.StatusOK, http.StatusCreated:
		return nil
	case http.StatusUnauthorized, http.StatusForbidden:
		return fmt.Errorf("artifactory %s: authentication failed (%d) deploying %s", a.reg.Name, resp.StatusCode, url)
	case http.StatusConflict:
		return fmt.Errorf("artifactory %s: %s is immutable (409) — cannot overwrite", a.reg.Name, url)
	default:
		return fmt.Errorf("artifactory %s: unexpected status %d deploying %s", a.reg.Name, resp.StatusCode, url)
	}
}

func highestVersion(vs []resolver.CandidateVersion) semver.Version {
	var best semver.Version
	for i, v := range vs {
		if i == 0 || v.Version.GreaterThan(best) {
			best = v.Version
		}
	}
	return best
}
