// Package resolver implements transitive dependency resolution for fglpkg.
//
// Resolution algorithm:
//  1. Detect the installed Genero BDL version (or accept an override).
//  2. If a Workspace is provided, local member dependencies are satisfied
//     from disk — they are never sent to the registry or written to the lock.
//  3. Start with the root manifest's direct dependencies as the work queue.
//  4. For each package, fetch its available versions from the registry.
//     Filter to those whose GeneroConstraint is satisfied by the detected
//     Genero version, then apply the semver package constraint.
//  5. If the package has already been seen, intersect the new constraint with
//     accumulated constraints — if no version satisfies all, report a conflict.
//  6. Recurse into each resolved package's own dependencies (BFS).
//  7. Return a flat, ordered install plan with no duplicates.
//
// Java JAR dependencies are collected separately and deduplicated; when the
// same JAR appears at different versions the higher version wins.
package resolver

import (
	"fmt"
	"os"
	"strings"

	"github.com/4js-mikefolcher/fglpkg/internal/genero"
	"github.com/4js-mikefolcher/fglpkg/internal/manifest"
	"github.com/4js-mikefolcher/fglpkg/internal/registry"
	"github.com/4js-mikefolcher/fglpkg/internal/semver"
	"github.com/4js-mikefolcher/fglpkg/internal/workspace"
)

// ResolvedPackage is a single package in the final install plan. Despite
// the legacy name, this entry may refer to either a BDL package or a
// webcomponent package — the Variant field discriminates and the installer
// routes accordingly.
type ResolvedPackage struct {
	Name        string
	Version     semver.Version
	DownloadURL string
	Checksum    string
	// Variant is the artifact variant tag selected by the registry: a
	// BDL package uses "genero<N>", a webcomponent package uses
	// "webcomponent". Empty for workspace-local members, which never
	// install through the artifact path.
	Variant string
	// RequiredBy lists the packages that introduced this dependency.
	RequiredBy []string
	// Scope is the resolved dependency scope: prod, dev, or optional.
	// When a package is reachable via multiple paths the strongest scope
	// wins: prod beats optional beats dev.
	Scope manifest.Scope
}

// IsWebcomponent reports whether this resolved entry is a webcomponent
// package (variant tag "webcomponent"). Callers use this to route the
// install step to .fglpkg/webcomponents/ instead of .fglpkg/packages/.
func (r ResolvedPackage) IsWebcomponent() bool {
	return r.Variant == "webcomponent"
}

// LocalMember describes a workspace member satisfying a local dependency.
type LocalMember struct {
	Name    string
	Version string
	Path    string
}

// Plan is the complete, ordered install plan produced by resolution.
type Plan struct {
	Packages      []ResolvedPackage
	JARs          []manifest.JavaDependency
	JARScopes     map[string]manifest.Scope // jar key → scope (empty key absent == prod)
	LocalMembers  []LocalMember
	GeneroVersion genero.Version
	// OptionalSkipped lists optional packages that could not be resolved or
	// downloaded. Populated only when Options.IncludeOptional is true.
	OptionalSkipped []string
}

// ResolveOptions controls which root-level dependency scopes contribute to
// resolution. Transitive dependencies of packages are always treated as
// production — a library's devDependencies are never pulled in by consumers.
type ResolveOptions struct {
	IncludeDev      bool
	IncludeOptional bool
}

// DefaultResolveOptions includes dev + optional (the developer workflow).
// `fglpkg install --production` uses {IncludeDev: false, IncludeOptional: true}.
func DefaultResolveOptions() ResolveOptions {
	return ResolveOptions{IncludeDev: true, IncludeOptional: true}
}

// scopeRank returns a numeric ranking for scope promotion. Higher wins.
//
//	prod (3) > optional (2) > dev (1)
//
// A package reached via any prod path is installed in production; reaching
// the same package only via dev paths allows `--production` to skip it.
func scopeRank(s manifest.Scope) int {
	switch s {
	case manifest.ScopeProd:
		return 3
	case manifest.ScopeOptional:
		return 2
	case manifest.ScopeDev:
		return 1
	}
	return 3 // unknown/empty treated as prod
}

func strongerScope(a, b manifest.Scope) manifest.Scope {
	if scopeRank(a) >= scopeRank(b) {
		return a
	}
	return b
}

// Conflict describes a version conflict between two or more requirers.
type Conflict struct {
	Package     string
	Constraints []constraintSource
}

func (c Conflict) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "version conflict for %q:\n", c.Package)
	for _, cs := range c.Constraints {
		fmt.Fprintf(&b, "  %s requires %q\n", cs.requiredBy, cs.constraint)
	}
	return b.String()
}

type constraintSource struct {
	constraint string
	requiredBy string
}

// CandidateVersion pairs a parsed semver version with its Genero constraint.
// Exported so test packages can construct fake VersionFetcher responses.
type CandidateVersion struct {
	Version          semver.Version
	GeneroConstraint string
}

// VersionFetcher fetches available versions and their Genero constraints.
type VersionFetcher func(name string) ([]CandidateVersion, error)

// InfoFetcher fetches full package metadata for a resolved name@version.
// generoMajor is the Genero major version to select the correct variant
// (e.g. "4", "6"). Pass "" for legacy packages without variants.
type InfoFetcher func(name, version, generoMajor string) (*registry.PackageInfo, error)

// Resolver resolves the full transitive dependency graph.
type Resolver struct {
	fetchVersions VersionFetcher
	fetchInfo     InfoFetcher
	generoVersion genero.Version
	ws            *workspace.Workspace // nil when not in a workspace
}

// New creates a Resolver that auto-detects the Genero version, detects any
// workspace from the current directory, and uses the live registry.
func New() (*Resolver, error) {
	gv, err := genero.Detect()
	if err != nil {
		return nil, fmt.Errorf("cannot create resolver: %w", err)
	}
	r := &Resolver{
		fetchVersions: registryVersions,
		fetchInfo:     registryInfo,
		generoVersion: gv,
	}
	if wsRoot := workspace.FindRoot("."); wsRoot != "" {
		ws, err := workspace.Load(wsRoot)
		if err != nil {
			return nil, fmt.Errorf("cannot load workspace: %w", err)
		}
		r.ws = ws
	}
	return r, nil
}

// NewWithFetchers creates a Resolver with injectable fetchers and a fixed
// Genero version (for testing). ws may be nil.
func NewWithFetchers(gv genero.Version, fv VersionFetcher, fi InfoFetcher) *Resolver {
	return &Resolver{fetchVersions: fv, fetchInfo: fi, generoVersion: gv}
}

// WithWorkspace attaches a workspace to an existing Resolver.
func (r *Resolver) WithWorkspace(ws *workspace.Workspace) *Resolver {
	r.ws = ws
	return r
}

// Resolve resolves all transitive dependencies of the given root manifest
// using DefaultResolveOptions (dev + optional included).
func (r *Resolver) Resolve(root *manifest.Manifest) (*Plan, error) {
	return r.ResolveWithOptions(root, DefaultResolveOptions())
}

// ResolveWithOptions resolves all transitive dependencies with control over
// which root-level scopes contribute. Transitive deps always use production.
// Optional deps whose fetch/resolve fails are recorded in Plan.OptionalSkipped
// rather than aborting resolution.
func (r *Resolver) ResolveWithOptions(root *manifest.Manifest, opts ResolveOptions) (*Plan, error) {
	if ok, err := r.generoVersion.Satisfies(root.GeneroConstraint); err != nil {
		return nil, fmt.Errorf("invalid genero constraint in root manifest: %w", err)
	} else if !ok {
		return nil, fmt.Errorf(
			"project requires Genero %q but detected version is %s",
			root.GeneroConstraint, r.generoVersion,
		)
	}

	state := newState()

	r.enqueueRootBucket(root.Dependencies, manifest.ScopeProd, state)
	if opts.IncludeDev {
		r.enqueueRootBucket(root.DevDependencies, manifest.ScopeDev, state)
	}
	if opts.IncludeOptional {
		r.enqueueRootBucket(root.OptionalDependencies, manifest.ScopeOptional, state)
	}

	for state.hasWork() {
		item := state.dequeue()

		if r.ws != nil && r.ws.IsLocal(item.name) {
			member := r.ws.Member(item.name)
			state.addLocalMember(LocalMember{
				Name:    member.Manifest.Name,
				Version: member.Manifest.Version,
				Path:    member.Path,
			})
			continue
		}

		if state.isResolved(item.name) {
			state.promoteScope(item.name, item.scope)
			if err := state.addConstraint(item.name, item.constraint, item.requiredBy); err != nil {
				state.addConflict(err.(Conflict))
			}
			if err := state.checkExistingResolution(item.name, item.constraint, item.requiredBy); err != nil {
				state.addConflict(err.(Conflict))
			}
			continue
		}

		state.addConstraint(item.name, item.constraint, item.requiredBy) //nolint:errcheck

		candidates, err := r.fetchVersions(item.name)
		if err != nil {
			if item.scope == manifest.ScopeOptional {
				state.skipOptional(item.name, fmt.Sprintf("fetch versions: %v", err))
				continue
			}
			return nil, fmt.Errorf("failed to fetch versions for %q: %w", item.name, err)
		}

		generoCompatible, err := r.filterByGenero(item.name, candidates)
		if err != nil {
			return nil, err
		}
		if len(generoCompatible) == 0 {
			if item.scope == manifest.ScopeOptional {
				state.skipOptional(item.name, fmt.Sprintf("no version compatible with Genero %s", r.generoVersion))
				continue
			}
			return nil, fmt.Errorf(
				"no version of %q is compatible with Genero %s",
				item.name, r.generoVersion,
			)
		}

		chosen, err := state.bestVersion(item.name, generoCompatible)
		if err != nil {
			if item.scope == manifest.ScopeOptional {
				state.skipOptional(item.name, fmt.Sprintf("no version satisfies constraints: %v", err))
				continue
			}
			state.addConflict(Conflict{
				Package:     item.name,
				Constraints: state.constraints[item.name],
			})
			continue
		}

		info, err := r.fetchInfo(item.name, chosen.String(), r.generoVersion.MajorString())
		if err != nil {
			if item.scope == manifest.ScopeOptional {
				state.skipOptional(item.name, fmt.Sprintf("fetch info: %v", err))
				continue
			}
			return nil, fmt.Errorf("failed to fetch info for %s@%s: %w", item.name, chosen, err)
		}

		state.markResolved(item.name, chosen, info, item.scope)

		for depName, depConstraint := range info.FGLDeps {
			if r.ws != nil && r.ws.IsLocal(depName) {
				member := r.ws.Member(depName)
				state.addLocalMember(LocalMember{
					Name:    member.Manifest.Name,
					Version: member.Manifest.Version,
					Path:    member.Path,
				})
				continue
			}
			if state.isResolved(depName) {
				state.promoteScope(depName, item.scope)
				if err := state.checkExistingResolution(depName, depConstraint, item.name); err != nil {
					state.addConflict(err.(Conflict))
				}
				continue
			}
			state.enqueue(workItem{name: depName, constraint: depConstraint, requiredBy: item.name, scope: item.scope})
		}
		for _, jar := range info.JavaDeps {
			state.addJARScoped(jar, item.scope)
		}
	}

	if len(state.conflicts) > 0 {
		return nil, &ConflictList{Conflicts: state.conflicts}
	}

	plan := state.buildPlan()
	plan.GeneroVersion = r.generoVersion
	return plan, nil
}

// enqueueRootBucket adds a single scope's root dependencies to the work queue.
// Workspace-local members short-circuit to disk; their own (production) deps
// are walked with the same root scope.
func (r *Resolver) enqueueRootBucket(deps manifest.Dependencies, scope manifest.Scope, state *state) {
	for name, constraint := range deps.FGL {
		if r.ws != nil && r.ws.IsLocal(name) {
			member := r.ws.Member(name)
			state.addLocalMember(LocalMember{
				Name:    member.Manifest.Name,
				Version: member.Manifest.Version,
				Path:    member.Path,
			})
			for depName, depConstraint := range member.Manifest.Dependencies.FGL {
				if r.ws.IsLocal(depName) {
					localDep := r.ws.Member(depName)
					state.addLocalMember(LocalMember{
						Name:    localDep.Manifest.Name,
						Version: localDep.Manifest.Version,
						Path:    localDep.Path,
					})
				} else {
					state.enqueue(workItem{name: depName, constraint: depConstraint, requiredBy: name, scope: scope})
				}
			}
			for _, jar := range member.Manifest.Dependencies.Java {
				state.addJARScoped(jar, scope)
			}
			continue
		}
		state.enqueue(workItem{name: name, constraint: constraint, requiredBy: "<root>", scope: scope})
	}
	for _, dep := range deps.Java {
		state.addJARScoped(dep, scope)
	}
}

// filterByGenero removes candidate versions whose GeneroConstraint is not
// satisfied by the detected Genero runtime version.
func (r *Resolver) filterByGenero(pkgName string, candidates []CandidateVersion) ([]semver.Version, error) {
	out := make([]semver.Version, 0, len(candidates))
	for _, c := range candidates {
		ok, err := r.generoVersion.Satisfies(c.GeneroConstraint)
		if err != nil {
			fmt.Fprintf(os.Stderr, "warning: %s@%s has invalid genero constraint %q: %v — skipping\n",
				pkgName, c.Version, c.GeneroConstraint, err)
			continue
		}
		if ok {
			out = append(out, c.Version)
		}
	}
	return out, nil
}

// ─── ConflictList ─────────────────────────────────────────────────────────────

// ConflictList is returned when one or more version conflicts are detected.
type ConflictList struct {
	Conflicts []Conflict
}

func (cl *ConflictList) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%d dependency conflict(s) found:\n\n", len(cl.Conflicts))
	for _, c := range cl.Conflicts {
		b.WriteString(c.Error())
		b.WriteString("\n")
	}
	return b.String()
}

// ─── Resolution state ─────────────────────────────────────────────────────────

type workItem struct {
	name       string
	constraint string
	requiredBy string
	scope      manifest.Scope
}

type resolvedEntry struct {
	version semver.Version
	info    *registry.PackageInfo
	order   int
	scope   manifest.Scope
}

type state struct {
	queue           []workItem
	constraints     map[string][]constraintSource
	resolved        map[string]*resolvedEntry
	jars            map[string]manifest.JavaDependency
	jarScopes       map[string]manifest.Scope
	localMembers    map[string]LocalMember
	conflicts       []Conflict
	orderSeq        int
	optionalSkipped []string
}

func newState() *state {
	return &state{
		constraints:  make(map[string][]constraintSource),
		resolved:     make(map[string]*resolvedEntry),
		jars:         make(map[string]manifest.JavaDependency),
		jarScopes:    make(map[string]manifest.Scope),
		localMembers: make(map[string]LocalMember),
	}
}

// promoteScope updates a resolved package's scope if the new scope is stronger.
// Called when a second path reaches an already-resolved package.
func (s *state) promoteScope(name string, candidate manifest.Scope) {
	entry, ok := s.resolved[name]
	if !ok {
		return
	}
	entry.scope = strongerScope(entry.scope, candidate)
}

// skipOptional records an optional-scope package whose resolution was aborted
// because of a fetch/download/compat failure. Suppresses the hard error.
func (s *state) skipOptional(name, reason string) {
	s.optionalSkipped = append(s.optionalSkipped, fmt.Sprintf("%s (%s)", name, reason))
	fmt.Fprintf(os.Stderr, "warning: skipping optional dependency %s: %s\n", name, reason)
}

func (s *state) enqueue(item workItem)        { s.queue = append(s.queue, item) }
func (s *state) dequeue() workItem            { item := s.queue[0]; s.queue = s.queue[1:]; return item }
func (s *state) hasWork() bool                { return len(s.queue) > 0 }
func (s *state) isResolved(n string) bool     { _, ok := s.resolved[n]; return ok }
func (s *state) addLocalMember(lm LocalMember) { s.localMembers[lm.Name] = lm }

func (s *state) addConstraint(name, constraint, requiredBy string) error {
	s.constraints[name] = append(s.constraints[name], constraintSource{
		constraint: constraint,
		requiredBy: requiredBy,
	})
	return nil
}

func (s *state) bestVersion(name string, candidates []semver.Version) (semver.Version, error) {
	parsed := make([]semver.Constraint, 0, len(s.constraints[name]))
	for _, cs := range s.constraints[name] {
		c, err := semver.ParseConstraint(cs.constraint)
		if err != nil {
			return semver.Version{}, fmt.Errorf("invalid constraint %q from %s: %w",
				cs.constraint, cs.requiredBy, err)
		}
		parsed = append(parsed, c)
	}

	var best *semver.Version
	for _, v := range candidates {
		v := v
		ok := true
		for _, c := range parsed {
			if !c.Matches(v) {
				ok = false
				break
			}
		}
		if ok && (best == nil || v.GreaterThan(*best)) {
			best = &v
		}
	}

	if best == nil {
		return semver.Version{}, fmt.Errorf("no version satisfies all constraints")
	}
	return *best, nil
}

func (s *state) checkExistingResolution(name, newConstraint, requiredBy string) error {
	entry := s.resolved[name]
	c, err := semver.ParseConstraint(newConstraint)
	if err != nil {
		return nil
	}
	s.constraints[name] = append(s.constraints[name], constraintSource{
		constraint: newConstraint,
		requiredBy: requiredBy,
	})
	if !c.Matches(entry.version) {
		return Conflict{Package: name, Constraints: s.constraints[name]}
	}
	return nil
}

func (s *state) markResolved(name string, v semver.Version, info *registry.PackageInfo, scope manifest.Scope) {
	s.resolved[name] = &resolvedEntry{version: v, info: info, order: s.orderSeq, scope: scope}
	s.orderSeq++
}

func (s *state) addConflict(c Conflict) { s.conflicts = append(s.conflicts, c) }

// addJARScoped adds a JAR and promotes its scope. When the same JAR appears
// at different versions the higher version wins. Scope promotion follows the
// same prod > optional > dev ordering as BDL packages.
func (s *state) addJARScoped(dep manifest.JavaDependency, scope manifest.Scope) {
	key := dep.Key()
	if existing, ok := s.jars[key]; ok {
		ev, _ := semver.Parse(existing.Version)
		nv, _ := semver.Parse(dep.Version)
		if nv.GreaterThan(ev) {
			s.jars[key] = dep
		}
	} else {
		s.jars[key] = dep
	}
	s.jarScopes[key] = strongerScope(s.jarScopes[key], scope)
}

func (s *state) buildPlan() *Plan {
	pkgs := make([]ResolvedPackage, 0, len(s.resolved))
	for name, entry := range s.resolved {
		var requiredBy []string
		for _, cs := range s.constraints[name] {
			requiredBy = append(requiredBy, cs.requiredBy)
		}
		pkgs = append(pkgs, ResolvedPackage{
			Name:        name,
			Version:     entry.version,
			DownloadURL: entry.info.DownloadURL,
			Checksum:    entry.info.Checksum,
			Variant:     entry.info.Variant,
			RequiredBy:  requiredBy,
			Scope:       entry.scope,
		})
	}
	for i := 1; i < len(pkgs); i++ {
		for j := i; j > 0 && s.resolved[pkgs[j].Name].order < s.resolved[pkgs[j-1].Name].order; j-- {
			pkgs[j], pkgs[j-1] = pkgs[j-1], pkgs[j]
		}
	}

	jars := make([]manifest.JavaDependency, 0, len(s.jars))
	for _, dep := range s.jars {
		jars = append(jars, dep)
	}

	locals := make([]LocalMember, 0, len(s.localMembers))
	for _, lm := range s.localMembers {
		locals = append(locals, lm)
	}

	scopes := make(map[string]manifest.Scope, len(s.jarScopes))
	for k, v := range s.jarScopes {
		scopes[k] = v
	}

	return &Plan{
		Packages:        pkgs,
		JARs:            jars,
		JARScopes:       scopes,
		LocalMembers:    locals,
		OptionalSkipped: s.optionalSkipped,
	}
}

// ─── Live registry fetchers ───────────────────────────────────────────────────

func registryVersions(name string) ([]CandidateVersion, error) {
	vl, err := registry.FetchVersionList(name)
	if err != nil {
		return nil, err
	}
	out := make([]CandidateVersion, 0, len(vl.VersionEntries))
	for _, ve := range vl.VersionEntries {
		v, err := semver.Parse(ve.Version)
		if err != nil {
			continue
		}
		out = append(out, CandidateVersion{
			Version:          v,
			GeneroConstraint: ve.GeneroConstraint,
		})
	}
	return out, nil
}

func registryInfo(name, version, generoMajor string) (*registry.PackageInfo, error) {
	return registry.FetchInfoForGenero(name, version, generoMajor)
}
