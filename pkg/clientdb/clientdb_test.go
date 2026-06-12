package clientdb

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/gandr-net/gandr/pkg/crypto"
)

func testDB(t *testing.T) (*DB, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "client.db")
	_, priv, err := crypto.GenerateIdentity()
	if err != nil {
		t.Fatal(err)
	}
	db, err := Open(path, priv)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db, path
}

func TestNicknames(t *testing.T) {
	db, _ := testDB(t)
	var pk [32]byte
	pk[0] = 0xAA

	if _, err := db.GetNickname(pk); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
	n := Nickname{Pubkey: pk, Name: "mara", Note: "sister, älvdalen", TrustHint: 2}
	if err := db.SetNickname(n); err != nil {
		t.Fatal(err)
	}
	got, err := db.GetNickname(pk)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "mara" || got.Note != "sister, älvdalen" || got.TrustHint != 2 {
		t.Fatalf("nickname mismatch: %+v", got)
	}
	// update keeps the row unique
	n.Name = "mara_prime"
	if err := db.SetNickname(n); err != nil {
		t.Fatal(err)
	}
	all, err := db.ListNicknames()
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 || all[0].Name != "mara_prime" {
		t.Fatalf("list mismatch: %+v", all)
	}
	// fuzzy search across name and note
	hits, err := db.SearchNicknames("ÄLVDALEN")
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 {
		t.Fatal("note search failed")
	}
	if hits, _ := db.SearchNicknames("nope"); len(hits) != 0 {
		t.Fatal("phantom search hit")
	}
	if err := db.DeleteNickname(pk); err != nil {
		t.Fatal(err)
	}
	if _, err := db.GetNickname(pk); !errors.Is(err, ErrNotFound) {
		t.Fatal("nickname survives delete")
	}
}

func TestNicknamesEncryptedAtRest(t *testing.T) {
	db, path := testDB(t)
	var pk [32]byte
	if err := db.SetNickname(Nickname{Pubkey: pk, Name: "supersecretname", Note: "supersecretnote"}); err != nil {
		t.Fatal(err)
	}
	db.Close()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// check WAL too in case the page is there
	if wal, err := os.ReadFile(path + "-wal"); err == nil {
		raw = append(raw, wal...)
	}
	if bytes.Contains(raw, []byte("supersecretname")) || bytes.Contains(raw, []byte("supersecretnote")) {
		t.Fatal("nickname stored in plaintext")
	}
}

func TestWrongIdentityCannotRead(t *testing.T) {
	path := filepath.Join(t.TempDir(), "client.db")
	_, priv1, _ := crypto.GenerateIdentity()
	_, priv2, _ := crypto.GenerateIdentity()

	db1, err := Open(path, priv1)
	if err != nil {
		t.Fatal(err)
	}
	var pk [32]byte
	if err := db1.SetNickname(Nickname{Pubkey: pk, Name: "private"}); err != nil {
		t.Fatal(err)
	}
	db1.Close()

	db2, err := Open(path, priv2)
	if err != nil {
		t.Fatal(err)
	}
	defer db2.Close()
	if _, err := db2.GetNickname(pk); err == nil {
		t.Fatal("different identity decrypted the nickname")
	}
}

func TestBlocklist(t *testing.T) {
	db, _ := testDB(t)
	var pk [32]byte
	pk[0] = 0xBB
	blocked, err := db.IsBlocked(pk)
	if err != nil || blocked {
		t.Fatal("fresh pubkey blocked")
	}
	if err := db.Block(pk, "spammer"); err != nil {
		t.Fatal(err)
	}
	if blocked, _ := db.IsBlocked(pk); !blocked {
		t.Fatal("block not recorded")
	}
	if err := db.Unblock(pk); err != nil {
		t.Fatal(err)
	}
	if blocked, _ := db.IsBlocked(pk); blocked {
		t.Fatal("unblock failed")
	}
}

func TestChannels(t *testing.T) {
	db, _ := testDB(t)
	var ch [32]byte
	ch[0] = 0xCC
	if err := db.JoinChannel(ch, "general"); err != nil {
		t.Fatal(err)
	}
	chans, err := db.ListChannels()
	if err != nil {
		t.Fatal(err)
	}
	if len(chans) != 1 || chans[0].Name != "general" || chans[0].ID != ch {
		t.Fatalf("channels mismatch: %+v", chans)
	}
	if err := db.LeaveChannel(ch); err != nil {
		t.Fatal(err)
	}
	if chans, _ := db.ListChannels(); len(chans) != 0 {
		t.Fatal("leave failed")
	}
}

func TestProfileCache(t *testing.T) {
	db, _ := testDB(t)
	var pk [32]byte
	pk[0] = 0xDD
	if _, err := db.GetProfile(pk); !errors.Is(err, ErrNotFound) {
		t.Fatal("expected ErrNotFound")
	}
	if err := db.CacheProfile(pk, []byte("serialized profile"), "abcd"); err != nil {
		t.Fatal(err)
	}
	data, err := db.GetProfile(pk)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "serialized profile" {
		t.Fatal("profile cache mismatch")
	}
	// update replaces
	if err := db.CacheProfile(pk, []byte("newer"), "ef01"); err != nil {
		t.Fatal(err)
	}
	if data, _ := db.GetProfile(pk); string(data) != "newer" {
		t.Fatal("profile cache not updated")
	}
}

func TestSealedInbox(t *testing.T) {
	db, _ := testDB(t)
	var hash, sender [32]byte
	hash[0], sender[0] = 0x11, 0x22

	if err := db.PutSealed(SealedMessage{MsgHash: hash, Data: []byte("secret words"), Sender: sender}); err != nil {
		t.Fatal(err)
	}
	// duplicate is ignored
	if err := db.PutSealed(SealedMessage{MsgHash: hash, Data: []byte("secret words"), Sender: sender}); err != nil {
		t.Fatal(err)
	}
	msgs, err := db.ListSealed()
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 1 {
		t.Fatalf("inbox length = %d", len(msgs))
	}
	if string(msgs[0].Data) != "secret words" || msgs[0].Sender != sender || msgs[0].Read {
		t.Fatalf("sealed message mismatch: %+v", msgs[0])
	}
	n, err := db.UnreadSealedCount()
	if err != nil || n != 1 {
		t.Fatalf("unread = %d err = %v", n, err)
	}
	if err := db.MarkSealedRead(hash); err != nil {
		t.Fatal(err)
	}
	if n, _ := db.UnreadSealedCount(); n != 0 {
		t.Fatal("mark read failed")
	}
}

func TestSealedEncryptedAtRest(t *testing.T) {
	db, path := testDB(t)
	var hash, sender [32]byte
	if err := db.PutSealed(SealedMessage{MsgHash: hash, Data: []byte("ultraconfidential"), Sender: sender}); err != nil {
		t.Fatal(err)
	}
	db.Close()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if wal, err := os.ReadFile(path + "-wal"); err == nil {
		raw = append(raw, wal...)
	}
	if bytes.Contains(raw, []byte("ultraconfidential")) {
		t.Fatal("sealed message stored in plaintext")
	}
}
