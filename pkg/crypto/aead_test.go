package crypto

import (
	"bytes"
	"testing"
)

func testKey(t *testing.T) [KeySize]byte {
	t.Helper()
	var k [KeySize]byte
	copy(k[:], RandomBytes(KeySize))
	return k
}

func TestEncryptDecryptRoundtrip(t *testing.T) {
	key := testKey(t)
	tests := []struct {
		name      string
		plaintext []byte
		aad       []byte
	}{
		{"basic", []byte("the network is the community"), nil},
		{"empty plaintext", []byte{}, nil},
		{"with aad", []byte("payload"), []byte("associated")},
		{"binary", bytes.Repeat([]byte{0x00, 0xFF}, 500), []byte{0x01}},
		{"large", bytes.Repeat([]byte("g"), 1<<16), nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nonce, ct, err := Encrypt(key, tt.plaintext, tt.aad)
			if err != nil {
				t.Fatalf("Encrypt: %v", err)
			}
			if len(ct) != len(tt.plaintext)+Overhead {
				t.Fatalf("ciphertext length %d, want %d", len(ct), len(tt.plaintext)+Overhead)
			}
			got, err := Decrypt(key, nonce, ct, tt.aad)
			if err != nil {
				t.Fatalf("Decrypt: %v", err)
			}
			if !bytes.Equal(got, tt.plaintext) {
				t.Fatal("roundtrip mismatch")
			}
		})
	}
}

func TestDecryptFailures(t *testing.T) {
	key := testKey(t)
	nonce, ct, err := Encrypt(key, []byte("secret"), []byte("aad"))
	if err != nil {
		t.Fatal(err)
	}

	t.Run("wrong key", func(t *testing.T) {
		if _, err := Decrypt(testKey(t), nonce, ct, []byte("aad")); err == nil {
			t.Fatal("decryption succeeded with wrong key")
		}
	})
	t.Run("wrong nonce", func(t *testing.T) {
		bad := nonce
		bad[0] ^= 1
		if _, err := Decrypt(key, bad, ct, []byte("aad")); err == nil {
			t.Fatal("decryption succeeded with wrong nonce")
		}
	})
	t.Run("wrong aad", func(t *testing.T) {
		if _, err := Decrypt(key, nonce, ct, []byte("other")); err == nil {
			t.Fatal("decryption succeeded with wrong aad")
		}
	})
	t.Run("flipped ciphertext bit", func(t *testing.T) {
		bad := append([]byte{}, ct...)
		bad[0] ^= 1
		if _, err := Decrypt(key, nonce, bad, []byte("aad")); err == nil {
			t.Fatal("decryption succeeded with corrupted ciphertext")
		}
	})
	t.Run("truncated ciphertext", func(t *testing.T) {
		if _, err := Decrypt(key, nonce, ct[:4], []byte("aad")); err == nil {
			t.Fatal("decryption succeeded with truncated ciphertext")
		}
	})
	t.Run("empty ciphertext", func(t *testing.T) {
		if _, err := Decrypt(key, nonce, nil, []byte("aad")); err == nil {
			t.Fatal("decryption succeeded with empty ciphertext")
		}
	})
}

func TestEncryptFreshNonces(t *testing.T) {
	key := testKey(t)
	n1, _, err := Encrypt(key, []byte("x"), nil)
	if err != nil {
		t.Fatal(err)
	}
	n2, _, err := Encrypt(key, []byte("x"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if n1 == n2 {
		t.Fatal("nonce reused across encryptions")
	}
}
