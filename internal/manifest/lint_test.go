package manifest

import "testing"

func TestReportPredicates(t *testing.T) {
	var r Report
	if r.HasErrors() {
		t.Fatal("empty report should have no errors")
	}
	r.Warnf("files", "matched nothing")
	if r.HasErrors() {
		t.Error("a warning must not count as an error")
	}
	r.Errorf("", "boom")
	if !r.HasErrors() {
		t.Error("HasErrors should be true after an error diagnostic")
	}
	if got := len(r.Errors()); got != 1 {
		t.Errorf("Errors() = %d, want 1", got)
	}
	if got := len(r.Warnings()); got != 1 {
		t.Errorf("Warnings() = %d, want 1", got)
	}
}

func TestLintIntoMissingPublishFieldsAreWarnings(t *testing.T) {
	m := &Manifest{Name: "pkg", Version: "1.0.0"}
	var r Report
	m.LintInto(&r)

	if r.HasErrors() {
		t.Fatalf("a structurally-valid manifest should yield no errors, got %+v", r.Errors())
	}
	// description, license, repository, author all missing → 4 warnings.
	if got := len(r.Warnings()); got != 4 {
		t.Fatalf("Warnings() = %d, want 4: %+v", got, r.Warnings())
	}
	wantFields := map[string]bool{"description": true, "license": true, "repository": true, "author": true}
	for _, d := range r.Warnings() {
		if !wantFields[d.Field] {
			t.Errorf("unexpected warning field %q", d.Field)
		}
	}
}

func TestLintIntoDuplicateKeywords(t *testing.T) {
	m := &Manifest{Name: "pkg", Version: "1.0.0", Keywords: []string{"db", "db", "util"}}
	var r Report
	m.LintInto(&r)

	found := false
	for _, d := range r.Warnings() {
		if d.Field == "keywords" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected a duplicate-keywords warning, got %+v", r.Warnings())
	}
}

func TestLintIntoStructuralError(t *testing.T) {
	// Missing name → Validate() fails → one error diagnostic.
	m := &Manifest{Version: "1.0.0"}
	var r Report
	m.LintInto(&r)
	if !r.HasErrors() {
		t.Fatalf("expected a structural error for a missing name, got %+v", r.Diagnostics)
	}
}

func TestDuplicateStrings(t *testing.T) {
	got := duplicateStrings([]string{"a", "b", "a", "c", "c", "c"})
	want := []string{"a", "c"}
	if len(got) != len(want) {
		t.Fatalf("duplicateStrings = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("duplicateStrings[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}
