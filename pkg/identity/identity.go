// Package identity manages the permanent Ed25519 identity keypair: the
// only identity that exists in Gandr. There are no accounts, no
// registration, no recovery. You are your key.
//
// The keyfile on disk is encrypted with XChaCha20-Poly1305 under an
// Argon2id-derived key; see pkg/crypto. The passphrase is requested
// once per process lifetime.
package identity

import (
	"crypto/ed25519"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/vmihailenco/msgpack/v5"

	"github.com/gandr-net/gandr/pkg/crypto"
)

// ErrNoKeyfile is returned by Load when the keyfile does not exist.
var ErrNoKeyfile = errors.New("identity: keyfile does not exist")

// Identity is a permanent Ed25519 identity.
type Identity struct {
	PrivateKey  ed25519.PrivateKey
	PublicKey   ed25519.PublicKey
	DisplayName string // announced to the network, not unique, not verified
	CreatedAt   int64
}

// keyfileData is the serialized (pre-encryption) keyfile content. Only
// the seed is stored; the keypair is rederived on load.
type keyfileData struct {
	Seed        []byte `msgpack:"s"`
	DisplayName string `msgpack:"d"`
	CreatedAt   int64  `msgpack:"c"`
}

// Generate creates a fresh identity.
func Generate(displayName string) (*Identity, error) {
	pub, priv, err := crypto.GenerateIdentity()
	if err != nil {
		return nil, err
	}
	return &Identity{
		PrivateKey:  priv,
		PublicKey:   pub,
		DisplayName: displayName,
		CreatedAt:   time.Now().Unix(),
	}, nil
}

// Pubkey returns the identity public key as a fixed array, the form
// used in envelopes.
func (id *Identity) Pubkey() [32]byte {
	var out [32]byte
	copy(out[:], id.PublicKey)
	return out
}

// Save writes the identity to path, encrypted under passphrase. The
// file is created with mode 0600 and written atomically.
func (id *Identity) Save(path string, passphrase []byte) error {
	plain, err := msgpack.Marshal(&keyfileData{
		Seed:        id.PrivateKey.Seed(),
		DisplayName: id.DisplayName,
		CreatedAt:   id.CreatedAt,
	})
	if err != nil {
		return fmt.Errorf("identity: serializing keyfile: %w", err)
	}
	enc, err := crypto.EncryptKeyfile(passphrase, plain)
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("identity: creating keyfile directory: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".keyfile-*")
	if err != nil {
		return fmt.Errorf("identity: creating temp keyfile: %w", err)
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return fmt.Errorf("identity: setting keyfile permissions: %w", err)
	}
	if _, err := tmp.Write(enc); err != nil {
		tmp.Close()
		return fmt.Errorf("identity: writing keyfile: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("identity: closing keyfile: %w", err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("identity: committing keyfile: %w", err)
	}
	return nil
}

// Load reads and decrypts an identity keyfile. A wrong passphrase
// fails cleanly via authenticated decryption.
func Load(path string, passphrase []byte) (*Identity, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, ErrNoKeyfile
	}
	if err != nil {
		return nil, fmt.Errorf("identity: reading keyfile: %w", err)
	}
	plain, err := crypto.DecryptKeyfile(passphrase, data)
	if err != nil {
		return nil, err
	}
	var kf keyfileData
	if err := msgpack.Unmarshal(plain, &kf); err != nil {
		return nil, fmt.Errorf("identity: deserializing keyfile: %w", err)
	}
	if len(kf.Seed) != ed25519.SeedSize {
		return nil, fmt.Errorf("identity: keyfile seed has invalid length %d", len(kf.Seed))
	}
	priv := ed25519.NewKeyFromSeed(kf.Seed)
	return &Identity{
		PrivateKey:  priv,
		PublicKey:   priv.Public().(ed25519.PublicKey),
		DisplayName: kf.DisplayName,
		CreatedAt:   kf.CreatedAt,
	}, nil
}

// LoadOrGenerate loads the identity at path, or generates and saves a
// fresh one if no keyfile exists. The returned bool reports whether a
// new identity was created.
func LoadOrGenerate(path string, passphrase []byte, displayName string) (*Identity, bool, error) {
	id, err := Load(path, passphrase)
	if err == nil {
		return id, false, nil
	}
	if !errors.Is(err, ErrNoKeyfile) {
		return nil, false, err
	}
	id, err = Generate(displayName)
	if err != nil {
		return nil, false, err
	}
	if err := id.Save(path, passphrase); err != nil {
		return nil, false, err
	}
	return id, true, nil
}
