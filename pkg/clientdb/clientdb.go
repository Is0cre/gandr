// Package clientdb is the gandr client's local store: nicknames,
// blocklist, channels, profile cache, content cache, and the sealed
// inbox, in a SQLite database whose sensitive values are encrypted at
// the application layer.
//
// Encryption design: the storage key is derived from the identity seed
// with HKDF (domain "gandr-clientdb-v1") and every sensitive value is
// encrypted with XChaCha20-Poly1305, using the table name and row key
// as associated data so a ciphertext cannot be transplanted between
// rows. Lookup keys (pubkeys, hashes) stay plaintext — they are public
// material on the network anyway. SQLCipher was rejected: it requires a
// CGO fork outside the approved dependency set.
//
// Nicknames and the blocklist NEVER leave this database. They are not
// part of any protocol message and no code path serializes them for the
// network.
package clientdb

import (
	"crypto/ed25519"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"golang.org/x/crypto/hkdf"

	"github.com/gandr-net/gandr/pkg/crypto"
)

// storageKeyInfo is the HKDF domain separator for the storage key.
const storageKeyInfo = "gandr-clientdb-v1"

// ErrNotFound is returned when a row does not exist.
var ErrNotFound = errors.New("clientdb: not found")

// DB is the client's local encrypted store.
type DB struct {
	db  *sql.DB
	key [crypto.KeySize]byte
}

// Nickname is a local petname for a pubkey. Purely local, never
// transmitted.
type Nickname struct {
	Pubkey    [32]byte
	Name      string
	Note      string
	AddedAt   int64
	TrustHint uint8
}

// SealedMessage is one decrypted sealed inbox entry.
type SealedMessage struct {
	MsgHash    [32]byte
	Data       []byte
	Sender     [32]byte
	ReceivedAt int64
	Read       bool
}

// Channel is a joined channel.
type Channel struct {
	ID       [32]byte
	Name     string
	JoinedAt int64
}

const schema = `
CREATE TABLE IF NOT EXISTS nicknames (
    pubkey      BLOB PRIMARY KEY,
    name        BLOB NOT NULL,
    note        BLOB,
    added_at    INTEGER NOT NULL,
    trust_hint  INTEGER DEFAULT 1
);
CREATE TABLE IF NOT EXISTS profile_cache (
    pubkey      BLOB PRIMARY KEY,
    data        BLOB NOT NULL,
    fetched_at  INTEGER NOT NULL,
    msg_hash    TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS content_cache (
    hash        TEXT PRIMARY KEY,
    data        BLOB NOT NULL,
    msg_type    INTEGER NOT NULL,
    fetched_at  INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS blocklist (
    pubkey      BLOB PRIMARY KEY,
    added_at    INTEGER NOT NULL,
    reason      BLOB
);
CREATE TABLE IF NOT EXISTS channels (
    channel_id  BLOB PRIMARY KEY,
    name        BLOB,
    joined_at   INTEGER NOT NULL,
    last_seen   TEXT
);
CREATE TABLE IF NOT EXISTS sealed_inbox (
    msg_hash    TEXT PRIMARY KEY,
    data        BLOB NOT NULL,
    sender      BLOB NOT NULL,
    received_at INTEGER NOT NULL,
    read        INTEGER DEFAULT 0
);
CREATE TABLE IF NOT EXISTS settings (
    key         TEXT PRIMARY KEY,
    value       BLOB NOT NULL
);
`

// Open opens (creating if needed) the client database at path, deriving
// the storage key from the identity private key.
func Open(path string, identityKey ed25519.PrivateKey) (*DB, error) {
	if len(identityKey) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("clientdb: invalid identity key length %d", len(identityKey))
	}
	var key [crypto.KeySize]byte
	r := hkdf.New(sha256.New, identityKey.Seed(), nil, []byte(storageKeyInfo))
	if _, err := io.ReadFull(r, key[:]); err != nil {
		return nil, fmt.Errorf("clientdb: deriving storage key: %w", err)
	}
	db, err := sql.Open("sqlite3", path+"?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		return nil, fmt.Errorf("clientdb: opening database: %w", err)
	}
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("clientdb: initializing schema: %w", err)
	}
	return &DB{db: db, key: key}, nil
}

// Close closes the database.
func (d *DB) Close() error { return d.db.Close() }

// seal encrypts a value bound to (table, rowKey).
func (d *DB) seal(table string, rowKey, value []byte) ([]byte, error) {
	if value == nil {
		value = []byte{}
	}
	aad := append([]byte(table+":"), rowKey...)
	nonce, ct, err := crypto.Encrypt(d.key, value, aad)
	if err != nil {
		return nil, err
	}
	return append(nonce[:], ct...), nil
}

// open decrypts a value bound to (table, rowKey).
func (d *DB) open(table string, rowKey, blob []byte) ([]byte, error) {
	if len(blob) < crypto.NonceSize+crypto.Overhead {
		return nil, errors.New("clientdb: encrypted value too short")
	}
	var nonce [crypto.NonceSize]byte
	copy(nonce[:], blob[:crypto.NonceSize])
	aad := append([]byte(table+":"), rowKey...)
	return crypto.Decrypt(d.key, nonce, blob[crypto.NonceSize:], aad)
}

// --- nicknames ---

// SetNickname inserts or updates the petname for a pubkey.
func (d *DB) SetNickname(n Nickname) error {
	if n.AddedAt == 0 {
		n.AddedAt = time.Now().Unix()
	}
	name, err := d.seal("nicknames", n.Pubkey[:], []byte(n.Name))
	if err != nil {
		return err
	}
	note, err := d.seal("nicknames", n.Pubkey[:], []byte(n.Note))
	if err != nil {
		return err
	}
	_, err = d.db.Exec(`INSERT INTO nicknames (pubkey, name, note, added_at, trust_hint)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(pubkey) DO UPDATE SET name=excluded.name, note=excluded.note, trust_hint=excluded.trust_hint`,
		n.Pubkey[:], name, note, n.AddedAt, n.TrustHint)
	if err != nil {
		return fmt.Errorf("clientdb: saving nickname: %w", err)
	}
	return nil
}

// GetNickname fetches the petname for a pubkey.
func (d *DB) GetNickname(pubkey [32]byte) (Nickname, error) {
	row := d.db.QueryRow(`SELECT name, note, added_at, trust_hint FROM nicknames WHERE pubkey = ?`, pubkey[:])
	var nameBlob, noteBlob []byte
	n := Nickname{Pubkey: pubkey}
	if err := row.Scan(&nameBlob, &noteBlob, &n.AddedAt, &n.TrustHint); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return n, ErrNotFound
		}
		return n, fmt.Errorf("clientdb: reading nickname: %w", err)
	}
	name, err := d.open("nicknames", pubkey[:], nameBlob)
	if err != nil {
		return n, err
	}
	note, err := d.open("nicknames", pubkey[:], noteBlob)
	if err != nil {
		return n, err
	}
	n.Name, n.Note = string(name), string(note)
	return n, nil
}

// DeleteNickname removes a petname.
func (d *DB) DeleteNickname(pubkey [32]byte) error {
	_, err := d.db.Exec(`DELETE FROM nicknames WHERE pubkey = ?`, pubkey[:])
	return err
}

// ListNicknames returns all petnames.
func (d *DB) ListNicknames() ([]Nickname, error) {
	rows, err := d.db.Query(`SELECT pubkey, name, note, added_at, trust_hint FROM nicknames`)
	if err != nil {
		return nil, fmt.Errorf("clientdb: listing nicknames: %w", err)
	}
	defer rows.Close()
	var out []Nickname
	for rows.Next() {
		var pk, nameBlob, noteBlob []byte
		var n Nickname
		if err := rows.Scan(&pk, &nameBlob, &noteBlob, &n.AddedAt, &n.TrustHint); err != nil {
			return nil, err
		}
		copy(n.Pubkey[:], pk)
		name, err := d.open("nicknames", pk, nameBlob)
		if err != nil {
			return nil, err
		}
		note, err := d.open("nicknames", pk, noteBlob)
		if err != nil {
			return nil, err
		}
		n.Name, n.Note = string(name), string(note)
		out = append(out, n)
	}
	return out, rows.Err()
}

// SearchNicknames returns petnames whose name or note contains the
// query, case-insensitive. Decrypt-then-match: the index never sees
// plaintext.
func (d *DB) SearchNicknames(query string) ([]Nickname, error) {
	all, err := d.ListNicknames()
	if err != nil {
		return nil, err
	}
	q := strings.ToLower(query)
	var out []Nickname
	for _, n := range all {
		if strings.Contains(strings.ToLower(n.Name), q) || strings.Contains(strings.ToLower(n.Note), q) {
			out = append(out, n)
		}
	}
	return out, nil
}

// --- blocklist ---

// Block adds a pubkey to the local blocklist.
func (d *DB) Block(pubkey [32]byte, reason string) error {
	enc, err := d.seal("blocklist", pubkey[:], []byte(reason))
	if err != nil {
		return err
	}
	_, err = d.db.Exec(`INSERT INTO blocklist (pubkey, added_at, reason) VALUES (?, ?, ?)
		ON CONFLICT(pubkey) DO UPDATE SET reason=excluded.reason`,
		pubkey[:], time.Now().Unix(), enc)
	return err
}

// Unblock removes a pubkey from the blocklist.
func (d *DB) Unblock(pubkey [32]byte) error {
	_, err := d.db.Exec(`DELETE FROM blocklist WHERE pubkey = ?`, pubkey[:])
	return err
}

// IsBlocked reports whether a pubkey is blocked.
func (d *DB) IsBlocked(pubkey [32]byte) (bool, error) {
	row := d.db.QueryRow(`SELECT 1 FROM blocklist WHERE pubkey = ?`, pubkey[:])
	var one int
	if err := row.Scan(&one); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// --- channels ---

// JoinChannel records a joined channel.
func (d *DB) JoinChannel(id [32]byte, name string) error {
	enc, err := d.seal("channels", id[:], []byte(name))
	if err != nil {
		return err
	}
	_, err = d.db.Exec(`INSERT INTO channels (channel_id, name, joined_at) VALUES (?, ?, ?)
		ON CONFLICT(channel_id) DO UPDATE SET name=excluded.name`,
		id[:], enc, time.Now().Unix())
	return err
}

// LeaveChannel removes a channel.
func (d *DB) LeaveChannel(id [32]byte) error {
	_, err := d.db.Exec(`DELETE FROM channels WHERE channel_id = ?`, id[:])
	return err
}

// ListChannels returns joined channels.
func (d *DB) ListChannels() ([]Channel, error) {
	rows, err := d.db.Query(`SELECT channel_id, name, joined_at FROM channels ORDER BY joined_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Channel
	for rows.Next() {
		var id, nameBlob []byte
		var c Channel
		if err := rows.Scan(&id, &nameBlob, &c.JoinedAt); err != nil {
			return nil, err
		}
		copy(c.ID[:], id)
		name, err := d.open("channels", id, nameBlob)
		if err != nil {
			return nil, err
		}
		c.Name = string(name)
		out = append(out, c)
	}
	return out, rows.Err()
}

// --- profile cache ---

// CacheProfile stores the latest serialized profile for a pubkey.
func (d *DB) CacheProfile(pubkey [32]byte, data []byte, msgHash string) error {
	enc, err := d.seal("profile_cache", pubkey[:], data)
	if err != nil {
		return err
	}
	_, err = d.db.Exec(`INSERT INTO profile_cache (pubkey, data, fetched_at, msg_hash) VALUES (?, ?, ?, ?)
		ON CONFLICT(pubkey) DO UPDATE SET data=excluded.data, fetched_at=excluded.fetched_at, msg_hash=excluded.msg_hash`,
		pubkey[:], enc, time.Now().Unix(), msgHash)
	return err
}

// GetProfile fetches a cached profile.
func (d *DB) GetProfile(pubkey [32]byte) ([]byte, error) {
	row := d.db.QueryRow(`SELECT data FROM profile_cache WHERE pubkey = ?`, pubkey[:])
	var enc []byte
	if err := row.Scan(&enc); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return d.open("profile_cache", pubkey[:], enc)
}

// --- sealed inbox ---

// PutSealed stores a decrypted sealed message.
func (d *DB) PutSealed(m SealedMessage) error {
	if m.ReceivedAt == 0 {
		m.ReceivedAt = time.Now().Unix()
	}
	enc, err := d.seal("sealed_inbox", m.MsgHash[:], m.Data)
	if err != nil {
		return err
	}
	hashHex := fmt.Sprintf("%x", m.MsgHash[:])
	_, err = d.db.Exec(`INSERT OR IGNORE INTO sealed_inbox (msg_hash, data, sender, received_at, read)
		VALUES (?, ?, ?, ?, ?)`,
		hashHex, enc, m.Sender[:], m.ReceivedAt, boolToInt(m.Read))
	return err
}

// ListSealed returns sealed inbox entries, newest first.
func (d *DB) ListSealed() ([]SealedMessage, error) {
	rows, err := d.db.Query(`SELECT msg_hash, data, sender, received_at, read FROM sealed_inbox ORDER BY received_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SealedMessage
	for rows.Next() {
		var hashHex string
		var enc, sender []byte
		var m SealedMessage
		var read int
		if err := rows.Scan(&hashHex, &enc, &sender, &m.ReceivedAt, &read); err != nil {
			return nil, err
		}
		hb, err := hex.DecodeString(hashHex)
		if err != nil || len(hb) != 32 {
			continue // unreadable row key; skip rather than fail the inbox
		}
		copy(m.MsgHash[:], hb)
		copy(m.Sender[:], sender)
		m.Read = read != 0
		data, err := d.open("sealed_inbox", m.MsgHash[:], enc)
		if err != nil {
			return nil, err
		}
		m.Data = data
		out = append(out, m)
	}
	return out, rows.Err()
}

// MarkSealedRead marks a sealed message read.
func (d *DB) MarkSealedRead(hash [32]byte) error {
	_, err := d.db.Exec(`UPDATE sealed_inbox SET read = 1 WHERE msg_hash = ?`, fmt.Sprintf("%x", hash[:]))
	return err
}

// UnreadSealedCount counts unread sealed messages.
func (d *DB) UnreadSealedCount() (int, error) {
	row := d.db.QueryRow(`SELECT COUNT(*) FROM sealed_inbox WHERE read = 0`)
	var n int
	err := row.Scan(&n)
	return n, err
}

// --- settings ---

// SetSetting stores one local client setting (theme choice, banner
// acceptance, …). Settings are local-only and, like every other value
// here, encrypted at rest; they are never part of any protocol message.
func (d *DB) SetSetting(key, value string) error {
	blob, err := d.seal("settings", []byte(key), []byte(value))
	if err != nil {
		return err
	}
	_, err = d.db.Exec(`INSERT INTO settings (key, value) VALUES (?, ?)
		ON CONFLICT(key) DO UPDATE SET value=excluded.value`, key, blob)
	if err != nil {
		return fmt.Errorf("clientdb: saving setting: %w", err)
	}
	return nil
}

// GetSetting fetches one local client setting.
func (d *DB) GetSetting(key string) (string, error) {
	row := d.db.QueryRow(`SELECT value FROM settings WHERE key = ?`, key)
	var blob []byte
	if err := row.Scan(&blob); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", ErrNotFound
		}
		return "", fmt.Errorf("clientdb: reading setting: %w", err)
	}
	value, err := d.open("settings", []byte(key), blob)
	if err != nil {
		return "", err
	}
	return string(value), nil
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
