package network

import (
	"bytes"
	"context"
	"errors"
	"math/rand"
	"net"
	"sync"
	"testing"
	"time"
)

// fakeAddr is a simple string-keyed net.Addr for the in-memory fabric.
type fakeAddr string

func (a fakeAddr) Network() string { return "fake" }
func (a fakeAddr) String() string  { return string(a) }

type fakeCodec struct{}

func (fakeCodec) toPeer(a net.Addr) (PeerAddr, bool) {
	fa, ok := a.(fakeAddr)
	if !ok {
		return PeerAddr{}, false
	}
	return PeerAddr{YggKey: []byte(fa)}, true
}

func (fakeCodec) toNet(a PeerAddr) net.Addr { return fakeAddr(a.YggKey) }

// fabric is an in-memory datagram network with optional packet loss.
type fabric struct {
	mu    sync.Mutex
	ports map[string]*fakePort
	loss  float64 // probability of dropping any datagram
	rng   *rand.Rand
}

func newFabric(loss float64) *fabric {
	return &fabric{ports: make(map[string]*fakePort), loss: loss, rng: rand.New(rand.NewSource(42))}
}

type packet struct {
	data []byte
	from fakeAddr
}

type fakePort struct {
	f      *fabric
	addr   fakeAddr
	inbox  chan packet
	closed chan struct{}
	once   sync.Once
}

func (f *fabric) port(name string) *fakePort {
	f.mu.Lock()
	defer f.mu.Unlock()
	p := &fakePort{f: f, addr: fakeAddr(name), inbox: make(chan packet, 1024), closed: make(chan struct{})}
	f.ports[name] = p
	return p
}

func (p *fakePort) ReadFrom(b []byte) (int, net.Addr, error) {
	select {
	case pkt := <-p.inbox:
		n := copy(b, pkt.data)
		return n, pkt.from, nil
	case <-p.closed:
		return 0, nil, errors.New("closed")
	}
}

func (p *fakePort) WriteTo(b []byte, addr net.Addr) (int, error) {
	p.f.mu.Lock()
	dst := p.f.ports[addr.String()]
	drop := p.f.rng.Float64() < p.f.loss
	p.f.mu.Unlock()
	if dst == nil || drop {
		return len(b), nil // silently dropped, like a real network
	}
	data := append([]byte(nil), b...)
	select {
	case dst.inbox <- packet{data: data, from: p.addr}:
	default:
	}
	return len(b), nil
}

func (p *fakePort) Close() error {
	p.once.Do(func() { close(p.closed) })
	return nil
}

func testMuxPair(t *testing.T, loss float64, mtu int) (*mux, *mux) {
	t.Helper()
	f := newFabric(loss)
	pa := f.port("a")
	pb := f.port("b")
	ma := newMux(pa, fakeCodec{}, mtu, PeerAddr{YggKey: []byte("a")})
	mb := newMux(pb, fakeCodec{}, mtu, PeerAddr{YggKey: []byte("b")})
	t.Cleanup(func() { ma.Close(); mb.Close() })
	return ma, mb
}

func TestMuxBasicExchange(t *testing.T) {
	ma, mb := testMuxPair(t, 0, 65535)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	connAB, err := ma.Dial(ctx, PeerAddr{YggKey: []byte("b")})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	if err := connAB.Send(ctx, []byte("hello over the void")); err != nil {
		t.Fatalf("Send: %v", err)
	}

	connBA, err := mb.Accept(ctx)
	if err != nil {
		t.Fatalf("Accept: %v", err)
	}
	if got := connBA.RemoteAddr().mapKey(); got != "a" {
		t.Fatalf("remote addr = %q", got)
	}
	msg, err := connBA.Recv(ctx)
	if err != nil {
		t.Fatalf("Recv: %v", err)
	}
	if string(msg) != "hello over the void" {
		t.Fatalf("got %q", msg)
	}

	// reply on the accepted conn
	if err := connBA.Send(ctx, []byte("ack from b")); err != nil {
		t.Fatalf("reply Send: %v", err)
	}
	reply, err := connAB.Recv(ctx)
	if err != nil {
		t.Fatalf("reply Recv: %v", err)
	}
	if string(reply) != "ack from b" {
		t.Fatalf("got %q", reply)
	}
}

func TestMuxFragmentation(t *testing.T) {
	// Small MTU forces multi-fragment messages.
	ma, mb := testMuxPair(t, 0, 1024)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	conn, err := ma.Dial(ctx, PeerAddr{YggKey: []byte("b")})
	if err != nil {
		t.Fatal(err)
	}
	msg := bytes.Repeat([]byte{0xAB, 0xCD}, 1500) // 3000 bytes -> 3 frags
	if err := conn.Send(ctx, msg); err != nil {
		t.Fatalf("Send: %v", err)
	}
	accepted, err := mb.Accept(ctx)
	if err != nil {
		t.Fatal(err)
	}
	got, err := accepted.Recv(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, msg) {
		t.Fatal("fragmented message corrupted")
	}
}

func TestMuxMaxSizeMessage(t *testing.T) {
	ma, mb := testMuxPair(t, 0, 65535)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	conn, err := ma.Dial(ctx, PeerAddr{YggKey: []byte("b")})
	if err != nil {
		t.Fatal(err)
	}
	msg := bytes.Repeat([]byte{0x42}, MaxMessageSize) // needs 2 fragments
	if err := conn.Send(ctx, msg); err != nil {
		t.Fatalf("Send max-size: %v", err)
	}
	accepted, err := mb.Accept(ctx)
	if err != nil {
		t.Fatal(err)
	}
	got, err := accepted.Recv(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, msg) {
		t.Fatal("max-size message corrupted")
	}

	if err := conn.Send(ctx, make([]byte, MaxMessageSize+1)); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("oversize send err = %v, want ErrTooLarge", err)
	}
}

func TestMuxLossyNetwork(t *testing.T) {
	// 20% datagram loss: retransmission must still deliver everything,
	// and dedupe must prevent duplicates.
	ma, mb := testMuxPair(t, 0.20, 4096)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	conn, err := ma.Dial(ctx, PeerAddr{YggKey: []byte("b")})
	if err != nil {
		t.Fatal(err)
	}
	accepted, err := mb.Accept(ctx)
	if err != nil {
		t.Fatal(err)
	}

	const count = 20
	var wg sync.WaitGroup
	sendErrs := make([]error, count)
	for i := 0; i < count; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			msg := bytes.Repeat([]byte{byte(i)}, 3000+i) // multi-fragment
			sendErrs[i] = conn.Send(ctx, msg)
		}(i)
	}

	received := make(map[byte]int)
	for i := 0; i < count; i++ {
		msg, err := accepted.Recv(ctx)
		if err != nil {
			t.Fatalf("Recv %d: %v", i, err)
		}
		if len(msg) < 3000 {
			t.Fatalf("short message: %d", len(msg))
		}
		received[msg[0]]++
	}
	wg.Wait()
	for i, err := range sendErrs {
		if err != nil {
			t.Errorf("send %d: %v", i, err)
		}
	}
	for b, n := range received {
		if n != 1 {
			t.Errorf("message %d delivered %d times", b, n)
		}
	}
}

func TestMuxDialUnreachable(t *testing.T) {
	ma, _ := testMuxPair(t, 1.0, 65535) // 100% loss
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := ma.Dial(ctx, PeerAddr{YggKey: []byte("b")}); !errors.Is(err, ErrUnreachable) {
		t.Fatalf("err = %v, want ErrUnreachable", err)
	}
}

func TestMuxSendTimeout(t *testing.T) {
	f := newFabric(0)
	pa := f.port("a")
	_ = f.port("void") // exists but never acks: give it a mux-less port
	ma := newMux(pa, fakeCodec{}, 65535, PeerAddr{YggKey: []byte("a")})
	t.Cleanup(func() { ma.Close() })

	c, _, err := ma.conn(PeerAddr{YggKey: []byte("void")})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	start := time.Now()
	if err := c.Send(ctx, []byte("into the void")); !errors.Is(err, ErrSendTimeout) {
		t.Fatalf("err = %v, want ErrSendTimeout", err)
	}
	if time.Since(start) < 3*time.Second {
		t.Fatal("gave up too quickly — retransmission schedule not honored")
	}
}

func TestMuxContextCancel(t *testing.T) {
	ma, _ := testMuxPair(t, 1.0, 65535)
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(100 * time.Millisecond)
		cancel()
	}()
	if _, err := ma.Dial(ctx, PeerAddr{YggKey: []byte("b")}); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

func TestMuxClose(t *testing.T) {
	ma, mb := testMuxPair(t, 0, 65535)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, err := ma.Dial(ctx, PeerAddr{YggKey: []byte("b")})
	if err != nil {
		t.Fatal(err)
	}
	if err := ma.Close(); err != nil {
		t.Fatal(err)
	}
	if err := conn.Send(ctx, []byte("x")); err == nil {
		t.Fatal("send succeeded on closed transport")
	}
	if _, err := ma.Dial(ctx, PeerAddr{YggKey: []byte("b")}); !errors.Is(err, ErrClosed) {
		t.Fatalf("Dial after close: %v", err)
	}
	// b still holds a's earlier dial in its accept queue; drain it, then
	// a fresh Accept must block until its context expires.
	if _, err := mb.Accept(ctx); err != nil {
		t.Fatalf("draining accept queue: %v", err)
	}
	shortCtx, shortCancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer shortCancel()
	if _, err := mb.Accept(shortCtx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Accept on idle mux: %v, want deadline exceeded", err)
	}
	if err := mb.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := mb.Accept(ctx); !errors.Is(err, ErrClosed) {
		t.Fatalf("Accept after close: %v, want ErrClosed", err)
	}
}

func TestMuxGarbageFrames(t *testing.T) {
	// Hostile datagrams must not panic the read loop or fabricate
	// messages.
	f := newFabric(0)
	pa := f.port("a")
	pb := f.port("b")
	ma := newMux(pa, fakeCodec{}, 65535, PeerAddr{YggKey: []byte("a")})
	t.Cleanup(func() { ma.Close() })

	garbage := [][]byte{
		nil,
		{0x00},
		{0x47},                          // magic only, too short
		bytes.Repeat([]byte{0x47}, 12),  // one byte short of a header
		{0x47, 0xFF, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1, 0}, // unknown frame type
		{0x47, 0x01, 0, 0, 0, 0, 0, 0, 0, 1, 5, 2, 0}, // fragIndex >= fragCount
		{0x47, 0x01, 0, 0, 0, 0, 0, 0, 0, 1, 0, 0, 0}, // fragCount == 0
		{0x47, 0x01, 0, 0, 0, 0, 0, 0, 0, 1, 0, 200, 0}, // fragCount > max
		{0x99, 0x01, 0, 0, 0, 0, 0, 0, 0, 1, 0, 1, 0}, // wrong magic
	}
	for _, g := range garbage {
		pb.WriteTo(g, fakeAddr("a"))
	}
	// transport must still work after the garbage barrage
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	mb := newMux(pb, fakeCodec{}, 65535, PeerAddr{YggKey: []byte("b")})
	t.Cleanup(func() { mb.Close() })
	conn, err := mb.Dial(ctx, PeerAddr{YggKey: []byte("a")})
	if err != nil {
		t.Fatalf("Dial after garbage: %v", err)
	}
	if err := conn.Send(ctx, []byte("still alive")); err != nil {
		t.Fatalf("Send after garbage: %v", err)
	}
}

func TestDedupe(t *testing.T) {
	d := newDedupe(4)
	for i := uint64(1); i <= 4; i++ {
		d.add(i)
	}
	for i := uint64(1); i <= 4; i++ {
		if !d.has(i) {
			t.Fatalf("id %d evicted too early", i)
		}
	}
	d.add(5) // evicts 1
	if d.has(1) {
		t.Fatal("oldest id not evicted")
	}
	if !d.has(5) || !d.has(2) {
		t.Fatal("wrong eviction")
	}
	d.add(5) // re-add must not corrupt the ring
	d.add(6)
	d.add(7)
	if !d.has(5) {
		t.Fatal("ring corrupted by duplicate add")
	}
}
