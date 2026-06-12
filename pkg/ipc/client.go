package ipc

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"

	"github.com/vmihailenco/msgpack/v5"

	"github.com/gandr-net/gandr/pkg/proto"
)

// Client is the gandr client's connection to gandrd. It owns no network
// code; everything goes through the daemon socket.
type Client struct {
	conn    net.Conn
	writeMu sync.Mutex

	mu      sync.Mutex
	nextID  uint32
	waiting map[uint32]chan Frame
	closed  bool

	incoming  chan *proto.Envelope
	delivered chan *proto.Envelope
	peerInfo  chan []PeerInfo
	done      chan struct{}
}

// Dial connects to the daemon socket.
func Dial(socketPath string) (*Client, error) {
	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		return nil, fmt.Errorf("ipc: connecting to gandrd at %s: %w", socketPath, err)
	}
	c := &Client{
		conn:      conn,
		nextID:    1,
		waiting:   make(map[uint32]chan Frame),
		incoming:  make(chan *proto.Envelope, 256),
		delivered: make(chan *proto.Envelope, 64),
		peerInfo:  make(chan []PeerInfo, 8),
		done:      make(chan struct{}),
	}
	go c.readLoop()
	return c, nil
}

// Incoming streams envelopes pushed by the daemon.
func (c *Client) Incoming() <-chan *proto.Envelope { return c.incoming }

// Delivered streams delivery confirmations.
func (c *Client) Delivered() <-chan *proto.Envelope { return c.delivered }

// PeerUpdates streams peer table changes.
func (c *Client) PeerUpdates() <-chan []PeerInfo { return c.peerInfo }

// Done is closed when the connection to the daemon is lost.
func (c *Client) Done() <-chan struct{} { return c.done }

func (c *Client) readLoop() {
	defer c.teardown()
	for {
		f, err := ReadFrame(c.conn)
		if err != nil {
			return
		}
		switch f.Type {
		case IPCIncoming, IPCDelivered:
			env, err := proto.Decode(f.Payload)
			if err != nil {
				continue
			}
			ch := c.incoming
			if f.Type == IPCDelivered {
				ch = c.delivered
			}
			select {
			case ch <- env:
			default: // client not draining; drop rather than stall the daemon link
			}
		case IPCPeerUpdate:
			var peers []PeerInfo
			if err := msgpack.Unmarshal(f.Payload, &peers); err != nil {
				continue
			}
			select {
			case c.peerInfo <- peers:
			default:
			}
		default:
			c.mu.Lock()
			ch, ok := c.waiting[f.RequestID]
			if ok {
				delete(c.waiting, f.RequestID)
			}
			c.mu.Unlock()
			if ok {
				ch <- f
			}
		}
	}
}

func (c *Client) teardown() {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	c.closed = true
	waiting := c.waiting
	c.waiting = make(map[uint32]chan Frame)
	c.mu.Unlock()
	close(c.done)
	for _, ch := range waiting {
		close(ch)
	}
	c.conn.Close()
}

// request performs one request/reply roundtrip.
func (c *Client) request(ctx context.Context, frameType uint8, payload []byte) (Frame, error) {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return Frame{}, ErrClosed
	}
	id := c.nextID
	c.nextID++
	if c.nextID == 0 {
		c.nextID = 1
	}
	ch := make(chan Frame, 1)
	c.waiting[id] = ch
	c.mu.Unlock()

	c.writeMu.Lock()
	err := WriteFrame(c.conn, Frame{Type: frameType, RequestID: id, Payload: payload})
	c.writeMu.Unlock()
	if err != nil {
		c.mu.Lock()
		delete(c.waiting, id)
		c.mu.Unlock()
		return Frame{}, err
	}

	select {
	case f, ok := <-ch:
		if !ok {
			return Frame{}, ErrClosed
		}
		if f.Type == IPCError {
			var ep ErrorPayload
			if err := msgpack.Unmarshal(f.Payload, &ep); err != nil {
				return Frame{}, errors.New("ipc: daemon error")
			}
			return Frame{}, fmt.Errorf("ipc: daemon: %s", ep.Message)
		}
		return f, nil
	case <-ctx.Done():
		c.mu.Lock()
		delete(c.waiting, id)
		c.mu.Unlock()
		return Frame{}, ctx.Err()
	}
}

// Send submits a complete signed envelope for routing.
func (c *Client) Send(ctx context.Context, env *proto.Envelope) error {
	_, err := c.request(ctx, IPCSend, env.Encode())
	return err
}

// Subscribe registers interest in a channel.
func (c *Client) Subscribe(ctx context.Context, channel [32]byte) error {
	_, err := c.request(ctx, IPCSubscribe, channel[:])
	return err
}

// Unsubscribe removes interest in a channel.
func (c *Client) Unsubscribe(ctx context.Context, channel [32]byte) error {
	_, err := c.request(ctx, IPCUnsubscribe, channel[:])
	return err
}

// Fetch retrieves an envelope by content hash.
func (c *Client) Fetch(ctx context.Context, hash [32]byte) (*proto.Envelope, error) {
	f, err := c.request(ctx, IPCFetch, hash[:])
	if err != nil {
		return nil, err
	}
	return proto.Decode(f.Payload)
}

// PeerList reports the daemon's current peers.
func (c *Client) PeerList(ctx context.Context) ([]PeerInfo, error) {
	f, err := c.request(ctx, IPCPeerList, nil)
	if err != nil {
		return nil, err
	}
	var peers []PeerInfo
	if err := msgpack.Unmarshal(f.Payload, &peers); err != nil {
		return nil, fmt.Errorf("ipc: decoding peer list: %w", err)
	}
	return peers, nil
}

// Profile fetches the latest profile envelope for a pubkey.
func (c *Client) Profile(ctx context.Context, pubkey [32]byte) (*proto.Envelope, error) {
	f, err := c.request(ctx, IPCProfile, pubkey[:])
	if err != nil {
		return nil, err
	}
	return proto.Decode(f.Payload)
}

// SetTrust sets the daemon's local trust level for a peer identity.
func (c *Client) SetTrust(ctx context.Context, identity [32]byte, level uint8) error {
	payload := make([]byte, 33)
	copy(payload, identity[:])
	payload[32] = level
	_, err := c.request(ctx, IPCTrust, payload)
	return err
}

// Connect asks the daemon to federate with a yggdrasil node key.
func (c *Client) Connect(ctx context.Context, yggKey [32]byte) error {
	_, err := c.request(ctx, IPCConnect, yggKey[:])
	return err
}

// Close disconnects from the daemon.
func (c *Client) Close() error {
	c.teardown()
	return nil
}
