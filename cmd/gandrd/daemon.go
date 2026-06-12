package main

import (
	"context"
	"crypto/ed25519"
	"errors"
	"net"
	"sync"
	"time"

	"github.com/yggdrasil-network/yggdrasil-go/src/address"

	"github.com/gandr-net/gandr/pkg/federation"
	"github.com/gandr-net/gandr/pkg/identity"
	"github.com/gandr-net/gandr/pkg/ipc"
	"github.com/gandr-net/gandr/pkg/network"
	"github.com/gandr-net/gandr/pkg/proto"
	"github.com/gandr-net/gandr/pkg/store"
)

// userAgent is announced in federation handshakes.
const userAgent = "gandrd/0.1.0"

// Daemon wires transport, federation, storage, and IPC together.
type Daemon struct {
	cfg       Config
	id        *identity.Identity
	transport network.Transport
	table     *federation.Table
	objects   *store.Store
	ipcServer *ipc.Server
	fedCfg    federation.Config

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	mu       sync.Mutex
	profiles map[[32]byte][32]byte // pubkey -> latest profile content hash
	rates    map[[32]byte]*rateWindow
}

// NewDaemon assembles a daemon from loaded components. The caller
// provides the identity (already decrypted) and a started transport.
func NewDaemon(cfg Config, id *identity.Identity, transport network.Transport, objects *store.Store) (*Daemon, error) {
	defaultTrust, err := cfg.DefaultTrust()
	if err != nil {
		return nil, err
	}
	table, err := federation.NewTable(cfg.Peering.MaxPeers, defaultTrust)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	d := &Daemon{
		cfg:       cfg,
		id:        id,
		transport: transport,
		table:     table,
		objects:   objects,
		profiles:  make(map[[32]byte][32]byte),
		rates:     make(map[[32]byte]*rateWindow),
		ctx:       ctx,
		cancel:    cancel,
		fedCfg: federation.Config{
			Identity:     id.PrivateKey,
			Capabilities: cfg.Capabilities.Bitmask(),
			UserAgent:    userAgent,
			Policy: proto.PeerPolicyPayload{
				MaxMessageAge:  cfg.Limits.MaxMessageAge,
				MaxPayloadSize: cfg.Limits.MaxPayloadSize,
				RateLimitRPM:   cfg.Limits.RateLimitRPM,
				TrustLevel:     defaultTrust,
			},
		},
	}
	return d, nil
}

// Run starts all daemon loops and blocks until ctx is cancelled or a
// fatal error occurs.
func (d *Daemon) Run() error {
	srv, err := ipc.Listen(d.cfg.IPC.Socket, d)
	if err != nil {
		return err
	}
	d.ipcServer = srv

	d.wg.Add(2)
	go d.acceptLoop()
	go d.pruneLoop()
	d.connectSeeds()

	<-d.ctx.Done()
	return nil
}

// Stop shuts the daemon down.
func (d *Daemon) Stop() {
	d.cancel()
	if d.ipcServer != nil {
		d.ipcServer.Close()
	}
	d.transport.Close()
	d.wg.Wait()
}

// acceptLoop responds to inbound federation attempts.
func (d *Daemon) acceptLoop() {
	defer d.wg.Done()
	for {
		conn, err := d.transport.Accept(d.ctx)
		if err != nil {
			return
		}
		d.wg.Add(1)
		go func() {
			defer d.wg.Done()
			sess, err := federation.Respond(d.ctx, conn, d.fedCfg)
			if err != nil {
				// silence on invalid handshakes: close, say nothing
				conn.Close()
				return
			}
			d.adoptSession(sess)
		}()
	}
}

// connectSeeds attempts federation with configured seed nodes.
func (d *Daemon) connectSeeds() {
	seeds, err := d.cfg.SeedKeys()
	if err != nil {
		return
	}
	for _, key := range seeds {
		key := key
		d.wg.Add(1)
		go func() {
			defer d.wg.Done()
			conn, err := d.transport.Dial(d.ctx, network.PeerAddr{YggKey: ed25519.PublicKey(key)})
			if err != nil {
				return
			}
			sess, err := federation.Initiate(d.ctx, conn, d.fedCfg)
			if err != nil {
				conn.Close()
				return
			}
			d.adoptSession(sess)
		}()
	}
}

// adoptSession registers an established session and pumps its messages.
func (d *Daemon) adoptSession(sess *federation.Session) {
	if _, err := d.table.Add(sess); err != nil {
		sess.Close()
		return
	}
	d.notifyPeerUpdate()
	d.wg.Add(1)
	go func() {
		defer d.wg.Done()
		defer func() {
			d.table.Remove(sess.PeerIdentity)
			d.notifyPeerUpdate()
		}()
		for {
			env, err := sess.Recv(d.ctx)
			if err != nil {
				return
			}
			d.handleEnvelope(env, sess)
		}
	}()
}

// handleEnvelope processes one validated envelope from a peer. All
// rejections are silent.
func (d *Daemon) handleEnvelope(env *proto.Envelope, from *federation.Session) {
	if err := proto.ValidateTimestamp(env.Timestamp, time.Now()); err != nil {
		return
	}
	if len(env.Payload) > int(d.cfg.Limits.MaxPayloadSize) {
		return
	}
	if !d.allowRate(from.PeerIdentity) {
		return
	}
	d.table.Touch(from.PeerIdentity)

	switch env.Type {
	case proto.MsgChat, proto.MsgPost, proto.MsgThread, proto.MsgReply,
		proto.MsgProfile, proto.MsgGuestbook, proto.MsgStatus, proto.MsgSealed:
		if !d.validatePayload(env) {
			return
		}
		_, existed, err := d.objects.Put(env)
		if err != nil || existed {
			return // duplicates end the flood here
		}
		if env.Type == proto.MsgProfile {
			d.recordProfile(env)
		}
		d.relay(env, &from.PeerIdentity)
		d.ipcServer.Push(env)
	case proto.MsgAck, proto.MsgSealedAck:
		if !d.validatePayload(env) {
			return
		}
		d.relay(env, &from.PeerIdentity)
		d.ipcServer.Delivered(env)
	case proto.MsgDelete:
		if !d.handleDelete(env) {
			return
		}
		d.relay(env, &from.PeerIdentity)
		d.ipcServer.Push(env)
	case proto.MsgPeerIntro:
		// introductions are accepted only from vouched peers
		p, ok := d.table.Get(from.PeerIdentity)
		if !ok || !p.Vouched() {
			return
		}
		// v1 records nothing and does not auto-dial; organic discovery
		// arrives in a later milestone
	default:
		// handshake types outside a handshake, unknown types: drop
	}
}

// validatePayload decodes the payload against its schema; failures mean
// the envelope is structurally invalid and silently dropped.
func (d *Daemon) validatePayload(env *proto.Envelope) bool {
	p, err := proto.NewPayload(env.Type)
	if err != nil {
		return false
	}
	return proto.DecodePayload(env.Payload, p) == nil
}

// handleDelete enforces the only deletion rule: you delete your own
// content and nothing else.
func (d *Daemon) handleDelete(env *proto.Envelope) bool {
	del := &proto.DeletePayload{}
	if err := proto.DecodePayload(env.Payload, del); err != nil {
		return false
	}
	hash, err := store.ParseHash(del.TargetHash)
	if err != nil {
		return false
	}
	target, err := d.objects.Get(hash)
	if err != nil {
		return false
	}
	owner := target.Sender == env.Sender
	// guestbook entries may also be deleted by the profile owner
	if !owner && target.Type == proto.MsgGuestbook {
		gb := &proto.GuestbookPayload{}
		if err := proto.DecodePayload(target.Payload, gb); err == nil {
			owner = gb.TargetPubkey == env.Sender
		}
	}
	if !owner {
		return false
	}
	if err := d.objects.Delete(hash); err != nil {
		return false
	}
	return true
}

// recordProfile tracks the newest profile per pubkey.
func (d *Daemon) recordProfile(env *proto.Envelope) {
	hash := env.ContentID()
	d.mu.Lock()
	defer d.mu.Unlock()
	if existing, ok := d.profiles[env.Sender]; ok {
		// keep only the newest by timestamp
		if cur, err := d.objects.Get(existing); err == nil && cur.Timestamp >= env.Timestamp {
			return
		}
	}
	d.profiles[env.Sender] = hash
}

// relay floods an envelope to all relaying peers except its origin.
func (d *Daemon) relay(env *proto.Envelope, except *[32]byte) {
	if !d.cfg.Capabilities.Relay {
		return
	}
	for _, p := range d.table.List() {
		if p.Session == nil || p.Trust < proto.TrustNeutral {
			continue
		}
		if except != nil && p.Identity == *except {
			continue
		}
		sess := p.Session
		d.wg.Add(1)
		go func() {
			defer d.wg.Done()
			ctx, cancel := context.WithTimeout(d.ctx, 30*time.Second)
			defer cancel()
			sess.Send(ctx, env)
		}()
	}
}

// rateWindow is a fixed one-minute message counter.
type rateWindow struct {
	start time.Time
	count int
}

// allowRate enforces the per-peer rate limit.
func (d *Daemon) allowRate(peer [32]byte) bool {
	limit := int(d.cfg.Limits.RateLimitRPM)
	if limit <= 0 {
		return true
	}
	now := time.Now()
	d.mu.Lock()
	defer d.mu.Unlock()
	w, ok := d.rates[peer]
	if !ok || now.Sub(w.start) > time.Minute {
		d.rates[peer] = &rateWindow{start: now, count: 1}
		return true
	}
	w.count++
	return w.count <= limit
}

// notifyPeerUpdate pushes the current peer set to IPC clients.
func (d *Daemon) notifyPeerUpdate() {
	if d.ipcServer == nil {
		return
	}
	d.ipcServer.PushPeerUpdate(d.peerInfos())
}

func (d *Daemon) peerInfos() []ipc.PeerInfo {
	peers := d.table.List()
	out := make([]ipc.PeerInfo, 0, len(peers))
	for _, p := range peers {
		info := ipc.PeerInfo{
			Identity:     p.Identity,
			Trust:        p.Trust,
			Capabilities: p.Capabilities,
			UserAgent:    p.UserAgent,
			ConnectedAt:  p.ConnectedAt.Unix(),
			LastSeen:     p.LastSeen.Unix(),
		}
		if len(p.Addr.YggKey) == ed25519.PublicKeySize {
			if a := address.AddrForKey(p.Addr.YggKey); a != nil {
				info.Addr = net.IP(a[:]).String()
			}
		}
		out = append(out, info)
	}
	return out
}

// pruneLoop runs the nightly object prune.
func (d *Daemon) pruneLoop() {
	defer d.wg.Done()
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			maxAge := time.Duration(d.cfg.Limits.MaxMessageAge) * time.Second
			d.objects.Prune(maxAge, time.Now())
		case <-d.ctx.Done():
			return
		}
	}
}

// --- ipc.Handler ---

// HandleSend routes a signed envelope submitted by the local client.
func (d *Daemon) HandleSend(env *proto.Envelope) error {
	if err := proto.ValidateTimestamp(env.Timestamp, time.Now()); err != nil {
		return errors.New("stale timestamp")
	}
	if !d.validatePayload(env) {
		return errors.New("invalid payload")
	}
	switch env.Type {
	case proto.MsgChat, proto.MsgPost, proto.MsgThread, proto.MsgReply,
		proto.MsgProfile, proto.MsgGuestbook, proto.MsgStatus, proto.MsgSealed:
		if _, _, err := d.objects.Put(env); err != nil {
			return errors.New("storage failure")
		}
		if env.Type == proto.MsgProfile {
			d.recordProfile(env)
		}
		d.relay(env, nil)
		return nil
	case proto.MsgAck, proto.MsgSealedAck:
		d.relay(env, nil)
		return nil
	case proto.MsgDelete:
		if !d.handleDelete(env) {
			return errors.New("delete rejected")
		}
		d.relay(env, nil)
		return nil
	default:
		return errors.New("unroutable message type")
	}
}

// HandleFetch serves an object by hash.
func (d *Daemon) HandleFetch(hash [32]byte) (*proto.Envelope, error) {
	return d.objects.Get(hash)
}

// HandlePeerList reports current peers.
func (d *Daemon) HandlePeerList() []ipc.PeerInfo {
	return d.peerInfos()
}

// HandleTrust sets the local trust level for a peer.
func (d *Daemon) HandleTrust(identity [32]byte, level uint8) error {
	if err := d.table.SetTrust(identity, level); err != nil {
		return err
	}
	d.notifyPeerUpdate()
	return nil
}

// HandleConnect queues a federation attempt with a yggdrasil node key.
func (d *Daemon) HandleConnect(yggKey [32]byte) error {
	d.wg.Add(1)
	go func() {
		defer d.wg.Done()
		ctx, cancel := context.WithTimeout(d.ctx, 60*time.Second)
		defer cancel()
		conn, err := d.transport.Dial(ctx, network.PeerAddr{YggKey: ed25519.PublicKey(yggKey[:])})
		if err != nil {
			return
		}
		sess, err := federation.Initiate(ctx, conn, d.fedCfg)
		if err != nil {
			conn.Close()
			return
		}
		d.adoptSession(sess)
	}()
	return nil
}

// HandleProfile returns the newest known profile for a pubkey.
func (d *Daemon) HandleProfile(pubkey [32]byte) (*proto.Envelope, error) {
	d.mu.Lock()
	hash, ok := d.profiles[pubkey]
	d.mu.Unlock()
	if !ok {
		return nil, store.ErrNotFound
	}
	return d.objects.Get(hash)
}
