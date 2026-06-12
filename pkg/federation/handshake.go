package federation

import (
	"context"
	"crypto/ed25519"
	"crypto/subtle"
	"fmt"
	"time"

	"github.com/gandr-net/gandr/pkg/crypto"
	"github.com/gandr-net/gandr/pkg/network"
	"github.com/gandr-net/gandr/pkg/proto"
)

// Config carries the local node's handshake parameters.
type Config struct {
	// Identity is the node's permanent Ed25519 identity key.
	Identity ed25519.PrivateKey
	// Capabilities is the local capability bitmask.
	Capabilities uint32
	// UserAgent announced to peers, e.g. "gandrd/0.1.0".
	UserAgent string
	// Policy announced to peers after session establishment.
	Policy proto.PeerPolicyPayload
}

// handshakeTimeout bounds each individual handshake step.
const handshakeTimeout = 30 * time.Second

// Initiate runs the initiator side of the handshake over conn and
// returns an established session. On any error the caller must close
// the connection without sending anything further.
func Initiate(ctx context.Context, conn network.Conn, cfg Config) (*Session, error) {
	ctx, cancel := context.WithTimeout(ctx, handshakeTimeout)
	defer cancel()

	// Step 1: HELLO
	var nonceA [32]byte
	copy(nonceA[:], crypto.RandomBytes(32))
	hello := &proto.HelloPayload{
		Capabilities: cfg.Capabilities,
		Nonce:        nonceA,
		UserAgent:    cfg.UserAgent,
	}
	if err := sendSigned(ctx, conn, cfg.Identity, proto.MsgPeerHello, proto.Broadcast, hello); err != nil {
		return nil, err
	}

	// Step 2: receive ACK
	ackEnv, err := recvHandshake(ctx, conn, proto.MsgPeerAck)
	if err != nil {
		return nil, err
	}
	ack := &proto.HelloAckPayload{}
	if err := proto.DecodePayload(ackEnv.Payload, ack); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrHandshakeFailed, err)
	}
	if subtle.ConstantTimeCompare(ack.EchoNonce[:], nonceA[:]) != 1 {
		return nil, fmt.Errorf("%w: nonce echo mismatch", ErrHandshakeFailed)
	}

	// Step 3: COMPLETE with our ephemeral session key
	ephPub, ephPriv, err := crypto.GenerateX25519()
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrHandshakeFailed, err)
	}
	complete := &proto.HelloCompletePayload{
		EchoNonce:     ack.Nonce,
		SessionPubkey: ephPub,
	}
	if err := sendSigned(ctx, conn, cfg.Identity, proto.MsgPeerComplete, ackEnv.Sender, complete); err != nil {
		return nil, err
	}

	sess, err := deriveSession(conn, ephPriv, ack.SessionPubkey, true)
	if err != nil {
		return nil, err
	}
	sess.PeerIdentity = ackEnv.Sender
	sess.Capabilities = ack.Capabilities
	sess.UserAgent = ack.UserAgent

	// Step 4: policy exchange, encrypted. Initiator sends first.
	if err := sendPolicy(ctx, sess, cfg); err != nil {
		return nil, err
	}
	if err := recvPolicy(ctx, sess); err != nil {
		return nil, err
	}
	return sess, nil
}

// Respond runs the responder side of the handshake over conn.
func Respond(ctx context.Context, conn network.Conn, cfg Config) (*Session, error) {
	ctx, cancel := context.WithTimeout(ctx, handshakeTimeout)
	defer cancel()

	// Step 1: receive HELLO
	helloEnv, err := recvHandshake(ctx, conn, proto.MsgPeerHello)
	if err != nil {
		return nil, err
	}
	hello := &proto.HelloPayload{}
	if err := proto.DecodePayload(helloEnv.Payload, hello); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrHandshakeFailed, err)
	}

	// Step 2: ACK with our nonce and ephemeral session key
	ephPub, ephPriv, err := crypto.GenerateX25519()
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrHandshakeFailed, err)
	}
	var nonceB [32]byte
	copy(nonceB[:], crypto.RandomBytes(32))
	ack := &proto.HelloAckPayload{
		Capabilities:  cfg.Capabilities,
		Nonce:         nonceB,
		EchoNonce:     hello.Nonce,
		SessionPubkey: ephPub,
		UserAgent:     cfg.UserAgent,
	}
	if err := sendSigned(ctx, conn, cfg.Identity, proto.MsgPeerAck, helloEnv.Sender, ack); err != nil {
		return nil, err
	}

	// Step 3: receive COMPLETE
	completeEnv, err := recvHandshake(ctx, conn, proto.MsgPeerComplete)
	if err != nil {
		return nil, err
	}
	// the completing party must be the same identity that said HELLO
	if completeEnv.Sender != helloEnv.Sender {
		return nil, fmt.Errorf("%w: identity changed mid-handshake", ErrHandshakeFailed)
	}
	complete := &proto.HelloCompletePayload{}
	if err := proto.DecodePayload(completeEnv.Payload, complete); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrHandshakeFailed, err)
	}
	if subtle.ConstantTimeCompare(complete.EchoNonce[:], nonceB[:]) != 1 {
		return nil, fmt.Errorf("%w: nonce echo mismatch", ErrHandshakeFailed)
	}

	sess, err := deriveSession(conn, ephPriv, complete.SessionPubkey, false)
	if err != nil {
		return nil, err
	}
	sess.PeerIdentity = helloEnv.Sender
	sess.Capabilities = hello.Capabilities
	sess.UserAgent = hello.UserAgent

	// Step 4: policy exchange. Responder receives first, then sends.
	if err := recvPolicy(ctx, sess); err != nil {
		return nil, err
	}
	if err := sendPolicy(ctx, sess, cfg); err != nil {
		return nil, err
	}
	return sess, nil
}

// sendSigned encodes, signs, and transmits one handshake envelope.
func sendSigned(ctx context.Context, conn network.Conn, identity ed25519.PrivateKey, msgType uint8, recipient [32]byte, payload proto.Payload) error {
	data, err := proto.EncodePayload(payload)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrHandshakeFailed, err)
	}
	env, err := proto.NewEnvelope(identity, msgType, recipient, data)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrHandshakeFailed, err)
	}
	if err := conn.Send(ctx, env.Encode()); err != nil {
		return fmt.Errorf("%w: %w", ErrHandshakeFailed, err)
	}
	return nil
}

// recvHandshake reads the next message and requires it to be a valid,
// fresh, signed envelope of the expected type. Anything else fails the
// handshake — one strike, no retries within a handshake attempt.
func recvHandshake(ctx context.Context, conn network.Conn, wantType uint8) (*proto.Envelope, error) {
	data, err := conn.Recv(ctx)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrHandshakeFailed, err)
	}
	env, err := proto.Decode(data)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrHandshakeFailed, err)
	}
	if env.Type != wantType {
		return nil, fmt.Errorf("%w: unexpected message type %#x", ErrHandshakeFailed, env.Type)
	}
	if err := proto.ValidateTimestamp(env.Timestamp, time.Now()); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrHandshakeFailed, err)
	}
	return env, nil
}

// sendPolicy transmits the local policy over the established session.
func sendPolicy(ctx context.Context, sess *Session, cfg Config) error {
	data, err := proto.EncodePayload(&cfg.Policy)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrHandshakeFailed, err)
	}
	env, err := proto.NewEnvelope(cfg.Identity, proto.MsgPeerPolicy, sess.PeerIdentity, data)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrHandshakeFailed, err)
	}
	if err := sess.Send(ctx, env); err != nil {
		return fmt.Errorf("%w: %w", ErrHandshakeFailed, err)
	}
	return nil
}

// recvPolicy receives and validates the peer's policy message.
func recvPolicy(ctx context.Context, sess *Session) error {
	env, err := sess.Recv(ctx)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrHandshakeFailed, err)
	}
	if env.Type != proto.MsgPeerPolicy {
		return fmt.Errorf("%w: expected policy, got %#x", ErrHandshakeFailed, env.Type)
	}
	if env.Sender != sess.PeerIdentity {
		return fmt.Errorf("%w: policy signed by wrong identity", ErrHandshakeFailed)
	}
	if err := proto.ValidateTimestamp(env.Timestamp, time.Now()); err != nil {
		return fmt.Errorf("%w: %w", ErrHandshakeFailed, err)
	}
	policy := &proto.PeerPolicyPayload{}
	if err := proto.DecodePayload(env.Payload, policy); err != nil {
		return fmt.Errorf("%w: %w", ErrHandshakeFailed, err)
	}
	sess.Policy = *policy
	return nil
}
