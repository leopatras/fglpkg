// Package sbom builds Software Bill of Materials documents from a
// project's fglpkg.lock. v1 emits CycloneDX 1.5 JSON only.
//
// The package performs no network I/O. All data flows from the
// lockfile through the Build() function into a Document value that
// callers marshal to JSON.
package sbom

import (
	"crypto/rand"
	"fmt"
	"sort"
	"time"

	"github.com/4js-mikefolcher/fglpkg/internal/lockfile"
)

// Spec constants for CycloneDX 1.5.
const (
	bomFormat   = "CycloneDX"
	specVersion = "1.5"

	defaultToolName   = "fglpkg"
	defaultToolVendor = "Four Js"
)

// Document is the CycloneDX 1.5 root object. JSON tags match the
// CycloneDX schema; fields that may be empty use omitempty so the
// output stays clean when data is unavailable.
type Document struct {
	BomFormat    string       `json:"bomFormat"`
	SpecVersion  string       `json:"specVersion"`
	SerialNumber string       `json:"serialNumber"`
	Version      int          `json:"version"`
	Metadata     Metadata     `json:"metadata"`
	Components   []Component  `json:"components,omitempty"`
	Dependencies []Dependency `json:"dependencies,omitempty"`
}

// Metadata describes who/when produced the BOM and the root component.
type Metadata struct {
	Timestamp string     `json:"timestamp"`
	Tools     []Tool     `json:"tools,omitempty"`
	Component *Component `json:"component,omitempty"`
}

// Tool identifies the tool that produced the BOM.
type Tool struct {
	Vendor  string `json:"vendor,omitempty"`
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
}

// Component is one software component (a library, application, etc.).
type Component struct {
	BomRef             string              `json:"bom-ref,omitempty"`
	Type               string              `json:"type"`
	Name               string              `json:"name"`
	Group              string              `json:"group,omitempty"`
	Version            string              `json:"version,omitempty"`
	PURL               string              `json:"purl,omitempty"`
	Hashes             []Hash              `json:"hashes,omitempty"`
	ExternalReferences []ExternalReference `json:"externalReferences,omitempty"`
	Properties         []Property          `json:"properties,omitempty"`
}

// Hash is a cryptographic digest of the component's artifact.
type Hash struct {
	Alg     string `json:"alg"`
	Content string `json:"content"`
}

// ExternalReference points to something outside the BOM (download URL,
// VCS, advisories, etc.). v1 emits only "distribution" entries.
type ExternalReference struct {
	Type string `json:"type"`
	URL  string `json:"url"`
}

// Property is a tool-specific key/value annotation. CycloneDX prescribes
// nothing about the semantics; we use it to carry Genero variant info.
type Property struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// Dependency is one edge of the dependency graph. Ref points at a
// component (by bom-ref), DependsOn lists components it requires.
type Dependency struct {
	Ref       string   `json:"ref"`
	DependsOn []string `json:"dependsOn,omitempty"`
}

// Options configure SBOM generation. Zero values pick sensible defaults
// suitable for production use; tests inject Now and NewUUID for
// deterministic output.
type Options struct {
	Production  bool
	ToolName    string
	ToolVendor  string
	ToolVersion string
	Now         func() time.Time
	NewUUID     func() string
}

// rootRef is the bom-ref used for the project itself. CycloneDX does
// not prescribe a value; we use a stable string so dependency edges
// rooted at the project are recognizable across runs.
const rootRef = "root"

// Build constructs a CycloneDX Document from a lockfile and the given
// options. Build performs no I/O; callers marshal the returned value
// to JSON themselves.
func Build(lf *lockfile.LockFile, opts Options) *Document {
	now := opts.Now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	newUUID := opts.NewUUID
	if newUUID == nil {
		newUUID = uuidV4
	}
	toolName := opts.ToolName
	if toolName == "" {
		toolName = defaultToolName
	}
	toolVendor := opts.ToolVendor
	if toolVendor == "" {
		toolVendor = defaultToolVendor
	}

	doc := &Document{
		BomFormat:    bomFormat,
		SpecVersion:  specVersion,
		SerialNumber: "urn:uuid:" + newUUID(),
		Version:      1,
		Metadata: Metadata{
			Timestamp: now().UTC().Format(time.RFC3339),
			Tools: []Tool{{
				Vendor:  toolVendor,
				Name:    toolName,
				Version: opts.ToolVersion,
			}},
			Component: &Component{
				BomRef:  rootRef,
				Type:    "application",
				Name:    lf.RootManifest.Name,
				Version: lf.RootManifest.Version,
			},
		},
	}

	// Build BDL package components (sorted for stable output).
	pkgs := append([]lockfile.LockedPackage(nil), lf.Packages...)
	sort.Slice(pkgs, func(i, j int) bool { return pkgs[i].Name < pkgs[j].Name })
	for _, p := range pkgs {
		doc.Components = append(doc.Components, bdlComponent(p))
	}

	// Build JAR components (sorted by key for stable output).
	jars := filterJARs(lf.JARs, opts.Production)
	sort.Slice(jars, func(i, j int) bool { return jars[i].Key < jars[j].Key })
	for _, j := range jars {
		doc.Components = append(doc.Components, jarComponent(j))
	}

	doc.Dependencies = buildDependencyEdges(pkgs, jars)
	return doc
}

// filterJARs drops dev-scoped JARs when production mode is on.
// optional-scoped JARs are always included.
func filterJARs(in []lockfile.LockedJAR, production bool) []lockfile.LockedJAR {
	if !production {
		return append([]lockfile.LockedJAR(nil), in...)
	}
	out := make([]lockfile.LockedJAR, 0, len(in))
	for _, j := range in {
		if j.Scope == "dev" {
			continue
		}
		out = append(out, j)
	}
	return out
}

func bdlComponent(p lockfile.LockedPackage) Component {
	purl := bdlPURL(p.Name, p.Version)
	c := Component{
		BomRef:  purl,
		Type:    "library",
		Name:    p.Name,
		Version: p.Version,
		PURL:    purl,
	}
	if p.Checksum != "" {
		c.Hashes = []Hash{{Alg: "SHA-256", Content: p.Checksum}}
	}
	if p.DownloadURL != "" {
		c.ExternalReferences = []ExternalReference{{
			Type: "distribution",
			URL:  p.DownloadURL,
		}}
	}
	if p.GeneroMajor != "" {
		c.Properties = []Property{{
			Name:  "fglpkg:generoMajor",
			Value: p.GeneroMajor,
		}}
	}
	return c
}

func jarComponent(j lockfile.LockedJAR) Component {
	purl := mavenPURL(j.GroupID, j.ArtifactID, j.Version)
	c := Component{
		BomRef:  purl,
		Type:    "library",
		Name:    j.ArtifactID,
		Group:   j.GroupID,
		Version: j.Version,
		PURL:    purl,
	}
	if j.Checksum != "" {
		c.Hashes = []Hash{{Alg: "SHA-256", Content: j.Checksum}}
	}
	if j.DownloadURL != "" {
		c.ExternalReferences = []ExternalReference{{
			Type: "distribution",
			URL:  j.DownloadURL,
		}}
	}
	return c
}

// buildDependencyEdges turns the lockfile's requiredBy fields into
// CycloneDX dependency entries. BDL packages carry requiredBy
// precisely; JARs do not yet (their parentage isn't in the lockfile),
// so we emit a single root → all-JARs edge until that gap is closed.
func buildDependencyEdges(pkgs []lockfile.LockedPackage, jars []lockfile.LockedJAR) []Dependency {
	// edges[ref] = ordered slice of children. Using maps lets multiple
	// requiredBy entries collapse cleanly into one edge per parent.
	edges := map[string][]string{}
	order := []string{}
	add := func(parent, child string) {
		if _, seen := edges[parent]; !seen {
			order = append(order, parent)
		}
		edges[parent] = append(edges[parent], child)
	}

	for _, p := range pkgs {
		child := bdlPURL(p.Name, p.Version)
		if len(p.RequiredBy) == 0 {
			// Stray entry with no parent — assume it's a direct dep.
			add(rootRef, child)
			continue
		}
		for _, parent := range p.RequiredBy {
			if parent == "<root>" {
				add(rootRef, child)
				continue
			}
			// Parent is another BDL package name. We need its version
			// to form a bom-ref; look it up.
			ver := findPkgVersion(pkgs, parent)
			if ver == "" {
				// Unknown parent — collapse onto root rather than emit
				// a dangling reference.
				add(rootRef, child)
				continue
			}
			add(bdlPURL(parent, ver), child)
		}
	}

	// JARs all hang off root for now (see spec open question).
	for _, j := range jars {
		add(rootRef, mavenPURL(j.GroupID, j.ArtifactID, j.Version))
	}

	out := make([]Dependency, 0, len(order))
	for _, parent := range order {
		out = append(out, Dependency{Ref: parent, DependsOn: edges[parent]})
	}
	return out
}

func findPkgVersion(pkgs []lockfile.LockedPackage, name string) string {
	for _, p := range pkgs {
		if p.Name == name {
			return p.Version
		}
	}
	return ""
}

func bdlPURL(name, version string) string {
	return "pkg:fglpkg/" + name + "@" + version
}

func mavenPURL(group, artifact, version string) string {
	return "pkg:maven/" + group + "/" + artifact + "@" + version
}

// uuidV4 returns a random v4 UUID using crypto/rand. Format:
// xxxxxxxx-xxxx-4xxx-yxxx-xxxxxxxxxxxx with version bits set to 4
// and variant bits set per RFC 4122.
func uuidV4() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand never fails on a real OS; if it does, return a
		// degenerate id rather than panic. The serial number is not
		// security-critical for the SBOM use case.
		return "00000000-0000-4000-8000-000000000000"
	}
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant 10
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
