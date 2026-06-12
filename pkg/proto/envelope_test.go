package proto

import (
	"bytes"
	"errors"
	"testing"
	"time"

	"github.com/gandr-net/gandr/pkg/crypto"
)

func testIdentity(t *testing.T) ([]byte, []byte) {
	t.Helper()
	pub, priv, err := crypto.GenerateIdentity()
	if err != nil {
		t.Fatal(err)
	}
	return pub, priv
}

func TestEnvelopeRoundtrip(t *testing.T) {
	pub, priv := testIdentity(t)
	var recipient [32]byte
	copy(recipient[:], bytes.Repeat([]byte{0xAB}, 32))

	tests := []struct {
		name    string
		msgType uint8
		payload []byte
	}{
		{"chat with payload", MsgChat, []byte("payload bytes")},
		{"empty payload", MsgAck, nil},
		{"max payload", MsgPost, bytes.Repeat([]byte{0x42}, MaxPayloadSize)},
		{"binary payload", MsgSealed, []byte{0x00, 0xFF, 0x00}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e, err := NewEnvelope(priv, tt.msgType, recipient, tt.payload)
			if err != nil {
				t.Fatalf("NewEnvelope: %v", err)
			}
			wire := e.Encode()
			if len(wire) != HeaderSize+len(tt.payload)+crypto.SignatureSize {
				t.Fatalf("wire length = %d", len(wire))
			}
			got, err := Decode(wire)
			if err != nil {
				t.Fatalf("Decode: %v", err)
			}
			if got.Version != Version || got.Type != tt.msgType {
				t.Error("version/type mismatch")
			}
			if !bytes.Equal(got.Sender[:], pub) {
				t.Error("sender mismatch")
			}
			if got.Recipient != recipient {
				t.Error("recipient mismatch")
			}
			if !bytes.Equal(got.Payload, tt.payload) {
				t.Error("payload mismatch")
			}
			if got.Timestamp != e.Timestamp {
				t.Error("timestamp mismatch")
			}
			if got.ContentID() != e.ContentID() {
				t.Error("content ID changed across roundtrip")
			}
		})
	}
}

func TestNewEnvelopeRejects(t *testing.T) {
	_, priv := testIdentity(t)
	var r [32]byte
	tests := []struct {
		name    string
		priv    []byte
		msgType uint8
		payload []byte
		wantErr error
	}{
		{"bad key", make([]byte, 9), MsgChat, nil, nil},
		{"oversize payload", priv, MsgChat, make([]byte, MaxPayloadSize+1), ErrPayloadTooLarge},
		{"zero type", priv, 0x00, nil, ErrBadType},
		{"unknown type", priv, 0x7F, nil, ErrBadType},
		{"local-only block", priv, MsgBlock, nil, ErrLocalOnlyType},
		{"local-only nickname", priv, MsgNickname, nil, ErrLocalOnlyType},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewEnvelope(tt.priv, tt.msgType, r, tt.payload)
			if err == nil {
				t.Fatal("expected error")
			}
			if tt.wantErr != nil && !errors.Is(err, tt.wantErr) {
				t.Fatalf("err = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestDecodeRejects(t *testing.T) {
	_, priv := testIdentity(t)
	var r [32]byte
	e, err := NewEnvelope(priv, MsgChat, r, []byte("hello"))
	if err != nil {
		t.Fatal(err)
	}
	wire := e.Encode()

	mutate := func(idx int) []byte {
		bad := append([]byte{}, wire...)
		bad[idx] ^= 0x01
		return bad
	}

	tests := []struct {
		name    string
		data    []byte
		wantErr error
	}{
		{"empty", nil, ErrTooShort},
		{"truncated", wire[:MinMessageSize-1], ErrTooShort},
		{"oversize", make([]byte, MaxMessageSize+1), ErrTooLong},
		{"wrong version", mutate(0), ErrBadVersion},
		{"flipped type", mutate(1), ErrBadSignature}, // 0x01->0x00 is caught... see below
		{"flipped timestamp", mutate(5), ErrBadSignature},
		{"flipped sender", mutate(10), ErrBadSignature},
		{"flipped recipient", mutate(42), ErrBadSignature},
		{"flipped payload", mutate(HeaderSize), ErrBadSignature},
		{"flipped signature", mutate(len(wire) - 1), ErrBadSignature},
		{"length field mismatch", mutate(77), ErrBadLength},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Decode(tt.data)
			if err == nil {
				t.Fatal("expected error")
			}
			// flipping the type byte of 0x01 yields 0x00 -> ErrBadType
			// before the signature check; accept either rejection
			if tt.name == "flipped type" {
				if !errors.Is(err, ErrBadType) && !errors.Is(err, ErrBadSignature) {
					t.Fatalf("err = %v", err)
				}
				return
			}
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("err = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestDecodeRejectsLocalOnlyOnWire(t *testing.T) {
	// Hand-craft a wire message with a local-only type and a valid
	// signature; Decode must still refuse it.
	_, priv := testIdentity(t)
	var r [32]byte
	e, err := NewEnvelope(priv, MsgChat, r, nil)
	if err != nil {
		t.Fatal(err)
	}
	e.Type = MsgBlock
	digest := e.SigningDigest()
	sig, err := crypto.Sign(priv, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	copy(e.Signature[:], sig)
	if _, err := Decode(e.Encode()); !errors.Is(err, ErrLocalOnlyType) {
		t.Fatalf("err = %v, want ErrLocalOnlyType", err)
	}
}

func TestDecodeRejectsOversizePayloadLength(t *testing.T) {
	// payload_len > 65535 must be rejected even when framing matches
	_, priv := testIdentity(t)
	var r [32]byte
	e, _ := NewEnvelope(priv, MsgChat, r, nil)
	bad := e.Encode()
	bad[75] = 0x01 // payload_len = 65536, exceeds MaxPayloadSize
	if _, err := Decode(bad); !errors.Is(err, ErrPayloadTooLarge) {
		t.Fatalf("err = %v, want ErrPayloadTooLarge", err)
	}
}

func TestContentID(t *testing.T) {
	_, priv := testIdentity(t)
	var r1, r2 [32]byte
	r2[0] = 1

	e1, err := NewEnvelope(priv, MsgChat, r1, []byte("same"))
	if err != nil {
		t.Fatal(err)
	}
	// identical message, different recipient: ContentID must NOT change
	e2 := *e1
	e2.Recipient = r2
	if e1.ContentID() != e2.ContentID() {
		t.Error("content ID depends on recipient; it must not")
	}
	// different payload: must change
	e3 := *e1
	e3.Payload = []byte("different")
	if e1.ContentID() == e3.ContentID() {
		t.Error("content ID identical for different payloads")
	}
	// different timestamp: must change
	e4 := *e1
	e4.Timestamp++
	if e1.ContentID() == e4.ContentID() {
		t.Error("content ID identical for different timestamps")
	}
}

func TestValidateTimestamp(t *testing.T) {
	now := time.Now()
	tests := []struct {
		name string
		ts   time.Time
		want error
	}{
		{"current", now, nil},
		{"1s old", now.Add(-time.Second), nil},
		{"119s old", now.Add(-119 * time.Second), nil},
		{"121s old", now.Add(-121 * time.Second), ErrTimestampOld},
		{"way old", now.Add(-24 * time.Hour), ErrTimestampOld},
		{"9s future", now.Add(9 * time.Second), nil},
		{"11s future", now.Add(11 * time.Second), ErrTimestampFuture},
		{"way future", now.Add(time.Hour), ErrTimestampFuture},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateTimestamp(tt.ts.UnixNano(), now)
			if !errors.Is(err, tt.want) {
				t.Fatalf("err = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestLocalOnly(t *testing.T) {
	for mt := uint8(1); mt <= maxMsgType; mt++ {
		want := mt == MsgBlock || mt == MsgNickname
		if got := LocalOnly(mt); got != want {
			t.Errorf("LocalOnly(%#x) = %v", mt, got)
		}
	}
}
