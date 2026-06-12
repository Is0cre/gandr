package identity

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/gandr-net/gandr/pkg/crypto"
	"github.com/gandr-net/gandr/pkg/proto"
)

func TestGenerate(t *testing.T) {
	id, err := Generate("byte_me")
	if err != nil {
		t.Fatal(err)
	}
	if id.DisplayName != "byte_me" || id.CreatedAt == 0 {
		t.Fatal("metadata not set")
	}
	// the identity must be able to sign protocol envelopes
	data, err := proto.EncodePayload(&proto.ChatPayload{Content: "x"})
	if err != nil {
		t.Fatal(err)
	}
	env, err := proto.NewEnvelope(id.PrivateKey, proto.MsgChat, proto.Broadcast, data)
	if err != nil {
		t.Fatal(err)
	}
	if env.Sender != id.Pubkey() {
		t.Fatal("envelope sender is not the identity pubkey")
	}
	if _, err := proto.Decode(env.Encode()); err != nil {
		t.Fatal(err)
	}
}

func TestSaveLoadRoundtrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "identity.key")
	pass := []byte("correct horse")
	id, err := Generate("mara")
	if err != nil {
		t.Fatal(err)
	}
	if err := id.Save(path, pass); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := Load(path, pass)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !bytes.Equal(got.PrivateKey, id.PrivateKey) {
		t.Fatal("private key mismatch")
	}
	if !bytes.Equal(got.PublicKey, id.PublicKey) {
		t.Fatal("public key mismatch")
	}
	if got.DisplayName != "mara" || got.CreatedAt != id.CreatedAt {
		t.Fatal("metadata mismatch")
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("keyfile mode = %o, want 600", info.Mode().Perm())
	}
}

func TestLoadWrongPassphrase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "identity.key")
	id, err := Generate("x")
	if err != nil {
		t.Fatal(err)
	}
	if err := id.Save(path, []byte("right")); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path, []byte("wrong")); err == nil {
		t.Fatal("keyfile decrypted with wrong passphrase")
	}
}

func TestLoadMissing(t *testing.T) {
	if _, err := Load(filepath.Join(t.TempDir(), "nope.key"), []byte("p")); !errors.Is(err, ErrNoKeyfile) {
		t.Fatalf("err = %v, want ErrNoKeyfile", err)
	}
}

func TestLoadCorrupt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "identity.key")
	id, err := Generate("x")
	if err != nil {
		t.Fatal(err)
	}
	if err := id.Save(path, []byte("p")); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	data[len(data)-1] ^= 0x01
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path, []byte("p")); err == nil {
		t.Fatal("corrupt keyfile loaded")
	}
	if _, err := Load(path, []byte("p")); errors.Is(err, ErrNoKeyfile) {
		t.Fatal("corrupt keyfile misreported as missing")
	}
}

func TestKeyfileIsEncrypted(t *testing.T) {
	// the keyfile on disk must not contain the seed or names in clear
	path := filepath.Join(t.TempDir(), "identity.key")
	id, err := Generate("super_unique_name_xyzzy")
	if err != nil {
		t.Fatal(err)
	}
	if err := id.Save(path, []byte("p")); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(data, id.PrivateKey.Seed()) {
		t.Fatal("keyfile contains plaintext seed")
	}
	if bytes.Contains(data, []byte("super_unique_name_xyzzy")) {
		t.Fatal("keyfile contains plaintext display name")
	}
}

func TestLoadOrGenerate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "identity.key")
	pass := []byte("p")
	id1, created, err := LoadOrGenerate(path, pass, "first")
	if err != nil {
		t.Fatal(err)
	}
	if !created {
		t.Fatal("first call should create")
	}
	id2, created, err := LoadOrGenerate(path, pass, "ignored")
	if err != nil {
		t.Fatal(err)
	}
	if created {
		t.Fatal("second call should load")
	}
	if !bytes.Equal(id1.PrivateKey, id2.PrivateKey) || id2.DisplayName != "first" {
		t.Fatal("loaded identity differs from generated")
	}
	// wrong passphrase must surface, not silently regenerate
	if _, _, err := LoadOrGenerate(path, []byte("wrong"), "x"); err == nil {
		t.Fatal("LoadOrGenerate overwrote identity on wrong passphrase")
	}
}

func TestKeyfileTooShortSurfaces(t *testing.T) {
	path := filepath.Join(t.TempDir(), "identity.key")
	if err := os.WriteFile(path, []byte("tiny"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path, []byte("p")); !errors.Is(err, crypto.ErrKeyfileTooShort) {
		t.Fatalf("err = %v, want ErrKeyfileTooShort", err)
	}
}
