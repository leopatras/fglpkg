package signing

import (
	"crypto/ed25519"
	"encoding/base64"
	"fmt"
	"strings"
)

// decodeEd25519Pub decodes a standard-base64 raw 32-byte Ed25519 public key.
func decodeEd25519Pub(b64 string) (ed25519.PublicKey, error) {
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(b64))
	if err != nil {
		return nil, fmt.Errorf("decode public key: %w", err)
	}
	if len(raw) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("public key is %d bytes, want %d", len(raw), ed25519.PublicKeySize)
	}
	return ed25519.PublicKey(raw), nil
}

// decodeEd25519Sig decodes a standard-base64 raw 64-byte Ed25519 signature.
func decodeEd25519Sig(b64 string) ([]byte, error) {
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(b64))
	if err != nil {
		return nil, fmt.Errorf("decode signature: %w", err)
	}
	if len(raw) != ed25519.SignatureSize {
		return nil, fmt.Errorf("signature is %d bytes, want %d", len(raw), ed25519.SignatureSize)
	}
	return raw, nil
}

// VerifyArtifact verifies an artifact signature (sigB64) over the canonical
// payload built from f, using the standard-base64 raw public key pubB64.
// Returns ErrSignatureMismatch on any failure (bad encoding or bad signature).
func VerifyArtifact(f ArtifactFields, sigB64, pubB64 string) error {
	pub, err := decodeEd25519Pub(pubB64)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrSignatureMismatch, err)
	}
	sig, err := decodeEd25519Sig(sigB64)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrSignatureMismatch, err)
	}
	payload, err := CanonicalArtifactPayload(f)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrSignatureMismatch, err)
	}
	if !ed25519.Verify(pub, payload, sig) {
		return ErrSignatureMismatch
	}
	return nil
}

// VerifyDetached verifies a detached Ed25519 signature (sigB64, standard base64)
// over the raw bytes data, using pubB64. This is the release path: data is the
// contents of checksums.txt and sigB64 the contents of checksums.txt.sig.
// Returns ErrSignatureMismatch on any failure.
func VerifyDetached(data []byte, sigB64, pubB64 string) error {
	pub, err := decodeEd25519Pub(pubB64)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrSignatureMismatch, err)
	}
	sig, err := decodeEd25519Sig(sigB64)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrSignatureMismatch, err)
	}
	if !ed25519.Verify(pub, data, sig) {
		return ErrSignatureMismatch
	}
	return nil
}
