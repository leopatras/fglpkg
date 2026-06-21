// Package lockfile manages fglpkg.lock — the reproducible install record.
//
// The lock file captures the exact resolved state of every dependency in the
// graph: BDL packages (name, version, download URL, SHA256 checksum, which
// packages required it) and Java JARs (Maven coordinates, download URL,
// SHA256 checksum). It also records the Genero runtime version that was active
// when resolution ran, so a mismatch can be detected on subsequent installs.
//
// File format: JSON, human-readable, intended to be committed to VCS.
//
// Workflow:
//
//	fglpkg install          → resolve → write lock → install from lock
//	fglpkg install (again)  → lock exists & valid → install directly from lock
//	fglpkg update           → re-resolve → overwrite lock → install from lock
package lockfile

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/4js-mikefolcher/fglpkg/internal/manifest"
	"github.com/4js-mikefolcher/fglpkg/internal/resolver"
)

const (
	// Filename is the lock file name, always written next to fglpkg.json.
	Filename = "fglpkg.lock"

	// lockVersion is bumped when the lock file schema changes incompatibly.
	lockVersion = 1
)

// LockFile is the top-level lock file structure.
type LockFile struct {
	// Version is the schema version of this lock file.
	Version int `json:"lockfileVersion"`

	// GeneratedAt is an RFC3339 timestamp of when this lock was written.
	GeneratedAt string `json:"generatedAt"`

	// GeneroVersion is the Genero BDL runtime version active during resolution.
	// If the detected version differs on a subsequent install, a warning is
	// emitted (but the install is not blocked — the user may be intentional).
	GeneroVersion string `json:"generoVersion"`

	// RootManifest records the name and version of the project that owns
	// this lock file, for human reference.
	RootManifest RootEntry `json:"root"`

	// Packages lists every resolved BDL package, sorted by name for stable diffs.
	Packages []LockedPackage `json:"packages"`

	// JARs lists every resolved Java JAR, sorted by key for stable diffs.
	JARs []LockedJAR `json:"jars"`

	// Webcomponents lists every resolved webcomponent package (variant
	// "webcomponent"), sorted by name for stable diffs. Separate from
	// Packages because webcomponent packages have different install
	// semantics (extracted to .fglpkg/webcomponents/, no genero variant,
	// no bin scripts).
	Webcomponents []LockedWebcomponent `json:"webcomponents,omitempty"`
}

// RootEntry records the identity of the root project.
type RootEntry struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// LockedPackage is the fully-pinned record of one BDL package.
type LockedPackage struct {
	// Name is the package name, e.g. "myutils".
	Name string `json:"name"`

	// Version is the exact resolved semver string, e.g. "1.2.3".
	Version string `json:"version"`

	// GeneroConstraint is the Genero compatibility range declared by this
	// package version, e.g. "^4.0.0". Stored for auditing.
	GeneroConstraint string `json:"genero,omitempty"`

	// DownloadURL is the exact URL used to download this version.
	DownloadURL string `json:"downloadUrl"`

	// Checksum is the SHA256 hex digest of the downloaded zip file.
	// Empty means the registry provided no checksum (install will skip verify).
	Checksum string `json:"checksum,omitempty"`

	// GeneroMajor is the Genero major version variant that was selected
	// (e.g. "4", "6"). Empty for legacy packages without variants.
	GeneroMajor string `json:"generoMajor,omitempty"`

	// RequiredBy lists every package (or "<root>") that declared a dependency
	// on this package, enabling humans to trace why it was included.
	RequiredBy []string `json:"requiredBy"`

	// Scope is the dependency scope this package was installed under: "dev"
	// or "optional". Empty means production. Used by `fglpkg install
	// --production` to skip dev-scoped entries.
	Scope string `json:"scope,omitempty"`
}

// LockedWebcomponent is the fully-pinned record of one webcomponent package.
// COMPONENTTYPE names are not persisted here in v1 — they are inferred at
// runtime by listing subdirectories of the install location. Future versions
// may persist them once the registry exposes the manifest's webcomponents
// field server-side.
type LockedWebcomponent struct {
	// Name is the package name, e.g. "chart-3d".
	Name string `json:"name"`

	// Version is the exact resolved semver string.
	Version string `json:"version"`

	// DownloadURL is the exact URL used to download this version (variant
	// "webcomponent").
	DownloadURL string `json:"downloadUrl"`

	// Checksum is the SHA256 hex digest of the downloaded zip file.
	Checksum string `json:"checksum,omitempty"`

	// RequiredBy lists every package (or "<root>") that declared a dependency
	// on this webcomponent package.
	RequiredBy []string `json:"requiredBy"`

	// Scope is the dependency scope: "dev" or "optional". Empty means
	// production.
	Scope string `json:"scope,omitempty"`
}

// LockedJAR is the fully-pinned record of one Java JAR.
type LockedJAR struct {
	// Key is "groupId:artifactId", the deduplication key.
	Key string `json:"key"`

	GroupID    string `json:"groupId"`
	ArtifactID string `json:"artifactId"`
	Version    string `json:"version"`

	// DownloadURL is the resolved Maven Central (or override) URL.
	DownloadURL string `json:"downloadUrl"`

	// Checksum is the SHA256 hex digest of the JAR file.
	Checksum string `json:"checksum,omitempty"`

	// Scope is the dependency scope this JAR was installed under: "dev"
	// or "optional". Empty means production.
	Scope string `json:"scope,omitempty"`
}

// ─── Construction ─────────────────────────────────────────────────────────────

// FromPlan builds a LockFile from a resolved Plan and the root manifest.
// Packages with variant "webcomponent" land in the Webcomponents array;
// everything else lands in Packages.
func FromPlan(plan *resolver.Plan, root *manifest.Manifest) *LockFile {
	pkgs := make([]LockedPackage, 0, len(plan.Packages))
	wcs := make([]LockedWebcomponent, 0)
	for _, p := range plan.Packages {
		requiredBy := make([]string, len(p.RequiredBy))
		copy(requiredBy, p.RequiredBy)
		sort.Strings(requiredBy)

		if p.IsWebcomponent() {
			wcs = append(wcs, LockedWebcomponent{
				Name:        p.Name,
				Version:     p.Version.String(),
				DownloadURL: p.DownloadURL,
				Checksum:    p.Checksum,
				RequiredBy:  requiredBy,
				Scope:       scopeLockString(p.Scope),
			})
			continue
		}

		pkgs = append(pkgs, LockedPackage{
			Name:        p.Name,
			Version:     p.Version.String(),
			DownloadURL: p.DownloadURL,
			Checksum:    p.Checksum,
			GeneroMajor: plan.GeneroVersion.MajorString(),
			RequiredBy:  requiredBy,
			Scope:       scopeLockString(p.Scope),
		})
	}
	sort.Slice(pkgs, func(i, j int) bool { return pkgs[i].Name < pkgs[j].Name })
	sort.Slice(wcs, func(i, j int) bool { return wcs[i].Name < wcs[j].Name })

	jars := make([]LockedJAR, 0, len(plan.JARs))
	for _, dep := range plan.JARs {
		jars = append(jars, LockedJAR{
			Key:         dep.Key(),
			GroupID:     dep.GroupID,
			ArtifactID:  dep.ArtifactID,
			Version:     dep.Version,
			DownloadURL: dep.MavenURL(),
			Checksum:    dep.Checksum,
			Scope:       scopeLockString(plan.JARScopes[dep.Key()]),
		})
	}
	sort.Slice(jars, func(i, j int) bool { return jars[i].Key < jars[j].Key })

	return &LockFile{
		Version:       lockVersion,
		GeneratedAt:   time.Now().UTC().Format(time.RFC3339),
		GeneroVersion: plan.GeneroVersion.String(),
		RootManifest:  RootEntry{Name: root.Name, Version: root.Version},
		Packages:      pkgs,
		JARs:          jars,
		Webcomponents: wcs,
	}
}

// ─── Persistence ──────────────────────────────────────────────────────────────

// Save writes the lock file as formatted JSON to dir/fglpkg.lock.
func (lf *LockFile) Save(dir string) error {
	data, err := json.MarshalIndent(lf, "", "  ")
	if err != nil {
		return fmt.Errorf("cannot serialise lock file: %w", err)
	}
	path := filepath.Join(dir, Filename)
	if err := os.WriteFile(path, append(data, '\n'), 0644); err != nil {
		return fmt.Errorf("cannot write %s: %w", path, err)
	}
	return nil
}

// Load reads and parses the lock file from dir/fglpkg.lock.
func Load(dir string) (*LockFile, error) {
	path := filepath.Join(dir, Filename)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var lf LockFile
	if err := json.Unmarshal(data, &lf); err != nil {
		return nil, fmt.Errorf("invalid %s: %w", Filename, err)
	}
	return &lf, nil
}

// Exists reports whether a lock file exists in dir.
func Exists(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, Filename))
	return err == nil
}

// ─── Validation ───────────────────────────────────────────────────────────────

// ValidationResult is returned by Validate, describing any problems found.
type ValidationResult struct {
	// SchemaError is set when the lock file's schema version is unrecognised.
	SchemaError error

	// GeneroMismatch is set when the current Genero version differs from the
	// one recorded in the lock file.
	GeneroMismatch *GeneroMismatchError

	// ManifestMismatch is set when the root manifest's name or version has
	// changed since the lock was written (lock is stale).
	ManifestMismatch *ManifestMismatchError

	// MissingPackages lists packages in the lock that are not yet installed
	// (install directory absent).
	MissingPackages []string

	// MissingWebcomponents lists webcomponent packages in the lock whose
	// install does not appear under the webcomponents directory.
	MissingWebcomponents []string
}

// IsClean returns true when there are no errors or mismatches at all.
func (vr *ValidationResult) IsClean() bool {
	return vr.SchemaError == nil &&
		vr.GeneroMismatch == nil &&
		vr.ManifestMismatch == nil &&
		len(vr.MissingPackages) == 0 &&
		len(vr.MissingWebcomponents) == 0
}

// NeedsResolve returns true when a full re-resolution is required before
// installing (schema incompatible or manifest has changed).
func (vr *ValidationResult) NeedsResolve() bool {
	return vr.SchemaError != nil || vr.ManifestMismatch != nil
}

// GeneroMismatchError describes a Genero version difference.
type GeneroMismatchError struct {
	Locked  string // version in lock file
	Current string // version detected now
}

func (e *GeneroMismatchError) Error() string {
	return fmt.Sprintf(
		"lock file was generated with Genero %s but current runtime is %s.\n"+
			"Run 'fglpkg install' to re-resolve for the current Genero version.",
		e.Locked, e.Current,
	)
}

// ManifestMismatchError describes a stale lock file (manifest changed).
type ManifestMismatchError struct {
	Field    string
	InLock   string
	InManifest string
}

func (e *ManifestMismatchError) Error() string {
	return fmt.Sprintf(
		"lock file is stale: %s changed from %q (lock) to %q (manifest).\n"+
			"Run 'fglpkg install' to update the lock file.",
		e.Field, e.InLock, e.InManifest,
	)
}

// Validate checks whether the lock file is consistent with the current
// environment and manifest. currentGenero may be "" to skip that check.
// packagesDir / webcomponentsDir are used to check which BDL and webcomponent
// installs are actually present on disk; pass "" to skip either check.
func (lf *LockFile) Validate(root *manifest.Manifest, currentGenero, packagesDir, webcomponentsDir string) *ValidationResult {
	result := &ValidationResult{}

	// Schema version check.
	if lf.Version != lockVersion {
		result.SchemaError = fmt.Errorf(
			"lock file schema version %d is not supported (expected %d)",
			lf.Version, lockVersion,
		)
		return result // nothing else makes sense to check
	}

	// Genero version check. Treated as a warning only — the user decides
	// whether to run `fglpkg update` to re-resolve for a different runtime.
	if currentGenero != "" && lf.GeneroVersion != currentGenero {
		result.GeneroMismatch = &GeneroMismatchError{
			Locked:  lf.GeneroVersion,
			Current: currentGenero,
		}
	}

	// Root manifest identity check.
	if lf.RootManifest.Name != root.Name {
		result.ManifestMismatch = &ManifestMismatchError{
			Field: "project name", InLock: lf.RootManifest.Name, InManifest: root.Name,
		}
	} else if lf.RootManifest.Version != root.Version {
		result.ManifestMismatch = &ManifestMismatchError{
			Field: "project version", InLock: lf.RootManifest.Version, InManifest: root.Version,
		}
	}

	// On-disk presence check.
	if packagesDir != "" {
		for _, pkg := range lf.Packages {
			dir := filepath.Join(packagesDir, pkg.Name)
			if _, err := os.Stat(dir); errors.Is(err, os.ErrNotExist) {
				result.MissingPackages = append(result.MissingPackages, pkg.Name)
			}
		}
	}

	// Webcomponent presence check — a locked webcomponent package is
	// considered installed if the webcomponents directory exists and is
	// non-empty (the publisher controls the COMPONENTTYPE subdir names,
	// not the package name, so we can only verify that *some* extraction
	// happened — Phase 5 may persist the per-package component list).
	if webcomponentsDir != "" {
		for _, wc := range lf.Webcomponents {
			// Treat a totally empty webcomponents directory as "all WC
			// packages missing" so a fresh checkout triggers re-install.
			entries, err := os.ReadDir(webcomponentsDir)
			if err != nil || len(entries) == 0 {
				result.MissingWebcomponents = append(result.MissingWebcomponents, wc.Name)
			}
		}
	}

	return result
}

// ─── Plan extraction ──────────────────────────────────────────────────────────

// generoMajor extracts the major version from a version string like "4.01.12".
func generoMajor(v string) string {
	for i, c := range v {
		if c == '.' {
			return v[:i]
		}
	}
	return v
}

// scopeLockString converts a manifest.Scope into the string value stored in
// the lock file. Production is stored as "" (omitempty) so the lock file
// stays compact and backwards-compatible.
func scopeLockString(s manifest.Scope) string {
	if s == manifest.ScopeDev {
		return "dev"
	}
	if s == manifest.ScopeOptional {
		return "optional"
	}
	return ""
}

// ToInstallList converts the lock file back into the flat lists needed by
// the installer, so a locked install never touches the resolver or registry
// for version negotiation — it uses exact URLs and checksums directly.
func (lf *LockFile) ToInstallList() ([]LockedPackage, []LockedJAR, []LockedWebcomponent) {
	return lf.Packages, lf.JARs, lf.Webcomponents
}

// FilterForProduction returns the subset of packages, JARs, and webcomponent
// packages that should be installed when `fglpkg install --production` is
// used: everything except dev-scoped entries. Optional entries are retained
// — they are still attempted (a bad optional dep was already dropped at
// resolve time).
func (lf *LockFile) FilterForProduction() ([]LockedPackage, []LockedJAR, []LockedWebcomponent) {
	pkgs := make([]LockedPackage, 0, len(lf.Packages))
	for _, p := range lf.Packages {
		if p.Scope == "dev" {
			continue
		}
		pkgs = append(pkgs, p)
	}
	jars := make([]LockedJAR, 0, len(lf.JARs))
	for _, j := range lf.JARs {
		if j.Scope == "dev" {
			continue
		}
		jars = append(jars, j)
	}
	wcs := make([]LockedWebcomponent, 0, len(lf.Webcomponents))
	for _, w := range lf.Webcomponents {
		if w.Scope == "dev" {
			continue
		}
		wcs = append(wcs, w)
	}
	return pkgs, jars, wcs
}
