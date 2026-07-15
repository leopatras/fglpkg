package provider

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/4js-mikefolcher/fglpkg/internal/config"
	"github.com/4js-mikefolcher/fglpkg/internal/registry"
	"github.com/4js-mikefolcher/fglpkg/internal/resolver"
	"github.com/4js-mikefolcher/fglpkg/internal/semver"
)

// RepositorySet fronts one or more providers and implements the routing +
// collision-guard policy (spec §6). It exposes Versions/Info methods shaped for
// resolver.NewWithFetchers.
//
// Routing per package name:
//   - pinned (a manifest registry: pin or a lockfile Source) → that provider only;
//   - otherwise query every admitting provider and count non-not-found hits:
//     0 → not found, 1 → resolve + record source, ≥2 → a hard collision error.
//
// The per-name decision is memoized so Versions and the later Info call route to
// the same provider without re-querying.
type RepositorySet struct {
	providers   []Provider                 // priority order (lowest priority value first)
	descriptors map[string]config.Registry // provider name → descriptor (for Admits)
	pins        map[string]string          // package name → required registry name (root manifest / explicit)
	restrictTo  string                     // if set, resolution is limited to this provider

	mu           sync.Mutex
	declaredPins map[string]string // package name → registry declared by a depending package's manifest
	routes       map[string]routeDecision
}

type routeDecision struct {
	provider Provider
	versions []resolver.CandidateVersion
}

// NewRepositorySet builds a set from providers (any order — sorted by the
// matching descriptor's Priority), the descriptors, and the per-name pins.
func NewRepositorySet(providers []Provider, descriptors []config.Registry, pins map[string]string) *RepositorySet {
	dmap := make(map[string]config.Registry, len(descriptors))
	for _, d := range descriptors {
		dmap[d.Name] = d
	}
	ordered := append([]Provider(nil), providers...)
	sort.SliceStable(ordered, func(i, j int) bool {
		return dmap[ordered[i].Name()].Priority < dmap[ordered[j].Name()].Priority
	})
	if pins == nil {
		pins = map[string]string{}
	}
	return &RepositorySet{
		providers:    ordered,
		descriptors:  dmap,
		pins:         pins,
		declaredPins: map[string]string{},
		routes:       map[string]routeDecision{},
	}
}

// DeclarePin records that a depending package's manifest pinned name to a
// registry. An explicit root/manifest pin (rs.pins) always wins and silently
// overrides. Two packages pinning the same name to different registries is an
// unresolvable ambiguity and returns an error. Must be called before the name
// is first routed (the resolver declares pins as it discovers each package's
// dependencies, before enqueuing them).
func (rs *RepositorySet) DeclarePin(name, registry string) error {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	if _, isRoot := rs.pins[name]; isRoot {
		return nil // consumer's explicit pin wins
	}
	if existing, ok := rs.declaredPins[name]; ok {
		if existing != registry {
			return fmt.Errorf(
				"dependency %q is pinned to different repositories by different packages (%q vs %q); "+
					"pin it explicitly in fglpkg.json to break the tie:\n"+
					"      \"dependencies\": { \"fgl\": { %q: { \"version\": \"…\", \"registry\": %q } } }",
				name, existing, registry, name, existing)
		}
		return nil
	}
	rs.declaredPins[name] = registry
	return nil
}

// pinFor returns the effective pin for name — an explicit root/manifest pin
// takes precedence over a pin declared by a depending package.
func (rs *RepositorySet) pinFor(name string) string {
	if p := rs.pins[name]; p != "" {
		return p
	}
	return rs.declaredPins[name]
}

// Restrict limits resolution to the single named provider (the --registry flag).
func (rs *RepositorySet) Restrict(name string) { rs.restrictTo = name }

// Providers returns the providers in priority order.
func (rs *RepositorySet) Providers() []Provider { return rs.providers }

// Versions implements resolver.VersionFetcher.
func (rs *RepositorySet) Versions(name string) ([]resolver.CandidateVersion, error) {
	d, err := rs.route(name)
	if err != nil {
		return nil, err
	}
	return d.versions, nil
}

// Info implements resolver.InfoFetcher, routing to the same provider Versions did.
func (rs *RepositorySet) Info(name, version, generoMajor string) (*registry.PackageInfo, error) {
	d, err := rs.route(name)
	if err != nil {
		return nil, err
	}
	return d.provider.FetchInfo(name, version, generoMajor)
}

// Resolve picks the highest version of name satisfying constraint and returns
// its full metadata, routed through the same routing + collision guard as the
// resolver's fetchers. It is the multi-provider analog of registry.Resolve,
// used by `fglpkg install <pkg>` to add a package that may live in a secondary
// repository (an Artifactory repo, not just GI).
func (rs *RepositorySet) Resolve(name, constraint, generoMajor string) (*registry.PackageInfo, error) {
	d, err := rs.route(name)
	if err != nil {
		return nil, err
	}
	candidates := make([]semver.Version, 0, len(d.versions))
	for _, cv := range d.versions {
		candidates = append(candidates, cv.Version)
	}
	c, err := semver.ParseConstraint(constraint)
	if err != nil {
		return nil, fmt.Errorf("invalid version constraint %q: %w", constraint, err)
	}
	best, err := c.Latest(candidates)
	if err != nil {
		return nil, fmt.Errorf("no version of %q satisfies constraint %q", name, constraint)
	}
	return d.provider.FetchInfo(name, best.String(), generoMajor)
}

// configuredNames returns the provider names in priority order, for use in
// diagnostics (e.g. an unknown-registry pin error).
func (rs *RepositorySet) configuredNames() string {
	names := make([]string, 0, len(rs.providers))
	for _, p := range rs.providers {
		names = append(names, p.Name())
	}
	return strings.Join(names, ", ")
}

func (rs *RepositorySet) byName(name string) Provider {
	for _, p := range rs.providers {
		if p.Name() == name {
			return p
		}
	}
	return nil
}

func (rs *RepositorySet) admits(p Provider, name string) bool {
	d, ok := rs.descriptors[p.Name()]
	if !ok {
		return true
	}
	return d.Admits(name)
}

func (rs *RepositorySet) route(name string) (routeDecision, error) {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	if d, ok := rs.routes[name]; ok {
		return d, nil
	}

	// A --registry restriction forces a single provider.
	if rs.restrictTo != "" {
		p := rs.byName(rs.restrictTo)
		if p == nil {
			return routeDecision{}, fmt.Errorf("registry %q is not configured", rs.restrictTo)
		}
		vs, err := p.FetchVersions(name)
		if err != nil {
			return routeDecision{}, err
		}
		d := routeDecision{provider: p, versions: vs}
		rs.routes[name] = d
		return d, nil
	}

	// Pinned name → that provider only (deterministic short-circuit). Covers an
	// explicit root-manifest pin and a pin declared by a depending package.
	if pin := rs.pinFor(name); pin != "" {
		p := rs.byName(pin)
		if p == nil {
			return routeDecision{}, fmt.Errorf(
				"package %q is pinned to registry %q, which is not configured.\n"+
					"  Configured registries: %s\n"+
					"  Fix the name in fglpkg.json, or add %q to fglpkg.json / ~/.fglpkg/config.json.",
				name, pin, rs.configuredNames(), pin)
		}
		vs, err := p.FetchVersions(name)
		if err != nil {
			if errors.Is(err, registry.ErrNotFound) {
				return routeDecision{}, fmt.Errorf(
					"package %q is pinned to registry %q but was not found there", name, pin)
			}
			return routeDecision{}, err
		}
		d := routeDecision{provider: p, versions: vs}
		rs.routes[name] = d
		return d, nil
	}

	// Unpinned → query all admitting providers; count hits.
	var hits []routeDecision
	var searched []string
	for _, p := range rs.providers {
		if !rs.admits(p, name) {
			continue
		}
		searched = append(searched, p.Name())
		vs, err := p.FetchVersions(name)
		if err != nil {
			if errors.Is(err, registry.ErrNotFound) {
				continue
			}
			// Auth or other hard error: abort. Never silently drop a repo from
			// the hit count — that could let a package mis-route (spec §7.2).
			return routeDecision{}, err
		}
		hits = append(hits, routeDecision{provider: p, versions: vs})
	}

	switch len(hits) {
	case 0:
		return routeDecision{}, fmt.Errorf(
			"package %q not found in any configured repository (%s): %w",
			name, strings.Join(searched, ", "), registry.ErrNotFound)
	case 1:
		rs.routes[name] = hits[0]
		return hits[0], nil
	default:
		return routeDecision{}, collisionError(name, hits)
	}
}

// collisionError builds the disambiguation message for a name present in more
// than one repository (spec §6).
func collisionError(name string, hits []routeDecision) error {
	var b strings.Builder
	fmt.Fprintf(&b, "package %q is available from more than one repository:\n", name)
	first := ""
	for _, h := range hits {
		vers := make([]string, 0, len(h.versions))
		for _, v := range h.versions {
			vers = append(vers, v.Version.String())
		}
		if first == "" {
			first = h.provider.Name()
		}
		fmt.Fprintf(&b, "    %-14s %s\n", h.provider.Name(), strings.Join(vers, ", "))
	}
	fmt.Fprintf(&b, "  Refusing to guess. Pin the source in fglpkg.json:\n")
	fmt.Fprintf(&b, "      \"dependencies\": { \"fgl\": { %q: { \"version\": \"^1.0.0\", \"registry\": %q } } }\n", name, first)
	fmt.Fprintf(&b, "  or rename so the name is unique to one repository.")
	// Wrap resolver.ErrCollision so the resolver can recognise a collision and
	// defer it (a later package may declare a pin that breaks the tie) rather
	// than failing at once. Error() still returns the full disambiguation text.
	return &collisionErr{msg: b.String()}
}

// collisionErr carries the human-readable disambiguation message while
// unwrapping to resolver.ErrCollision for errors.Is checks.
type collisionErr struct{ msg string }

func (e *collisionErr) Error() string { return e.msg }
func (e *collisionErr) Unwrap() error { return resolver.ErrCollision }
