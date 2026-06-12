// Package network provides Gandr's transport layer over the Yggdrasil
// overlay network. All inter-node bytes flow through this package and
// nothing else; no clearnet listener exists anywhere in gandrd.
//
// The embedded backend runs a yggdrasil-go core in-process. Yggdrasil
// delivers best-effort, unordered datagrams up to its MTU; a complete
// Gandr envelope can exceed that by a small margin, so a thin
// message-protocol sublayer adds fragmentation, acknowledgement,
// retransmission, and deduplication. The result is reliable,
// message-boundary-preserving delivery without ordering guarantees —
// exactly what the signed, individually-validated envelope layer above
// needs, and nothing more.
package network

import (
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"errors"
	"net/netip"

	"github.com/gandr-net/gandr/pkg/proto"
)

// MaxMessageSize is the largest message the transport will carry: one
// maximal protocol envelope.
const MaxMessageSize = proto.MaxMessageSize

// Transport errors.
var (
	ErrClosed      = errors.New("network: transport closed")
	ErrTooLarge    = errors.New("network: message exceeds maximum size")
	ErrSendTimeout = errors.New("network: send not acknowledged")
	ErrUnreachable = errors.New("network: peer unreachable")
)

// PeerAddr identifies a remote gandrd. Exactly one form is set: YggKey
// for the embedded backend (the peer's Yggdrasil node key), IPPort for
// the external-daemon backend (the peer's Yggdrasil IPv6 and TCP port).
type PeerAddr struct {
	YggKey ed25519.PublicKey
	IPPort netip.AddrPort
}

// String renders the address for display. Never logged by gandrd in
// production paths; used by the client UI.
func (a PeerAddr) String() string {
	if len(a.YggKey) > 0 {
		return "ygg:" + hex.EncodeToString(a.YggKey)
	}
	return a.IPPort.String()
}

// mapKey returns a comparable representation for map indexing.
func (a PeerAddr) mapKey() string {
	if len(a.YggKey) > 0 {
		return string(a.YggKey)
	}
	return a.IPPort.String()
}

// Conn is a reliable, message-oriented connection to one peer.
// Delivery is at-least-once with deduplication (effectively once) and
// is NOT ordered; the layers above tolerate reordering by design.
type Conn interface {
	// Send transmits one message and blocks until it is acknowledged,
	// the context is done, or retransmission gives up.
	Send(ctx context.Context, msg []byte) error
	// Recv returns the next received message.
	Recv(ctx context.Context) ([]byte, error)
	RemoteAddr() PeerAddr
	Close() error
}

// Transport binds a local Yggdrasil presence and produces connections.
type Transport interface {
	Dial(ctx context.Context, addr PeerAddr) (Conn, error)
	Accept(ctx context.Context) (Conn, error)
	LocalAddr() PeerAddr
	Close() error
}
