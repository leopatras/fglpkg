package signing

// ArtifactFields are the registry-artifact attributes that are signed. The set
// and their JSON names are frozen by the GI registry; the canonical payload is
// the object {"artifact": {...}} carrying these fields (JCS sorts the fields
// alphabetically). Size is a byte count and the only non-string value.
type ArtifactFields struct {
	Name       string
	Version    string
	Variant    string
	SHA256     string
	Size       int64
	UploadedAt string
	Uploader   string
}

// CanonicalArtifactPayload returns the RFC 8785 canonical bytes signed for an
// artifact. This is the exact byte sequence the GI registry signs, so it is the
// exact input the client verifies against (see the golden vector in
// signing_test.go).
func CanonicalArtifactPayload(f ArtifactFields) ([]byte, error) {
	return canonicalize(map[string]interface{}{
		"artifact": map[string]interface{}{
			"name":        f.Name,
			"version":     f.Version,
			"variant":     f.Variant,
			"sha256":      f.SHA256,
			"size":        f.Size,
			"uploaded_at": f.UploadedAt,
			"uploader":    f.Uploader,
		},
	})
}
