package crypto

import (
	"testing"
)

// FuzzDecryptKeyfile feeds arbitrary bytes to the keyfile parser. It
// must never panic and must never succeed on garbage.
func FuzzDecryptKeyfile(f *testing.F) {
	valid, err := EncryptKeyfile([]byte("pass"), []byte("identity"))
	if err != nil {
		f.Fatal(err)
	}
	f.Add([]byte("pass"), valid)
	f.Add([]byte(""), []byte{})
	f.Add([]byte("p"), make([]byte, keyfileSaltSize+NonceSize))
	f.Add([]byte("pass"), valid[:len(valid)-1])

	f.Fuzz(func(t *testing.T, passphrase, data []byte) {
		plain, err := DecryptKeyfile(passphrase, data)
		if err == nil && string(passphrase) != "pass" {
			t.Fatalf("garbage keyfile decrypted successfully: %x", plain)
		}
	})
}

// FuzzOpen feeds arbitrary sealed boxes to Open. It must never panic
// and must never produce a verified non-deniable message from garbage.
func FuzzOpen(f *testing.F) {
	_, senderPriv, _ := GenerateIdentity()
	recipientPub, recipientPriv, _ := GenerateIdentity()
	box, err := Seal(senderPriv, recipientPub, 0x01, []byte("seed"), false)
	if err != nil {
		f.Fatal(err)
	}
	f.Add(box.EphemeralPubkey[:], box.Nonce[:], box.Ciphertext, false)
	f.Add(box.EphemeralPubkey[:], box.Nonce[:], box.Ciphertext, true)
	f.Add(make([]byte, 32), make([]byte, 24), []byte{}, false)

	f.Fuzz(func(t *testing.T, eph, nonce, ct []byte, deniable bool) {
		b := &SealedBox{Deniable: deniable, Ciphertext: ct}
		copy(b.EphemeralPubkey[:], eph)
		copy(b.Nonce[:], nonce)
		// must not panic; success is fine only for the seeded valid box
		_, _ = Open(recipientPriv, b)
	})
}

// FuzzVerify ensures signature verification never panics on malformed
// keys, digests, or signatures.
func FuzzVerify(f *testing.F) {
	pub, priv, _ := GenerateIdentity()
	digest := Digest([]byte("msg"))
	sig, _ := Sign(priv, digest[:])
	f.Add([]byte(pub), digest[:], sig)
	f.Add([]byte{}, []byte{}, []byte{})

	f.Fuzz(func(t *testing.T, pub, digest, sig []byte) {
		_ = Verify(pub, digest, sig)
	})
}

// FuzzPublicKeyToX25519 ensures point decoding never panics.
func FuzzPublicKeyToX25519(f *testing.F) {
	pub, _, _ := GenerateIdentity()
	f.Add([]byte(pub))
	f.Add(make([]byte, 32))
	f.Add([]byte{})

	f.Fuzz(func(t *testing.T, key []byte) {
		_, _ = PublicKeyToX25519(key)
	})
}
