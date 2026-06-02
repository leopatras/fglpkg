package manifest

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/4js-mikefolcher/fglpkg/internal/semver"
)

const Filename = "fglpkg.json"

// Manifest represents the fglpkg.json file for a package or project.
type Manifest struct {
	// Schema is the optional JSON Schema URL editors use for autocomplete
	// (`"$schema": "https://.../fglpkg.schema.json"`). It is not validated
	// or used by fglpkg itself; the field exists only so DisallowUnknownFields
	// does not reject manifests that opt into editor tooling.
	Schema           string            `json:"$schema,omitempty"`
	Name             string            `json:"name"`
	Version          string            `json:"version"`
	Description      string            `json:"description,omitempty"`
	Author           string            `json:"author,omitempty"`
	License          string            `json:"license,omitempty"`
	Repository       string            `json:"repository,omitempty"`
	Main             string            `json:"main,omitempty"` // primary .42m entry point
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
	Root                 string            `json:"root,omitempty"`     // base directory for package files (default ".")
	Files                []string          `json:"files,omitempty"`    // glob patterns for package zip
	Bin                  map[string]string `json:"bin,omitempty"`      // command name -> script path
	Docs                 []string          `json:"docs,omitempty"`     // glob patterns for doc files
	Programs             []string          `json:"programs,omitempty"` // modules with MAIN blocks (e.g. "PoiConvert")
	// Hooks declare lifecycle steps to run on well-known events. Each value
	// is an ordered list of declarative operations from a fixed vocabulary
	// (see HookOp). Arbitrary shell commands are intentionally not supported.
	Hooks Hooks `json:"hooks,omitempty"`
}

// Hooks maps a lifecycle event name to the ordered list of operations to
// run for that event. Events:
//   - preinstall    runs before a package's files are extracted
//   - postinstall   runs after a package's files are extracted and bin
//                   scripts are made executable
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
	FGL  map[string]string    `json:"fgl,omitempty"`  // name -> version constraint
	Java []JavaDependency      `json:"java,omitempty"` // Maven coordinates
}

// UnmarshalJSON rejects unknown keys under `dependencies` with a hint,
// since a common mistake is to put package names directly under
// `dependencies` instead of nested under `dependencies.fgl`.
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
		if err := json.Unmarshal(fglRaw, &d.FGL); err != nil {
			return fmt.Errorf(`invalid "dependencies.fgl": %w`, err)
		}
	}
	if javaRaw, ok := raw["java"]; ok {
		if err := json.Unmarshal(javaRaw, &d.Java); err != nil {
			return fmt.Errorf(`invalid "dependencies.java": %w`, err)
		}
	}
	return nil
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
	Checksum   string `json:"checksum,omitempty"`
	// Optional: if omitted, derived from groupId/artifactId/version automatically.
	JarFile    string `json:"jar,omitempty"`
	// Optional: override the download URL entirely.
	URL        string `json:"url,omitempty"`
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
	data, err := json.MarshalIndent(m, "", "  ")
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
	buf, err := json.Marshal((*alias)(m))
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
	for _, s := range []Scope{ScopeProd, ScopeDev, ScopeOptional} {
		if s == scope {
			continue
		}
		delete(m.bucket(s).FGL, name)
	}
	b := m.bucket(scope)
	if b.FGL == nil {
		b.FGL = map[string]string{}
	}
	b.FGL[name] = version
}

// RemoveFGLDependency removes a BDL package dependency from whichever scope
// it lives in. Returns the scope it was removed from, or "" if not present.
func (m *Manifest) RemoveFGLDependency(name string) Scope {
	for _, s := range []Scope{ScopeProd, ScopeDev, ScopeOptional} {
		if _, ok := m.bucket(s).FGL[name]; ok {
			delete(m.bucket(s).FGL, name)
			return s
		}
	}
	return ""
}

// FindFGLDependency returns the version constraint and scope for the named
// package, or "", "" if it is not declared in any scope.
func (m *Manifest) FindFGLDependency(name string) (constraint string, scope Scope) {
	for _, s := range []Scope{ScopeProd, ScopeDev, ScopeOptional} {
		if v, ok := m.bucket(s).FGL[name]; ok {
			return v, s
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
		if scriptPath == "" {
			return fmt.Errorf("bin script path for command %q must not be empty", cmd)
		}
		if filepath.IsAbs(scriptPath) {
			return fmt.Errorf("bin script path %q for command %q must be relative", scriptPath, cmd)
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
	var missing []string
	for _, f := range publishRequiredFields {
		if strings.TrimSpace(f.getter(m)) == "" {
			line := "  - " + f.name + " is required"
			if f.hintMsg != "" {
				line += " (" + f.hintMsg + ")"
			}
			missing = append(missing, line)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	return fmt.Errorf("manifest is not ready to publish:\n%s",
		strings.Join(missing, "\n"))
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
