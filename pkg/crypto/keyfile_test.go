package crypto

import (
	"bytes"
	"errors"
	"testing"
)

func TestKeyfileRoundtrip(t *testing.T) {
	plaintext := []byte("serialized identity bytes go here")
	passphrase := []byte("correct horse battery staple")

	data, err := EncryptKeyfile(passphrase, plaintext)
	if err != nil {
		t.Fatalf("EncryptKeyfile: %v", err)
	}
	if len(data) != keyfileSaltSize+NonceSize+len(plaintext)+Overhead {
		t.Fatalf("keyfile length = %d, unexpected framing", len(data))
	}
	got, err := DecryptKeyfile(passphrase, data)
	if err != nil {
		t.Fatalf("DecryptKeyfile: %v", err)
	}
	if !bytes.Equal(got, plaintext) {
		t.Fatal("keyfile roundtrip mismatch")
	}
}

func TestKeyfileWrongPassphrase(t *testing.T) {
	data, err := EncryptKeyfile([]byte("right"), []byte("identity"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecryptKeyfile([]byte("wrong"), data); err == nil {
		t.Fatal("keyfile decrypted with wrong passphrase")
	}
	if _, err := DecryptKeyfile([]byte(""), data); err == nil {
		t.Fatal("keyfile decrypted with empty passphrase")
	}
}

func TestKeyfileCorrupt(t *testing.T) {
	pass := []byte("pass")
	data, err := EncryptKeyfile(pass, []byte("identity"))
	if err != nil {
		t.Fatal(err)
	}
	for _, idx := range []int{0, keyfileSaltSize, keyfileSaltSize + NonceSize, len(data) - 1} {
		bad := append([]byte{}, data...)
		bad[idx] ^= 0x01
		if _, err := DecryptKeyfile(pass, bad); err == nil {
			t.Errorf("keyfile decrypted with corrupted byte at %d", idx)
		}
	}
}

func TestKeyfileTooShort(t *testing.T) {
	for _, n := range []int{0, 1, keyfileSaltSize, keyfileSaltSize + NonceSize, keyfileSaltSize + NonceSize + Overhead - 1} {
		if _, err := DecryptKeyfile([]byte("p"), make([]byte, n)); !errors.Is(err, ErrKeyfileTooShort) {
			t.Errorf("len %d: err = %v, want ErrKeyfileTooShort", n, err)
		}
	}
}

func TestKeyfileSaltUnique(t *testing.T) {
	a, err := EncryptKeyfile([]byte("p"), []byte("id"))
	if err != nil {
		t.Fatal(err)
	}
	b, err := EncryptKeyfile([]byte("p"), []byte("id"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(a[:keyfileSaltSize], b[:keyfileSaltSize]) {
		t.Fatal("salt reused across keyfile encryptions")
	}
	if bytes.Equal(a, b) {
		t.Fatal("identical keyfiles for identical input — no randomness")
	}
}

func TestKeyfileEmptyPlaintext(t *testing.T) {
	data, err := EncryptKeyfile([]byte("p"), nil)
	if err != nil {
		t.Fatal(err)
	}
	got, err := DecryptKeyfile([]byte("p"), data)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatal("expected empty plaintext")
	}
}
