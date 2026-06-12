package crypto

import (
	"errors"
	"fmt"

	"golang.org/x/crypto/argon2"
)

// Argon2id parameters for keyfile encryption. These are fixed by the
// protocol; changing them invalidates existing keyfiles.
const (
	keyfileSaltSize    = 32
	keyfileArgonTime   = 3
	keyfileArgonMemory = 64 * 1024 // KiB => 64 MiB
	keyfileArgonLanes  = 4
)

// ErrKeyfileTooShort is returned when keyfile data is shorter than the
// fixed salt+nonce+tag framing.
var ErrKeyfileTooShort = errors.New("crypto: keyfile data too short")

// keyfileKey derives the keyfile encryption key from a passphrase and
// salt using Argon2id.
func keyfileKey(passphrase, salt []byte) [KeySize]byte {
	var key [KeySize]byte
	copy(key[:], argon2.IDKey(passphrase, salt, keyfileArgonTime, keyfileArgonMemory, keyfileArgonLanes, KeySize))
	return key
}

// EncryptKeyfile encrypts plaintext (a serialized identity) under a
// passphrase. The output layout is:
//
//	[salt: 32 bytes][nonce: 24 bytes][ciphertext+tag: variable]
func EncryptKeyfile(passphrase, plaintext []byte) ([]byte, error) {
	salt := RandomBytes(keyfileSaltSize)
	key := keyfileKey(passphrase, salt)
	nonce, ct, err := Encrypt(key, plaintext, nil)
	if err != nil {
		return nil, err
	}
	out := make([]byte, 0, keyfileSaltSize+NonceSize+len(ct))
	out = append(out, salt...)
	out = append(out, nonce[:]...)
	out = append(out, ct...)
	return out, nil
}

// DecryptKeyfile decrypts a keyfile produced by EncryptKeyfile. A wrong
// passphrase fails authentication and returns an error; it can never
// silently yield garbage.
func DecryptKeyfile(passphrase, data []byte) ([]byte, error) {
	if len(data) < keyfileSaltSize+NonceSize+Overhead {
		return nil, ErrKeyfileTooShort
	}
	salt := data[:keyfileSaltSize]
	var nonce [NonceSize]byte
	copy(nonce[:], data[keyfileSaltSize:keyfileSaltSize+NonceSize])
	ct := data[keyfileSaltSize+NonceSize:]

	key := keyfileKey(passphrase, salt)
	plain, err := Decrypt(key, nonce, ct, nil)
	if err != nil {
		return nil, fmt.Errorf("crypto: keyfile decryption failed (wrong passphrase or corrupt file): %w", err)
	}
	return plain, nil
}
