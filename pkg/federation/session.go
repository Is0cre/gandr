// Package federation implements peering between gandrd nodes: the
// four-step signed handshake, the encrypted session layer that carries
// all post-handshake traffic, and the peer trust table.
//
// Handshake (all steps are signed envelopes; any invalid signature or
// protocol violation aborts silently):
//
//	A -> B  MsgPeerHello     capabilities, nonce_A
//	B -> A  MsgPeerAck       capabilities, nonce_B, echo nonce_A, B's ephemeral X25519 key
//	A -> B  MsgPeerComplete  echo nonce_B, A's ephemeral X25519 key
//	A <-> B MsgPeerPolicy    encrypted with the derived session key
//
// Both sides derive the session key with X25519 between the ephemeral
// keys and HKDF-SHA256. Identity keys only ever sign; they never
// encrypt. All session traffic is XChaCha20-Poly1305 inside Yggdrasil's
// own transport encryption — double encrypted.
package federation

import (
	"context"
	"errors"
	"fmt"

	"github.com/gandr-net/gandr/pkg/crypto"
	"github.com/gandr-net/gandr/pkg/network"
	"github.com/gandr-net/gandr/pkg/proto"
)

// sessionKeyInfo is the HKDF domain separator for session keys.
const sessionKeyInfo = "gandr-session-v1"

// Direction tags bound into the AEAD as associated data, preventing
// reflection of a peer's own ciphertext back at it.
var (
	aadInitiatorToResponder = []byte("gandr-i2r-v1")
	aadResponderToInitiator = []byte("gandr-r2i-v1")
)

// Session errors. Handshake failures are reported to the caller, who
// closes the connection without responding — silence on the wire.
var (
	ErrHandshakeFailed = errors.New("federation: handshake failed")
	ErrSessionClosed   = errors.New("federation: session closed")
)

// Session is an established, encrypted peering with one remote node.
type Session struct {
	conn network.Conn
	key  [crypto.KeySize]byte

	sendAAD []byte
	recvAAD []byte

	// PeerIdentity is the remote node's Ed25519 identity public key,
	// proven by its handshake signatures.
	PeerIdentity [32]byte
	// Capabilities announced by the peer during the handshake.
	Capabilities uint32
	// UserAgent announced by the peer.
	UserAgent string
	// Policy received from the peer after session establishment.
	Policy proto.PeerPolicyPayload
}

// Send encrypts and transmits one envelope over the session.
func (s *Session) Send(ctx context.Context, env *proto.Envelope) error {
	nonce, ct, err := crypto.Encrypt(s.key, env.Encode(), s.sendAAD)
	if err != nil {
		return err
	}
	frame := make([]byte, 0, crypto.NonceSize+len(ct))
	frame = append(frame, nonce[:]...)
	frame = append(frame, ct...)
	return s.conn.Send(ctx, frame)
}

// Recv returns the next valid envelope from the peer. Frames that fail
// decryption, decoding, or signature verification are dropped silently
// and Recv keeps waiting — exactly the behavior the protocol mandates
// for invalid traffic.
func (s *Session) Recv(ctx context.Context) (*proto.Envelope, error) {
	for {
		frame, err := s.conn.Recv(ctx)
		if err != nil {
			return nil, err
		}
		env, ok := s.open(frame)
		if !ok {
			continue
		}
		return env, nil
	}
}

// open decrypts and decodes one session frame.
func (s *Session) open(frame []byte) (*proto.Envelope, bool) {
	if len(frame) < crypto.NonceSize+crypto.Overhead {
		return nil, false
	}
	var nonce [crypto.NonceSize]byte
	copy(nonce[:], frame[:crypto.NonceSize])
	plain, err := crypto.Decrypt(s.key, nonce, frame[crypto.NonceSize:], s.recvAAD)
	if err != nil {
		return nil, false
	}
	env, err := proto.Decode(plain)
	if err != nil {
		return nil, false
	}
	return env, true
}

// Close tears down the underlying connection.
func (s *Session) Close() error {
	return s.conn.Close()
}

// RemoteAddr reports the transport address of the peer.
func (s *Session) RemoteAddr() network.PeerAddr {
	return s.conn.RemoteAddr()
}

// deriveSession builds the Session struct common to both handshake
// roles.
func deriveSession(conn network.Conn, ephPriv, peerSessionPub [32]byte, initiator bool) (*Session, error) {
	key, err := crypto.DeriveSharedKey(ephPriv, peerSessionPub, sessionKeyInfo)
	if err != nil {
		return nil, fmt.Errorf("%w: session key derivation: %w", ErrHandshakeFailed, err)
	}
	s := &Session{conn: conn, key: key}
	if initiator {
		s.sendAAD, s.recvAAD = aadInitiatorToResponder, aadResponderToInitiator
	} else {
		s.sendAAD, s.recvAAD = aadResponderToInitiator, aadInitiatorToResponder
	}
	return s, nil
}
