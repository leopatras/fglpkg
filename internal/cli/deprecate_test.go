package cli

import (
	"strings"
	"testing"
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
		// ── error cases ──
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
