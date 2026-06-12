package network

import (
	"context"
	"encoding/binary"
	"net"
	"sync"
	"time"

	"github.com/gandr-net/gandr/pkg/crypto"
)

// Datagram sublayer ("gmp" — Gandr message protocol). Every datagram
// carries a fixed 13-byte header:
//
//	[magic:      1 byte ]  0x47 'G'
//	[frame_type: 1 byte ]  data / ack / ping / pong
//	[msg_id:     8 bytes]  random uint64, big-endian
//	[frag_index: 1 byte ]
//	[frag_count: 1 byte ]  >= 1
//	[epoch:      1 byte ]  random nonzero per mux lifetime
//
// DATA frames carry fragment payload. ACK acknowledges a fully
// reassembled message id. PING/PONG probe reachability.
//
// The epoch detects peer restarts: connections are keyed by node key,
// so without it a restarted peer's new handshake would be demuxed into
// the dead connection's inbox and silently swallowed. When a frame
// arrives bearing a different nonzero epoch than the one the conn was
// established with, the old conn is closed and a fresh one surfaces to
// Accept. Senders predating this field emit 0x00, which never triggers
// a reset — mixed versions behave as before.
const (
	frameMagic      = 0x47
	frameHeaderSize = 13

	frameData uint8 = 0x01
	frameAck  uint8 = 0x02
	framePing uint8 = 0x03
	framePong uint8 = 0x04

	// maxFragments bounds fragments per message. The max envelope needs
	// 2 at Yggdrasil's 65535 MTU; 4 leaves headroom for smaller MTUs.
	maxFragments = 4

	// reassemblyTimeout discards incomplete messages.
	reassemblyTimeout = 30 * time.Second
	// maxReassemblies bounds concurrent partial messages per peer.
	maxReassemblies = 64
	// dedupeWindow is how many delivered message ids are remembered per
	// peer to suppress retransmitted duplicates.
	dedupeWindow = 1024
	// inboxSize is the per-peer receive queue. When full, new messages
	// are dropped unacknowledged so the sender retries later.
	inboxSize = 128
	// acceptBacklog bounds connections waiting in Accept.
	acceptBacklog = 64

	// sendAttempts and sendBaseDelay shape the retransmission schedule:
	// 250ms, 500ms, 1s, 2s, 4s — then give up.
	sendAttempts  = 5
	sendBaseDelay = 250 * time.Millisecond
)

// datagramConn is the unreliable packet interface the mux runs over.
// *core.Core satisfies it; tests use an in-memory lossy fake.
type datagramConn interface {
	ReadFrom(p []byte) (int, net.Addr, error)
	WriteTo(p []byte, addr net.Addr) (int, error)
	Close() error
}

// addrCodec converts between net.Addr of the underlying packet layer
// and PeerAddr.
type addrCodec interface {
	toPeer(net.Addr) (PeerAddr, bool)
	toNet(PeerAddr) net.Addr
}

// mux multiplexes reliable per-peer message connections over one
// datagram socket.
type mux struct {
	pc      datagramConn
	codec   addrCodec
	mtu     int
	local   PeerAddr
	epoch   uint8 // random nonzero, stamped on every outgoing frame
	accepts chan *muxConn

	mu     sync.Mutex
	peers  map[string]*muxConn
	closed bool
	done   chan struct{}
}

func newMux(pc datagramConn, codec addrCodec, mtu int, local PeerAddr) *mux {
	m := &mux{
		pc:      pc,
		codec:   codec,
		mtu:     mtu,
		local:   local,
		epoch:   randomEpoch(),
		accepts: make(chan *muxConn, acceptBacklog),
		peers:   make(map[string]*muxConn),
		done:    make(chan struct{}),
	}
	go m.readLoop()
	return m
}

// randomEpoch picks the mux's lifetime marker: any nonzero byte (zero
// is reserved for senders that predate the field).
func randomEpoch() uint8 {
	for {
		if b := crypto.RandomBytes(1)[0]; b != 0 {
			return b
		}
	}
}

func (m *mux) LocalAddr() PeerAddr { return m.local }

func (m *mux) Close() error {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil
	}
	m.closed = true
	close(m.done)
	peers := make([]*muxConn, 0, len(m.peers))
	for _, c := range m.peers {
		peers = append(peers, c)
	}
	m.mu.Unlock()
	for _, c := range peers {
		c.Close()
	}
	return m.pc.Close()
}

// conn returns the connection for addr, creating it if needed. The
// second result reports whether the conn was newly created.
func (m *mux) conn(addr PeerAddr) (*muxConn, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return nil, false, ErrClosed
	}
	key := addr.mapKey()
	if c, ok := m.peers[key]; ok {
		return c, false, nil
	}
	c := &muxConn{
		mux:     m,
		peer:    addr,
		netAddr: m.codec.toNet(addr),
		inbox:   make(chan []byte, inboxSize),
		pending: make(map[uint64]chan struct{}),
		reasm:   make(map[uint64]*reassembly),
		seen:    newDedupe(dedupeWindow),
		closed:  make(chan struct{}),
	}
	m.peers[key] = c
	return c, true, nil
}

func (m *mux) removeConn(c *muxConn) {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := c.peer.mapKey()
	if m.peers[key] == c {
		delete(m.peers, key)
	}
}

// Dial returns a connection to addr after confirming reachability with
// a ping/pong exchange.
func (m *mux) Dial(ctx context.Context, addr PeerAddr) (Conn, error) {
	c, _, err := m.conn(addr)
	if err != nil {
		return nil, err
	}
	if err := c.ping(ctx); err != nil {
		return nil, err
	}
	return c, nil
}

// Accept returns the next connection initiated by a remote peer.
func (m *mux) Accept(ctx context.Context) (Conn, error) {
	select {
	case c := <-m.accepts:
		return c, nil
	case <-m.done:
		return nil, ErrClosed
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// readLoop is the single reader of the datagram socket. It demuxes
// frames to per-peer connections. Malformed frames are dropped
// silently, per protocol policy.
func (m *mux) readLoop() {
	buf := make([]byte, m.mtu+frameHeaderSize)
	for {
		n, from, err := m.pc.ReadFrom(buf)
		if err != nil {
			select {
			case <-m.done:
			default:
				m.Close()
			}
			return
		}
		if n < frameHeaderSize || buf[0] != frameMagic {
			continue
		}
		peer, ok := m.codec.toPeer(from)
		if !ok {
			continue
		}
		frameType := buf[1]
		msgID := binary.BigEndian.Uint64(buf[2:10])
		fragIndex, fragCount := buf[10], buf[11]
		epoch := buf[12]
		body := buf[frameHeaderSize:n]

		c, isNew, err := m.conn(peer)
		if err != nil {
			return
		}
		if !isNew && epoch != 0 && c.remoteEpoch != 0 && c.remoteEpoch != epoch {
			// the peer restarted: this conn's reader is wired to a dead
			// session and would swallow the new handshake. Retire it and
			// let the fresh conn surface to Accept below.
			c.Close()
			c, isNew, err = m.conn(peer)
			if err != nil {
				return
			}
		}
		if c.remoteEpoch == 0 {
			c.remoteEpoch = epoch
		}
		if isNew {
			select {
			case m.accepts <- c:
			default:
				// accept backlog full; the conn still exists and will be
				// visible to Dial, but is not surfaced to Accept
			}
		}
		switch frameType {
		case frameData:
			c.handleData(msgID, fragIndex, fragCount, body)
		case frameAck:
			c.handleSignal(msgID)
		case framePing:
			c.writeFrame(framePong, msgID, 0, 1, nil)
		case framePong:
			c.handleSignal(msgID)
		}
	}
}

// muxConn is one peer connection. It implements Conn.
type muxConn struct {
	mux     *mux
	peer    PeerAddr
	netAddr net.Addr
	inbox   chan []byte
	// remoteEpoch is the peer's epoch at establishment; 0 until the
	// first inbound frame (or forever, for pre-epoch senders). Touched
	// only by the mux readLoop.
	remoteEpoch uint8

	mu      sync.Mutex
	pending map[uint64]chan struct{} // msgID -> ack/pong signal
	reasm   map[uint64]*reassembly
	seen    *dedupe
	once    sync.Once
	closed  chan struct{}
}

// RemoteAddr implements Conn.
func (c *muxConn) RemoteAddr() PeerAddr { return c.peer }

// Close implements Conn. It tears down local state only; there is no
// wire-level close (the protocol is connectionless underneath).
func (c *muxConn) Close() error {
	c.once.Do(func() {
		close(c.closed)
		c.mux.removeConn(c)
	})
	return nil
}

// Recv implements Conn.
func (c *muxConn) Recv(ctx context.Context) ([]byte, error) {
	select {
	case msg := <-c.inbox:
		return msg, nil
	case <-c.closed:
		return nil, ErrClosed
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// Send implements Conn: fragment, transmit, await ack, retransmit with
// exponential backoff, give up after sendAttempts.
func (c *muxConn) Send(ctx context.Context, msg []byte) error {
	if len(msg) > MaxMessageSize {
		return ErrTooLarge
	}
	fragSize := c.mux.mtu - frameHeaderSize
	fragCount := (len(msg) + fragSize - 1) / fragSize
	if fragCount == 0 {
		fragCount = 1
	}
	if fragCount > maxFragments {
		return ErrTooLarge
	}
	msgID := randomID()
	return c.transmit(ctx, msgID, func() error {
		for i := 0; i < fragCount; i++ {
			start := i * fragSize
			end := min(start+fragSize, len(msg))
			if err := c.writeFrame(frameData, msgID, uint8(i), uint8(fragCount), msg[start:end]); err != nil {
				return err
			}
		}
		return nil
	}, ErrSendTimeout)
}

// ping confirms reachability via a pong echo, using the same
// retransmission schedule as Send.
func (c *muxConn) ping(ctx context.Context) error {
	msgID := randomID()
	return c.transmit(ctx, msgID, func() error {
		return c.writeFrame(framePing, msgID, 0, 1, nil)
	}, ErrUnreachable)
}

// transmit runs the shared retransmit-until-signalled loop.
func (c *muxConn) transmit(ctx context.Context, msgID uint64, write func() error, timeoutErr error) error {
	signal := make(chan struct{}, 1)
	c.mu.Lock()
	c.pending[msgID] = signal
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		delete(c.pending, msgID)
		c.mu.Unlock()
	}()

	delay := sendBaseDelay
	for attempt := 0; attempt < sendAttempts; attempt++ {
		if err := write(); err != nil {
			return err
		}
		timer := time.NewTimer(delay)
		select {
		case <-signal:
			timer.Stop()
			return nil
		case <-timer.C:
			delay *= 2
		case <-c.closed:
			timer.Stop()
			return ErrClosed
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		}
	}
	return timeoutErr
}

func (c *muxConn) writeFrame(frameType uint8, msgID uint64, fragIndex, fragCount uint8, body []byte) error {
	frame := make([]byte, frameHeaderSize+len(body))
	frame[0] = frameMagic
	frame[1] = frameType
	binary.BigEndian.PutUint64(frame[2:10], msgID)
	frame[10] = fragIndex
	frame[11] = fragCount
	frame[12] = c.mux.epoch
	copy(frame[frameHeaderSize:], body)
	_, err := c.mux.pc.WriteTo(frame, c.netAddr)
	return err
}

// handleSignal completes a pending Send or ping.
func (c *muxConn) handleSignal(msgID uint64) {
	c.mu.Lock()
	signal, ok := c.pending[msgID]
	c.mu.Unlock()
	if ok {
		select {
		case signal <- struct{}{}:
		default:
		}
	}
}

// handleData processes one DATA fragment: reassemble, dedupe, deliver,
// acknowledge. A message is acknowledged only once it has been handed
// to the inbox (or recognized as a duplicate of one that was); a full
// inbox means no ack, so the sender retries later — backpressure
// instead of silent loss.
func (c *muxConn) handleData(msgID uint64, fragIndex, fragCount uint8, body []byte) {
	if fragCount == 0 || fragCount > maxFragments || fragIndex >= fragCount {
		return
	}
	c.mu.Lock()
	if c.seen.has(msgID) {
		c.mu.Unlock()
		c.writeFrame(frameAck, msgID, 0, 1, nil)
		return
	}
	msg, complete := c.reassemble(msgID, fragIndex, fragCount, body)
	if !complete {
		c.mu.Unlock()
		return
	}
	if len(msg) > MaxMessageSize {
		delete(c.reasm, msgID)
		c.mu.Unlock()
		return
	}
	select {
	case c.inbox <- msg:
		c.seen.add(msgID)
		delete(c.reasm, msgID)
		c.mu.Unlock()
		c.writeFrame(frameAck, msgID, 0, 1, nil)
	default:
		// inbox full: keep reassembly state, skip the ack
		c.mu.Unlock()
	}
}

// reassembly tracks one partially received message.
type reassembly struct {
	frags   [][]byte
	have    int
	created time.Time
}

// reassemble records a fragment and returns the whole message when
// complete. Caller holds c.mu.
func (c *muxConn) reassemble(msgID uint64, fragIndex, fragCount uint8, body []byte) ([]byte, bool) {
	r, ok := c.reasm[msgID]
	if !ok {
		c.gcReassemblies()
		r = &reassembly{frags: make([][]byte, fragCount), created: time.Now()}
		c.reasm[msgID] = r
	}
	if int(fragCount) != len(r.frags) {
		// inconsistent sender; discard the whole attempt
		delete(c.reasm, msgID)
		return nil, false
	}
	if r.frags[fragIndex] == nil {
		r.frags[fragIndex] = append([]byte(nil), body...)
		r.have++
	}
	if r.have < len(r.frags) {
		return nil, false
	}
	var msg []byte
	for _, f := range r.frags {
		msg = append(msg, f...)
	}
	return msg, true
}

// gcReassemblies drops expired partial messages and, if still over
// capacity, the oldest one. Caller holds c.mu.
func (c *muxConn) gcReassemblies() {
	now := time.Now()
	for id, r := range c.reasm {
		if now.Sub(r.created) > reassemblyTimeout {
			delete(c.reasm, id)
		}
	}
	if len(c.reasm) < maxReassemblies {
		return
	}
	var oldestID uint64
	var oldest time.Time
	first := true
	for id, r := range c.reasm {
		if first || r.created.Before(oldest) {
			oldestID, oldest, first = id, r.created, false
		}
	}
	delete(c.reasm, oldestID)
}

// dedupe is a fixed-size set of recently delivered message ids.
type dedupe struct {
	ids   map[uint64]struct{}
	order []uint64
	next  int
}

func newDedupe(size int) *dedupe {
	return &dedupe{ids: make(map[uint64]struct{}, size), order: make([]uint64, size)}
}

func (d *dedupe) has(id uint64) bool {
	_, ok := d.ids[id]
	return ok
}

func (d *dedupe) add(id uint64) {
	if d.has(id) {
		return
	}
	if old := d.order[d.next]; old != 0 {
		delete(d.ids, old)
	}
	d.order[d.next] = id
	d.ids[id] = struct{}{}
	d.next = (d.next + 1) % len(d.order)
}

func randomID() uint64 {
	for {
		if id := binary.BigEndian.Uint64(crypto.RandomBytes(8)); id != 0 {
			return id
		}
	}
}
