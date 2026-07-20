package manifest

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/4js-mikefolcher/fglpkg/internal/config"
	"github.com/4js-mikefolcher/fglpkg/internal/jsonutil"
	"github.com/4js-mikefolcher/fglpkg/internal/semver"
	slugutil "github.com/4js-mikefolcher/fglpkg/internal/slug"
)

// componentTypeName matches the Genero COMPONENTTYPE lexical rule:
// alphanumeric leading character (digit-leading names like "3DChart" are
// valid) followed by letters, digits, underscore, or hyphen.
var componentTypeName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]*$`)

const Filename = "fglpkg.json"

// Manifest represents the fglpkg.json file for a package or project.
type Manifest struct {
	// Schema is the optional JSON Schema URL editors use for autocomplete
	// (`"$schema": "https://.../fglpkg.schema.json"`). It is not validated
	// or used by fglpkg itself; the field exists only so DisallowUnknownFields
	// does not reject manifests that opt into editor tooling.
	Schema string `json:"$schema,omitempty"`
	// Type is accepted-but-ignored for backwards compatibility with older
	// manifests that explicitly declared `"type": "webcomponent"`. Package
	// kind is now derived from the presence of Webcomponents and BDL
	// fields — see specs/webcomponent-packages.md. The field is preserved
	// on round-trip but plays no role in validation, packing, or publish.
	Type        string `json:"type,omitempty"`
	Name        string `json:"name"`
	Version     string `json:"version"`
	Description string `json:"description,omitempty"`
	Author      string `json:"author,omitempty"`
	License     string `json:"license,omitempty"`
	Repository  string `json:"repository,omitempty"`
	// Keywords are free-form tags that aid registry search and discovery.
	// They are advisory metadata only; fglpkg does not interpret them.
	Keywords []string `json:"keywords,omitempty"`
	Main     string   `json:"main,omitempty"` // primary .42m entry point
	// Visibility controls who can read this package on the registry.
	// "public" (default) — anyone can browse and install.
	// "private" — only members of the owning partner can see it.
	// Set when creating a new package; ignored on subsequent publishes.
	Visibility string `json:"visibility,omitempty"`
	// GeneroConstraint declares which Genero BDL runtime versions this package
	// is compatible with, using standard semver constraint syntax.
	// Examples: "^4.0.0", ">=3.20.0 <5.0.0", "^3.20.0 || ^4.0.0"
	// Omit or set to "*" to indicate compatibility with any version.
	GeneroConstraint string `json:"genero,omitempty"`
	// Dependencies are production dependencies — required at runtime by
	// anyone who installs this package.
	Dependencies Dependencies `json:"dependencies"`
	// DevDependencies are only installed for the root project (e.g. test
	// harnesses, linters). A package's own dev dependencies are never
	// pulled in transitively when another project depends on it.
	DevDependencies Dependencies `json:"devDependencies,omitempty"`
	// OptionalDependencies are installed like production deps but a failure
	// to resolve or download one only emits a warning rather than aborting
	// the install. Their transitive deps inherit the optional tolerance.
	OptionalDependencies Dependencies      `json:"optionalDependencies,omitempty"`
	Root                 string            `json:"root,omitempty"`       // base directory for package files (default ".")
	ImportRoot           string            `json:"importRoot,omitempty"` // dir whose contents become the archive root (prefix stripped)
	Files                []string          `json:"files,omitempty"`      // glob patterns for package zip
	Include              []string          `json:"include,omitempty"`    // extra files folded into the archive root by basename
	Bin                  map[string]string `json:"bin,omitempty"`        // command name -> script path
	Docs                 []string          `json:"docs,omitempty"`       // glob patterns for doc files
	Programs             []string          `json:"programs,omitempty"`   // modules with MAIN blocks (e.g. "PoiConvert")
	// Webcomponents lists the COMPONENTTYPE names this package provides.
	// Required (and non-empty) when Type is KindWebcomponent; forbidden
	// otherwise. Each name matches Genero's COMPONENTTYPE lexical rule and
	// corresponds to a directory webcomponents/<NAME>/ in the source tree.
	Webcomponents []string `json:"webcomponents,omitempty"`
	// Hooks declare lifecycle steps to run on well-known events. Each value
	// is an ordered list of declarative operations from a fixed vocabulary
	// (see HookOp). Arbitrary shell commands are intentionally not supported.
	Hooks Hooks `json:"hooks,omitempty"`
	// Registries declares additional package repositories (e.g. a JFrog
	// Artifactory instance) alongside the built-in Genero Intelligence
	// registry. Committed with the project so teammates inherit the repo URLs
	// on clone; credentials stay per-developer in ~/.fglpkg/credentials.json.
	// See specs/artifactory-secondary-repository.md.
	Registries []config.Registry `json:"registries,omitempty"`
	// DefaultRegistry names the repository that `fglpkg publish` targets when no
	// --registry flag is given (and FGLPKG_PUBLISH_REGISTRY is unset). Lets a
	// team that publishes to their own Artifactory avoid typing --registry every
	// time. Empty ("" or "gi") preserves the default of publishing to GI. This
	// is a publish-only default; it does not bias consume-side routing.
	DefaultRegistry string `json:"defaultRegistry,omitempty"`
}

// HasWebcomponents reports whether the manifest declares one or more
// webcomponents. Used by the pack and publish flows to decide whether to
// run the webcomponent walker and which variant tag to use.
func (m *Manifest) HasWebcomponents() bool {
	return len(m.Webcomponents) > 0
}

// HasBDLContent reports whether the manifest declares any BDL-side assets
// — compiled modules, programs, bin scripts, Java JARs, or an explicit
// `files` / `root` declaration that targets BDL source. This is the
// signal that triggers the per-Genero-major variant fan-out at publish
// time. A manifest with only Webcomponents declared returns false; a
// manifest pairing a BDL wrapper with a webcomponent returns true.
func (m *Manifest) HasBDLContent() bool {
	if m.Main != "" || m.Root != "" {
		return true
	}
	if len(m.Files) > 0 || len(m.Programs) > 0 || len(m.Bin) > 0 {
		return true
	}
	for _, scope := range []Scope{ScopeProd, ScopeDev, ScopeOptional} {
		if len(m.bucket(scope).Java) > 0 {
			return true
		}
	}
	return false
}

// Hooks maps a lifecycle event name to the ordered list of operations to
// run for that event. Events:
//   - preinstall    runs before a package's files are extracted
//   - postinstall   runs after a package's files are extracted and bin
//     scripts are made executable
//   - prepublish    runs before the publishable zip is built
//   - postpublish   runs after the registry has accepted the upload
//   - preuninstall  runs before a package's directory is removed
type Hooks map[HookEvent][]HookOperation

// HookEvent is a lifecycle event name. Unknown events are rejected at
// manifest-load time.
type HookEvent string

const (
	HookPreInstall   HookEvent = "preinstall"
	HookPostInstall  HookEvent = "postinstall"
	HookPrePublish   HookEvent = "prepublish"
	HookPostPublish  HookEvent = "postpublish"
	HookPreUninstall HookEvent = "preuninstall"
)

// validHookEvents is the closed set of accepted event names.
var validHookEvents = map[HookEvent]bool{
	HookPreInstall:   true,
	HookPostInstall:  true,
	HookPrePublish:   true,
	HookPostPublish:  true,
	HookPreUninstall: true,
}

// HookOp is the operation discriminator for HookOperation.
type HookOp string

const (
	HookOpCopyFiles HookOp = "copy-files"
	HookOpMkdir     HookOp = "mkdir"
)

// validHookOps is the closed set of accepted operation names.
var validHookOps = map[HookOp]bool{
	HookOpCopyFiles: true,
	HookOpMkdir:     true,
}

// HookOperation is one declarative step within a hook. The set of
// meaningful fields depends on Op; unused fields are simply omitted.
type HookOperation struct {
	Op HookOp `json:"op"`
	// From is the source path (or glob) for copy-files. Relative to the
	// hook's working directory; absolute paths and ".." traversal are
	// rejected.
	From string `json:"from,omitempty"`
	// To is the destination path for copy-files and mkdir. Same path
	// constraints as From.
	To string `json:"to,omitempty"`
	// Path is the target path for mkdir.
	Path string `json:"path,omitempty"`
}

// Dependencies holds both FGL and Java dependency declarations.
type Dependencies struct {
	FGL  map[string]string `json:"fgl,omitempty"`  // name -> version constraint
	Java []JavaDependency  `json:"java,omitempty"` // Maven coordinates
	// FGLPins records the optional per-dependency repository pin from the
	// object form `{"version": ..., "registry": ...}` (name -> registry name).
	// It is not a distinct JSON key — it is derived from / re-emitted into the
	// `fgl` object entries by (Un)MarshalJSON. See
	// specs/artifactory-secondary-repository.md §6.
	FGLPins map[string]string `json:"-"`
}

// fglDepObject is the object form of an fgl dependency value, an alternative
// to the plain version-constraint string.
type fglDepObject struct {
	Version  string `json:"version"`
	Registry string `json:"registry,omitempty"`
}

// UnmarshalJSON rejects unknown keys under `dependencies` with a hint (a common
// mistake is putting package names directly under `dependencies` rather than
// nested under `dependencies.fgl`). Each `fgl` value may be either a plain
// version-constraint string or an object `{"version": ..., "registry": ...}`;
// the optional registry pin is collected into FGLPins.
func (d *Dependencies) UnmarshalJSON(data []byte) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	for k := range raw {
		if k != "fgl" && k != "java" {
			return fmt.Errorf(
				`unknown key %q under "dependencies": expected "fgl" or "java". Did you mean "dependencies.fgl.%s"?`,
				k, k,
			)
		}
	}
	if fglRaw, ok := raw["fgl"]; ok {
		var entries map[string]json.RawMessage
		if err := json.Unmarshal(fglRaw, &entries); err != nil {
			return fmt.Errorf(`invalid "dependencies.fgl": %w`, err)
		}
		d.FGL = make(map[string]string, len(entries))
		for name, v := range entries {
			// Plain string form: "^1.0.0".
			var s string
			if err := json.Unmarshal(v, &s); err == nil {
				d.FGL[name] = s
				continue
			}
			// Object form: {"version": "^1.0.0", "registry": "acme-internal"}.
			var obj fglDepObject
			dec := json.NewDecoder(bytes.NewReader(v))
			dec.DisallowUnknownFields()
			if err := dec.Decode(&obj); err != nil {
				return fmt.Errorf(
					`invalid "dependencies.fgl.%s": expected a version string or {"version": ..., "registry": ...}: %w`,
					name, err,
				)
			}
			if obj.Version == "" {
				return fmt.Errorf(`invalid "dependencies.fgl.%s": the object form requires "version"`, name)
			}
			d.FGL[name] = obj.Version
			if obj.Registry != "" {
				if d.FGLPins == nil {
					d.FGLPins = map[string]string{}
				}
				d.FGLPins[name] = obj.Registry
			}
		}
	}
	if javaRaw, ok := raw["java"]; ok {
		if err := json.Unmarshal(javaRaw, &d.Java); err != nil {
			return fmt.Errorf(`invalid "dependencies.java": %w`, err)
		}
	}
	return nil
}

// MarshalJSON emits fgl entries as a plain version string, or as the object
// form `{"version": ..., "registry": ...}` when the entry carries a registry
// pin (FGLPins). Empty buckets marshal to `{}`, matching the previous shape so
// the manifest's own empty-bucket stripping keeps working.
func (d Dependencies) MarshalJSON() ([]byte, error) {
	out := map[string]interface{}{}
	if len(d.FGL) > 0 {
		fgl := make(map[string]interface{}, len(d.FGL))
		for name, constraint := range d.FGL {
			if reg := d.FGLPins[name]; reg != "" {
				fgl[name] = fglDepObject{Version: constraint, Registry: reg}
			} else {
				fgl[name] = constraint
			}
		}
		out["fgl"] = fgl
	}
	if len(d.Java) > 0 {
		out["java"] = d.Java
	}
	// No-escape: a dep version constraint like ">=1.0.0" must not become a
	// Unicode escape (GIS-280). Re-escaped by the outer boundaries otherwise.
	return jsonutil.Marshal(out)
}

// UnmarshalJSON rejects unknown lifecycle event names with a helpful
// error so typos like "postintsall" surface at load time rather than
// being silently ignored.
func (h *Hooks) UnmarshalJSON(data []byte) error {
	var raw map[string][]HookOperation
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	out := make(Hooks, len(raw))
	for k, ops := range raw {
		ev := HookEvent(k)
		if !validHookEvents[ev] {
			return fmt.Errorf(
				`unknown hook event %q: expected one of preinstall, postinstall, prepublish, postpublish, preuninstall`,
				k,
			)
		}
		out[ev] = ops
	}
	*h = out
	return nil
}

// UnmarshalJSON rejects unknown operation names and unknown fields on a
// HookOperation. Each op has its own required-field check in Validate().
func (op *HookOperation) UnmarshalJSON(data []byte) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	allowed := map[string]bool{"op": true, "from": true, "to": true, "path": true}
	for k := range raw {
		if !allowed[k] {
			return fmt.Errorf(`unknown field %q in hook operation`, k)
		}
	}
	type alias HookOperation
	var a alias
	if err := json.Unmarshal(data, &a); err != nil {
		return err
	}
	if !validHookOps[a.Op] {
		return fmt.Errorf(
			`unknown hook op %q: expected one of copy-files, mkdir`,
			string(a.Op),
		)
	}
	*op = HookOperation(a)
	return nil
}

// JavaDependency describes a Java JAR dependency using Maven coordinates.
type JavaDependency struct {
	GroupID    string `json:"groupId"`
	ArtifactID string `json:"artifactId"`
	Version    string `json:"version"`
	// Checksum is the expected SHA256 hex digest of the JAR file.
	// If provided, the downloaded JAR is verified before use.
	// If omitted, the integrity check is skipped (Maven Central is trusted).
	Checksum string `json:"checksum,omitempty"`
	// Optional: if omitted, derived from groupId/artifactId/version automatically.
	JarFile string `json:"jar,omitempty"`
	// Optional: override the download URL entirely.
	URL string `json:"url,omitempty"`
}

// MavenURL returns the Maven Central download URL for this JAR.
func (j JavaDependency) MavenURL() string {
	if j.URL != "" {
		return j.URL
	}
	// Convert groupId dots to slashes for the URL path
	groupPath := ""
	for _, c := range j.GroupID {
		if c == '.' {
			groupPath += "/"
		} else {
			groupPath += string(c)
		}
	}
	jar := j.JarFile
	if jar == "" {
		jar = fmt.Sprintf("%s-%s.jar", j.ArtifactID, j.Version)
	}
	return fmt.Sprintf(
		"https://repo1.maven.org/maven2/%s/%s/%s/%s",
		groupPath, j.ArtifactID, j.Version, jar,
	)
}

// JarFileName returns the local filename to use when saving this JAR.
func (j JavaDependency) JarFileName() string {
	if j.JarFile != "" {
		return j.JarFile
	}
	return fmt.Sprintf("%s-%s.jar", j.ArtifactID, j.Version)
}

// Key returns a unique string key for this Java dep (groupId:artifactId).
func (j JavaDependency) Key() string {
	return j.GroupID + ":" + j.ArtifactID
}

// BinFiles returns the deduplicated script file paths from the bin map,
// sorted for deterministic ordering.
func (m *Manifest) BinFiles() []string {
	if len(m.Bin) == 0 {
		return nil
	}
	seen := make(map[string]bool)
	var paths []string
	for _, p := range m.Bin {
		if !seen[p] {
			seen[p] = true
			paths = append(paths, p)
		}
	}
	sort.Strings(paths)
	return paths
}

// New creates a new Manifest with sensible defaults.
func New(name, version, description, author string) *Manifest {
	return &Manifest{
		Name:        name,
		Version:     version,
		Description: description,
		Author:      author,
		License:     "UNLICENSED",
		Dependencies: Dependencies{
			FGL:  map[string]string{},
			Java: []JavaDependency{},
		},
	}
}

// Scope identifies which dependency bucket a declaration belongs to.
type Scope string

const (
	ScopeProd     Scope = "prod"
	ScopeDev      Scope = "dev"
	ScopeOptional Scope = "optional"
)

// bucket returns a pointer to the Dependencies struct for the given scope.
func (m *Manifest) bucket(scope Scope) *Dependencies {
	switch scope {
	case ScopeDev:
		return &m.DevDependencies
	case ScopeOptional:
		return &m.OptionalDependencies
	default:
		return &m.Dependencies
	}
}

// Load reads and parses fglpkg.json from dir. Unknown fields at the top
// level (or anywhere in the schema) produce an error rather than being
// silently ignored, so typos like putting packages directly under
// `dependencies` are caught early.
func Load(dir string) (*Manifest, error) {
	path := filepath.Join(dir, Filename)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var m Manifest
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&m); err != nil {
		if strings.Contains(err.Error(), `"scripts"`) {
			return nil, fmt.Errorf(
				`invalid %s: the "scripts" field has been replaced by "hooks" with declarative operations — see docs/user-guide.md`,
				Filename,
			)
		}
		return nil, fmt.Errorf("invalid %s: %w", Filename, err)
	}
	if m.Dependencies.FGL == nil {
		m.Dependencies.FGL = map[string]string{}
	}
	if m.DevDependencies.FGL == nil {
		m.DevDependencies.FGL = map[string]string{}
	}
	if m.OptionalDependencies.FGL == nil {
		m.OptionalDependencies.FGL = map[string]string{}
	}
	return &m, nil
}

// LoadOrNew loads fglpkg.json if it exists, otherwise returns a blank manifest.
func LoadOrNew(dir string) (*Manifest, error) {
	m, err := Load(dir)
	if os.IsNotExist(err) {
		return New(filepath.Base(dir), "0.1.0", "", ""), nil
	}
	return m, err
}

// Save writes the manifest as formatted JSON to dir/fglpkg.json.
func (m *Manifest) Save(dir string) error {
	// jsonutil (no HTML escaping) so a genero constraint like ">=6.0.0" keeps
	// its literal '>' rather than a numeric Unicode escape in the consumer's
	// fglpkg.json (GIS-280).
	data, err := jsonutil.MarshalIndent(m, "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(dir, Filename)
	return os.WriteFile(path, append(data, '\n'), 0644)
}

// MarshalJSON is a thin wrapper around the default encoder that drops the
// devDependencies and optionalDependencies keys when their bucket is empty.
// The standard json package's `omitempty` tag only recognises nil pointers,
// empty maps/slices, and zero primitives — it does not skip zero-value struct
// fields, so we strip them with a token-stream rewrite that preserves the
// original (struct-defined) key order.
func (m *Manifest) MarshalJSON() ([]byte, error) {
	type alias Manifest
	// No-escape marshal so field values keep their literal '<'/'>'/'&'; Go
	// re-escapes at every boundary, so this must match Save + Dependencies
	// (GIS-280).
	buf, err := jsonutil.Marshal((*alias)(m))
	if err != nil {
		return nil, err
	}
	dec := json.NewDecoder(bytes.NewReader(buf))
	if _, err := dec.Token(); err != nil { // read opening '{'
		return nil, err
	}
	var out bytes.Buffer
	out.WriteByte('{')
	first := true
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return nil, err
		}
		key, ok := tok.(string)
		if !ok {
			return nil, fmt.Errorf("manifest marshal: expected string key, got %T", tok)
		}
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return nil, err
		}
		if (key == "devDependencies" || key == "optionalDependencies") && isEmptyDependenciesJSON(raw) {
			continue
		}
		if !first {
			out.WriteByte(',')
		}
		first = false
		keyJSON, err := json.Marshal(key)
		if err != nil {
			return nil, err
		}
		out.Write(keyJSON)
		out.WriteByte(':')
		out.Write(raw)
	}
	out.WriteByte('}')
	return out.Bytes(), nil
}

// PublishCopy returns a copy of the manifest stripped of fields that are
// only meaningful while developing the package. Dev dependencies are not
// pulled in transitively (see resolver), so leaving them in the published
// fglpkg.json just exposes private tooling choices to consumers. The
// receiver is left untouched.
func (m *Manifest) PublishCopy() *Manifest {
	clone := *m
	clone.DevDependencies = Dependencies{}
	// importRoot rebases the archive to strip a build-output prefix, so the
	// shipped manifest must describe the post-strip layout: rewrite root to its
	// path relative to importRoot (so `fglpkg run` and FGLLDPATH resolve against
	// the installed tree) and drop importRoot/include, which have already been
	// applied to the staged archive.
	if clone.ImportRoot != "" {
		base := clone.Root
		if base == "" {
			base = "."
		}
		if rebased, err := filepath.Rel(clone.ImportRoot, base); err == nil {
			rebased = filepath.ToSlash(rebased)
			if rebased != ".." && !strings.HasPrefix(rebased, "../") {
				clone.Root = rebased
			}
		}
		clone.ImportRoot = ""
	}
	clone.Include = nil
	// defaultRegistry is a publisher-side convenience (where THIS project
	// publishes); it is meaningless to a consumer reading the sidecar.
	clone.DefaultRegistry = ""
	return &clone
}

// isEmptyDependenciesJSON returns true when the JSON-encoded Dependencies
// struct has no fgl entries and no java entries.
func isEmptyDependenciesJSON(raw json.RawMessage) bool {
	var d Dependencies
	if err := json.Unmarshal(raw, &d); err != nil {
		return false
	}
	return len(d.FGL) == 0 && len(d.Java) == 0
}

// AddFGLDependency adds or updates a BDL package dependency in the production
// scope. It also removes the name from dev and optional scopes so a package
// lives in exactly one bucket.
func (m *Manifest) AddFGLDependency(name, version string) {
	m.AddFGLDependencyScoped(name, version, ScopeProd)
}

// AddFGLDependencyScoped adds or updates a BDL package dependency in the
// given scope. Any existing declaration in a different scope is removed, so
// a given name appears in exactly one bucket.
func (m *Manifest) AddFGLDependencyScoped(name, version string, scope Scope) {
	canon := slugutil.Canonical(name)
	// Remove any existing declaration of this package — matched by canonical
	// slug, so a separator/case variant (e.g. "fgl_ai_sdk_2" vs "fgl-ai-sdk-2")
	// is replaced rather than left as a duplicate (GIS-280) — from every scope,
	// so the package lands in exactly one bucket keyed by its canonical slug.
	for _, s := range []Scope{ScopeProd, ScopeDev, ScopeOptional} {
		b := m.bucket(s)
		for k := range b.FGL {
			if slugutil.Canonical(k) == canon {
				delete(b.FGL, k)
				delete(b.FGLPins, k)
			}
		}
	}
	b := m.bucket(scope)
	if b.FGL == nil {
		b.FGL = map[string]string{}
	}
	// Choice A: store under the canonical slug — the package's identity per
	// GIS-271 — regardless of how the caller spelled the name.
	b.FGL[canon] = version
	// A plain add carries no registry pin; drop any stale one.
	delete(b.FGLPins, canon)
}

// AddFGLDependencyPinned adds or updates a BDL package dependency pinned to a
// specific repository (the inline `{ "version": …, "registry": … }` form). An
// empty registry falls back to the unpinned add. Like AddFGLDependencyScoped,
// any declaration in a different scope is removed first.
func (m *Manifest) AddFGLDependencyPinned(name, version, registry string, scope Scope) {
	m.AddFGLDependencyScoped(name, version, scope)
	if registry == "" {
		return
	}
	b := m.bucket(scope)
	if b.FGLPins == nil {
		b.FGLPins = map[string]string{}
	}
	// Key the pin by the canonical slug so it matches the FGL entry stored by
	// AddFGLDependencyScoped (choice A).
	b.FGLPins[slugutil.Canonical(name)] = registry
}

// RemoveFGLDependency removes a BDL package dependency from whichever scope
// it lives in. Returns the scope it was removed from, or "" if not present.
func (m *Manifest) RemoveFGLDependency(name string) Scope {
	// Match by canonical slug so a package added under its slug ("fgl-ai-sdk-2")
	// is still removed when the user types a variant ("fgl_ai_sdk_2") — and
	// vice versa for legacy non-canonical keys (GIS-280 / GIS-271).
	canon := slugutil.Canonical(name)
	for _, s := range []Scope{ScopeProd, ScopeDev, ScopeOptional} {
		b := m.bucket(s)
		found := false
		for k := range b.FGL {
			if slugutil.Canonical(k) == canon {
				delete(b.FGL, k)
				delete(b.FGLPins, k)
				found = true
			}
		}
		if found {
			return s
		}
	}
	return ""
}

// FindFGLDependency returns the version constraint and scope for the named
// package, or "", "" if it is not declared in any scope.
func (m *Manifest) FindFGLDependency(name string) (constraint string, scope Scope) {
	// Canonical-slug match, mirroring AddFGLDependencyScoped's storage key.
	canon := slugutil.Canonical(name)
	for _, s := range []Scope{ScopeProd, ScopeDev, ScopeOptional} {
		for k, v := range m.bucket(s).FGL {
			if slugutil.Canonical(k) == canon {
				return v, s
			}
		}
	}
	return "", ""
}

// AddJavaDependency adds or replaces a Java dependency by groupId:artifactId
// key in the production scope.
func (m *Manifest) AddJavaDependency(dep JavaDependency) {
	m.AddJavaDependencyScoped(dep, ScopeProd)
}

// AddJavaDependencyScoped adds or replaces a Java dependency by
// groupId:artifactId key in the given scope. Removes the dep from other
// scopes so it appears in exactly one bucket.
func (m *Manifest) AddJavaDependencyScoped(dep JavaDependency, scope Scope) {
	for _, s := range []Scope{ScopeProd, ScopeDev, ScopeOptional} {
		if s == scope {
			continue
		}
		m.removeJavaKeyFrom(s, dep.Key())
	}
	b := m.bucket(scope)
	for i, existing := range b.Java {
		if existing.Key() == dep.Key() {
			b.Java[i] = dep
			return
		}
	}
	b.Java = append(b.Java, dep)
}

// RemoveJavaDependency removes a Java dependency by groupId:artifactId key
// from whichever scope it lives in. Returns the scope it was removed from,
// or "" if not present.
func (m *Manifest) RemoveJavaDependency(key string) Scope {
	for _, s := range []Scope{ScopeProd, ScopeDev, ScopeOptional} {
		if m.removeJavaKeyFrom(s, key) {
			return s
		}
	}
	return ""
}

func (m *Manifest) removeJavaKeyFrom(scope Scope, key string) bool {
	b := m.bucket(scope)
	removed := false
	filtered := b.Java[:0]
	for _, dep := range b.Java {
		if dep.Key() == key {
			removed = true
			continue
		}
		filtered = append(filtered, dep)
	}
	b.Java = filtered
	return removed
}

// Validate performs basic sanity checks on the manifest.
func (m *Manifest) Validate() error {
	if m.Name == "" {
		return fmt.Errorf("manifest missing required field: name")
	}
	if m.Version == "" {
		return fmt.Errorf("manifest missing required field: version")
	}
	if err := m.validateWebcomponentNames(); err != nil {
		return err
	}
	if err := m.validateNoSelfDependency(); err != nil {
		return err
	}
	if m.GeneroConstraint != "" && m.GeneroConstraint != "*" {
		if _, err := semver.ParseConstraint(m.GeneroConstraint); err != nil {
			return fmt.Errorf("invalid genero constraint %q: %w", m.GeneroConstraint, err)
		}
	}
	for _, scope := range []Scope{ScopeProd, ScopeDev, ScopeOptional} {
		for _, dep := range m.bucket(scope).Java {
			if dep.GroupID == "" || dep.ArtifactID == "" || dep.Version == "" {
				return fmt.Errorf(
					"java dependency missing required fields (groupId, artifactId, version): %+v", dep,
				)
			}
		}
	}
	for cmd, scriptPath := range m.Bin {
		if cmd == "" {
			return fmt.Errorf("bin command name must not be empty")
		}
		if strings.ContainsAny(cmd, "/\\") {
			return fmt.Errorf("bin command name %q must not contain path separators", cmd)
		}
		// safeRelPath rejects empty, absolute (including "/"-rooted paths
		// that filepath.IsAbs misses on Windows), and ".."-escaping paths.
		if err := safeRelPath(fmt.Sprintf("bin script path for command %q", cmd), scriptPath); err != nil {
			return err
		}
	}
	if m.ImportRoot != "" {
		if err := safeRelPath("importRoot", m.ImportRoot); err != nil {
			return err
		}
		if m.Root != "" {
			// root and importRoot must lie on the same branch — one must
			// contain the other. root under importRoot scopes the walk
			// (root "lib/com/x", importRoot "lib"); importRoot under root
			// covers the `fglpkg init` default (root ".", importRoot "lib").
			// A disjoint pair can never rebase any file, so it is rejected.
			ir, rt := filepath.Clean(m.ImportRoot), filepath.Clean(m.Root)
			if !pathWithin(ir, rt) && !pathWithin(rt, ir) {
				return fmt.Errorf("root %q and importRoot %q are on different paths; one must contain the other", m.Root, m.ImportRoot)
			}
		}
	}
	for i, inc := range m.Include {
		if err := safeRelPath(fmt.Sprintf("include[%d]", i), inc); err != nil {
			return err
		}
	}
	for _, pattern := range m.Docs {
		// Strip doublestar segments for validation since filepath.Match
		// doesn't support "**", but the rest of the pattern must be valid.
		cleaned := strings.ReplaceAll(pattern, "**", "star")
		if _, err := filepath.Match(cleaned, "test"); err != nil {
			return fmt.Errorf("invalid docs glob pattern %q: %w", pattern, err)
		}
	}
	for event, ops := range m.Hooks {
		for i, op := range ops {
			if err := validateHookOp(op); err != nil {
				return fmt.Errorf("hooks.%s[%d]: %w", event, i, err)
			}
		}
	}
	return nil
}

// publishRequiredFields lists manifest fields that must be populated
// before a package can be published. These are layered on top of the
// structural checks in Validate(); see ValidateForPublish.
type publishField struct {
	name    string
	getter  func(*Manifest) string
	hintMsg string
}

var publishRequiredFields = []publishField{
	{"description", func(m *Manifest) string { return m.Description }, ""},
	{"license", func(m *Manifest) string { return m.License }, `e.g. "MIT", "Apache-2.0"`},
	{"repository", func(m *Manifest) string { return m.Repository }, `e.g. "https://github.com/owner/repo"`},
	{"author", func(m *Manifest) string { return m.Author }, ""},
}

// ValidateForPublish performs the structural sanity checks (delegates
// to Validate) plus the extra required-field checks for publishing:
// description, license, repository, author. Returns a single error
// whose message lists every missing field, so developers can fix them
// all in one pass instead of discovering them one by one.
func (m *Manifest) ValidateForPublish() error {
	if err := m.Validate(); err != nil {
		return err
	}
	var problems []string
	for _, f := range publishRequiredFields {
		if strings.TrimSpace(f.getter(m)) == "" {
			line := "  - " + f.name + " is required"
			if f.hintMsg != "" {
				line += " (" + f.hintMsg + ")"
			}
			problems = append(problems, line)
		}
	}
	// Format checks: name and version are present (Validate guaranteed
	// non-empty) but may be malformed. The backend can't be relied on to
	// reject these — a secondary Artifactory repo stores whatever it's given
	// — so enforce them here, the single choke point both publish paths hit.
	// The name is validated in its normalized form to match the publish
	// path, which canonicalizes before uploading (GIS-271).
	if slug := slugutil.Canonical(m.Name); !slugutil.IsValid(slug) {
		problems = append(problems, fmt.Sprintf(
			"  - name %q is not a valid package name: normalizes to slug %q "+
				"(need 2-64 chars; lowercase letters, digits, hyphens; must start and end alphanumeric)",
			m.Name, slug))
	}
	if !semver.ValidateVersion(strings.TrimSpace(m.Version)) {
		problems = append(problems, fmt.Sprintf(
			`  - version %q is not valid semver (expected MAJOR.MINOR.PATCH[-prerelease], e.g. "1.2.3")`,
			m.Version))
	}
	if len(problems) == 0 {
		return nil
	}
	return fmt.Errorf("manifest is not ready to publish:\n%s",
		strings.Join(problems, "\n"))
}

// validateWebcomponentNames enforces the COMPONENTTYPE naming rules on
// every entry in m.Webcomponents — each must match the Genero
// COMPONENTTYPE lexical rule and be unique within the list. Empty lists
// are valid (the package simply has no webcomponents).
func (m *Manifest) validateWebcomponentNames() error {
	if len(m.Webcomponents) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(m.Webcomponents))
	for _, name := range m.Webcomponents {
		if !componentTypeName.MatchString(name) {
			return fmt.Errorf(
				`invalid COMPONENTTYPE %q in "webcomponents": must match %s`,
				name, componentTypeName,
			)
		}
		if seen[name] {
			return fmt.Errorf(`duplicate COMPONENTTYPE %q in "webcomponents"`, name)
		}
		seen[name] = true
	}
	return nil
}

// validateNoSelfDependency rejects a manifest that lists its own package
// among its dependencies in any scope. The resolver dedups every canonical
// name to a single resolved version, so a self-dependency can never mean "a
// different copy of me" — it can only pull a stale registry snapshot of this
// package into its own tree, or form a trivial cycle. Every serious package
// manager (npm's ENOSELF, Cargo's "package depends on itself", Maven, …)
// blocks this; so do we. Names are compared canonically (GIS-271) so a
// separator/case variant of the package's own name cannot slip past.
func (m *Manifest) validateNoSelfDependency() error {
	if m.Name == "" {
		return nil // name is validated separately; nothing to compare against
	}
	self := slugutil.Canonical(m.Name)
	for _, scope := range []Scope{ScopeProd, ScopeDev, ScopeOptional} {
		for dep := range m.bucket(scope).FGL {
			if slugutil.Canonical(dep) == self {
				return fmt.Errorf(
					"package %q cannot depend on itself (found %q in %s dependencies)",
					m.Name, dep, scope,
				)
			}
		}
	}
	return nil
}

// validateHookOp enforces per-operation required fields and the shared
// path-safety rules: no absolute paths, no ".." traversal.
func validateHookOp(op HookOperation) error {
	switch op.Op {
	case HookOpCopyFiles:
		if op.From == "" {
			return fmt.Errorf(`copy-files: "from" is required`)
		}
		if op.To == "" {
			return fmt.Errorf(`copy-files: "to" is required`)
		}
		if op.Path != "" {
			return fmt.Errorf(`copy-files: "path" is not valid (use "from"/"to")`)
		}
		if err := safeRelPath("from", op.From); err != nil {
			return err
		}
		if err := safeRelPath("to", op.To); err != nil {
			return err
		}
	case HookOpMkdir:
		if op.Path == "" {
			return fmt.Errorf(`mkdir: "path" is required`)
		}
		if op.From != "" || op.To != "" {
			return fmt.Errorf(`mkdir: only "path" is valid (got "from"/"to")`)
		}
		if err := safeRelPath("path", op.Path); err != nil {
			return err
		}
	default:
		return fmt.Errorf("unknown op %q", op.Op)
	}
	return nil
}

// safeRelPath rejects absolute paths and any path that escapes its base
// via ".." segments. Forward slashes are normalised so manifests work the
// same on Windows and Unix.
func safeRelPath(field, p string) error {
	if p == "" {
		return fmt.Errorf("%s must not be empty", field)
	}
	if filepath.IsAbs(p) || strings.HasPrefix(p, "/") {
		return fmt.Errorf("%s %q must be relative, not absolute", field, p)
	}
	cleaned := filepath.ToSlash(filepath.Clean(p))
	if cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return fmt.Errorf("%s %q must not escape the package root with ..", field, p)
	}
	return nil
}

// pathWithin reports whether target is base or a descendant of base, comparing
// cleaned relative paths (no "../" escape). Both paths are treated as relative
// to the package root.
func pathWithin(base, target string) bool {
	rel, err := filepath.Rel(base, target)
	if err != nil {
		return false
	}
	rel = filepath.ToSlash(rel)
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, "../"))
}
