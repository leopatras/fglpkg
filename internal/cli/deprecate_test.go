package cli

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/4js-mikefolcher/fglpkg/internal/registry"
)

func TestParseDeprecateArgs(t *testing.T) {
	cases := []struct {
		name        string
		args        []string
		wantErr     string // substring; "" = expect success
		wantSlug    string
		wantVersion string
		wantMessage string
		wantMovedTo string
		wantUndo    bool
	}{
		{
			name:        "version with positional message",
			args:        []string{"chart-3d@1.2.3", "please upgrade"},
			wantSlug:    "chart-3d",
			wantVersion: "1.2.3",
			wantMessage: "please upgrade",
		},
		{
			name:        "message flag",
			args:        []string{"chart-3d@1.2.3", "--message", "unmaintained"},
			wantSlug:    "chart-3d",
			wantVersion: "1.2.3",
			wantMessage: "unmaintained",
		},
		{
			name:        "moved-to auto-fills message",
			args:        []string{"chart-3d@1.2.3", "--moved-to", "chart-3d-ng"},
			wantSlug:    "chart-3d",
			wantVersion: "1.2.3",
			wantMessage: "chart-3d has moved to chart-3d-ng",
			wantMovedTo: "chart-3d-ng",
		},
		{
			name:        "whole package (no version)",
			args:        []string{"chart-3d", "--moved-to", "chart-3d-ng"},
			wantSlug:    "chart-3d",
			wantVersion: "",
			wantMovedTo: "chart-3d-ng",
			wantMessage: "chart-3d has moved to chart-3d-ng",
		},
		{
			name:        "moved-to with version pin",
			args:        []string{"chart-3d", "--moved-to", "chart-3d-ng@2.0.0"},
			wantSlug:    "chart-3d",
			wantMovedTo: "chart-3d-ng@2.0.0",
			wantMessage: "chart-3d has moved to chart-3d-ng@2.0.0",
		},
		{
			name:        "undo version",
			args:        []string{"chart-3d@1.2.3", "--undo"},
			wantSlug:    "chart-3d",
			wantVersion: "1.2.3",
			wantUndo:    true,
		},
		{
			name:        "non-canonical primary slug is accepted (canonicalized on send)",
			args:        []string{"Chart_3D", "please upgrade"},
			wantSlug:    "Chart_3D",
			wantMessage: "please upgrade",
		},
		{
			name:        "moved-to to a different version of the same package is allowed",
			args:        []string{"chart-3d@1.2.3", "--moved-to", "chart-3d@2.0.0"},
			wantSlug:    "chart-3d",
			wantVersion: "1.2.3",
			wantMovedTo: "chart-3d@2.0.0",
			wantMessage: "chart-3d has moved to chart-3d@2.0.0",
		},
		{
			name:        "whole-package moved-to a specific version of itself is allowed",
			args:        []string{"chart-3d", "--moved-to", "chart-3d@2.0.0"},
			wantSlug:    "chart-3d",
			wantMovedTo: "chart-3d@2.0.0",
			wantMessage: "chart-3d has moved to chart-3d@2.0.0",
		},
		// ── error cases ──
		{
			name:    "invalid primary slug",
			args:    []string{"!!!", "msg"},
			wantErr: "is not a valid package name",
		},
		{
			name:    "message over 512-byte cap",
			args:    []string{"chart-3d@1.2.3", strings.Repeat("x", 513)},
			wantErr: "exceeds the 512-byte limit",
		},
		{
			name:    "whitespace-only positional message",
			args:    []string{"chart-3d@1.2.3", "   "},
			wantErr: "cannot be blank",
		},
		{
			name:    "whitespace-only --message",
			args:    []string{"chart-3d@1.2.3", "--message", "  "},
			wantErr: "cannot be blank",
		},
		{
			name:    "self-reference whole package",
			args:    []string{"chart-3d", "--moved-to", "chart-3d"},
			wantErr: "cannot point chart-3d at itself",
		},
		{
			name:    "self-reference same version",
			args:    []string{"chart-3d@1.2.3", "--moved-to", "chart-3d@1.2.3"},
			wantErr: "at itself",
		},
		{
			name:    "no package",
			args:    []string{},
			wantErr: "a package is required",
		},
		{
			name:    "missing message and moved-to",
			args:    []string{"chart-3d@1.2.3"},
			wantErr: "a deprecation message is required",
		},
		{
			name:    "undo with message",
			args:    []string{"chart-3d@1.2.3", "oops", "--undo"},
			wantErr: "--undo does not take",
		},
		{
			name:    "undo with moved-to",
			args:    []string{"chart-3d@1.2.3", "--undo", "--moved-to", "x"},
			wantErr: "--undo does not take",
		},
		{
			name:    "positional and --message conflict",
			args:    []string{"chart-3d@1.2.3", "pos msg", "--message", "flag msg"},
			wantErr: "not both",
		},
		{
			name:    "malformed moved-to",
			args:    []string{"chart-3d@1.2.3", "--moved-to", "Chart_3D"},
			wantErr: "is not a valid package name",
		},
		{
			name:    "leading-@ scoped name rejected",
			args:    []string{"@scope/pkg", "msg"},
			wantErr: "scoped names are not supported",
		},
		{
			name:    "empty version after @",
			args:    []string{"chart-3d@", "msg"},
			wantErr: "missing version",
		},
		{
			name:    "unknown flag",
			args:    []string{"chart-3d@1.2.3", "--nope"},
			wantErr: "unknown flag",
		},
		{
			name:    "too many positionals",
			args:    []string{"chart-3d@1.2.3", "msg", "extra"},
			wantErr: "too many arguments",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			da, err := parseDeprecateArgs(tc.args)
			if tc.wantErr != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tc.wantErr)
				}
				if !strings.Contains(err.Error(), tc.wantErr) {
					t.Errorf("error = %q, want substring %q", err.Error(), tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if da.slug != tc.wantSlug {
				t.Errorf("slug = %q, want %q", da.slug, tc.wantSlug)
			}
			if da.version != tc.wantVersion {
				t.Errorf("version = %q, want %q", da.version, tc.wantVersion)
			}
			if da.message != tc.wantMessage {
				t.Errorf("message = %q, want %q", da.message, tc.wantMessage)
			}
			if da.movedTo != tc.wantMovedTo {
				t.Errorf("movedTo = %q, want %q", da.movedTo, tc.wantMovedTo)
			}
			if da.undo != tc.wantUndo {
				t.Errorf("undo = %v, want %v", da.undo, tc.wantUndo)
			}
		})
	}
}

func TestMapDeprecateError(t *testing.T) {
	cases := []struct {
		name         string
		err          error
		da           deprecateArgs
		wantContains []string
		wantAbsent   []string
		wantEqual    bool // err is returned unchanged
	}{
		{
			name:         "unauthorized",
			err:          fmt.Errorf("deprecate chart-3d: %w", registry.ErrUnauthorized),
			da:           deprecateArgs{slug: "chart-3d"},
			wantContains: []string{"logged in", "fglpkg login"},
		},
		{
			name:         "forbidden names the package",
			err:          fmt.Errorf("deprecate chart-3d: %w", registry.ErrForbidden),
			da:           deprecateArgs{slug: "chart-3d"},
			wantContains: []string{"only the owning partner", "chart-3d"},
		},
		{
			name:         "not found — version branch",
			err:          fmt.Errorf("deprecate chart-3d@1.2.3: %w", registry.ErrNotFound),
			da:           deprecateArgs{slug: "chart-3d", version: "1.2.3"},
			wantContains: []string{"no published version", "1.2.3"},
		},
		{
			name:         "not found — whole-package branch",
			err:          fmt.Errorf("deprecate chart-3d: %w", registry.ErrNotFound),
			da:           deprecateArgs{slug: "chart-3d"},
			wantContains: []string{"no such package", "chart-3d"},
		},
		{
			name:         "bad request surfaces the server's message cleanly",
			err:          &registry.BadRequestError{Op: "deprecate chart-3d@1.2.3", Detail: "invalid moved_to version"},
			da:           deprecateArgs{slug: "chart-3d", version: "1.2.3"},
			wantContains: []string{"deprecation rejected by the registry: invalid moved_to version"},
			// The internal op-prefix must not leak and the "rejected by the
			// registry" phrase must not be doubled.
			wantAbsent: []string{"deprecate chart-3d", "registry: deprecate"},
		},
		{
			name:         "bad request with no server body still reads cleanly",
			err:          &registry.BadRequestError{Op: "deprecate chart-3d"},
			da:           deprecateArgs{slug: "chart-3d"},
			wantContains: []string{"deprecation rejected by the registry"},
		},
		{
			name:      "unknown error passes through unchanged",
			err:       fmt.Errorf("connection reset"),
			da:        deprecateArgs{slug: "chart-3d"},
			wantEqual: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := mapDeprecateError(tc.err, tc.da)
			if tc.wantEqual {
				if got.Error() != tc.err.Error() {
					t.Errorf("got %q, want unchanged %q", got, tc.err)
				}
				return
			}
			for _, sub := range tc.wantContains {
				if !strings.Contains(got.Error(), sub) {
					t.Errorf("error = %q, want substring %q", got.Error(), sub)
				}
			}
			for _, sub := range tc.wantAbsent {
				if strings.Contains(got.Error(), sub) {
					t.Errorf("error = %q, should not contain %q", got.Error(), sub)
				}
			}
		})
	}
}

func TestPrintDeprecateResult(t *testing.T) {
	cases := []struct {
		name         string
		da           deprecateArgs
		wantContains []string
		wantAbsent   []string
	}{
		{
			name:         "version success with moved-to",
			da:           deprecateArgs{slug: "chart-3d", version: "1.2.3", message: "please upgrade", movedTo: "chart-3d-ng"},
			wantContains: []string{"✓ Deprecated chart-3d@1.2.3", "message:  please upgrade", "moved to: chart-3d-ng", "Consumers can still install it"},
		},
		{
			name:         "whole-package success without moved-to omits the line",
			da:           deprecateArgs{slug: "chart-3d", message: "unmaintained"},
			wantContains: []string{"✓ Deprecated chart-3d", "message:  unmaintained"},
			wantAbsent:   []string{"moved to:"},
		},
		{
			name:         "undo",
			da:           deprecateArgs{slug: "chart-3d", version: "1.2.3", undo: true},
			wantContains: []string{"✓ Cleared deprecation on chart-3d@1.2.3"},
			wantAbsent:   []string{"message:"},
		},
		{
			name:         "json render",
			da:           deprecateArgs{slug: "chart-3d", version: "1.2.3", movedTo: "chart-3d-ng", jsonOut: true},
			wantContains: []string{`"slug": "chart-3d"`, `"deprecated": true`, `"version": "1.2.3"`, `"movedTo": "chart-3d-ng"`},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, err := captureStdout(t, func() error {
				printDeprecateResult(tc.da)
				return nil
			})
			if err != nil {
				t.Fatalf("captureStdout: %v", err)
			}
			for _, sub := range tc.wantContains {
				if !strings.Contains(out, sub) {
					t.Errorf("output missing %q\n---\n%s", sub, out)
				}
			}
			for _, sub := range tc.wantAbsent {
				if strings.Contains(out, sub) {
					t.Errorf("output unexpectedly contains %q\n---\n%s", sub, out)
				}
			}
			// The JSON branch must be valid JSON.
			if tc.da.jsonOut {
				var m map[string]any
				if err := json.Unmarshal([]byte(out), &m); err != nil {
					t.Errorf("json output not parseable: %v\n%s", err, out)
				}
			}
		})
	}
}

func TestDeprecationNote(t *testing.T) {
	cases := []struct {
		row  outdatedRow
		want string
	}{
		{outdatedRow{}, ""},
		{outdatedRow{Deprecated: true}, "deprecated"},
		{outdatedRow{Deprecated: true, MovedTo: "chart-3d-ng"}, "deprecated → chart-3d-ng"},
	}
	for _, tc := range cases {
		if got := deprecationNote(tc.row); got != tc.want {
			t.Errorf("deprecationNote(%+v) = %q, want %q", tc.row, got, tc.want)
		}
	}
}
