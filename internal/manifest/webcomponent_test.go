package manifest_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/4js-mikefolcher/fglpkg/internal/manifest"
)

func TestEffectiveKindDefaultsToBDL(t *testing.T) {
	m := &manifest.Manifest{Name: "p", Version: "1.0.0"}
	if got := m.EffectiveKind(); got != manifest.KindBDL {
		t.Fatalf("EffectiveKind() with empty Type = %q, want %q", got, manifest.KindBDL)
	}
}

func TestEffectiveKindRespectsExplicit(t *testing.T) {
	m := &manifest.Manifest{Name: "p", Version: "1.0.0", Type: manifest.KindWebcomponent,
		Webcomponents: []string{"MyWidget"}}
	if got := m.EffectiveKind(); got != manifest.KindWebcomponent {
		t.Fatalf("EffectiveKind() = %q, want %q", got, manifest.KindWebcomponent)
	}
}

func TestValidateWebcomponentRequiresList(t *testing.T) {
	m := &manifest.Manifest{Name: "p", Version: "1.0.0", Type: manifest.KindWebcomponent}
	err := m.Validate()
	if err == nil || !strings.Contains(err.Error(), "must list at least one COMPONENTTYPE") {
		t.Fatalf("expected COMPONENTTYPE-required error, got %v", err)
	}
}

func TestValidateWebcomponentNameFormat(t *testing.T) {
	cases := []string{"", "has space", "has/slash", "-leading", "name.dot", "name!bang"}
	for _, bad := range cases {
		t.Run(bad, func(t *testing.T) {
			m := &manifest.Manifest{
				Name: "p", Version: "1.0.0",
				Type:          manifest.KindWebcomponent,
				Webcomponents: []string{bad},
			}
			if err := m.Validate(); err == nil {
				t.Fatalf("expected validation error for COMPONENTTYPE %q", bad)
			}
		})
	}
}

func TestValidateWebcomponentDuplicateName(t *testing.T) {
	m := &manifest.Manifest{
		Name: "p", Version: "1.0.0",
		Type:          manifest.KindWebcomponent,
		Webcomponents: []string{"Chart", "Chart"},
	}
	err := m.Validate()
	if err == nil || !strings.Contains(err.Error(), "duplicate COMPONENTTYPE") {
		t.Fatalf("expected duplicate-COMPONENTTYPE error, got %v", err)
	}
}

func TestValidateWebcomponentForbidsBDLFields(t *testing.T) {
	base := func() *manifest.Manifest {
		return &manifest.Manifest{
			Name: "p", Version: "1.0.0",
			Type:          manifest.KindWebcomponent,
			Webcomponents: []string{"MyWidget"},
		}
	}
	cases := []struct {
		name string
		mut  func(*manifest.Manifest)
		hint string
	}{
		{"main", func(m *manifest.Manifest) { m.Main = "Entry.42m" }, `"main"`},
		{"programs", func(m *manifest.Manifest) { m.Programs = []string{"Main"} }, `"programs"`},
		{"bin", func(m *manifest.Manifest) { m.Bin = map[string]string{"go": "run.sh"} }, `"bin"`},
		{"root", func(m *manifest.Manifest) { m.Root = "src" }, `"root"`},
		{"dependencies.java", func(m *manifest.Manifest) {
			m.Dependencies.Java = []manifest.JavaDependency{{GroupID: "g", ArtifactID: "a", Version: "1"}}
		}, `"dependencies.java"`},
		{"devDependencies.java", func(m *manifest.Manifest) {
			m.DevDependencies.Java = []manifest.JavaDependency{{GroupID: "g", ArtifactID: "a", Version: "1"}}
		}, `"devDependencies.java"`},
		{"optionalDependencies.java", func(m *manifest.Manifest) {
			m.OptionalDependencies.Java = []manifest.JavaDependency{{GroupID: "g", ArtifactID: "a", Version: "1"}}
		}, `"optionalDependencies.java"`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := base()
			c.mut(m)
			err := m.Validate()
			if err == nil || !strings.Contains(err.Error(), c.hint) {
				t.Fatalf("expected error mentioning %s, got %v", c.hint, err)
			}
			if !strings.Contains(err.Error(), `"webcomponent"`) {
				t.Fatalf("error should reference the webcomponent type: %v", err)
			}
		})
	}
}

func TestValidateBDLForbidsWebcomponentsField(t *testing.T) {
	m := &manifest.Manifest{
		Name: "p", Version: "1.0.0",
		// Type omitted -> defaults to bdl.
		Webcomponents: []string{"MyWidget"},
	}
	err := m.Validate()
	if err == nil || !strings.Contains(err.Error(), `"webcomponents"`) {
		t.Fatalf("expected forbidden-webcomponents error, got %v", err)
	}
}

func TestValidateUnknownType(t *testing.T) {
	m := &manifest.Manifest{
		Name: "p", Version: "1.0.0",
		Type: manifest.PackageKind("schema"),
	}
	err := m.Validate()
	if err == nil || !strings.Contains(err.Error(), "unknown package type") {
		t.Fatalf("expected unknown-type error, got %v", err)
	}
}

func TestValidateWebcomponentHappyPath(t *testing.T) {
	m := &manifest.Manifest{
		Name: "chart-3d", Version: "1.0.0",
		Type:          manifest.KindWebcomponent,
		Description:   "3D chart widget",
		License:       "MIT",
		Webcomponents: []string{"3DChart", "Heatmap"},
		Dependencies: manifest.Dependencies{
			FGL: map[string]string{"wc-theme-base": "^1.0.0"},
		},
	}
	if err := m.Validate(); err != nil {
		t.Fatalf("expected validation success, got %v", err)
	}
}

func TestWebcomponentManifestRoundTrip(t *testing.T) {
	dir := t.TempDir()
	orig := &manifest.Manifest{
		Name: "chart-3d", Version: "1.0.0",
		Type:          manifest.KindWebcomponent,
		Description:   "3D chart widget",
		Webcomponents: []string{"3DChart"},
		Dependencies:  manifest.Dependencies{FGL: map[string]string{}},
	}
	if err := orig.Save(dir); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := manifest.Load(dir)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got.Type != manifest.KindWebcomponent {
		t.Fatalf("Type round-trip: got %q want %q", got.Type, manifest.KindWebcomponent)
	}
	if len(got.Webcomponents) != 1 || got.Webcomponents[0] != "3DChart" {
		t.Fatalf("Webcomponents round-trip: got %v", got.Webcomponents)
	}
	// Confirm "type" key appears in the on-disk JSON.
	data, err := os.ReadFile(filepath.Join(dir, manifest.Filename))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(string(data), `"type": "webcomponent"`) {
		t.Fatalf("type field not serialized:\n%s", data)
	}
}

func TestBDLManifestOmitsTypeAndWebcomponents(t *testing.T) {
	// A classic BDL manifest must not pollute the output with empty
	// "type": "" or "webcomponents": [] keys.
	m := &manifest.Manifest{
		Name: "p", Version: "1.0.0",
		Dependencies: manifest.Dependencies{FGL: map[string]string{}},
	}
	data, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(data), `"type"`) {
		t.Fatalf("expected no `type` key in BDL manifest JSON: %s", data)
	}
	if strings.Contains(string(data), `"webcomponents"`) {
		t.Fatalf("expected no `webcomponents` key in BDL manifest JSON: %s", data)
	}
}
