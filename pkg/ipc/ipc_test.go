package ipc

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/gandr-net/gandr/pkg/crypto"
	"github.com/gandr-net/gandr/pkg/proto"
)

// fakeHandler records calls and serves canned responses.
type fakeHandler struct {
	mu       sync.Mutex
	sent     []*proto.Envelope
	stored   map[[32]byte]*proto.Envelope
	profiles map[[32]byte]*proto.Envelope
	peers    []PeerInfo
	sendErr  error
	trusts   []trustCall
	connects [][32]byte
}

type trustCall struct {
	id    [32]byte
	level uint8
}

func newFakeHandler() *fakeHandler {
	return &fakeHandler{
		stored:   make(map[[32]byte]*proto.Envelope),
		profiles: make(map[[32]byte]*proto.Envelope),
	}
}

func (h *fakeHandler) HandleSend(env *proto.Envelope) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.sendErr != nil {
		return h.sendErr
	}
	h.sent = append(h.sent, env)
	return nil
}

func (h *fakeHandler) HandleFetch(hash [32]byte) (*proto.Envelope, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	env, ok := h.stored[hash]
	if !ok {
		return nil, errors.New("not found")
	}
	return env, nil
}

func (h *fakeHandler) HandlePeerList() []PeerInfo {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.peers
}

func (h *fakeHandler) HandleProfile(pk [32]byte) (*proto.Envelope, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	env, ok := h.profiles[pk]
	if !ok {
		return nil, errors.New("not found")
	}
	return env, nil
}

func (h *fakeHandler) HandleTrust(id [32]byte, level uint8) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if level > proto.TrustVouched {
		return errors.New("bad trust level")
	}
	h.trusts = append(h.trusts, trustCall{id, level})
	return nil
}

func (h *fakeHandler) HandleConnect(key [32]byte) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.connects = append(h.connects, key)
	return nil
}

func testEnvelope(t *testing.T, content string, channel [32]byte) *proto.Envelope {
	t.Helper()
	_, priv, err := crypto.GenerateIdentity()
	if err != nil {
		t.Fatal(err)
	}
	data, err := proto.EncodePayload(&proto.ChatPayload{ChannelID: channel, Content: content})
	if err != nil {
		t.Fatal(err)
	}
	env, err := proto.NewEnvelope(priv, proto.MsgChat, channel, data)
	if err != nil {
		t.Fatal(err)
	}
	return env
}

func testServerClient(t *testing.T) (*Server, *Client, *fakeHandler) {
	t.Helper()
	sock := filepath.Join(t.TempDir(), "gandr.sock")
	h := newFakeHandler()
	srv, err := Listen(sock, h)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { srv.Close() })
	cli, err := Dial(sock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cli.Close() })
	return srv, cli, h
}

func TestSendRoundtrip(t *testing.T) {
	_, cli, h := testServerClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	env := testEnvelope(t, "over the socket", [32]byte{})
	if err := cli.Send(ctx, env); err != nil {
		t.Fatalf("Send: %v", err)
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.sent) != 1 || h.sent[0].ContentID() != env.ContentID() {
		t.Fatal("daemon did not receive the envelope intact")
	}
}

func TestSendErrorSurfaces(t *testing.T) {
	_, cli, h := testServerClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	h.mu.Lock()
	h.sendErr = errors.New("peer table full")
	h.mu.Unlock()
	if err := cli.Send(ctx, testEnvelope(t, "x", [32]byte{})); err == nil {
		t.Fatal("daemon error not surfaced to client")
	}
}

func TestSendRejectsGarbage(t *testing.T) {
	// a client that sends a malformed envelope gets an error frame
	sock := filepath.Join(t.TempDir(), "gandr.sock")
	h := newFakeHandler()
	srv, err := Listen(sock, h)
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	cli, err := Dial(sock)
	if err != nil {
		t.Fatal(err)
	}
	defer cli.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := cli.request(ctx, IPCSend, []byte("not an envelope")); err == nil {
		t.Fatal("malformed envelope accepted")
	}
}

func TestFetch(t *testing.T) {
	_, cli, h := testServerClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	env := testEnvelope(t, "fetch me", [32]byte{})
	h.mu.Lock()
	h.stored[env.ContentID()] = env
	h.mu.Unlock()

	got, err := cli.Fetch(ctx, env.ContentID())
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if got.ContentID() != env.ContentID() {
		t.Fatal("fetched envelope differs")
	}
	if _, err := cli.Fetch(ctx, [32]byte{0xEE}); err == nil {
		t.Fatal("fetch of missing hash succeeded")
	}
}

func TestPeerList(t *testing.T) {
	_, cli, h := testServerClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	h.mu.Lock()
	h.peers = []PeerInfo{{Identity: [32]byte{1}, Trust: proto.TrustVouched, UserAgent: "gandrd/0.1.0"}}
	h.mu.Unlock()

	peers, err := cli.PeerList(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(peers) != 1 || peers[0].Trust != proto.TrustVouched {
		t.Fatalf("peer list mismatch: %+v", peers)
	}
}

func TestProfile(t *testing.T) {
	_, cli, h := testServerClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, priv, _ := crypto.GenerateIdentity()
	data, _ := proto.EncodePayload(&proto.ProfilePayload{DisplayName: "mara"})
	env, err := proto.NewEnvelope(priv, proto.MsgProfile, proto.Broadcast, data)
	if err != nil {
		t.Fatal(err)
	}
	h.mu.Lock()
	h.profiles[env.Sender] = env
	h.mu.Unlock()

	got, err := cli.Profile(ctx, env.Sender)
	if err != nil {
		t.Fatal(err)
	}
	p := &proto.ProfilePayload{}
	if err := proto.DecodePayload(got.Payload, p); err != nil {
		t.Fatal(err)
	}
	if p.DisplayName != "mara" {
		t.Fatal("profile content mismatch")
	}
}

func TestIncomingPushWithSubscription(t *testing.T) {
	srv, cli, _ := testServerClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	channel := [32]byte{0xC4}
	other := [32]byte{0xD5}

	if err := cli.Subscribe(ctx, channel); err != nil {
		t.Fatal(err)
	}

	// a message on an unsubscribed channel must not arrive
	srv.Push(testEnvelope(t, "unrelated", other))
	// a message on the subscribed channel must arrive
	want := testEnvelope(t, "for me", channel)
	srv.Push(want)

	select {
	case env := <-cli.Incoming():
		if env.ContentID() != want.ContentID() {
			t.Fatal("received the unsubscribed message")
		}
	case <-ctx.Done():
		t.Fatal("subscribed message never arrived")
	}

	// DMs (zero channel) always arrive
	dm := testEnvelope(t, "direct", [32]byte{})
	srv.Push(dm)
	select {
	case env := <-cli.Incoming():
		if env.ContentID() != dm.ContentID() {
			t.Fatal("wrong dm")
		}
	case <-ctx.Done():
		t.Fatal("dm never arrived")
	}

	// unsubscribe stops channel delivery
	if err := cli.Unsubscribe(ctx, channel); err != nil {
		t.Fatal(err)
	}
	srv.Push(testEnvelope(t, "after unsub", channel))
	select {
	case env := <-cli.Incoming():
		t.Fatalf("received after unsubscribe: %x", env.ContentID())
	case <-time.After(300 * time.Millisecond):
	}
}

func TestSetTrustAndConnect(t *testing.T) {
	_, cli, h := testServerClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	id := [32]byte{0x5A}
	if err := cli.SetTrust(ctx, id, proto.TrustVouched); err != nil {
		t.Fatalf("SetTrust: %v", err)
	}
	if err := cli.SetTrust(ctx, id, 0x09); err == nil {
		t.Fatal("invalid trust level accepted")
	}
	key := [32]byte{0x6B}
	if err := cli.Connect(ctx, key); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.trusts) != 1 || h.trusts[0].id != id || h.trusts[0].level != proto.TrustVouched {
		t.Fatalf("trust calls: %+v", h.trusts)
	}
	if len(h.connects) != 1 || h.connects[0] != key {
		t.Fatalf("connect calls: %+v", h.connects)
	}
}

func TestPeerUpdatePush(t *testing.T) {
	srv, cli, _ := testServerClient(t)
	// a request/reply roundtrip guarantees the server has registered
	// this connection before we push
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := cli.PeerList(ctx); err != nil {
		t.Fatal(err)
	}
	srv.PushPeerUpdate([]PeerInfo{{Identity: [32]byte{9}, Trust: proto.TrustNeutral}})
	select {
	case peers := <-cli.PeerUpdates():
		if len(peers) != 1 || peers[0].Identity != ([32]byte{9}) {
			t.Fatal("peer update mismatch")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("peer update never arrived")
	}
}

func TestClientDetectsDaemonExit(t *testing.T) {
	srv, cli, _ := testServerClient(t)
	srv.Close()
	select {
	case <-cli.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("client did not notice daemon exit")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := cli.Send(ctx, testEnvelope(t, "x", [32]byte{})); err == nil {
		t.Fatal("send succeeded after daemon exit")
	}
}

func TestFrameRoundtrip(t *testing.T) {
	var buf bytes.Buffer
	in := Frame{Type: IPCSend, RequestID: 42, Payload: []byte("payload")}
	if err := WriteFrame(&buf, in); err != nil {
		t.Fatal(err)
	}
	out, err := ReadFrame(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if out.Type != in.Type || out.RequestID != in.RequestID || !bytes.Equal(out.Payload, in.Payload) {
		t.Fatal("frame roundtrip mismatch")
	}
}

func TestFrameRejects(t *testing.T) {
	// wrong magic
	if _, err := ReadFrame(bytes.NewReader([]byte{0x00, 1, 0, 0, 0, 1, 0, 0, 0, 0})); !errors.Is(err, ErrBadFrame) {
		t.Fatalf("err = %v, want ErrBadFrame", err)
	}
	// oversize payload length
	hdr := []byte{frameMagic, 1, 0, 0, 0, 1, 0xFF, 0xFF, 0xFF, 0xFF}
	if _, err := ReadFrame(bytes.NewReader(hdr)); !errors.Is(err, ErrFrameTooBig) {
		t.Fatalf("err = %v, want ErrFrameTooBig", err)
	}
	// truncated payload
	var buf bytes.Buffer
	WriteFrame(&buf, Frame{Type: 1, Payload: []byte("full payload")})
	data := buf.Bytes()
	if _, err := ReadFrame(bytes.NewReader(data[:len(data)-3])); err == nil {
		t.Fatal("truncated frame accepted")
	}
	// oversize write
	if err := WriteFrame(&buf, Frame{Type: 1, Payload: make([]byte, maxFramePayload+1)}); !errors.Is(err, ErrFrameTooBig) {
		t.Fatalf("err = %v, want ErrFrameTooBig", err)
	}
}

func FuzzReadFrame(f *testing.F) {
	var buf bytes.Buffer
	WriteFrame(&buf, Frame{Type: IPCSend, RequestID: 7, Payload: []byte("seed")})
	f.Add(buf.Bytes())
	f.Add([]byte{})
	f.Fuzz(func(t *testing.T, data []byte) {
		ReadFrame(bytes.NewReader(data))
	})
}
