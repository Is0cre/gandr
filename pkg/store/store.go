// Package store implements gandrd's content-addressed object storage:
// flat files keyed by content hash, no database, no user tables, no
// indexes that map content to people. The node is deliberately ignorant
// of its users; seizure of the object store yields signed envelopes
// already considered public by the protocol, and opaque blobs for
// sealed messages.
//
// Layout:
//
//	<root>/objects/<first 2 hex chars>/<full 64 hex chars>
//
// Objects are whole encoded envelopes. The content hash is the
// envelope's ContentID. Writes are atomic (temp file + rename), so a
// crashed node never leaves a torn object behind.
package store

import (
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/gandr-net/gandr/pkg/proto"
)

// Store errors.
var (
	ErrNotFound = errors.New("store: object not found")
	ErrBadHash  = errors.New("store: malformed object hash")
)

// Store is a content-addressed object store rooted at one directory.
type Store struct {
	objects string
}

// Open initializes a store under root, creating directories as needed.
func Open(root string) (*Store, error) {
	objects := filepath.Join(root, "objects")
	if err := os.MkdirAll(objects, 0o700); err != nil {
		return nil, fmt.Errorf("store: creating object directory: %w", err)
	}
	return &Store{objects: objects}, nil
}

// path maps a hash to its on-disk location.
func (s *Store) path(hash [32]byte) (dir, file string) {
	h := hex.EncodeToString(hash[:])
	dir = filepath.Join(s.objects, h[:2])
	return dir, filepath.Join(dir, h)
}

// Put stores an encoded envelope under its content id. It returns the
// hash and whether the object already existed — deduplication is
// inherent: the same message stored twice is one object.
func (s *Store) Put(env *proto.Envelope) (hash [32]byte, existed bool, err error) {
	hash = env.ContentID()
	dir, file := s.path(hash)
	if _, err := os.Stat(file); err == nil {
		return hash, true, nil
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return hash, false, fmt.Errorf("store: creating shard dir: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return hash, false, fmt.Errorf("store: creating temp file: %w", err)
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return hash, false, fmt.Errorf("store: setting permissions: %w", err)
	}
	if _, err := tmp.Write(env.Encode()); err != nil {
		tmp.Close()
		return hash, false, fmt.Errorf("store: writing object: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return hash, false, fmt.Errorf("store: closing object: %w", err)
	}
	if err := os.Rename(tmp.Name(), file); err != nil {
		return hash, false, fmt.Errorf("store: committing object: %w", err)
	}
	return hash, false, nil
}

// Get retrieves and decodes the envelope stored under hash. A stored
// object that no longer decodes (disk corruption, tampering) is
// reported as not found and removed.
func (s *Store) Get(hash [32]byte) (*proto.Envelope, error) {
	_, file := s.path(hash)
	data, err := os.ReadFile(file)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("store: reading object: %w", err)
	}
	env, err := proto.Decode(data)
	if err != nil || env.ContentID() != hash {
		os.Remove(file)
		return nil, ErrNotFound
	}
	return env, nil
}

// Has reports whether an object exists.
func (s *Store) Has(hash [32]byte) bool {
	_, file := s.path(hash)
	_, err := os.Stat(file)
	return err == nil
}

// Delete removes an object. Deleting a missing object is not an error.
func (s *Store) Delete(hash [32]byte) error {
	_, file := s.path(hash)
	if err := os.Remove(file); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("store: deleting object: %w", err)
	}
	return nil
}

// ParseHash converts a 64-char hex string to a hash.
func ParseHash(s string) ([32]byte, error) {
	var hash [32]byte
	if len(s) != 64 {
		return hash, ErrBadHash
	}
	b, err := hex.DecodeString(s)
	if err != nil {
		return hash, ErrBadHash
	}
	copy(hash[:], b)
	return hash, nil
}

// statusMaxAge is the fixed retention for ephemeral status messages.
const statusMaxAge = 24 * time.Hour

// Prune removes objects older than maxAge (by envelope timestamp).
// Status messages are always pruned after 24 hours regardless of
// maxAge, per protocol. Undecodable objects are removed. Returns the
// number of objects removed.
func (s *Store) Prune(maxAge time.Duration, now time.Time) (int, error) {
	removed := 0
	err := filepath.WalkDir(s.objects, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil // raced with delete; skip
		}
		env, err := proto.Decode(data)
		if err != nil {
			os.Remove(path)
			removed++
			return nil
		}
		age := now.Sub(time.Unix(0, env.Timestamp))
		limit := maxAge
		if env.Type == proto.MsgStatus && statusMaxAge < limit {
			limit = statusMaxAge
		}
		if age > limit {
			if rmErr := os.Remove(path); rmErr == nil {
				removed++
			}
		}
		return nil
	})
	if err != nil {
		return removed, fmt.Errorf("store: prune walk: %w", err)
	}
	return removed, nil
}

// Count returns the number of stored objects. Used by tests and the
// operator status display, never logged.
func (s *Store) Count() (int, error) {
	n := 0
	err := filepath.WalkDir(s.objects, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			n++
		}
		return nil
	})
	return n, err
}
