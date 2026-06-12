package crypto

import (
	"bytes"
	"crypto/ed25519"
	"errors"
	"testing"
)

func TestSealOpenRoundtrip(t *testing.T) {
	_, senderPriv, _ := GenerateIdentity()
	senderPub := senderPriv.Public().(ed25519.PublicKey)
	recipientPub, recipientPriv, _ := GenerateIdentity()

	tests := []struct {
		name     string
		msgType  uint8
		content  []byte
		deniable bool
	}{
		{"signed chat", 0x01, []byte("meet at the usual place"), false},
		{"deniable chat", 0x01, []byte("you didn't hear this from me"), true},
		{"empty content signed", 0x02, []byte{}, false},
		{"empty content deniable", 0x02, []byte{}, true},
		{"exactly one block", 0x01, bytes.Repeat([]byte("a"), SealedBlockSize-sealedHeaderMax), false},
		{"just over one block", 0x01, bytes.Repeat([]byte("a"), SealedBlockSize-sealedHeaderMax+1), false},
		{"binary content", 0x10, bytes.Repeat([]byte{0x00, 0xFF}, 2000), false},
		{"content ending in zeros", 0x01, append([]byte("data"), make([]byte, 100)...), false},
		{"max content", 0x01, bytes.Repeat([]byte("m"), MaxSealedContent-sealedHeaderMax), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			box, err := Seal(senderPriv, recipientPub, tt.msgType, tt.content, tt.deniable)
			if err != nil {
				t.Fatalf("Seal: %v", err)
			}
			if (len(box.Ciphertext)-Overhead)%SealedBlockSize != 0 {
				t.Fatalf("plaintext length %d not padded to block boundary", len(box.Ciphertext)-Overhead)
			}
			got, err := Open(recipientPriv, box)
			if err != nil {
				t.Fatalf("Open: %v", err)
			}
			if !bytes.Equal(got.SenderPubkey[:], senderPub) {
				t.Error("sender pubkey mismatch")
			}
			if got.MessageType != tt.msgType {
				t.Errorf("message type = %#x, want %#x", got.MessageType, tt.msgType)
			}
			if !bytes.Equal(got.Content, tt.content) {
				t.Error("content mismatch")
			}
			if got.Deniable != tt.deniable {
				t.Error("deniable flag mismatch")
			}
		})
	}
}

func TestSealPaddingHidesLength(t *testing.T) {
	_, senderPriv, _ := GenerateIdentity()
	recipientPub, _, _ := GenerateIdentity()

	// Messages of very different lengths within one block must produce
	// identical ciphertext sizes.
	short, err := Seal(senderPriv, recipientPub, 0x01, []byte("hi"), false)
	if err != nil {
		t.Fatal(err)
	}
	long, err := Seal(senderPriv, recipientPub, 0x01, bytes.Repeat([]byte("x"), 800), false)
	if err != nil {
		t.Fatal(err)
	}
	if len(short.Ciphertext) != len(long.Ciphertext) {
		t.Fatalf("padding leak: %d vs %d", len(short.Ciphertext), len(long.Ciphertext))
	}
}

func TestSealEphemeralKeysFresh(t *testing.T) {
	_, senderPriv, _ := GenerateIdentity()
	recipientPub, _, _ := GenerateIdentity()
	b1, err := Seal(senderPriv, recipientPub, 0x01, []byte("a"), false)
	if err != nil {
		t.Fatal(err)
	}
	b2, err := Seal(senderPriv, recipientPub, 0x01, []byte("a"), false)
	if err != nil {
		t.Fatal(err)
	}
	if b1.EphemeralPubkey == b2.EphemeralPubkey {
		t.Fatal("ephemeral key reused across sealed messages")
	}
}

func TestOpenWrongRecipient(t *testing.T) {
	_, senderPriv, _ := GenerateIdentity()
	recipientPub, _, _ := GenerateIdentity()
	_, otherPriv, _ := GenerateIdentity()

	box, err := Seal(senderPriv, recipientPub, 0x01, []byte("for your eyes only"), false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Open(otherPriv, box); err == nil {
		t.Fatal("non-recipient opened a sealed message")
	}
}

func TestOpenTamperedCiphertext(t *testing.T) {
	_, senderPriv, _ := GenerateIdentity()
	recipientPub, recipientPriv, _ := GenerateIdentity()
	box, err := Seal(senderPriv, recipientPub, 0x01, []byte("payload"), false)
	if err != nil {
		t.Fatal(err)
	}
	box.Ciphertext[10] ^= 0x01
	if _, err := Open(recipientPriv, box); err == nil {
		t.Fatal("opened tampered ciphertext")
	}
}

func TestOpenDeniableFlagFlipped(t *testing.T) {
	// Flipping the (node-visible) Deniable flag must not let an attacker
	// bypass signature verification or corrupt parsing silently.
	_, senderPriv, _ := GenerateIdentity()
	recipientPub, recipientPriv, _ := GenerateIdentity()

	box, err := Seal(senderPriv, recipientPub, 0x01, []byte("signed message"), false)
	if err != nil {
		t.Fatal(err)
	}
	box.Deniable = true
	got, err := Open(recipientPriv, box)
	// Parsing with the wrong layout either fails outright or yields
	// garbage that must NOT carry a verified signature. The signed bytes
	// are misinterpreted, so content integrity is gone — but the message
	// must never appear as a validly signed message from the sender.
	if err == nil && !got.Deniable {
		t.Fatal("flag flip produced a non-deniable result")
	}

	box2, err := Seal(senderPriv, recipientPub, 0x01, []byte("deniable message"), true)
	if err != nil {
		t.Fatal(err)
	}
	box2.Deniable = false
	if _, err := Open(recipientPriv, box2); err == nil {
		t.Fatal("deniable message opened as signed without a valid signature")
	}
}

func TestOpenForwardedSealedFails(t *testing.T) {
	// The inner signature binds the recipient: re-targeting a sealed
	// message is impossible because the ciphertext is bound to the
	// recipient key via ECDH, but even at the signature level a replayed
	// inner content signed for recipient A must not verify for B.
	pubA, privA, _ := GenerateIdentity()
	pubB, _, _ := GenerateIdentity()
	_, senderPriv, _ := GenerateIdentity()

	digestA := sealedInnerDigest(pubA, 0x01, []byte("content"))
	digestB := sealedInnerDigest(pubB, 0x01, []byte("content"))
	if digestA == digestB {
		t.Fatal("inner digest does not bind recipient")
	}
	_ = privA
	_ = senderPriv
}

func TestSealRejectsOversizeContent(t *testing.T) {
	_, senderPriv, _ := GenerateIdentity()
	recipientPub, _, _ := GenerateIdentity()
	if _, err := Seal(senderPriv, recipientPub, 0x01, make([]byte, MaxSealedContent+1), false); err == nil {
		t.Fatal("Seal accepted oversize content")
	}
}

func TestSealRejectsBadKeys(t *testing.T) {
	_, senderPriv, _ := GenerateIdentity()
	recipientPub, _, _ := GenerateIdentity()
	if _, err := Seal(make([]byte, 3), recipientPub, 0x01, []byte("x"), false); err == nil {
		t.Fatal("Seal accepted malformed sender key")
	}
	if _, err := Seal(senderPriv, make([]byte, 3), 0x01, []byte("x"), false); err == nil {
		t.Fatal("Seal accepted malformed recipient key")
	}
}

func TestOpenNilBox(t *testing.T) {
	_, priv, _ := GenerateIdentity()
	if _, err := Open(priv, nil); err == nil {
		t.Fatal("Open accepted nil box")
	}
}

func TestOpenCorruptStructure(t *testing.T) {
	_, senderPriv, _ := GenerateIdentity()
	recipientPub, recipientPriv, _ := GenerateIdentity()

	t.Run("nonzero padding", func(t *testing.T) {
		// Build a box whose decrypted padding is nonzero by re-encrypting
		// a hand-crafted plaintext under the same derived key.
		box, err := Seal(senderPriv, recipientPub, 0x01, []byte("x"), true)
		if err != nil {
			t.Fatal(err)
		}
		recipientX, _ := PrivateKeyToX25519(recipientPriv)
		key, err := DeriveSharedKey(recipientX, box.EphemeralPubkey, sealedKeyInfo)
		if err != nil {
			t.Fatal(err)
		}
		plain, err := Decrypt(key, box.Nonce, box.Ciphertext, nil)
		if err != nil {
			t.Fatal(err)
		}
		plain[len(plain)-1] = 0xAA // corrupt padding
		nonce, ct, err := Encrypt(key, plain, nil)
		if err != nil {
			t.Fatal(err)
		}
		box.Nonce, box.Ciphertext = nonce, ct
		if _, err := Open(recipientPriv, box); !errors.Is(err, ErrSealedCorrupt) {
			t.Fatalf("err = %v, want ErrSealedCorrupt", err)
		}
	})

	t.Run("content length exceeds plaintext", func(t *testing.T) {
		box, err := Seal(senderPriv, recipientPub, 0x01, []byte("x"), true)
		if err != nil {
			t.Fatal(err)
		}
		recipientX, _ := PrivateKeyToX25519(recipientPriv)
		key, err := DeriveSharedKey(recipientX, box.EphemeralPubkey, sealedKeyInfo)
		if err != nil {
			t.Fatal(err)
		}
		plain, err := Decrypt(key, box.Nonce, box.Ciphertext, nil)
		if err != nil {
			t.Fatal(err)
		}
		// content_len lives right after sender pubkey + msg type in
		// deniable mode; blow it up
		plain[KeySize+1] = 0xFF
		plain[KeySize+2] = 0xFF
		plain[KeySize+3] = 0xFF
		plain[KeySize+4] = 0xFF
		nonce, ct, err := Encrypt(key, plain, nil)
		if err != nil {
			t.Fatal(err)
		}
		box.Nonce, box.Ciphertext = nonce, ct
		if _, err := Open(recipientPriv, box); !errors.Is(err, ErrSealedCorrupt) {
			t.Fatalf("err = %v, want ErrSealedCorrupt", err)
		}
	})
}

func TestPaddedLen(t *testing.T) {
	tests := []struct{ in, want int }{
		{0, SealedBlockSize},
		{1, SealedBlockSize},
		{SealedBlockSize - 1, SealedBlockSize},
		{SealedBlockSize, SealedBlockSize},
		{SealedBlockSize + 1, 2 * SealedBlockSize},
		{3*SealedBlockSize - 7, 3 * SealedBlockSize},
	}
	for _, tt := range tests {
		if got := paddedLen(tt.in); got != tt.want {
			t.Errorf("paddedLen(%d) = %d, want %d", tt.in, got, tt.want)
		}
	}
}
