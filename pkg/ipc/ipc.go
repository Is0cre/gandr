// Package ipc implements the local protocol between gandrd and the
// gandr client over a Unix domain socket. The client never touches the
// network; the daemon never touches user keys. The client signs
// envelopes with the user identity and submits them complete; the
// daemon routes them. Incoming envelopes stream back unsolicited.
//
// Frame layout (all integers big-endian):
//
//	[magic:       1 byte ]  0x49 'I'
//	[type:        1 byte ]  IPC message type
//	[request_id:  4 bytes]  matches replies to requests; 0 = unsolicited
//	[payload_len: 4 bytes]
//	[payload:     variable]
//
// Replies reuse the request's type and id. Errors use IPCError with a
// msgpack ErrorPayload.
package ipc

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

// IPC message types.
const (
	IPCSend        uint8 = 0x01 // client -> daemon: signed envelope to route
	IPCSubscribe   uint8 = 0x02 // client -> daemon: subscribe to channel
	IPCUnsubscribe uint8 = 0x03 // client -> daemon: unsubscribe
	IPCFetch       uint8 = 0x04 // client -> daemon: fetch content by hash
	IPCPeerList    uint8 = 0x05 // client -> daemon: get peer list
	IPCProfile     uint8 = 0x06 // client -> daemon: get latest profile for pubkey
	IPCTrust       uint8 = 0x07 // client -> daemon: set peer trust level
	IPCConnect     uint8 = 0x08 // client -> daemon: federate with a yggdrasil node key
	IPCIncoming    uint8 = 0x80 // daemon -> client: incoming envelope
	IPCDelivered   uint8 = 0x81 // daemon -> client: delivery confirmation
	IPCPeerUpdate  uint8 = 0x82 // daemon -> client: peer status changed
	IPCError       uint8 = 0xFF // daemon -> client: error reply
)

const (
	frameMagic      = 0x49
	frameHeaderSize = 10
	// maxFramePayload bounds any IPC payload. Envelopes are < 64 KiB;
	// peer lists are small. 1 MiB is generous and still bounded.
	maxFramePayload = 1 << 20
)

// Framing errors.
var (
	ErrBadFrame     = errors.New("ipc: malformed frame")
	ErrFrameTooBig  = errors.New("ipc: frame payload too large")
	ErrClosed       = errors.New("ipc: connection closed")
)

// Frame is one IPC message.
type Frame struct {
	Type      uint8
	RequestID uint32
	Payload   []byte
}

// WriteFrame serializes one frame to w.
func WriteFrame(w io.Writer, f Frame) error {
	if len(f.Payload) > maxFramePayload {
		return ErrFrameTooBig
	}
	hdr := make([]byte, frameHeaderSize)
	hdr[0] = frameMagic
	hdr[1] = f.Type
	binary.BigEndian.PutUint32(hdr[2:6], f.RequestID)
	binary.BigEndian.PutUint32(hdr[6:10], uint32(len(f.Payload)))
	if _, err := w.Write(hdr); err != nil {
		return fmt.Errorf("ipc: writing frame header: %w", err)
	}
	if len(f.Payload) > 0 {
		if _, err := w.Write(f.Payload); err != nil {
			return fmt.Errorf("ipc: writing frame payload: %w", err)
		}
	}
	return nil
}

// ReadFrame reads one frame from r, enforcing size limits.
func ReadFrame(r io.Reader) (Frame, error) {
	var hdr [frameHeaderSize]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return Frame{}, err
	}
	if hdr[0] != frameMagic {
		return Frame{}, ErrBadFrame
	}
	f := Frame{
		Type:      hdr[1],
		RequestID: binary.BigEndian.Uint32(hdr[2:6]),
	}
	n := binary.BigEndian.Uint32(hdr[6:10])
	if n > maxFramePayload {
		return Frame{}, ErrFrameTooBig
	}
	if n > 0 {
		f.Payload = make([]byte, n)
		if _, err := io.ReadFull(r, f.Payload); err != nil {
			return Frame{}, fmt.Errorf("ipc: reading frame payload: %w", err)
		}
	}
	return f, nil
}

// ErrorPayload is the msgpack body of an IPCError frame.
type ErrorPayload struct {
	Message string `msgpack:"m"`
}

// PeerInfo is one entry in an IPCPeerList reply (msgpack array).
type PeerInfo struct {
	Identity     [32]byte `msgpack:"i"`
	Trust        uint8    `msgpack:"t"`
	Capabilities uint32   `msgpack:"c"`
	UserAgent    string   `msgpack:"u"`
	ConnectedAt  int64    `msgpack:"a"`
	LastSeen     int64    `msgpack:"l"`
	// Addr is the peer's Yggdrasil IPv6 address (200::/7), derived from
	// its transport node key. Display only.
	Addr string `msgpack:"d"`
}
