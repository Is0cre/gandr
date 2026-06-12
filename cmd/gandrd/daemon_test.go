package main

import (
	"context"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/gandr-net/gandr/pkg/crypto"
	"github.com/gandr-net/gandr/pkg/identity"
	"github.com/gandr-net/gandr/pkg/ipc"
	"github.com/gandr-net/gandr/pkg/network"
	"github.com/gandr-net/gandr/pkg/proto"
	"github.com/gandr-net/gandr/pkg/store"
)

// testNode is one running daemon plus its plumbing.
type testNode struct {
	daemon    *Daemon
	transport *network.EmbeddedTransport
	socket    string
}

func startNode(t *testing.T, listen, peers []string, seeds []string) *testNode {
	t.Helper()
	dir := t.TempDir()
	cfg := defaultConfig()
	cfg.Identity.Keyfile = filepath.Join(dir, "identity.key")
	cfg.Storage.Path = filepath.Join(dir, "objects")
	cfg.IPC.Socket = filepath.Join(dir, "gandr.sock")
	cfg.Peering.Seeds = seeds

	id, err := identity.Generate("node")
	if err != nil {
		t.Fatal(err)
	}
	_, yggPriv, err := crypto.GenerateIdentity()
	if err != nil {
		t.Fatal(err)
	}
	transport, err := network.NewEmbedded(network.EmbeddedConfig{
		PrivateKey: yggPriv,
		Listen:     listen,
		Peers:      peers,
	})
	if err != nil {
		t.Fatal(err)
	}
	objects, err := store.Open(cfg.Storage.Path)
	if err != nil {
		t.Fatal(err)
	}
	d, err := NewDaemon(cfg, id, transport, objects)
	if err != nil {
		t.Fatal(err)
	}
	go d.Run()
	t.Cleanup(d.Stop)
	return &testNode{daemon: d, transport: transport, socket: cfg.IPC.Socket}
}

// TestTwoDaemonsEndToEnd runs the full stack: two daemons federate over
// embedded Yggdrasil; a client posts a chat message through one
// daemon's Unix socket and another client receives it from the other
// daemon's socket.
func TestTwoDaemonsEndToEnd(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping full-stack test in -short mode")
	}

	a := startNode(t, []string{"tcp://127.0.0.1:0"}, nil, nil)

	// B peers with A at the yggdrasil link layer and federates with A's
	// transport key as its seed.
	seed := hex.EncodeToString(a.transport.LocalAddr().YggKey)
	b := startNode(t, nil,
		[]string{fmt.Sprintf("tcp://%s", a.transport.ListenAddrs()[0])},
		[]string{seed},
	)

	// wait for federation to establish on both sides
	deadline := time.Now().Add(30 * time.Second)
	for a.daemon.table.Count() == 0 || b.daemon.table.Count() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("daemons did not federate within 30s")
		}
		time.Sleep(100 * time.Millisecond)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// user clients, one per node, with their own identities
	userA, err := identity.Generate("aira")
	if err != nil {
		t.Fatal(err)
	}
	cliA, err := ipc.Dial(a.socket)
	if err != nil {
		t.Fatal(err)
	}
	defer cliA.Close()
	cliB, err := ipc.Dial(b.socket)
	if err != nil {
		t.Fatal(err)
	}
	defer cliB.Close()

	channel := [32]byte{0x6E}
	if err := cliB.Subscribe(ctx, channel); err != nil {
		t.Fatal(err)
	}

	// client A sends a chat to the channel through daemon A
	payload, err := proto.EncodePayload(&proto.ChatPayload{ChannelID: channel, Content: "across two daemons"})
	if err != nil {
		t.Fatal(err)
	}
	env, err := proto.NewEnvelope(userA.PrivateKey, proto.MsgChat, channel, payload)
	if err != nil {
		t.Fatal(err)
	}
	if err := cliA.Send(ctx, env); err != nil {
		t.Fatalf("client send: %v", err)
	}

	// client B receives it from daemon B
	select {
	case got := <-cliB.Incoming():
		if got.ContentID() != env.ContentID() {
			t.Fatal("received wrong envelope")
		}
		chat := &proto.ChatPayload{}
		if err := proto.DecodePayload(got.Payload, chat); err != nil {
			t.Fatal(err)
		}
		if chat.Content != "across two daemons" {
			t.Fatal("content mismatch")
		}
	case <-ctx.Done():
		t.Fatal("message never crossed the federation")
	}

	// the object is now stored on both nodes and fetchable from B
	fetched, err := cliB.Fetch(ctx, env.ContentID())
	if err != nil {
		t.Fatalf("fetch from B: %v", err)
	}
	if fetched.ContentID() != env.ContentID() {
		t.Fatal("fetched envelope differs")
	}

	// peer lists are visible over IPC
	peers, err := cliA.PeerList(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(peers) != 1 {
		t.Fatalf("peer list length = %d", len(peers))
	}

	// profiles propagate and are queryable from the remote node
	profPayload, err := proto.EncodePayload(&proto.ProfilePayload{DisplayName: "aira", UpdatedAt: time.Now().Unix()})
	if err != nil {
		t.Fatal(err)
	}
	profEnv, err := proto.NewEnvelope(userA.PrivateKey, proto.MsgProfile, proto.Broadcast, profPayload)
	if err != nil {
		t.Fatal(err)
	}
	if err := cliA.Send(ctx, profEnv); err != nil {
		t.Fatal(err)
	}
	profDeadline := time.Now().Add(15 * time.Second)
	for {
		if prof, err := cliB.Profile(ctx, userA.Pubkey()); err == nil {
			p := &proto.ProfilePayload{}
			if err := proto.DecodePayload(prof.Payload, p); err != nil || p.DisplayName != "aira" {
				t.Fatal("profile content mismatch")
			}
			break
		}
		if time.Now().After(profDeadline) {
			t.Fatal("profile never propagated")
		}
		time.Sleep(100 * time.Millisecond)
	}

	// deletion: author deletes their chat message; both stores drop it
	delPayload, err := proto.EncodePayload(&proto.DeletePayload{TargetHash: hex.EncodeToString(func() []byte { h := env.ContentID(); return h[:] }())})
	if err != nil {
		t.Fatal(err)
	}
	delEnv, err := proto.NewEnvelope(userA.PrivateKey, proto.MsgDelete, proto.Broadcast, delPayload)
	if err != nil {
		t.Fatal(err)
	}
	if err := cliA.Send(ctx, delEnv); err != nil {
		t.Fatalf("delete send: %v", err)
	}
	delDeadline := time.Now().Add(15 * time.Second)
	for b.daemon.objects.Has(env.ContentID()) {
		if time.Now().After(delDeadline) {
			t.Fatal("delete never propagated to B")
		}
		time.Sleep(100 * time.Millisecond)
	}
	if a.daemon.objects.Has(env.ContentID()) {
		t.Fatal("delete did not remove object on A")
	}
}

// TestDeleteRejectsForeignContent verifies nobody can delete content
// they did not author.
func TestDeleteRejectsForeignContent(t *testing.T) {
	a := startNode(t, []string{"tcp://127.0.0.1:0"}, nil, nil)

	author, err := identity.Generate("author")
	if err != nil {
		t.Fatal(err)
	}
	attacker, err := identity.Generate("attacker")
	if err != nil {
		t.Fatal(err)
	}

	payload, err := proto.EncodePayload(&proto.ChatPayload{Content: "mine"})
	if err != nil {
		t.Fatal(err)
	}
	env, err := proto.NewEnvelope(author.PrivateKey, proto.MsgChat, proto.Broadcast, payload)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.daemon.HandleSend(env); err != nil {
		t.Fatal(err)
	}

	hash := env.ContentID()
	delPayload, err := proto.EncodePayload(&proto.DeletePayload{TargetHash: hex.EncodeToString(hash[:])})
	if err != nil {
		t.Fatal(err)
	}
	delEnv, err := proto.NewEnvelope(attacker.PrivateKey, proto.MsgDelete, proto.Broadcast, delPayload)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.daemon.HandleSend(delEnv); err == nil {
		t.Fatal("foreign delete accepted")
	}
	if !a.daemon.objects.Has(hash) {
		t.Fatal("object deleted by non-author")
	}
}

// TestRateLimit verifies the per-peer rate limiter.
func TestRateLimit(t *testing.T) {
	a := startNode(t, []string{"tcp://127.0.0.1:0"}, nil, nil)
	a.daemon.cfg.Limits.RateLimitRPM = 5

	var peer [32]byte
	peer[0] = 1
	for i := 0; i < 5; i++ {
		if !a.daemon.allowRate(peer) {
			t.Fatalf("message %d blocked under the limit", i)
		}
	}
	if a.daemon.allowRate(peer) {
		t.Fatal("sixth message in a minute allowed at limit 5")
	}
	var other [32]byte
	other[0] = 2
	if !a.daemon.allowRate(other) {
		t.Fatal("rate limit leaked across peers")
	}
}
