package store

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gandr-net/gandr/pkg/crypto"
	"github.com/gandr-net/gandr/pkg/proto"
)

func testEnvelope(t *testing.T, msgType uint8, content string) *proto.Envelope {
	t.Helper()
	_, priv, err := crypto.GenerateIdentity()
	if err != nil {
		t.Fatal(err)
	}
	var payload proto.Payload
	switch msgType {
	case proto.MsgStatus:
		payload = &proto.StatusPayload{Status: content}
	default:
		payload = &proto.ChatPayload{Content: content}
	}
	data, err := proto.EncodePayload(payload)
	if err != nil {
		t.Fatal(err)
	}
	env, err := proto.NewEnvelope(priv, msgType, proto.Broadcast, data)
	if err != nil {
		t.Fatal(err)
	}
	return env
}

func TestPutGetRoundtrip(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	env := testEnvelope(t, proto.MsgChat, "stored words")
	hash, existed, err := s.Put(env)
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	if existed {
		t.Fatal("fresh object reported as existing")
	}
	if hash != env.ContentID() {
		t.Fatal("hash is not the content id")
	}
	got, err := s.Get(hash)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.ContentID() != env.ContentID() {
		t.Fatal("retrieved envelope differs")
	}
	if !s.Has(hash) {
		t.Fatal("Has = false for stored object")
	}
}

func TestDeduplication(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	env := testEnvelope(t, proto.MsgChat, "once only")
	if _, existed, err := s.Put(env); err != nil || existed {
		t.Fatalf("first put: existed=%v err=%v", existed, err)
	}
	if _, existed, err := s.Put(env); err != nil || !existed {
		t.Fatalf("second put: existed=%v err=%v", existed, err)
	}
	n, err := s.Count()
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("count = %d, want 1 (dedup)", n)
	}
}

func TestGetMissing(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	var hash [32]byte
	hash[0] = 0xEE
	if _, err := s.Get(hash); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
	if s.Has(hash) {
		t.Fatal("Has = true for missing object")
	}
}

func TestDelete(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	env := testEnvelope(t, proto.MsgChat, "to be deleted")
	hash, _, err := s.Put(env)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(hash); err != nil {
		t.Fatal(err)
	}
	if s.Has(hash) {
		t.Fatal("object survives delete")
	}
	if err := s.Delete(hash); err != nil {
		t.Fatal("deleting missing object should not error")
	}
}

func TestCorruptObjectRemovedOnGet(t *testing.T) {
	root := t.TempDir()
	s, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	env := testEnvelope(t, proto.MsgChat, "soon corrupt")
	hash, _, err := s.Put(env)
	if err != nil {
		t.Fatal(err)
	}
	// corrupt the object on disk
	dir, file := s.path(hash)
	_ = dir
	if err := os.WriteFile(file, []byte("not an envelope"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(hash); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound for corrupt object", err)
	}
	if s.Has(hash) {
		t.Fatal("corrupt object not removed")
	}
}

func TestWrongContentRemovedOnGet(t *testing.T) {
	// A valid envelope stored under the wrong hash must not be served.
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	env := testEnvelope(t, proto.MsgChat, "real")
	other := testEnvelope(t, proto.MsgChat, "mislabeled")
	hash, _, err := s.Put(env)
	if err != nil {
		t.Fatal(err)
	}
	_, file := s.path(hash)
	if err := os.WriteFile(file, other.Encode(), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(hash); !errors.Is(err, ErrNotFound) {
		t.Fatalf("served object whose content does not match its hash: %v", err)
	}
}

func TestPersistenceAcrossReopen(t *testing.T) {
	root := t.TempDir()
	s1, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	env := testEnvelope(t, proto.MsgChat, "durable")
	hash, _, err := s1.Put(env)
	if err != nil {
		t.Fatal(err)
	}
	s2, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s2.Get(hash); err != nil {
		t.Fatalf("object lost across reopen: %v", err)
	}
}

func TestPrune(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	fresh := testEnvelope(t, proto.MsgChat, "fresh")
	old := testEnvelope(t, proto.MsgChat, "old")
	staleStatus := testEnvelope(t, proto.MsgStatus, "stale status")

	if _, _, err := s.Put(fresh); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Put(old); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Put(staleStatus); err != nil {
		t.Fatal(err)
	}

	// evaluate "now" 25 hours in the future: status (24h cap) expires,
	// chat (7 day limit) survives; then 8 days out everything expires
	now := time.Unix(0, fresh.Timestamp)
	removed, err := s.Prune(7*24*time.Hour, now.Add(25*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if removed != 1 {
		t.Fatalf("removed = %d, want 1 (status only)", removed)
	}
	if !s.Has(fresh.ContentID()) || !s.Has(old.ContentID()) {
		t.Fatal("prune removed unexpired content")
	}
	if s.Has(staleStatus.ContentID()) {
		t.Fatal("stale status survived 24h cap")
	}

	removed, err = s.Prune(7*24*time.Hour, now.Add(8*24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if removed != 2 {
		t.Fatalf("removed = %d, want 2", removed)
	}
	n, err := s.Count()
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("count after full prune = %d", n)
	}
}

func TestPruneRemovesUndecodable(t *testing.T) {
	root := t.TempDir()
	s, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	junk := filepath.Join(root, "objects", "zz")
	if err := os.MkdirAll(junk, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(junk, "junkfile"), []byte("junk"), 0o600); err != nil {
		t.Fatal(err)
	}
	removed, err := s.Prune(time.Hour, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if removed != 1 {
		t.Fatalf("removed = %d, want 1", removed)
	}
}

func TestParseHash(t *testing.T) {
	valid := strings.Repeat("ab", 32)
	h, err := ParseHash(valid)
	if err != nil {
		t.Fatal(err)
	}
	if h[0] != 0xAB {
		t.Fatal("parse mismatch")
	}
	for _, bad := range []string{"", "short", strings.Repeat("z", 64), valid + "ff"} {
		if _, err := ParseHash(bad); !errors.Is(err, ErrBadHash) {
			t.Errorf("ParseHash(%q) err = %v, want ErrBadHash", bad, err)
		}
	}
}

func TestObjectFilePermissions(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	env := testEnvelope(t, proto.MsgChat, "private file")
	hash, _, err := s.Put(env)
	if err != nil {
		t.Fatal(err)
	}
	_, file := s.path(hash)
	info, err := os.Stat(file)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("object mode = %o, want 600", info.Mode().Perm())
	}
}
