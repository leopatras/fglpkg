// Package config models fglpkg's package-repository configuration: the
// priority-ordered set of registries fglpkg consults for FGL/BDL packages.
//
// A repository is described by a secrets-free descriptor (see Registry). The
// effective set is a cascade, in increasing precedence (later wins per name):
//
//  1. the built-in Genero Intelligence (GI) registry — always present;
//  2. a machine-wide ~/.fglpkg/config.json ({"registries": [...]});
//  3. the project's fglpkg.json "registries" array.
//
// Credentials never live here — they stay in ~/.fglpkg/credentials.json, keyed
// by the repository URL. This mirrors Maven's pom.xml <repositories> + user
// settings.xml split.
package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

// Registry describes one package repository. It carries no secrets.
type Registry struct {
	Name     string   `json:"name"`               // logical id; used in --registry, lock, credentials key
	Type     string   `json:"type"`               // "genero" | "artifactory"
	URL      string   `json:"url"`                // base URL (incl. any context path)
	RepoKey  string   `json:"repoKey,omitempty"`  // Artifactory generic-repo key; required for type=artifactory
	Priority int      `json:"priority,omitempty"` // lower = tried first; must be unique across the set
	Auth     string   `json:"auth,omitempty"`     // bearer|basic|apikey|anonymous (default bearer)
	Packages []string `json:"packages,omitempty"` // optional name-scope glob allow-list
}

// Repository types.
const (
	TypeGenero      = "genero"
	TypeArtifactory = "artifactory"
)

// Auth schemes.
const (
	AuthBearer    = "bearer"
	AuthBasic     = "basic"
	AuthAPIKey    = "apikey"
	AuthAnonymous = "anonymous"
)

// GIName is the logical name of the built-in Genero Intelligence registry.
const GIName = "gi"

// defaultGIURL mirrors registry.defaultRegistryBase — the hardcoded GI base.
const defaultGIURL = "https://service.generointelligence.ai"

// GlobalFilename is the machine-wide config file name under the fglpkg home.
const GlobalFilename = "config.json"

// BuiltinGI returns the always-present GI registry descriptor. When
// fglpkgRegistry is non-empty (the FGLPKG_REGISTRY env override) it retargets
// the GI URL, so existing single-registry users are unaffected.
func BuiltinGI(fglpkgRegistry string) Registry {
	url := defaultGIURL
	if fglpkgRegistry != "" {
		url = strings.TrimRight(fglpkgRegistry, "/")
	}
	return Registry{Name: GIName, Type: TypeGenero, URL: url, Priority: 1, Auth: AuthBearer}
}

// LoadGlobal reads {home}/config.json and returns its registries. A missing
// file is not an error (returns nil). Unknown fields are rejected to catch typos.
func LoadGlobal(home string) ([]Registry, error) {
	g, err := loadGlobalFile(home)
	return g.Registries, err
}

// GlobalFile is the parsed shape of ~/.fglpkg/config.json.
type GlobalFile struct {
	Registries      []Registry `json:"registries"`
	DefaultRegistry string     `json:"defaultRegistry"` // logical name of the default publish target
}

// loadGlobalFile reads and parses the global config file. A missing or
// blank/whitespace-only file yields a zero GlobalFile (not an error). Unknown
// fields are rejected to catch typos.
func loadGlobalFile(home string) (GlobalFile, error) {
	p := filepath.Join(home, GlobalFilename)
	data, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return GlobalFile{}, nil
		}
		return GlobalFile{}, err
	}
	// A blank or whitespace-only file is treated as "no config", the same as a
	// missing file — an empty file is morally identical to absence, and an
	// editor leaving a 0-byte file should not hard-fail every command.
	if len(bytes.TrimSpace(data)) == 0 {
		return GlobalFile{}, nil
	}
	var f GlobalFile
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&f); err != nil {
		return GlobalFile{}, fmt.Errorf("invalid %s: %w", p, err)
	}
	return f, nil
}

// LoadGlobalFile reads and parses ~/.fglpkg/config.json. A missing or blank
// file yields a zero GlobalFile (not an error). It is the read half of the
// read-modify-write cycle used by `fglpkg registry add/remove`.
func LoadGlobalFile(home string) (GlobalFile, error) {
	return loadGlobalFile(home)
}

// WriteGlobalFile writes g to ~/.fglpkg/config.json as formatted JSON, creating
// the home directory if needed. It is the write half of `registry add/remove`.
func WriteGlobalFile(home string, g GlobalFile) error {
	if err := os.MkdirAll(home, 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(g, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(home, GlobalFilename), append(data, '\n'), 0644)
}

// GlobalDefaultRegistry returns the defaultRegistry declared in the global
// config file, or "" if none (or no file). Errors mirror LoadGlobal.
func GlobalDefaultRegistry(home string) (string, error) {
	g, err := loadGlobalFile(home)
	return g.DefaultRegistry, err
}

// Load resolves the effective registry set: built-in GI (honoring
// FGLPKG_REGISTRY) ⊕ the global ~/.fglpkg/config.json ⊕ the project's manifest
// registries. The project registries are passed in by the caller so this
// package never imports the manifest package (avoiding an import cycle).
func Load(home, fglpkgRegistry string, projectRegistries []Registry) ([]Registry, error) {
	global, err := LoadGlobal(home)
	if err != nil {
		return nil, err
	}
	return Resolve(BuiltinGI(fglpkgRegistry), global, projectRegistries)
}

// Resolve merges builtin ⊕ global ⊕ project (increasing precedence, later wins
// per name), normalises, validates, and returns the priority-sorted set. It
// errors on duplicate priorities, an unknown type/auth, or an artifactory entry
// missing repoKey.
func Resolve(builtin Registry, global, project []Registry) ([]Registry, error) {
	byName := map[string]Registry{}
	order := []string{}
	add := func(r Registry) {
		if _, ok := byName[r.Name]; !ok {
			order = append(order, r.Name)
		}
		byName[r.Name] = r
	}
	add(builtin)
	for _, r := range global {
		add(r)
	}
	for _, r := range project {
		add(r)
	}

	merged := make([]Registry, 0, len(order))
	for _, n := range order {
		merged = append(merged, byName[n])
	}

	seenPriority := map[int]string{}
	for i := range merged {
		r := &merged[i]
		r.URL = strings.TrimRight(r.URL, "/")
		if r.Auth == "" {
			r.Auth = AuthBearer
		}
		// A GI entry (built-in or a retarget) defaults to priority 1 so simply
		// retargeting its URL without restating priority stays valid.
		if r.Name == GIName && r.Priority == 0 {
			r.Priority = 1
		}
		if err := validate(*r); err != nil {
			return nil, err
		}
		if other, dup := seenPriority[r.Priority]; dup {
			return nil, fmt.Errorf(
				"registries %q and %q share priority %d; priorities must be unique",
				other, r.Name, r.Priority,
			)
		}
		seenPriority[r.Priority] = r.Name
	}

	sort.SliceStable(merged, func(i, j int) bool { return merged[i].Priority < merged[j].Priority })
	return merged, nil
}

func validate(r Registry) error {
	if r.Name == "" {
		return fmt.Errorf("registry entry is missing 'name'")
	}
	if r.URL == "" {
		return fmt.Errorf("registry %q is missing 'url'", r.Name)
	}
	if r.Priority < 1 {
		return fmt.Errorf("registry %q must set a positive 'priority' (lower = tried first)", r.Name)
	}
	switch r.Type {
	case TypeGenero:
		// no extra requirements
	case TypeArtifactory:
		if r.RepoKey == "" {
			return fmt.Errorf("registry %q (type=artifactory) requires 'repoKey'", r.Name)
		}
	default:
		return fmt.Errorf(
			"registry %q has unknown type %q (expected %q or %q)",
			r.Name, r.Type, TypeGenero, TypeArtifactory,
		)
	}
	switch r.Auth {
	case AuthBearer, AuthBasic, AuthAPIKey, AuthAnonymous:
		// ok
	default:
		return fmt.Errorf(
			"registry %q has unknown auth %q (expected bearer|basic|apikey|anonymous)",
			r.Name, r.Auth,
		)
	}
	return nil
}

// Admits reports whether this registry may serve the given package name, per
// its optional 'packages' glob allow-list. An empty list admits every name.
func (r Registry) Admits(name string) bool {
	if len(r.Packages) == 0 {
		return true
	}
	for _, pat := range r.Packages {
		if ok, _ := path.Match(pat, name); ok {
			return true
		}
	}
	return false
}

// Find returns the registry with the given name, if present.
func Find(regs []Registry, name string) (Registry, bool) {
	for _, r := range regs {
		if r.Name == name {
			return r, true
		}
	}
	return Registry{}, false
}
