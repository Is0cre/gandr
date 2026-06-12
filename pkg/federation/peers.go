package federation

import (
	"errors"
	"sync"
	"time"

	"github.com/gandr-net/gandr/pkg/network"
	"github.com/gandr-net/gandr/pkg/proto"
)

// Peer table errors.
var (
	ErrTableFull   = errors.New("federation: peer table full")
	ErrUnknownPeer = errors.New("federation: unknown peer")
	ErrBadTrust    = errors.New("federation: invalid trust level")
)

// Peer is one entry in the local peer table.
type Peer struct {
	// Identity is the peer's Ed25519 identity public key.
	Identity [32]byte
	// Addr is the transport address the peering runs over.
	Addr network.PeerAddr
	// Trust is the locally assigned trust level (proto.Trust*).
	Trust uint8
	// Capabilities, UserAgent, and Policy as announced in the handshake.
	Capabilities uint32
	UserAgent    string
	Policy       proto.PeerPolicyPayload
	// ConnectedAt is when the current session was established.
	ConnectedAt time.Time
	// LastSeen is updated on every valid message from the peer.
	LastSeen time.Time

	// Session is the live encrypted session, nil if disconnected.
	Session *Session
}

// Vouched reports whether this peer may send and receive peer
// introductions.
func (p *Peer) Vouched() bool { return p.Trust >= proto.TrustVouched }

// Table is the in-memory peer table. The node deliberately persists no
// peer identity information; the table is rebuilt from live handshakes
// on every start.
type Table struct {
	mu           sync.RWMutex
	peers        map[[32]byte]*Peer
	maxPeers     int
	defaultTrust uint8
}

// NewTable creates a peer table. defaultTrust is assigned to newly
// added peers (TrustNeutral per spec unless the operator overrides).
func NewTable(maxPeers int, defaultTrust uint8) (*Table, error) {
	if defaultTrust > proto.TrustVouched {
		return nil, ErrBadTrust
	}
	return &Table{
		peers:        make(map[[32]byte]*Peer),
		maxPeers:     maxPeers,
		defaultTrust: defaultTrust,
	}, nil
}

// Add registers an established session as a peer. Reconnecting an
// existing identity replaces its session but keeps its earned trust.
func (t *Table) Add(sess *Session) (*Peer, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := time.Now()
	if existing, ok := t.peers[sess.PeerIdentity]; ok {
		if existing.Session != nil && existing.Session != sess {
			existing.Session.Close()
		}
		existing.Session = sess
		existing.Addr = sess.RemoteAddr()
		existing.Capabilities = sess.Capabilities
		existing.UserAgent = sess.UserAgent
		existing.Policy = sess.Policy
		existing.ConnectedAt = now
		existing.LastSeen = now
		return existing, nil
	}
	if len(t.peers) >= t.maxPeers {
		return nil, ErrTableFull
	}
	p := &Peer{
		Identity:     sess.PeerIdentity,
		Addr:         sess.RemoteAddr(),
		Trust:        t.defaultTrust,
		Capabilities: sess.Capabilities,
		UserAgent:    sess.UserAgent,
		Policy:       sess.Policy,
		ConnectedAt:  now,
		LastSeen:     now,
		Session:      sess,
	}
	t.peers[sess.PeerIdentity] = p
	return p, nil
}

// Get returns the peer with the given identity.
func (t *Table) Get(identity [32]byte) (*Peer, bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	p, ok := t.peers[identity]
	return p, ok
}

// RemoveSession drops a peer only while it still runs the given
// session. A pump cleaning up after a dead session must not remove
// the entry once a reconnect has replaced the session under the same
// identity.
func (t *Table) RemoveSession(identity [32]byte, sess *Session) {
	t.mu.Lock()
	p, ok := t.peers[identity]
	if !ok || p.Session != sess {
		t.mu.Unlock()
		return
	}
	delete(t.peers, identity)
	t.mu.Unlock()
	sess.Close()
}

// Remove drops a peer and closes its session if live.
func (t *Table) Remove(identity [32]byte) {
	t.mu.Lock()
	p, ok := t.peers[identity]
	delete(t.peers, identity)
	t.mu.Unlock()
	if ok && p.Session != nil {
		p.Session.Close()
	}
}

// SetTrust changes a peer's trust level.
func (t *Table) SetTrust(identity [32]byte, trust uint8) error {
	if trust > proto.TrustVouched {
		return ErrBadTrust
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	p, ok := t.peers[identity]
	if !ok {
		return ErrUnknownPeer
	}
	p.Trust = trust
	return nil
}

// Touch updates LastSeen for a peer.
func (t *Table) Touch(identity [32]byte) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if p, ok := t.peers[identity]; ok {
		p.LastSeen = time.Now()
	}
}

// List returns a snapshot of all peers.
func (t *Table) List() []Peer {
	t.mu.RLock()
	defer t.mu.RUnlock()
	out := make([]Peer, 0, len(t.peers))
	for _, p := range t.peers {
		out = append(out, *p)
	}
	return out
}

// Count returns the number of peers in the table.
func (t *Table) Count() int {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return len(t.peers)
}
