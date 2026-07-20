package signing

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"unicode/utf16"
)

// canonicalize returns the RFC 8785 (JCS) canonical serialization of v.
//
// Supported value types: nil, bool, string, json.Number, int, int64, float64
// (only if integral), map[string]interface{}, and []interface{}. Numbers must
// be integers — a fractional or exponent value is rejected. Every signing input
// in this package is strings + at most one integer by design, so failing closed
// on a float is safer than reproducing JCS's ECMAScript number formatting.
func canonicalize(v interface{}) ([]byte, error) {
	var b bytes.Buffer
	if err := canonicalizeValue(&b, v); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

func canonicalizeValue(b *bytes.Buffer, v interface{}) error {
	switch t := v.(type) {
	case nil:
		b.WriteString("null")
	case bool:
		if t {
			b.WriteString("true")
		} else {
			b.WriteString("false")
		}
	case string:
		writeJCSString(b, t)
	case json.Number:
		return writeJCSNumber(b, string(t))
	case int:
		b.WriteString(strconv.FormatInt(int64(t), 10))
	case int64:
		b.WriteString(strconv.FormatInt(t, 10))
	case float64:
		// json.Unmarshal without UseNumber yields float64; accept only integral
		// values, reject anything with a fractional part.
		if t != float64(int64(t)) {
			return fmt.Errorf("signing: non-integer number %v is not allowed in a signing payload", t)
		}
		b.WriteString(strconv.FormatInt(int64(t), 10))
	case map[string]interface{}:
		return writeJCSObject(b, t)
	case []interface{}:
		return writeJCSArray(b, t)
	default:
		return fmt.Errorf("signing: unsupported type %T in signing payload", v)
	}
	return nil
}

func writeJCSObject(b *bytes.Buffer, m map[string]interface{}) error {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return lessUTF16(keys[i], keys[j]) })
	b.WriteByte('{')
	for i, k := range keys {
		if i > 0 {
			b.WriteByte(',')
		}
		writeJCSString(b, k)
		b.WriteByte(':')
		if err := canonicalizeValue(b, m[k]); err != nil {
			return err
		}
	}
	b.WriteByte('}')
	return nil
}

func writeJCSArray(b *bytes.Buffer, a []interface{}) error {
	b.WriteByte('[')
	for i, v := range a {
		if i > 0 {
			b.WriteByte(',')
		}
		if err := canonicalizeValue(b, v); err != nil {
			return err
		}
	}
	b.WriteByte(']')
	return nil
}

func writeJCSNumber(b *bytes.Buffer, s string) error {
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return fmt.Errorf("signing: non-integer number %q is not allowed in a signing payload", s)
	}
	b.WriteString(strconv.FormatInt(n, 10))
	return nil
}

// writeJCSString writes s as an RFC 8785 JSON string literal: escape " and \,
// short escapes for the six named control characters, \u00xx (lowercase hex)
// for the remaining C0 controls, everything else emitted as literal UTF-8.
func writeJCSString(b *bytes.Buffer, s string) {
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\b':
			b.WriteString(`\b`)
		case '\t':
			b.WriteString(`\t`)
		case '\n':
			b.WriteString(`\n`)
		case '\f':
			b.WriteString(`\f`)
		case '\r':
			b.WriteString(`\r`)
		default:
			if r < 0x20 {
				fmt.Fprintf(b, `\u%04x`, r)
			} else {
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte('"')
}

// lessUTF16 reports whether a sorts before b by UTF-16 code unit, per RFC 8785
// object-key ordering. For the ASCII keys in this package this equals byte
// order, but the full rule is implemented for correctness.
func lessUTF16(a, b string) bool {
	ua := utf16.Encode([]rune(a))
	ub := utf16.Encode([]rune(b))
	for i := 0; i < len(ua) && i < len(ub); i++ {
		if ua[i] != ub[i] {
			return ua[i] < ub[i]
		}
	}
	return len(ua) < len(ub)
}
