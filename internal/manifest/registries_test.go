package manifest

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestFGLDep_StringFormUnchanged(t *testing.T) {
	var d Dependencies
	if err := json.Unmarshal([]byte(`{"fgl":{"logft":"^2.0.0"}}`), &d); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if d.FGL["logft"] != "^2.0.0" {
		t.Fatalf("FGL = %+v", d.FGL)
	}
	if len(d.FGLPins) != 0 {
		t.Fatalf("expected no pins, got %+v", d.FGLPins)
	}
	// Round-trips back to the plain string form.
	out, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(out); got != `{"fgl":{"logft":"^2.0.0"}}` {
		t.Fatalf("marshal = %s", got)
	}
}

func TestFGLDep_ObjectFormParsesPin(t *testing.T) {
	var d Dependencies
	body := `{"fgl":{"utils":{"version":"^1.0.0","registry":"acme-internal"}}}`
	if err := json.Unmarshal([]byte(body), &d); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if d.FGL["utils"] != "^1.0.0" {
		t.Fatalf("version = %q", d.FGL["utils"])
	}
	if d.FGLPins["utils"] != "acme-internal" {
		t.Fatalf("pin = %q", d.FGLPins["utils"])
	}
	// Round-trips back to the object form.
	out, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), `"registry":"acme-internal"`) ||
		!strings.Contains(string(out), `"version":"^1.0.0"`) {
		t.Fatalf("marshal lost the pin: %s", out)
	}
}

func TestFGLDep_MixedStringAndObject(t *testing.T) {
	var d Dependencies
	body := `{"fgl":{"logft":"^2.0.0","utils":{"version":"^1.0.0","registry":"acme"}}}`
	if err := json.Unmarshal([]byte(body), &d); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if d.FGL["logft"] != "^2.0.0" || d.FGL["utils"] != "^1.0.0" {
		t.Fatalf("FGL = %+v", d.FGL)
	}
	if _, pinned := d.FGLPins["logft"]; pinned {
		t.Fatal("logft should not be pinned")
	}
	if d.FGLPins["utils"] != "acme" {
		t.Fatalf("utils pin = %q", d.FGLPins["utils"])
	}
}

func TestFGLDep_ObjectMissingVersionErrors(t *testing.T) {
	var d Dependencies
	if err := json.Unmarshal([]byte(`{"fgl":{"utils":{"registry":"acme"}}}`), &d); err == nil {
		t.Fatal("expected error when object form omits version")
	}
}

func TestFGLDep_ObjectUnknownFieldErrors(t *testing.T) {
	var d Dependencies
	body := `{"fgl":{"utils":{"version":"^1.0.0","registryy":"acme"}}}`
	if err := json.Unmarshal([]byte(body), &d); err == nil {
		t.Fatal("expected error on unknown field in object form")
	}
}

func TestManifest_RegistriesRoundTrip(t *testing.T) {
	body := `{
	  "name": "demo", "version": "1.0.0",
	  "dependencies": {},
	  "registries": [
	    {"name":"acme","type":"artifactory","url":"https://a/artifactory","repoKey":"k","priority":2,"auth":"bearer","packages":["acme-*"]}
	  ]
	}`
	var m Manifest
	if err := json.Unmarshal([]byte(body), &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(m.Registries) != 1 || m.Registries[0].RepoKey != "k" {
		t.Fatalf("registries = %+v", m.Registries)
	}
	out, err := json.Marshal(&m)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), `"registries"`) {
		t.Fatalf("registries dropped on marshal: %s", out)
	}
}
