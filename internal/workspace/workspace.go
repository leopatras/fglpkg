// Package workspace implements fglpkg workspace support — a way to manage
// multiple related BDL packages in a single repository (monorepo).
//
// A workspace is defined by a fglpkg.workspace.json file in the repository
// root. Member packages are ordinary fglpkg.json packages whose paths are
// listed in the workspace file.
//
// Key behaviours:
//
//   - Local members can depend on each other. The resolver satisfies those
//     dependencies from disk rather than the registry, and they are never
//     written to the lock file as remote downloads.
//
//   - A single shared fglpkg.lock lives at the workspace root, covering all
//     external (registry) dependencies across all members. This ensures every
//     member uses the same resolved versions.
//
//   - FGLLDPATH is extended with every member's source directory so all
//     members can import each other during compilation without installing.
//
// Directory layout example:
//
//	myrepo/
//	├── fglpkg.workspace.json
//	├── fglpkg.lock               ← shared lock file
//	├── core/
//	│   └── fglpkg.json           { "name": "core", ... }
//	├── utils/
//	│   └── fglpkg.json           { "name": "utils", "dependencies": { "fgl": { "core": "*" } } }
//	└── app/
//	    └── fglpkg.json           { "name": "app",   "dependencies": { "fgl": { "utils": "^1.0" } } }
package workspace

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/4js-mikefolcher/fglpkg/internal/manifest"
)

const (
	// WorkspaceFilename is the workspace definition file.
	WorkspaceFilename = "fglpkg.workspace.json"
)

// WorkspaceFile is the parsed content of fglpkg.workspace.json.
type WorkspaceFile struct {
	// Members lists relative paths to member package directories.
	// Each must contain a fglpkg.json.
	Members []string `json:"members"`

	// Exclude lists relative paths to exclude from auto-discovered members.
	// Used when Members contains a glob-like pattern in the future.
	Exclude []string `json:"exclude,omitempty"`
}

// Member is a resolved workspace member: its manifest plus its absolute path.
type Member struct {
	// Path is the absolute path to the member's directory.
	Path string

	// RelPath is the path relative to the workspace root (for display).
	RelPath string

	// Manifest is the parsed fglpkg.json for this member.
	Manifest *manifest.Manifest
}

// Workspace is the fully-loaded workspace: root location plus all members.
type Workspace struct {
	// RootDir is the absolute path to the directory containing
	// fglpkg.workspace.json.
	RootDir string

	// File is the parsed workspace file.
	File WorkspaceFile

	// Members is the ordered list of resolved members.
	// Order is determined by topological sort of local dependencies so
	// that members are always listed after their local deps.
	Members []*Member

	// memberByName maps package name to member for fast lookup.
	memberByName map[string]*Member
}

// ─── Loading ──────────────────────────────────────────────────────────────────

// Load finds and loads the workspace rooted at rootDir.
// rootDir must be the directory containing fglpkg.workspace.json.
func Load(rootDir string) (*Workspace, error) {
	abs, err := filepath.Abs(rootDir)
	if err != nil {
		return nil, err
	}

	data, err := os.ReadFile(filepath.Join(abs, WorkspaceFilename))
	if err != nil {
		return nil, fmt.Errorf("cannot read %s: %w", WorkspaceFilename, err)
	}

	var wf WorkspaceFile
	if err := json.Unmarshal(data, &wf); err != nil {
		return nil, fmt.Errorf("invalid %s: %w", WorkspaceFilename, err)
	}

	if len(wf.Members) == 0 {
		return nil, fmt.Errorf("%s: members list is empty", WorkspaceFilename)
	}

	ws := &Workspace{
		RootDir:      abs,
		File:         wf,
		memberByName: make(map[string]*Member),
	}

	// Load each member manifest.
	raw := make([]*Member, 0, len(wf.Members))
	for _, rel := range wf.Members {
		memberDir := filepath.Join(abs, filepath.FromSlash(rel))
		m, err := manifest.Load(memberDir)
		if err != nil {
			return nil, fmt.Errorf("workspace member %q: %w", rel, err)
		}
		member := &Member{
			Path:     memberDir,
			RelPath:  rel,
			Manifest: m,
		}
		if _, dup := ws.memberByName[m.Name]; dup {
			return nil, fmt.Errorf("workspace has two members with name %q", m.Name)
		}
		ws.memberByName[m.Name] = member
		raw = append(raw, member)
	}

	// Topologically sort members so local deps come first.
	sorted, err := topoSort(raw, ws.memberByName)
	if err != nil {
		return nil, fmt.Errorf("workspace dependency cycle: %w", err)
	}
	ws.Members = sorted

	return ws, nil
}

// FindRoot walks up from dir looking for fglpkg.workspace.json.
// Returns "" if none is found (the project is not in a workspace).
func FindRoot(dir string) string {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return ""
	}
	for {
		if _, err := os.Stat(filepath.Join(abs, WorkspaceFilename)); err == nil {
			return abs
		}
		parent := filepath.Dir(abs)
		if parent == abs {
			return "" // reached filesystem root
		}
		abs = parent
	}
}

// Exists reports whether dir contains a workspace file.
func Exists(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, WorkspaceFilename))
	return err == nil
}

// ─── Querying ─────────────────────────────────────────────────────────────────

// Member returns the member with the given package name, or nil.
func (ws *Workspace) Member(name string) *Member {
	return ws.memberByName[name]
}

// IsLocal reports whether the given package name is a workspace member.
func (ws *Workspace) IsLocal(name string) bool {
	_, ok := ws.memberByName[name]
	return ok
}

// LocalDeps returns the workspace members that m directly depends on locally.
func (ws *Workspace) LocalDeps(m *manifest.Manifest) []*Member {
	var deps []*Member
	for name := range m.Dependencies.FGL {
		if member, ok := ws.memberByName[name]; ok {
			deps = append(deps, member)
		}
	}
	sort.Slice(deps, func(i, j int) bool { return deps[i].Manifest.Name < deps[j].Manifest.Name })
	return deps
}

// ExternalDeps returns a merged manifest of all non-local FGL dependencies
// and all Java dependencies across all workspace members, deduplicated.
// This is used to drive the single shared resolution pass.
func (ws *Workspace) ExternalDeps() *manifest.Manifest {
	merged := manifest.New("__workspace__", "0.0.0", "", "")

	seenJava := make(map[string]bool)
	for _, m := range ws.Members {
		for name, constraint := range m.Manifest.Dependencies.FGL {
			if ws.IsLocal(name) {
				continue // skip local deps — resolved from disk
			}
			// If the same external dep appears in multiple members, keep the
			// most restrictive (first-seen) constraint. A conflict will surface
			// during resolution, which is the correct behaviour.
			if _, sc := merged.FindFGLDependency(name); sc == "" {
				merged.AddFGLDependency(name, constraint)
			}
		}
		for _, dep := range m.Manifest.Dependencies.Java {
			if !seenJava[dep.Key()] {
				merged.AddJavaDependency(dep)
				seenJava[dep.Key()] = true
			}
		}
	}
	return merged
}

// FGLLDPATHEntries returns the list of member source directories to prepend
// to FGLLDPATH so local packages can import each other during development
// without being installed to ~/.fglpkg/packages.
func (ws *Workspace) FGLLDPATHEntries() []string {
	entries := make([]string, 0, len(ws.Members))
	for _, m := range ws.Members {
		entries = append(entries, m.Path)
	}
	return entries
}

// ─── Initialisation ───────────────────────────────────────────────────────────

// Init creates a fglpkg.workspace.json in rootDir listing the given member
// relative paths. Returns an error if the file already exists.
func Init(rootDir string, members []string) error {
	path := filepath.Join(rootDir, WorkspaceFilename)
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("%s already exists", WorkspaceFilename)
	}

	wf := WorkspaceFile{Members: members}
	data, err := json.MarshalIndent(wf, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0644)
}

// AddMember appends a new member path to an existing workspace file.
func AddMember(rootDir, relPath string) error {
	path := filepath.Join(rootDir, WorkspaceFilename)
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("cannot read %s: %w", WorkspaceFilename, err)
	}

	var wf WorkspaceFile
	if err := json.Unmarshal(data, &wf); err != nil {
		return fmt.Errorf("invalid %s: %w", WorkspaceFilename, err)
	}

	for _, m := range wf.Members {
		if m == relPath {
			return fmt.Errorf("%q is already a workspace member", relPath)
		}
	}
	wf.Members = append(wf.Members, relPath)

	out, err := json.MarshalIndent(wf, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(out, '\n'), 0644)
}

// ─── Topological sort ─────────────────────────────────────────────────────────

// topoSort returns members in dependency order (deps before dependents).
// Returns an error if a cycle is detected.
func topoSort(members []*Member, byName map[string]*Member) ([]*Member, error) {
	type state int
	const (
		unvisited state = iota
		visiting        // currently in DFS stack — cycle if seen again
		visited
	)

	states := make(map[string]state, len(members))
	var sorted []*Member

	var visit func(m *Member) error
	visit = func(m *Member) error {
		switch states[m.Manifest.Name] {
		case visited:
			return nil
		case visiting:
			return fmt.Errorf("cycle detected at %q", m.Manifest.Name)
		}

		states[m.Manifest.Name] = visiting
		for depName := range m.Manifest.Dependencies.FGL {
			dep, ok := byName[depName]
			if !ok {
				continue // external dep — skip
			}
			if err := visit(dep); err != nil {
				return fmt.Errorf("%s → %w", m.Manifest.Name, err)
			}
		}
		states[m.Manifest.Name] = visited
		sorted = append(sorted, m)
		return nil
	}

	// Visit in stable input order so output is deterministic.
	for _, m := range members {
		if err := visit(m); err != nil {
			return nil, err
		}
	}
	return sorted, nil
}

// ─── Display ──────────────────────────────────────────────────────────────────

// Summary returns a human-readable summary of the workspace for `fglpkg info`.
func (ws *Workspace) Summary() string {
	var b strings.Builder
	fmt.Fprintf(&b, "Workspace root: %s\n", ws.RootDir)
	fmt.Fprintf(&b, "Members (%d):\n", len(ws.Members))
	for _, m := range ws.Members {
		localDeps := ws.LocalDeps(m.Manifest)
		names := make([]string, len(localDeps))
		for i, d := range localDeps {
			names[i] = d.Manifest.Name
		}
		line := fmt.Sprintf("  %-30s v%-10s", m.Manifest.Name, m.Manifest.Version)
		if len(names) > 0 {
			line += fmt.Sprintf("  [local deps: %s]", strings.Join(names, ", "))
		}
		b.WriteString(line + "\n")
	}
	return b.String()
}
