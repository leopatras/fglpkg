// Package jsonutil provides JSON marshaling that does NOT HTML-escape.
//
// By default encoding/json escapes the three HTML-significant bytes '<', '>',
// and '&' into numeric Unicode escapes, and re-applies that at every marshal
// boundary — including when it compacts the output of a custom MarshalJSON.
// That mangles the human-facing files fglpkg writes: a Genero constraint like
// ">=6.0.0" and a lockfile "<root>" come out with those escapes instead of the
// literal '>' and '<' characters (GIS-280).
//
// These helpers disable HTML escaping so those files stay readable and diff
// cleanly. Every marshal step in a nested chain must go through them — a single
// default json.Marshal anywhere in the chain re-escapes the bytes.
package jsonutil

import (
	"bytes"
	"encoding/json"
)

// Marshal is json.Marshal without HTML escaping.
func Marshal(v any) ([]byte, error) {
	return encode(v, "")
}

// MarshalIndent is json.MarshalIndent (empty prefix) without HTML escaping.
func MarshalIndent(v any, indent string) ([]byte, error) {
	return encode(v, indent)
}

func encode(v any, indent string) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if indent != "" {
		enc.SetIndent("", indent)
	}
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	// json.Encoder.Encode appends a trailing newline; strip it so the result
	// matches the json.Marshal/MarshalIndent contract callers expect.
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}
