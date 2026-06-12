package network

import (
	"context"
	"crypto/ed25519"
	"fmt"
	"net"
	"net/url"

	iwt "github.com/Arceliar/ironwood/types"
	"github.com/yggdrasil-network/yggdrasil-go/src/config"
	"github.com/yggdrasil-network/yggdrasil-go/src/core"
)

// EmbeddedConfig configures an in-process Yggdrasil node.
type EmbeddedConfig struct {
	// PrivateKey is the Yggdrasil transport key. It is distinct from the
	// Gandr identity key by design: the federation handshake binds the
	// two, and no key is ever used across protocols.
	PrivateKey ed25519.PrivateKey
	// Listen are link-layer listener URIs other Yggdrasil nodes can peer
	// with, e.g. "tcp://0.0.0.0:4242" or "tcp://127.0.0.1:0" in tests.
	Listen []string
	// Peers are link-layer URIs of Yggdrasil nodes to peer with at
	// startup, e.g. the operator's chosen public peers or seed nodes.
	Peers []string
}

// EmbeddedTransport is a Transport backed by an in-process yggdrasil-go
// core. It requires no TUN device, no root, and no external daemon.
type EmbeddedTransport struct {
	core      *mux
	ygg       *core.Core
	listeners []*core.Listener
}

// yggCodec converts between ironwood addresses (raw Ed25519 node keys)
// and PeerAddr.
type yggCodec struct{}

func (yggCodec) toPeer(a net.Addr) (PeerAddr, bool) {
	key, ok := a.(iwt.Addr)
	if !ok || len(key) != ed25519.PublicKeySize {
		return PeerAddr{}, false
	}
	return PeerAddr{YggKey: append(ed25519.PublicKey(nil), key...)}, true
}

func (yggCodec) toNet(a PeerAddr) net.Addr {
	return iwt.Addr(a.YggKey)
}

// NewEmbedded starts an in-process Yggdrasil node and returns a
// Transport multiplexed over it.
func NewEmbedded(cfg EmbeddedConfig) (*EmbeddedTransport, error) {
	if len(cfg.PrivateKey) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("network: invalid yggdrasil private key length %d", len(cfg.PrivateKey))
	}
	ncfg := config.GenerateConfig()
	ncfg.PrivateKey = config.KeyBytes(cfg.PrivateKey)
	if err := ncfg.GenerateSelfSignedCertificate(); err != nil {
		return nil, fmt.Errorf("network: generating yggdrasil certificate: %w", err)
	}

	// nil logger: yggdrasil-go logs to io.Discard. No transport logs, by
	// design.
	ygg, err := core.New(ncfg.Certificate, nil)
	if err != nil {
		return nil, fmt.Errorf("network: starting yggdrasil core: %w", err)
	}

	t := &EmbeddedTransport{ygg: ygg}
	for _, l := range cfg.Listen {
		u, err := url.Parse(l)
		if err != nil {
			ygg.Stop()
			return nil, fmt.Errorf("network: invalid listen URI %q: %w", l, err)
		}
		listener, err := ygg.Listen(u, "")
		if err != nil {
			ygg.Stop()
			return nil, fmt.Errorf("network: listening on %q: %w", l, err)
		}
		t.listeners = append(t.listeners, listener)
	}
	for _, p := range cfg.Peers {
		u, err := url.Parse(p)
		if err != nil {
			ygg.Stop()
			return nil, fmt.Errorf("network: invalid peer URI %q: %w", p, err)
		}
		if err := ygg.AddPeer(u, ""); err != nil {
			ygg.Stop()
			return nil, fmt.Errorf("network: adding peer %q: %w", p, err)
		}
	}

	local := PeerAddr{YggKey: ygg.PublicKey()}
	t.core = newMux(yggAdapter{ygg}, yggCodec{}, int(ygg.MTU()), local)
	return t, nil
}

// yggAdapter adapts core.Core's Stop() to datagramConn's Close.
type yggAdapter struct{ *core.Core }

func (a yggAdapter) Close() error {
	a.Core.Stop()
	return nil
}

// Dial implements Transport.
func (t *EmbeddedTransport) Dial(ctx context.Context, addr PeerAddr) (Conn, error) {
	if len(addr.YggKey) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("network: embedded transport requires a yggdrasil key address")
	}
	return t.core.Dial(ctx, addr)
}

// Accept implements Transport.
func (t *EmbeddedTransport) Accept(ctx context.Context) (Conn, error) {
	return t.core.Accept(ctx)
}

// LocalAddr implements Transport.
func (t *EmbeddedTransport) LocalAddr() PeerAddr {
	return t.core.LocalAddr()
}

// Close implements Transport.
func (t *EmbeddedTransport) Close() error {
	return t.core.Close()
}

// ListenAddrs reports the actually bound link-layer listener addresses
// (host:port). Used by tests and by operators who listen on port 0.
func (t *EmbeddedTransport) ListenAddrs() []string {
	out := make([]string, 0, len(t.listeners))
	for _, l := range t.listeners {
		out = append(out, l.Addr().String())
	}
	return out
}

// PeerCount reports the number of established link-layer peerings.
func (t *EmbeddedTransport) PeerCount() int {
	n := 0
	for _, p := range t.ygg.GetPeers() {
		if p.Up {
			n++
		}
	}
	return n
}
