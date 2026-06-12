package crypto

import (
	"crypto/sha256"
	"fmt"
	"io"

	"golang.org/x/crypto/chacha20poly1305"
	"golang.org/x/crypto/curve25519"
	"golang.org/x/crypto/hkdf"
)

// NonceSize is the size in bytes of an XChaCha20-Poly1305 nonce.
const NonceSize = chacha20poly1305.NonceSizeX

// Overhead is the ciphertext expansion of XChaCha20-Poly1305 (the
// Poly1305 authentication tag).
const Overhead = chacha20poly1305.Overhead

// DeriveSharedKey performs an X25519 ECDH between priv and pub and
// derives a 32-byte symmetric key via HKDF-SHA256 with the given info
// string as domain separator.
func DeriveSharedKey(priv, pub [KeySize]byte, info string) ([KeySize]byte, error) {
	var key [KeySize]byte
	shared, err := curve25519.X25519(priv[:], pub[:])
	if err != nil {
		return key, fmt.Errorf("crypto: X25519 key agreement: %w", err)
	}
	r := hkdf.New(sha256.New, shared, nil, []byte(info))
	if _, err := io.ReadFull(r, key[:]); err != nil {
		return key, fmt.Errorf("crypto: HKDF expand: %w", err)
	}
	return key, nil
}

// Encrypt encrypts plaintext with XChaCha20-Poly1305 under key, binding
// aad as associated data. It generates a fresh random nonce and returns
// it alongside the ciphertext.
func Encrypt(key [KeySize]byte, plaintext, aad []byte) (nonce [NonceSize]byte, ciphertext []byte, err error) {
	aead, err := chacha20poly1305.NewX(key[:])
	if err != nil {
		return nonce, nil, fmt.Errorf("crypto: creating AEAD: %w", err)
	}
	copy(nonce[:], RandomBytes(NonceSize))
	ciphertext = aead.Seal(nil, nonce[:], plaintext, aad)
	return nonce, ciphertext, nil
}

// Decrypt decrypts an XChaCha20-Poly1305 ciphertext produced by
// Encrypt. It returns an error if authentication fails.
func Decrypt(key [KeySize]byte, nonce [NonceSize]byte, ciphertext, aad []byte) ([]byte, error) {
	aead, err := chacha20poly1305.NewX(key[:])
	if err != nil {
		return nil, fmt.Errorf("crypto: creating AEAD: %w", err)
	}
	plaintext, err := aead.Open(nil, nonce[:], ciphertext, aad)
	if err != nil {
		return nil, fmt.Errorf("crypto: decrypting: %w", err)
	}
	return plaintext, nil
}
