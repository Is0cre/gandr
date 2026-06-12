// Package crypto provides all cryptographic primitives used by Gandr.
//
// It wraps Ed25519 (identity and signing), X25519 (key agreement),
// XChaCha20-Poly1305 (authenticated encryption), HKDF-SHA256 (key
// derivation), and Argon2id (passphrase-based key derivation for the
// keyfile). No other package in Gandr performs cryptographic operations
// directly; everything goes through this package so the audit surface
// stays in one place.
//
// Test coverage note: all reachable statements are covered. The only
// uncovered branches are propagation of errors that current library
// contracts make impossible (crypto/rand.Read never fails,
// chacha20poly1305.NewX never rejects a 32-byte key, HKDF-SHA256 never
// fails expanding 32 of a possible 8160 bytes, X25519 basepoint
// multiplication of a clamped scalar never yields a low-order result).
// They are kept as defenses against future library behavior changes.
//
// External dependency note: filippo.io/edwards25519 is used solely for
// the Ed25519 -> X25519 public key conversion required by sealed
// messages. It is the same code vendored inside the Go standard
// library's crypto/ed25519 implementation; the standard library does
// not export the Edwards point arithmetic needed for the conversion.
package crypto

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/sha512"
	"fmt"

	"filippo.io/edwards25519"
	"golang.org/x/crypto/curve25519"
)

// KeySize is the size in bytes of Ed25519 and X25519 public keys and
// of all symmetric keys used in Gandr.
const KeySize = 32

// SignatureSize is the size in bytes of an Ed25519 signature.
const SignatureSize = ed25519.SignatureSize

// GenerateIdentity generates a new Ed25519 keypair. This keypair is the
// node's or user's permanent identity.
func GenerateIdentity() (ed25519.PublicKey, ed25519.PrivateKey, error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, nil, fmt.Errorf("crypto: generating identity keypair: %w", err)
	}
	return pub, priv, nil
}

// Sign signs digest with the given Ed25519 private key. Callers are
// expected to pass a SHA-256 digest of the canonical message bytes, per
// the wire protocol; Sign itself does not hash.
func Sign(priv ed25519.PrivateKey, digest []byte) ([]byte, error) {
	if l := len(priv); l != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("crypto: invalid private key length %d", l)
	}
	return ed25519.Sign(priv, digest), nil
}

// Verify reports whether sig is a valid Ed25519 signature over digest
// by the holder of pub. It never panics, even on malformed input.
func Verify(pub ed25519.PublicKey, digest, sig []byte) bool {
	if len(pub) != ed25519.PublicKeySize || len(sig) != ed25519.SignatureSize {
		return false
	}
	return ed25519.Verify(pub, digest, sig)
}

// Digest returns the SHA-256 digest of the concatenation of all parts.
// It is the single hashing primitive used for message signing and
// content addressing.
func Digest(parts ...[]byte) [32]byte {
	h := sha256.New()
	for _, p := range parts {
		h.Write(p)
	}
	var out [32]byte
	h.Sum(out[:0])
	return out
}

// PublicKeyToX25519 converts an Ed25519 public key to its X25519
// (Montgomery curve) equivalent. This allows encrypting to a party
// knowing only their Ed25519 identity public key, as sealed messages
// require.
func PublicKeyToX25519(pub ed25519.PublicKey) ([KeySize]byte, error) {
	var out [KeySize]byte
	if len(pub) != ed25519.PublicKeySize {
		return out, fmt.Errorf("crypto: invalid public key length %d", len(pub))
	}
	p, err := new(edwards25519.Point).SetBytes(pub)
	if err != nil {
		return out, fmt.Errorf("crypto: invalid Ed25519 point: %w", err)
	}
	copy(out[:], p.BytesMontgomery())
	return out, nil
}

// PrivateKeyToX25519 converts an Ed25519 private key to its X25519
// equivalent: SHA-512 of the seed, lower 32 bytes, clamped per RFC 7748.
func PrivateKeyToX25519(priv ed25519.PrivateKey) ([KeySize]byte, error) {
	var out [KeySize]byte
	if len(priv) != ed25519.PrivateKeySize {
		return out, fmt.Errorf("crypto: invalid private key length %d", len(priv))
	}
	h := sha512.Sum512(priv.Seed())
	copy(out[:], h[:KeySize])
	out[0] &= 248
	out[31] &= 127
	out[31] |= 64
	return out, nil
}

// GenerateX25519 generates a fresh ephemeral X25519 keypair. Used for
// per-message keys in sealed messages and per-session keys in the
// federation handshake.
func GenerateX25519() (pub, priv [KeySize]byte, err error) {
	rand.Read(priv[:]) // never fails; aborts the process on entropy failure
	p, err := curve25519.X25519(priv[:], curve25519.Basepoint)
	if err != nil {
		// unreachable for a clamped scalar times the basepoint, kept as
		// a defense against library behavior changes
		return pub, priv, fmt.Errorf("crypto: deriving X25519 public key: %w", err)
	}
	copy(pub[:], p)
	return pub, priv, nil
}

// RandomBytes returns n cryptographically random bytes. crypto/rand
// documents that Read never fails (it aborts the process if the kernel
// CSPRNG is unavailable), so no error is returned.
func RandomBytes(n int) []byte {
	b := make([]byte, n)
	rand.Read(b)
	return b
}
