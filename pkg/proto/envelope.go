package proto

import (
	"crypto/ed25519"
	"encoding/binary"
	"errors"
	"fmt"
	"time"

	"github.com/gandr-net/gandr/pkg/crypto"
)

// Envelope field sizes and limits.
const (
	// HeaderSize is the length of all fixed fields before the payload.
	HeaderSize = 1 + 1 + 8 + 32 + 32 + 4
	// MaxPayloadSize is the protocol-wide upper bound on payload bytes.
	MaxPayloadSize = 65535
	// MinMessageSize is an envelope with an empty payload.
	MinMessageSize = HeaderSize + crypto.SignatureSize
	// MaxMessageSize is an envelope with a maximum payload.
	MaxMessageSize = HeaderSize + MaxPayloadSize + crypto.SignatureSize
)

// Timestamp acceptance window. Non-negotiable, not configurable.
const (
	// MaxMessageAge is how far in the past a live message may be stamped.
	MaxMessageAge = 120 * time.Second
	// MaxClockSkew is how far in the future a message may be stamped.
	MaxClockSkew = 10 * time.Second
)

// Envelope decoding and validation errors. Callers at network
// boundaries treat all of them identically: drop silently.
var (
	ErrTooShort         = errors.New("proto: message too short")
	ErrTooLong          = errors.New("proto: message too long")
	ErrBadVersion       = errors.New("proto: unsupported protocol version")
	ErrBadType          = errors.New("proto: unknown message type")
	ErrLocalOnlyType    = errors.New("proto: local-only message type on the wire")
	ErrBadLength        = errors.New("proto: payload length field mismatch")
	ErrBadSignature     = errors.New("proto: invalid signature")
	ErrTimestampOld     = errors.New("proto: timestamp too old")
	ErrTimestampFuture  = errors.New("proto: timestamp in the future")
	ErrPayloadTooLarge  = errors.New("proto: payload exceeds maximum size")
)

// Broadcast is the all-zero recipient used for broadcast messages.
var Broadcast [32]byte

// Envelope is a decoded wire message. The signature covers every field
// except itself; the payload is opaque at this layer.
type Envelope struct {
	Version   uint8
	Type      uint8
	Timestamp int64 // unix nanoseconds
	Sender    [32]byte
	Recipient [32]byte
	Payload   []byte
	Signature [64]byte
}

// header serializes the fixed-size prefix plus payload — exactly the
// bytes the signature covers.
func (e *Envelope) signedBytes() []byte {
	b := make([]byte, HeaderSize, HeaderSize+len(e.Payload))
	b[0] = e.Version
	b[1] = e.Type
	binary.BigEndian.PutUint64(b[2:10], uint64(e.Timestamp))
	copy(b[10:42], e.Sender[:])
	copy(b[42:74], e.Recipient[:])
	binary.BigEndian.PutUint32(b[74:78], uint32(len(e.Payload)))
	return append(b, e.Payload...)
}

// SigningDigest returns the SHA-256 digest the envelope signature is
// computed over.
func (e *Envelope) SigningDigest() [32]byte {
	return crypto.Digest(e.signedBytes())
}

// ContentID returns the canonical content address of the message:
// SHA256(version || message_type || timestamp || sender_pubkey || payload).
// The recipient and signature are deliberately excluded.
func (e *Envelope) ContentID() [32]byte {
	var ts [8]byte
	binary.BigEndian.PutUint64(ts[:], uint64(e.Timestamp))
	return crypto.Digest(
		[]byte{e.Version, e.Type},
		ts[:],
		e.Sender[:],
		e.Payload,
	)
}

// NewEnvelope builds and signs an envelope from the sender's identity
// key. The timestamp is stamped at call time.
func NewEnvelope(senderPriv ed25519.PrivateKey, msgType uint8, recipient [32]byte, payload []byte) (*Envelope, error) {
	if len(senderPriv) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("proto: invalid sender key length %d", len(senderPriv))
	}
	if len(payload) > MaxPayloadSize {
		return nil, ErrPayloadTooLarge
	}
	if msgType == 0 || msgType > maxMsgType {
		return nil, ErrBadType
	}
	if LocalOnly(msgType) {
		return nil, ErrLocalOnlyType
	}
	e := &Envelope{
		Version:   Version,
		Type:      msgType,
		Timestamp: time.Now().UnixNano(),
		Recipient: recipient,
		Payload:   payload,
	}
	copy(e.Sender[:], senderPriv.Public().(ed25519.PublicKey))
	digest := e.SigningDigest()
	sig, err := crypto.Sign(senderPriv, digest[:])
	if err != nil {
		return nil, err
	}
	copy(e.Signature[:], sig)
	return e, nil
}

// Encode serializes the envelope to wire bytes.
func (e *Envelope) Encode() []byte {
	signed := e.signedBytes()
	out := make([]byte, 0, len(signed)+crypto.SignatureSize)
	out = append(out, signed...)
	return append(out, e.Signature[:]...)
}

// Decode parses and validates wire bytes into an Envelope. It checks
// framing, version, type, and the signature — in that order — and
// returns an error on any violation. Receivers MUST treat any error as
// "drop silently": no response, no log entry.
//
// Decode verifies the signature before the payload is ever handed to a
// deserializer; the payload bytes returned are still opaque and must be
// decoded with DecodePayload by the layer that knows the expected type.
func Decode(data []byte) (*Envelope, error) {
	if len(data) < MinMessageSize {
		return nil, ErrTooShort
	}
	if len(data) > MaxMessageSize {
		return nil, ErrTooLong
	}
	e := &Envelope{
		Version:   data[0],
		Type:      data[1],
		Timestamp: int64(binary.BigEndian.Uint64(data[2:10])),
	}
	copy(e.Sender[:], data[10:42])
	copy(e.Recipient[:], data[42:74])
	payloadLen := binary.BigEndian.Uint32(data[74:78])

	if e.Version != Version {
		return nil, ErrBadVersion
	}
	if e.Type == 0 || e.Type > maxMsgType {
		return nil, ErrBadType
	}
	if LocalOnly(e.Type) {
		return nil, ErrLocalOnlyType
	}
	if payloadLen > MaxPayloadSize {
		return nil, ErrPayloadTooLarge
	}
	if len(data) != HeaderSize+int(payloadLen)+crypto.SignatureSize {
		return nil, ErrBadLength
	}
	e.Payload = make([]byte, payloadLen)
	copy(e.Payload, data[HeaderSize:HeaderSize+payloadLen])
	copy(e.Signature[:], data[HeaderSize+payloadLen:])

	digest := e.SigningDigest()
	if !crypto.Verify(e.Sender[:], digest[:], e.Signature[:]) {
		return nil, ErrBadSignature
	}
	return e, nil
}

// ValidateTimestamp enforces the replay window against the given
// reference time: stamps older than MaxMessageAge or more than
// MaxClockSkew in the future are rejected.
func ValidateTimestamp(tsNanos int64, now time.Time) error {
	ts := time.Unix(0, tsNanos)
	if now.Sub(ts) > MaxMessageAge {
		return ErrTimestampOld
	}
	if ts.Sub(now) > MaxClockSkew {
		return ErrTimestampFuture
	}
	return nil
}
