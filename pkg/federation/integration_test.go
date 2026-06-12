package federation

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/gandr-net/gandr/pkg/crypto"
	"github.com/gandr-net/gandr/pkg/network"
	"github.com/gandr-net/gandr/pkg/proto"
)

// TestPeeringOverYggdrasil is the proof-of-concept milestone from the
// build order: two nodes, each with its own identity and its own
// embedded Yggdrasil core, complete the full federation handshake over
// the real overlay and exchange encrypted content.
func TestPeeringOverYggdrasil(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping yggdrasil integration test in -short mode")
	}

	cfgA, _ := testConfig(t, proto.CapChat|proto.CapRelay)
	cfgB, pubB := testConfig(t, proto.CapChat|proto.CapStorage)

	// Yggdrasil transport keys are separate from identity keys.
	_, yggPrivA, err := crypto.GenerateIdentity()
	if err != nil {
		t.Fatal(err)
	}
	_, yggPrivB, err := crypto.GenerateIdentity()
	if err != nil {
		t.Fatal(err)
	}

	a, err := network.NewEmbedded(network.EmbeddedConfig{
		PrivateKey: yggPrivA,
		Listen:     []string{"tcp://127.0.0.1:0"},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	b, err := network.NewEmbedded(network.EmbeddedConfig{
		PrivateKey: yggPrivB,
		Peers:      []string{fmt.Sprintf("tcp://%s", a.ListenAddrs()[0])},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()

	deadline := time.Now().Add(15 * time.Second)
	for a.PeerCount() == 0 || b.PeerCount() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("yggdrasil peering did not come up")
		}
		time.Sleep(100 * time.Millisecond)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	type result struct {
		sess *Session
		err  error
	}
	resA := make(chan result, 1)
	go func() {
		conn, err := a.Accept(ctx)
		if err != nil {
			resA <- result{nil, err}
			return
		}
		s, err := Respond(ctx, conn, cfgA)
		resA <- result{s, err}
	}()

	conn, err := b.Dial(ctx, a.LocalAddr())
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	sessB, err := Initiate(ctx, conn, cfgB)
	if err != nil {
		t.Fatalf("Initiate over yggdrasil: %v", err)
	}
	ra := <-resA
	if ra.err != nil {
		t.Fatalf("Respond over yggdrasil: %v", ra.err)
	}
	sessA := ra.sess

	if sessA.PeerIdentity != [32]byte(pubB) {
		t.Fatal("responder learned wrong identity")
	}
	if sessA.key != sessB.key {
		t.Fatal("session keys diverge")
	}

	// Exchange a chat message through the encrypted session, both ways.
	chat, err := proto.EncodePayload(&proto.ChatPayload{Content: "two nodes, one network"})
	if err != nil {
		t.Fatal(err)
	}
	env, err := proto.NewEnvelope(cfgB.Identity, proto.MsgChat, sessB.PeerIdentity, chat)
	if err != nil {
		t.Fatal(err)
	}
	if err := sessB.Send(ctx, env); err != nil {
		t.Fatalf("Send: %v", err)
	}
	got, err := sessA.Recv(ctx)
	if err != nil {
		t.Fatalf("Recv: %v", err)
	}
	if got.ContentID() != env.ContentID() {
		t.Fatal("message mutated crossing the overlay")
	}

	reply, err := proto.EncodePayload(&proto.ChatPayload{Content: "the protocol works"})
	if err != nil {
		t.Fatal(err)
	}
	replyEnv, err := proto.NewEnvelope(cfgA.Identity, proto.MsgChat, sessA.PeerIdentity, reply)
	if err != nil {
		t.Fatal(err)
	}
	if err := sessA.Send(ctx, replyEnv); err != nil {
		t.Fatal(err)
	}
	gotReply, err := sessB.Recv(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if gotReply.ContentID() != replyEnv.ContentID() {
		t.Fatal("reply mutated crossing the overlay")
	}

	// Peer tables on both sides.
	tableA, err := NewTable(200, proto.TrustNeutral)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tableA.Add(sessA); err != nil {
		t.Fatal(err)
	}
	if p, ok := tableA.Get(sessA.PeerIdentity); !ok || p.Trust != proto.TrustNeutral {
		t.Fatal("peer table state wrong after live peering")
	}
}
