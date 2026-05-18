package sbom

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/4js-mikefolcher/fglpkg/internal/lockfile"
)

// fixedTime / fixedUUID inject deterministic values into Build so
// tests can assert the document shape without time/randomness noise.
func fixedTime() time.Time {
	return time.Date(2026, 5, 15, 10, 30, 0, 0, time.UTC)
}

func fixedUUID() string {
	return "00000000-0000-4000-8000-aaaaaaaaaaaa"
}

func minimalLock() *lockfile.LockFile {
	return &lockfile.LockFile{
		Version:       1,
		GeneratedAt:   "2026-05-15T00:00:00Z",
		GeneroVersion: "4.0.0",
		RootManifest:  lockfile.RootEntry{Name: "demo", Version: "1.0.0"},
	}
}

func buildOpts() Options {
	return Options{
		ToolVersion: "2.0.1",
		Now:         fixedTime,
		NewUUID:     fixedUUID,
	}
}

func TestBuildShape(t *testing.T) {
	lf := minimalLock()
	lf.Packages = []lockfile.LockedPackage{
		{
			Name: "poiapi", Version: "1.0.0",
			DownloadURL: "https://example.com/poiapi.zip",
			Checksum:    "abc123",
			GeneroMajor: "4",
			RequiredBy:  []string{"<root>"},
		},
	}
	lf.JARs = []lockfile.LockedJAR{
		{
			Key:        "org.apache.poi:poi-ooxml",
			GroupID:    "org.apache.poi", ArtifactID: "poi-ooxml", Version: "5.3.0",
			DownloadURL: "https://repo1.maven.org/maven2/org/apache/poi/poi-ooxml/5.3.0/poi-ooxml-5.3.0.jar",
			Checksum:    "def456",
		},
	}
	doc := Build(lf, buildOpts())

	if doc.BomFormat != "CycloneDX" || doc.SpecVersion != "1.5" {
		t.Errorf("doc header = %q/%q, want CycloneDX/1.5", doc.BomFormat, doc.SpecVersion)
	}
	if doc.SerialNumber != "urn:uuid:"+fixedUUID() {
		t.Errorf("serialNumber = %q, want urn:uuid:<fixed>", doc.SerialNumber)
	}
	if doc.Version != 1 {
		t.Errorf("version = %d, want 1", doc.Version)
	}
	if doc.Metadata.Timestamp != "2026-05-15T10:30:00Z" {
		t.Errorf("timestamp = %q, want fixed 2026-05-15T10:30:00Z", doc.Metadata.Timestamp)
	}
	if len(doc.Metadata.Tools) != 1 || doc.Metadata.Tools[0].Name != "fglpkg" {
		t.Errorf("tools missing or wrong: %+v", doc.Metadata.Tools)
	}
	if doc.Metadata.Tools[0].Version != "2.0.1" {
		t.Errorf("tool version = %q, want 2.0.1", doc.Metadata.Tools[0].Version)
	}
	if doc.Metadata.Component == nil || doc.Metadata.Component.Name != "demo" {
		t.Errorf("root component wrong: %+v", doc.Metadata.Component)
	}
	if len(doc.Components) != 2 {
		t.Fatalf("components = %d, want 2", len(doc.Components))
	}
}

func TestBuildPURLsForBDL(t *testing.T) {
	lf := minimalLock()
	lf.Packages = []lockfile.LockedPackage{{Name: "myutils", Version: "1.2.3"}}
	doc := Build(lf, buildOpts())
	if doc.Components[0].PURL != "pkg:fglpkg/myutils@1.2.3" {
		t.Errorf("purl = %q, want pkg:fglpkg/myutils@1.2.3", doc.Components[0].PURL)
	}
	if doc.Components[0].BomRef != "pkg:fglpkg/myutils@1.2.3" {
		t.Errorf("bom-ref = %q, want purl", doc.Components[0].BomRef)
	}
}

func TestBuildPURLsForJARs(t *testing.T) {
	lf := minimalLock()
	lf.JARs = []lockfile.LockedJAR{
		{Key: "g:a", GroupID: "com.example", ArtifactID: "thing", Version: "2.0.0"},
	}
	doc := Build(lf, buildOpts())
	c := doc.Components[0]
	if c.PURL != "pkg:maven/com.example/thing@2.0.0" {
		t.Errorf("purl = %q, want pkg:maven/com.example/thing@2.0.0", c.PURL)
	}
	if c.Group != "com.example" || c.Name != "thing" {
		t.Errorf("group/name = %q/%q, want com.example/thing", c.Group, c.Name)
	}
}

func TestBuildHashesOmittedWhenEmpty(t *testing.T) {
	lf := minimalLock()
	lf.Packages = []lockfile.LockedPackage{{Name: "a", Version: "1.0.0"}}
	lf.JARs = []lockfile.LockedJAR{
		{Key: "g:b", GroupID: "g", ArtifactID: "b", Version: "1.0.0"},
	}
	doc := Build(lf, buildOpts())
	for i, c := range doc.Components {
		if len(c.Hashes) != 0 {
			t.Errorf("components[%d].Hashes = %v, want empty (no checksum)", i, c.Hashes)
		}
		if len(c.ExternalReferences) != 0 {
			t.Errorf("components[%d].ExternalReferences = %v, want empty (no url)", i, c.ExternalReferences)
		}
	}
}

func TestBuildGeneroMajorProperty(t *testing.T) {
	lf := minimalLock()
	lf.Packages = []lockfile.LockedPackage{
		{Name: "withVar", Version: "1.0.0", GeneroMajor: "4"},
		{Name: "noVar", Version: "1.0.0"}, // no GeneroMajor
	}
	doc := Build(lf, buildOpts())

	// Components are sorted by name: "noVar" then "withVar".
	noVar := doc.Components[0]
	withVar := doc.Components[1]
	if noVar.Name != "noVar" || withVar.Name != "withVar" {
		t.Fatalf("sort order wrong: %s, %s", noVar.Name, withVar.Name)
	}

	if len(noVar.Properties) != 0 {
		t.Errorf("noVar.Properties = %v, want empty", noVar.Properties)
	}
	if len(withVar.Properties) != 1 || withVar.Properties[0].Name != "fglpkg:generoMajor" {
		t.Errorf("withVar.Properties = %v, want one fglpkg:generoMajor entry", withVar.Properties)
	}
	if withVar.Properties[0].Value != "4" {
		t.Errorf("generoMajor value = %q, want 4", withVar.Properties[0].Value)
	}
}

func TestBuildDependencyGraph(t *testing.T) {
	lf := minimalLock()
	lf.Packages = []lockfile.LockedPackage{
		{Name: "myapp", Version: "1.0.0", RequiredBy: []string{"<root>"}},
		{Name: "helper", Version: "0.5.0", RequiredBy: []string{"myapp"}},
	}
	doc := Build(lf, buildOpts())

	// Expect two edges: root → myapp, myapp → helper.
	edges := indexEdges(doc.Dependencies)
	rootEdge, ok := edges["root"]
	if !ok || !contains(rootEdge, "pkg:fglpkg/myapp@1.0.0") {
		t.Errorf("root edge missing myapp; got %v", edges)
	}
	appEdge, ok := edges["pkg:fglpkg/myapp@1.0.0"]
	if !ok || !contains(appEdge, "pkg:fglpkg/helper@0.5.0") {
		t.Errorf("myapp edge missing helper; got %v", edges)
	}
}

func TestBuildProductionFilterDropsDevJARs(t *testing.T) {
	lf := minimalLock()
	lf.JARs = []lockfile.LockedJAR{
		{Key: "g:prod", GroupID: "g", ArtifactID: "prod", Version: "1.0.0"},
		{Key: "g:dev", GroupID: "g", ArtifactID: "dev", Version: "1.0.0", Scope: "dev"},
	}
	opts := buildOpts()
	opts.Production = true
	doc := Build(lf, opts)
	if len(doc.Components) != 1 {
		t.Fatalf("components = %d, want 1 (dev jar should be filtered)", len(doc.Components))
	}
	if doc.Components[0].Name != "prod" {
		t.Errorf("kept the wrong jar: %s", doc.Components[0].Name)
	}
}

func TestBuildEmptyLockfile(t *testing.T) {
	doc := Build(minimalLock(), buildOpts())
	if len(doc.Components) != 0 {
		t.Errorf("components = %v, want empty", doc.Components)
	}
	if len(doc.Dependencies) != 0 {
		t.Errorf("dependencies = %v, want empty", doc.Dependencies)
	}
	// Document still has valid header + root metadata.
	if doc.Metadata.Component == nil || doc.Metadata.Component.Name != "demo" {
		t.Errorf("root metadata wrong: %+v", doc.Metadata.Component)
	}
}

func TestBuildMarshalRoundTrip(t *testing.T) {
	lf := minimalLock()
	lf.Packages = []lockfile.LockedPackage{
		{Name: "a", Version: "1.0.0", RequiredBy: []string{"<root>"}, Checksum: "abc"},
	}
	doc := Build(lf, buildOpts())
	data, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(data), `"bomFormat":"CycloneDX"`) {
		t.Errorf("missing bomFormat in output: %s", data)
	}
	if !strings.Contains(string(data), `"purl":"pkg:fglpkg/a@1.0.0"`) {
		t.Errorf("missing component purl in output: %s", data)
	}
}

func TestUUIDFormat(t *testing.T) {
	u := uuidV4()
	if len(u) != 36 || u[8] != '-' || u[13] != '-' || u[14] != '4' || u[18] != '-' || u[23] != '-' {
		t.Errorf("UUID v4 format wrong: %q", u)
	}
}

func indexEdges(deps []Dependency) map[string][]string {
	m := map[string][]string{}
	for _, d := range deps {
		m[d.Ref] = d.DependsOn
	}
	return m
}

func contains(s []string, x string) bool {
	for _, e := range s {
		if e == x {
			return true
		}
	}
	return false
}
