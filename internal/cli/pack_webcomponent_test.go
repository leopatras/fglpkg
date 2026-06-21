package cli

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/4js-mikefolcher/fglpkg/internal/manifest"
)

// TestBuildWebcomponentZipContents verifies the zip builder handles
// type=webcomponent packages: the source layout is webcomponents/<NAME>/...
// but the in-zip paths strip that prefix, so the installer can extract
// directly into .fglpkg/webcomponents/.
func TestBuildWebcomponentZipContents(t *testing.T) {
	dir := t.TempDir()
	write := func(rel, content string) {
		full := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
			t.Fatalf("mkdir %s: %v", rel, err)
		}
		if err := os.WriteFile(full, []byte(content), 0644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	write("fglpkg.json", `{
  "name": "chart-3d",
  "version": "1.0.0",
  "type": "webcomponent",
  "description": "Demo chart",
  "dependencies": { "fgl": {} },
  "docs": ["README.md"],
  "webcomponents": ["3DChart", "Heatmap"]
}`)
	write("webcomponents/3DChart/3DChart.html", "<html><body>3DChart</body></html>")
	write("webcomponents/3DChart/3DChart.css", "body{color:red}")
	write("webcomponents/3DChart/3DChart.js", "// 3DChart js")
	write("webcomponents/3DChart/assets/icon.svg", "<svg/>")
	write("webcomponents/Heatmap/Heatmap.html", "<html><body>Heatmap</body></html>")
	write("README.md", "# chart-3d\n")
	// Stray file that should NOT end up in the zip (no Genero pattern match,
	// no docs match, not under any declared webcomponent dir).
	write("notes.txt", "scratch\n")

	origDir, _ := os.Getwd()
	t.Cleanup(func() { _ = os.Chdir(origDir) })
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	m, err := manifest.Load(".")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	data, sum, err := buildPackageZip(m)
	if err != nil {
		t.Fatalf("buildPackageZip: %v", err)
	}
	if len(sum) != 64 {
		t.Errorf("SHA256 hex digest should be 64 chars, got %d", len(sum))
	}

	r, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("zip.NewReader: %v", err)
	}
	got := map[string]bool{}
	for _, f := range r.File {
		got[f.Name] = true
	}

	wantIncluded := []string{
		"fglpkg.json",
		"README.md",
		"3DChart/3DChart.html",
		"3DChart/3DChart.css",
		"3DChart/3DChart.js",
		"3DChart/assets/icon.svg",
		"Heatmap/Heatmap.html",
	}
	for _, w := range wantIncluded {
		if !got[w] {
			t.Errorf("expected %q in zip; got %v", w, got)
		}
	}

	wantExcluded := []string{
		"notes.txt",
		"webcomponents/3DChart/3DChart.html", // prefix must be stripped
		"webcomponents/3DChart/3DChart.css",
		"webcomponents/Heatmap/Heatmap.html",
	}
	for _, w := range wantExcluded {
		if got[w] {
			t.Errorf("unexpected entry %q in zip", w)
		}
	}
}

// TestBuildWebcomponentZipMissingEntry fails when a declared COMPONENTTYPE
// has no <NAME>.html entry point — that file is required by Genero's
// webcomponent loader.
func TestBuildWebcomponentZipMissingEntry(t *testing.T) {
	dir := t.TempDir()
	mustWrite := func(rel, content string) {
		full := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(full, []byte(content), 0644); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	mustWrite("fglpkg.json", `{
  "name": "broken-wc",
  "version": "1.0.0",
  "type": "webcomponent",
  "dependencies": { "fgl": {} },
  "webcomponents": ["MyWidget"]
}`)
	// Has a stylesheet but no MyWidget.html.
	mustWrite("webcomponents/MyWidget/MyWidget.css", "/*nothing*/")

	origDir, _ := os.Getwd()
	t.Cleanup(func() { _ = os.Chdir(origDir) })
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	m, err := manifest.Load(".")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if _, _, err := buildPackageZip(m); err == nil {
		t.Fatal("expected buildPackageZip to fail when entry point HTML is missing")
	}
}

// TestArtifactVariant covers the per-kind variant tag mapping.
func TestArtifactVariant(t *testing.T) {
	bdl := &manifest.Manifest{Name: "x", Version: "1.0.0"}
	if got := artifactVariant(bdl, "6"); got != "genero6" {
		t.Errorf("artifactVariant(bdl, 6) = %q; want genero6", got)
	}
	wc := &manifest.Manifest{
		Name: "y", Version: "1.0.0",
		Type:          manifest.KindWebcomponent,
		Webcomponents: []string{"W"},
	}
	if got := artifactVariant(wc, "6"); got != "webcomponent" {
		t.Errorf("artifactVariant(wc) = %q; want webcomponent", got)
	}
}

// TestArtifactFilename covers the zip filename format for each variant.
func TestArtifactFilename(t *testing.T) {
	if got := artifactFilename("pkg", "1.2.3", "genero6"); got != "pkg-1.2.3-genero6.zip" {
		t.Errorf("BDL filename = %q", got)
	}
	if got := artifactFilename("pkg", "1.2.3", "webcomponent"); got != "pkg-1.2.3-webcomponent.zip" {
		t.Errorf("WC filename = %q", got)
	}
}
