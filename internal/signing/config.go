package signing

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// Enforcement modes for Layer 1 signature verification.
const (
	EnforceRequire = "require" // a bad or missing signature aborts install
	EnforceWarn    = "warn"    // a bad or missing signature warns but continues
	EnforceOff     = "off"     // signature verification is disabled
)

// DefaultEnforce is the v1.0 default. Per the rollout plan it flips to
// EnforceRequire in a later release once the registry backfill is confirmed
// complete.
const DefaultEnforce = EnforceWarn

// configFile is the per-user config read for the signing.enforce setting.
const configFile = "config.json"

type signingConfig struct {
	Signing struct {
		Enforce string `json:"enforce"`
	} `json:"signing"`
}

// EnforceMode resolves the active enforcement mode, in priority order:
//  1. FGLPKG_SIGNING environment variable (require|warn|off);
//  2. signing.enforce in <home>/config.json;
//  3. DefaultEnforce.
//
// An unrecognised value at any level falls through to the next source.
func EnforceMode(home string) string {
	if m, ok := normaliseEnforce(os.Getenv("FGLPKG_SIGNING")); ok {
		return m
	}
	if data, err := os.ReadFile(filepath.Join(home, configFile)); err == nil {
		var c signingConfig
		if json.Unmarshal(data, &c) == nil {
			if m, ok := normaliseEnforce(c.Signing.Enforce); ok {
				return m
			}
		}
	}
	return DefaultEnforce
}

// normaliseEnforce validates and lower-cases an enforce value.
func normaliseEnforce(s string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case EnforceRequire:
		return EnforceRequire, true
	case EnforceWarn:
		return EnforceWarn, true
	case EnforceOff:
		return EnforceOff, true
	default:
		return "", false
	}
}
