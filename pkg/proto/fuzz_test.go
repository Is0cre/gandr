package proto

import (
	"bytes"
	"testing"

	"github.com/gandr-net/gandr/pkg/crypto"
)

// FuzzDecode feeds arbitrary bytes to the envelope decoder. It must
// never panic, and anything it accepts must re-encode to identical
// bytes (decode/encode is the identity on valid wire data).
func FuzzDecode(f *testing.F) {
	_, priv, err := crypto.GenerateIdentity()
	if err != nil {
		f.Fatal(err)
	}
	var r [32]byte
	env, err := NewEnvelope(priv, MsgChat, r, []byte("seed payload"))
	if err != nil {
		f.Fatal(err)
	}
	f.Add(env.Encode())
	empty, err := NewEnvelope(priv, MsgAck, r, nil)
	if err != nil {
		f.Fatal(err)
	}
	f.Add(empty.Encode())
	f.Add([]byte{})
	f.Add(make([]byte, MinMessageSize))

	f.Fuzz(func(t *testing.T, data []byte) {
		e, err := Decode(data)
		if err != nil {
			return
		}
		if !bytes.Equal(e.Encode(), data) {
			t.Fatal("accepted message does not re-encode identically")
		}
		// any accepted message carries a valid signature by definition;
		// recompute to be sure the digest path is consistent
		digest := e.SigningDigest()
		if !crypto.Verify(e.Sender[:], digest[:], e.Signature[:]) {
			t.Fatal("Decode accepted an envelope whose signature does not verify")
		}
	})
}

// FuzzDecodePayload feeds arbitrary bytes to every payload schema's
// deserializer. It must never panic.
func FuzzDecodePayload(f *testing.F) {
	chat, err := EncodePayload(&ChatPayload{Content: "seed"})
	if err != nil {
		f.Fatal(err)
	}
	f.Add(uint8(MsgChat), chat)
	profile, err := EncodePayload(&ProfilePayload{DisplayName: "seed"})
	if err != nil {
		f.Fatal(err)
	}
	f.Add(uint8(MsgProfile), profile)
	f.Add(uint8(MsgSealed), []byte{0xC1})
	f.Add(uint8(0xFF), []byte{})

	f.Fuzz(func(t *testing.T, msgType uint8, data []byte) {
		p, err := NewPayload(msgType)
		if err != nil {
			return
		}
		if err := DecodePayload(data, p); err != nil {
			return
		}
		// accepted payloads must validate (DecodePayload validates) and
		// re-encode without error
		if _, err := EncodePayload(p); err != nil {
			t.Fatalf("accepted payload failed to re-encode: %v", err)
		}
	})
}
