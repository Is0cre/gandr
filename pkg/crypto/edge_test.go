package crypto

import (
	"crypto/ed25519"
	"errors"
	"testing"
)

// Low-order / degenerate point handling. X25519 must refuse to produce
// an all-zero shared secret, and every path through this package that
// performs ECDH must surface that as an error, never as a usable key.

func TestDeriveSharedKeyLowOrderPoint(t *testing.T) {
	_, priv, err := GenerateX25519()
	if err != nil {
		t.Fatal(err)
	}
	var zero [KeySize]byte
	if _, err := DeriveSharedKey(priv, zero, "test"); err == nil {
		t.Fatal("DeriveSharedKey accepted a low-order point")
	}
}

func TestSealToDegenerateRecipient(t *testing.T) {
	// y=1 encodes the Edwards identity element, which converts to the
	// all-zero Montgomery point. Sealing to it must fail, not silently
	// derive a key from a zero shared secret.
	_, senderPriv, _ := GenerateIdentity()
	identityPoint := make([]byte, ed25519.PublicKeySize)
	identityPoint[0] = 1
	if _, err := Seal(senderPriv, identityPoint, 0x01, []byte("x"), false); err == nil {
		t.Fatal("Seal succeeded against degenerate recipient key")
	}
}

func TestOpenDegenerateEphemeral(t *testing.T) {
	_, recipientPriv, _ := GenerateIdentity()
	box := &SealedBox{Ciphertext: make([]byte, SealedBlockSize+Overhead)}
	// all-zero ephemeral pubkey is a low-order Montgomery point
	if _, err := Open(recipientPriv, box); err == nil {
		t.Fatal("Open accepted a low-order ephemeral key")
	}
}

func TestOpenMalformedRecipientKey(t *testing.T) {
	box := &SealedBox{Ciphertext: make([]byte, SealedBlockSize+Overhead)}
	if _, err := Open(make([]byte, 7), box); err == nil {
		t.Fatal("Open accepted malformed recipient private key")
	}
}

// forgeBox encrypts an arbitrary plaintext under the key a recipient
// would derive, simulating a malicious or non-conforming sender.
func forgeBox(t *testing.T, recipientPriv ed25519.PrivateKey, plain []byte) *SealedBox {
	t.Helper()
	ephPub, ephPriv, err := GenerateX25519()
	if err != nil {
		t.Fatal(err)
	}
	recipientPub := recipientPriv.Public().(ed25519.PublicKey)
	recipientX, err := PublicKeyToX25519(recipientPub)
	if err != nil {
		t.Fatal(err)
	}
	key, err := DeriveSharedKey(ephPriv, recipientX, sealedKeyInfo)
	if err != nil {
		t.Fatal(err)
	}
	nonce, ct, err := Encrypt(key, plain, nil)
	if err != nil {
		t.Fatal(err)
	}
	box := &SealedBox{Nonce: nonce, Deniable: true, Ciphertext: ct}
	copy(box.EphemeralPubkey[:], ephPub[:])
	return box
}

func TestOpenUnpaddedPlaintext(t *testing.T) {
	_, recipientPriv, _ := GenerateIdentity()
	box := forgeBox(t, recipientPriv, []byte("not a block multiple"))
	if _, err := Open(recipientPriv, box); !errors.Is(err, ErrSealedCorrupt) {
		t.Fatalf("err = %v, want ErrSealedCorrupt", err)
	}
}

func TestOpenEmptyPlaintext(t *testing.T) {
	_, recipientPriv, _ := GenerateIdentity()
	box := forgeBox(t, recipientPriv, nil)
	if _, err := Open(recipientPriv, box); !errors.Is(err, ErrSealedCorrupt) {
		t.Fatalf("err = %v, want ErrSealedCorrupt", err)
	}
}
