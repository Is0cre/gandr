package federation

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/gandr-net/gandr/pkg/crypto"
	"github.com/gandr-net/gandr/pkg/network"
	"github.com/gandr-net/gandr/pkg/proto"
)

// pipeConn is an in-memory network.Conn for handshake tests.
type pipeConn struct {
	in     chan []byte
	out    chan []byte
	addr   network.PeerAddr
	closed chan struct{}
	once   sync.Once
}

func pipePair() (*pipeConn, *pipeConn) {
	ab := make(chan []byte, 64)
	ba := make(chan []byte, 64)
	a := &pipeConn{in: ba, out: ab, addr: network.PeerAddr{YggKey: []byte("peer-b")}, closed: make(chan struct{})}
	b := &pipeConn{in: ab, out: ba, addr: network.PeerAddr{YggKey: []byte("peer-a")}, closed: make(chan struct{})}
	return a, b
}

func (c *pipeConn) Send(ctx context.Context, msg []byte) error {
	data := append([]byte(nil), msg...)
	select {
	case c.out <- data:
		return nil
	case <-c.closed:
		return network.ErrClosed
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (c *pipeConn) Recv(ctx context.Context) ([]byte, error) {
	select {
	case msg := <-c.in:
		return msg, nil
	case <-c.closed:
		return nil, network.ErrClosed
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (c *pipeConn) RemoteAddr() network.PeerAddr { return c.addr }

func (c *pipeConn) Close() error {
	c.once.Do(func() { close(c.closed) })
	return nil
}

func testConfig(t *testing.T, caps uint32) (Config, ed25519.PublicKey) {
	t.Helper()
	pub, priv, err := crypto.GenerateIdentity()
	if err != nil {
		t.Fatal(err)
	}
	return Config{
		Identity:     priv,
		Capabilities: caps,
		UserAgent:    "gandrd/0.1.0-test",
		Policy: proto.PeerPolicyPayload{
			MaxMessageAge:  604800,
			MaxPayloadSize: 65535,
			RateLimitRPM:   600,
			TrustLevel:     proto.TrustNeutral,
		},
	}, pub
}

// runHandshake performs a full handshake between two in-memory nodes.
func runHandshake(t *testing.T, cfgA, cfgB Config) (*Session, *Session) {
	t.Helper()
	connA, connB := pipePair()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	type result struct {
		sess *Session
		err  error
	}
	resB := make(chan result, 1)
	go func() {
		s, err := Respond(ctx, connB, cfgB)
		resB <- result{s, err}
	}()
	sessA, errA := Initiate(ctx, connA, cfgA)
	rb := <-resB
	if errA != nil {
		t.Fatalf("Initiate: %v", errA)
	}
	if rb.err != nil {
		t.Fatalf("Respond: %v", rb.err)
	}
	return sessA, rb.sess
}

func TestHandshakeEstablishesSession(t *testing.T) {
	cfgA, pubA := testConfig(t, proto.CapChat|proto.CapRelay)
	cfgB, pubB := testConfig(t, proto.CapChat|proto.CapStorage)
	sessA, sessB := runHandshake(t, cfgA, cfgB)

	if !bytes.Equal(sessA.PeerIdentity[:], pubB) {
		t.Error("initiator learned wrong peer identity")
	}
	if !bytes.Equal(sessB.PeerIdentity[:], pubA) {
		t.Error("responder learned wrong peer identity")
	}
	if sessA.Capabilities != cfgB.Capabilities || sessB.Capabilities != cfgA.Capabilities {
		t.Error("capability exchange mismatch")
	}
	if sessA.UserAgent != cfgB.UserAgent {
		t.Error("user agent mismatch")
	}
	if sessA.Policy != cfgB.Policy || sessB.Policy != cfgA.Policy {
		t.Error("policy exchange mismatch")
	}
	if sessA.key != sessB.key {
		t.Fatal("session keys do not match")
	}
	if sessA.key == ([32]byte{}) {
		t.Fatal("session key is zero")
	}
}

func TestSessionEncryptedExchange(t *testing.T) {
	cfgA, _ := testConfig(t, proto.CapChat)
	cfgB, _ := testConfig(t, proto.CapChat)
	sessA, sessB := runHandshake(t, cfgA, cfgB)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	chat, err := proto.EncodePayload(&proto.ChatPayload{Content: "first federated words"})
	if err != nil {
		t.Fatal(err)
	}
	env, err := proto.NewEnvelope(cfgA.Identity, proto.MsgChat, sessA.PeerIdentity, chat)
	if err != nil {
		t.Fatal(err)
	}
	if err := sessA.Send(ctx, env); err != nil {
		t.Fatalf("session Send: %v", err)
	}
	got, err := sessB.Recv(ctx)
	if err != nil {
		t.Fatalf("session Recv: %v", err)
	}
	if got.ContentID() != env.ContentID() {
		t.Fatal("envelope mutated in transit")
	}
	decoded := &proto.ChatPayload{}
	if err := proto.DecodePayload(got.Payload, decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Content != "first federated words" {
		t.Fatal("content mismatch")
	}
}

func TestSessionTrafficIsEncrypted(t *testing.T) {
	// The wire frames must not contain envelope plaintext.
	cfgA, _ := testConfig(t, proto.CapChat)
	cfgB, _ := testConfig(t, proto.CapChat)
	connA, connB := pipePair()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	go func() {
		Respond(ctx, connB, cfgB)
	}()
	sessA, err := Initiate(ctx, connA, cfgA)
	if err != nil {
		t.Fatal(err)
	}

	secret := "completely confidential content"
	chat, _ := proto.EncodePayload(&proto.ChatPayload{Content: secret})
	env, _ := proto.NewEnvelope(cfgA.Identity, proto.MsgChat, sessA.PeerIdentity, chat)
	if err := sessA.Send(ctx, env); err != nil {
		t.Fatal(err)
	}
	frame := <-connB.in
	if bytes.Contains(frame, []byte(secret)) {
		t.Fatal("session frame contains plaintext content")
	}
	if bytes.Contains(frame, env.Sender[:]) {
		t.Fatal("session frame contains plaintext sender key")
	}
}

func TestSessionDropsGarbageSilently(t *testing.T) {
	cfgA, _ := testConfig(t, proto.CapChat)
	cfgB, _ := testConfig(t, proto.CapChat)
	sessA, sessB := runHandshake(t, cfgA, cfgB)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// inject garbage frames directly into B's pipe, then a valid message
	connB := sessB.conn.(*pipeConn)
	connB.in <- []byte("garbage")
	connB.in <- make([]byte, 100)
	connB.in <- nil

	chat, _ := proto.EncodePayload(&proto.ChatPayload{Content: "real"})
	env, _ := proto.NewEnvelope(cfgA.Identity, proto.MsgChat, sessA.PeerIdentity, chat)
	if err := sessA.Send(ctx, env); err != nil {
		t.Fatal(err)
	}
	got, err := sessB.Recv(ctx)
	if err != nil {
		t.Fatalf("Recv after garbage: %v", err)
	}
	if got.ContentID() != env.ContentID() {
		t.Fatal("wrong message delivered")
	}
}

func TestSessionRejectsReflectedFrames(t *testing.T) {
	// A frame a peer sent must not be accepted back by that same peer:
	// direction AADs differ.
	cfgA, _ := testConfig(t, proto.CapChat)
	cfgB, _ := testConfig(t, proto.CapChat)
	sessA, _ := runHandshake(t, cfgA, cfgB)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	chat, _ := proto.EncodePayload(&proto.ChatPayload{Content: "reflect me"})
	env, _ := proto.NewEnvelope(cfgA.Identity, proto.MsgChat, sessA.PeerIdentity, chat)
	if err := sessA.Send(ctx, env); err != nil {
		t.Fatal(err)
	}
	// capture A's outgoing frame and reflect it back at A
	connA := sessA.conn.(*pipeConn)
	frame := <-connA.out
	connA.in <- frame

	recvCtx, recvCancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer recvCancel()
	if _, err := sessA.Recv(recvCtx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("reflected frame was accepted: %v", err)
	}
}

func TestHandshakeWrongEchoNonce(t *testing.T) {
	// A responder that echoes the wrong nonce must be rejected.
	cfgA, _ := testConfig(t, proto.CapChat)
	cfgB, _ := testConfig(t, proto.CapChat)
	connA, connB := pipePair()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	go func() {
		// malicious responder: valid signature, wrong echo
		data, _ := connB.Recv(ctx)
		env, err := proto.Decode(data)
		if err != nil {
			return
		}
		ephPub, _, _ := crypto.GenerateX25519()
		ack := &proto.HelloAckPayload{
			Capabilities:  proto.CapChat,
			EchoNonce:     [32]byte{0xBA, 0xD0}, // wrong
			SessionPubkey: ephPub,
			UserAgent:     "evil/1.0",
		}
		ackData, _ := proto.EncodePayload(ack)
		ackEnv, _ := proto.NewEnvelope(cfgB.Identity, proto.MsgPeerAck, env.Sender, ackData)
		connB.Send(ctx, ackEnv.Encode())
	}()

	if _, err := Initiate(ctx, connA, cfgA); !errors.Is(err, ErrHandshakeFailed) {
		t.Fatalf("err = %v, want ErrHandshakeFailed", err)
	}
}

func TestHandshakeTamperedEnvelope(t *testing.T) {
	// Bit-flipped handshake messages must fail (signature verification).
	cfgA, _ := testConfig(t, proto.CapChat)
	connA, connB := pipePair()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	go func() {
		data, _ := connB.Recv(ctx)
		data[20] ^= 0x01 // corrupt sender pubkey
		// act as a relay delivering the corrupted HELLO back as an ACK-shaped reply
		connB.Send(ctx, data)
	}()
	if _, err := Initiate(ctx, connA, cfgA); !errors.Is(err, ErrHandshakeFailed) {
		t.Fatalf("err = %v, want ErrHandshakeFailed", err)
	}
}

func TestHandshakeIdentitySwitchMidway(t *testing.T) {
	// COMPLETE signed by a different identity than HELLO must fail.
	cfgA, _ := testConfig(t, proto.CapChat)
	cfgB, _ := testConfig(t, proto.CapChat)
	cfgEvil, _ := testConfig(t, proto.CapChat)
	connA, connB := pipePair()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	type result struct{ err error }
	resB := make(chan result, 1)
	go func() {
		_, err := Respond(ctx, connB, cfgB)
		resB <- result{err}
	}()

	// initiator sends HELLO normally, then COMPLETE under another key
	var nonceA [32]byte
	copy(nonceA[:], crypto.RandomBytes(32))
	if err := sendSigned(ctx, connA, cfgA.Identity, proto.MsgPeerHello, proto.Broadcast, &proto.HelloPayload{Nonce: nonceA, UserAgent: "x"}); err != nil {
		t.Fatal(err)
	}
	ackData, err := connA.Recv(ctx)
	if err != nil {
		t.Fatal(err)
	}
	ackEnv, err := proto.Decode(ackData)
	if err != nil {
		t.Fatal(err)
	}
	ack := &proto.HelloAckPayload{}
	if err := proto.DecodePayload(ackEnv.Payload, ack); err != nil {
		t.Fatal(err)
	}
	ephPub, _, _ := crypto.GenerateX25519()
	complete := &proto.HelloCompletePayload{EchoNonce: ack.Nonce, SessionPubkey: ephPub}
	if err := sendSigned(ctx, connA, cfgEvil.Identity, proto.MsgPeerComplete, ackEnv.Sender, complete); err != nil {
		t.Fatal(err)
	}
	if rb := <-resB; !errors.Is(rb.err, ErrHandshakeFailed) {
		t.Fatalf("err = %v, want ErrHandshakeFailed", rb.err)
	}
}

func TestHandshakeStaleTimestamp(t *testing.T) {
	// Replayed (old) handshake messages must be rejected.
	cfgA, _ := testConfig(t, proto.CapChat)
	cfgB, _ := testConfig(t, proto.CapChat)
	connA, connB := pipePair()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	type result struct{ err error }
	resB := make(chan result, 1)
	go func() {
		_, err := Respond(ctx, connB, cfgB)
		resB <- result{err}
	}()

	// hand-craft a HELLO stamped 10 minutes ago
	data, err := proto.EncodePayload(&proto.HelloPayload{UserAgent: "old"})
	if err != nil {
		t.Fatal(err)
	}
	env, err := proto.NewEnvelope(cfgA.Identity, proto.MsgPeerHello, proto.Broadcast, data)
	if err != nil {
		t.Fatal(err)
	}
	env.Timestamp = time.Now().Add(-10 * time.Minute).UnixNano()
	digest := env.SigningDigest()
	sig, err := crypto.Sign(cfgA.Identity, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	copy(env.Signature[:], sig)
	if err := connA.Send(ctx, env.Encode()); err != nil {
		t.Fatal(err)
	}
	if rb := <-resB; !errors.Is(rb.err, ErrHandshakeFailed) {
		t.Fatalf("err = %v, want ErrHandshakeFailed", rb.err)
	}
}

func TestPeerTable(t *testing.T) {
	cfgA, _ := testConfig(t, proto.CapChat)
	cfgB, _ := testConfig(t, proto.CapChat)
	_, sessB := runHandshake(t, cfgA, cfgB)

	table, err := NewTable(2, proto.TrustNeutral)
	if err != nil {
		t.Fatal(err)
	}
	p, err := table.Add(sessB)
	if err != nil {
		t.Fatal(err)
	}
	if p.Trust != proto.TrustNeutral {
		t.Error("new peer not at default trust")
	}
	if got, ok := table.Get(sessB.PeerIdentity); !ok || got != p {
		t.Error("Get failed")
	}
	if table.Count() != 1 {
		t.Error("count mismatch")
	}

	if err := table.SetTrust(sessB.PeerIdentity, proto.TrustVouched); err != nil {
		t.Fatal(err)
	}
	if got, _ := table.Get(sessB.PeerIdentity); !got.Vouched() {
		t.Error("trust update lost")
	}
	if err := table.SetTrust(sessB.PeerIdentity, 0x09); !errors.Is(err, ErrBadTrust) {
		t.Error("accepted invalid trust level")
	}
	if err := table.SetTrust([32]byte{0xFF}, proto.TrustTrusted); !errors.Is(err, ErrUnknownPeer) {
		t.Error("SetTrust on unknown peer")
	}

	// re-adding the same identity keeps earned trust
	if _, err := table.Add(sessB); err != nil {
		t.Fatal(err)
	}
	if got, _ := table.Get(sessB.PeerIdentity); got.Trust != proto.TrustVouched {
		t.Error("reconnect reset trust")
	}
	if table.Count() != 1 {
		t.Error("reconnect duplicated peer")
	}

	table.Remove(sessB.PeerIdentity)
	if table.Count() != 0 {
		t.Error("remove failed")
	}
}

func TestPeerTableFull(t *testing.T) {
	table, err := NewTable(1, proto.TrustNeutral)
	if err != nil {
		t.Fatal(err)
	}
	cfg1, _ := testConfig(t, proto.CapChat)
	cfg2, _ := testConfig(t, proto.CapChat)
	cfg3, _ := testConfig(t, proto.CapChat)

	// node 1's view: one session to peer 2, one to peer 3
	sessTo2, _ := runHandshake(t, cfg1, cfg2)
	if _, err := table.Add(sessTo2); err != nil {
		t.Fatal(err)
	}
	sessTo3, _ := runHandshake(t, cfg1, cfg3)
	if _, err := table.Add(sessTo3); !errors.Is(err, ErrTableFull) {
		t.Fatalf("err = %v, want ErrTableFull", err)
	}
}

func TestNewTableRejectsBadDefault(t *testing.T) {
	if _, err := NewTable(10, 0x07); !errors.Is(err, ErrBadTrust) {
		t.Fatal("accepted invalid default trust")
	}
}
