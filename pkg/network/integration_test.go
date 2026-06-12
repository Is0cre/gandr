package network

import (
	"bytes"
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/gandr-net/gandr/pkg/crypto"
)

// TestYggdrasilLoopback is the build-order step 3 milestone: two
// embedded Yggdrasil cores on one host, peered over loopback TCP,
// exchanging Gandr transport messages end to end. No TUN, no root, no
// external daemon.
func TestYggdrasilLoopback(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping yggdrasil integration test in -short mode")
	}

	_, privA, err := crypto.GenerateIdentity()
	if err != nil {
		t.Fatal(err)
	}
	_, privB, err := crypto.GenerateIdentity()
	if err != nil {
		t.Fatal(err)
	}

	// Node A listens on an ephemeral loopback port.
	a, err := NewEmbedded(EmbeddedConfig{
		PrivateKey: privA,
		Listen:     []string{"tcp://127.0.0.1:0"},
	})
	if err != nil {
		t.Fatalf("starting node A: %v", err)
	}
	defer a.Close()

	addrs := a.ListenAddrs()
	if len(addrs) != 1 {
		t.Fatalf("node A listener count = %d", len(addrs))
	}

	// Node B peers with A over loopback.
	b, err := NewEmbedded(EmbeddedConfig{
		PrivateKey: privB,
		Peers:      []string{fmt.Sprintf("tcp://%s", addrs[0])},
	})
	if err != nil {
		t.Fatalf("starting node B: %v", err)
	}
	defer b.Close()

	// Wait for the link-layer peering to come up.
	deadline := time.Now().Add(15 * time.Second)
	for a.PeerCount() == 0 || b.PeerCount() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("yggdrasil peering did not come up within 15s")
		}
		time.Sleep(100 * time.Millisecond)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// B dials A by its Yggdrasil key and sends messages.
	conn, err := b.Dial(ctx, a.LocalAddr())
	if err != nil {
		t.Fatalf("Dial over yggdrasil: %v", err)
	}
	if err := conn.Send(ctx, []byte("bytes in one end")); err != nil {
		t.Fatalf("Send: %v", err)
	}

	accepted, err := a.Accept(ctx)
	if err != nil {
		t.Fatalf("Accept: %v", err)
	}
	if !bytes.Equal(accepted.RemoteAddr().YggKey, b.LocalAddr().YggKey) {
		t.Fatal("accepted connection reports wrong remote key")
	}
	got, err := accepted.Recv(ctx)
	if err != nil {
		t.Fatalf("Recv: %v", err)
	}
	if string(got) != "bytes in one end" {
		t.Fatalf("got %q", got)
	}

	// Reply path.
	if err := accepted.Send(ctx, []byte("come out the other")); err != nil {
		t.Fatalf("reply Send: %v", err)
	}
	reply, err := conn.Recv(ctx)
	if err != nil {
		t.Fatalf("reply Recv: %v", err)
	}
	if string(reply) != "come out the other" {
		t.Fatalf("got %q", reply)
	}

	// Max-size message exercises fragmentation over the real overlay.
	big := bytes.Repeat([]byte{0xA7}, MaxMessageSize)
	if err := conn.Send(ctx, big); err != nil {
		t.Fatalf("Send max-size: %v", err)
	}
	gotBig, err := accepted.Recv(ctx)
	if err != nil {
		t.Fatalf("Recv max-size: %v", err)
	}
	if !bytes.Equal(gotBig, big) {
		t.Fatal("max-size message corrupted over yggdrasil")
	}
}
