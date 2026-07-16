package slug

import "testing"

func TestCanonical(t *testing.T) {
	cases := []struct{ in, want string }{
		{"fgl_ai_sdk", "fgl-ai-sdk"},   // underscores → hyphens
		{"Fgl.AI.SDK", "fgl-ai-sdk"},   // dots → hyphens, lowercased
		{"fgl__ai--sdk", "fgl-ai-sdk"}, // runs collapse to one '-'
		{"fgl.ai_sdk-x", "fgl-ai-sdk-x"},
		{"fgl-ai-sdk", "fgl-ai-sdk"}, // already canonical
		{"POIAPI", "poiapi"},
		{"a", "a"}, // canonicalizes fine even though IsValid rejects (too short)
	}
	for _, c := range cases {
		if got := Canonical(c.in); got != c.want {
			t.Errorf("Canonical(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestCanonicalIdempotent(t *testing.T) {
	for _, in := range []string{"fgl_ai_sdk", "Fgl.AI.SDK", "fgl__ai--sdk", "already-canonical"} {
		once := Canonical(in)
		if twice := Canonical(once); twice != once {
			t.Errorf("Canonical not idempotent for %q: %q -> %q", in, once, twice)
		}
	}
}

func TestIsValid(t *testing.T) {
	valid := []string{"ab", "fgl-ai-sdk", "a1", "x0y", "poiapi"}
	for _, s := range valid {
		if !IsValid(s) {
			t.Errorf("IsValid(%q) = false, want true", s)
		}
	}
	invalid := []string{
		"",       // empty
		"a",      // too short (< 2)
		"-ab",    // leading hyphen
		"ab-",    // trailing hyphen
		"fgl_ai", // underscore (not canonical)
		"Fgl",    // uppercase
		"a.b",    // dot
	}
	// ("fgl--ai" is intentionally NOT here: a double hyphen mid-slug is valid per
	// the shape — only the start/end are constrained — it just never arises from
	// Canonical, which collapses runs.)
	for _, s := range invalid {
		if IsValid(s) {
			t.Errorf("IsValid(%q) = true, want false", s)
		}
	}
}

// TestCanonicalOutputIsValidForRealNames guards the property that a normal
// name (start/end alphanumeric) always canonicalizes to a valid slug.
func TestCanonicalOutputIsValidForRealNames(t *testing.T) {
	for _, in := range []string{"fgl_ai_sdk", "Fgl.AI.SDK", "My_Cool.Pkg", "poiapi"} {
		if got := Canonical(in); !IsValid(got) {
			t.Errorf("Canonical(%q) = %q, which is not a valid slug", in, got)
		}
	}
}
