package proto

import (
	"reflect"
	"strings"
	"testing"

	"github.com/gandr-net/gandr/pkg/crypto"
)

// validSealedCiphertext returns a ciphertext of the right shape for
// SealedPayload validation (one padded block plus AEAD tag).
func validSealedCiphertext() []byte {
	return make([]byte, crypto.SealedBlockSize+crypto.Overhead)
}

var hash64 = strings.Repeat("ab", 32)

// payloadCases enumerates one valid instance of every payload schema.
func payloadCases() map[uint8]Payload {
	return map[uint8]Payload{
		MsgChat:   &ChatPayload{ChannelID: [32]byte{1}, Content: "hello", ReplyTo: hash64},
		MsgPost:   &PostPayload{Content: "post **body**", Tags: []string{"tech", "norse"}},
		MsgThread: &ThreadPayload{Title: "title", Content: "body", Category: "general", Tags: []string{"t"}},
		MsgReply:  &ReplyPayload{ParentHash: hash64, Content: "reply"},
		MsgPeerHello: &HelloPayload{
			Capabilities: CapChat | CapRelay, Nonce: [32]byte{9}, UserAgent: "gandrd/0.1.0",
		},
		MsgPeerAck: &HelloAckPayload{
			Capabilities: CapChat, Nonce: [32]byte{1}, EchoNonce: [32]byte{2},
			SessionPubkey: [32]byte{3}, UserAgent: "gandrd/0.1.0",
		},
		MsgPeerComplete: &HelloCompletePayload{EchoNonce: [32]byte{4}, SessionPubkey: [32]byte{5}},
		MsgPeerPolicy: &PeerPolicyPayload{
			MaxMessageAge: 604800, MaxPayloadSize: 65535, RateLimitRPM: 600, TrustLevel: TrustNeutral,
		},
		MsgProfile: &ProfilePayload{
			DisplayName: "byte_me",
			Bio:         "building things that shouldn't exist.",
			Theme:       ProfileTheme{BgColor: "#1a1a2e", FgColor: "#e0e0e0", AccentColor: "#ff0055", Font: "mono", Layout: "terminal"},
			Links:       []ProfileLink{{Label: "site", URL: "https://kernelcraft.net"}},
			PinnedHashes: []string{hash64},
			Status:      "somewhere in the pacific",
			Mood:        "caffeinated",
			NowPlaying:  "wardruna",
			UpdatedAt:   1700000000,
		},
		MsgGuestbook: &GuestbookPayload{TargetPubkey: [32]byte{7}, Message: "miss you, come home"},
		MsgPeerIntro: &PeerIntroPayload{
			IntroducedPubkey: [32]byte{8}, IntroducedYggAddr: [16]byte{0x02},
			VoucherSignature: [64]byte{9}, Timestamp: 1700000000,
		},
		MsgAck:    &AckPayload{TargetHash: hash64},
		MsgStatus: &StatusPayload{Status: "afk", Mood: "tired", NowPlaying: "rain"},
		MsgSealed: &SealedPayload{
			EphemeralPubkey: [32]byte{1}, Nonce: [24]byte{2}, Deniable: true,
			Ciphertext: validSealedCiphertext(),
		},
		MsgSealedAck: &SealedAckPayload{MessageHash: [32]byte{3}},
		MsgDelete:    &DeletePayload{TargetHash: hash64, Reason: "my content, my call"},
	}
}

func TestPayloadRoundtripAllTypes(t *testing.T) {
	for msgType, payload := range payloadCases() {
		t.Run(typeName(msgType), func(t *testing.T) {
			data, err := EncodePayload(payload)
			if err != nil {
				t.Fatalf("EncodePayload: %v", err)
			}
			fresh, err := NewPayload(msgType)
			if err != nil {
				t.Fatalf("NewPayload: %v", err)
			}
			if err := DecodePayload(data, fresh); err != nil {
				t.Fatalf("DecodePayload: %v", err)
			}
			if !reflect.DeepEqual(payload, fresh) {
				t.Fatalf("roundtrip mismatch:\n in: %+v\nout: %+v", payload, fresh)
			}
		})
	}
}

func typeName(mt uint8) string {
	names := map[uint8]string{
		MsgChat: "chat", MsgPost: "post", MsgThread: "thread", MsgReply: "reply",
		MsgPeerHello: "hello", MsgPeerAck: "hello_ack", MsgPeerComplete: "hello_complete",
		MsgPeerPolicy: "peer_policy", MsgProfile: "profile", MsgGuestbook: "guestbook",
		MsgPeerIntro: "peer_intro", MsgAck: "ack", MsgStatus: "status",
		MsgSealed: "sealed", MsgSealedAck: "sealed_ack", MsgDelete: "delete",
	}
	return names[mt]
}

func TestNewPayloadUnknownTypes(t *testing.T) {
	for _, mt := range []uint8{0x00, MsgBlock, MsgNickname, 0x13, 0xFF} {
		if _, err := NewPayload(mt); err == nil {
			t.Errorf("NewPayload(%#x) succeeded; local-only and unknown types have no wire schema", mt)
		}
	}
}

func TestPayloadValidationRejects(t *testing.T) {
	long := func(n int) string { return strings.Repeat("x", n) }
	badUTF8 := string([]byte{0xFF, 0xFE, 0xFD})

	tests := []struct {
		name    string
		payload Payload
	}{
		{"chat content too long", &ChatPayload{Content: long(2001)}},
		{"chat invalid utf8", &ChatPayload{Content: badUTF8}},
		{"chat bad reply hash", &ChatPayload{Content: "x", ReplyTo: "nothash"}},
		{"chat odd hex hash", &ChatPayload{Content: "x", ReplyTo: strings.Repeat("z", 64)}},
		{"post content too long", &PostPayload{Content: long(2001)}},
		{"post too many tags", &PostPayload{Content: "x", Tags: make([]string, maxTags+1)}},
		{"post tag too long", &PostPayload{Content: "x", Tags: []string{long(51)}}},
		{"thread title too long", &ThreadPayload{Title: long(201), Content: "x"}},
		{"thread content too long", &ThreadPayload{Title: "t", Content: long(10001)}},
		{"thread category too long", &ThreadPayload{Title: "t", Content: "x", Category: long(51)}},
		{"reply missing parent", &ReplyPayload{Content: "x"}},
		{"reply content too long", &ReplyPayload{ParentHash: hash64, Content: long(2001)}},
		{"hello user agent too long", &HelloPayload{UserAgent: long(65)}},
		{"hello_ack user agent too long", &HelloAckPayload{UserAgent: long(65)}},
		{"policy unknown trust", &PeerPolicyPayload{TrustLevel: 0x04}},
		{"profile name too long", &ProfilePayload{DisplayName: long(65)}},
		{"profile bio too long", &ProfilePayload{Bio: long(501)}},
		{"profile bad bg color", &ProfilePayload{Theme: ProfileTheme{BgColor: "1a1a2e"}}},
		{"profile bad fg color", &ProfilePayload{Theme: ProfileTheme{FgColor: "#xyzxyz"}}},
		{"profile bad accent", &ProfilePayload{Theme: ProfileTheme{AccentColor: "#12345"}}},
		{"profile bad font", &ProfilePayload{Theme: ProfileTheme{Font: "comic-sans"}}},
		{"profile bad layout", &ProfilePayload{Theme: ProfileTheme{Layout: "tiled"}}},
		{"profile too many links", &ProfilePayload{Links: make([]ProfileLink, maxProfileLinks+1)}},
		{"profile link label too long", &ProfilePayload{Links: []ProfileLink{{Label: long(51)}}}},
		{"profile link url too long", &ProfilePayload{Links: []ProfileLink{{URL: long(201)}}}},
		{"profile too many pins", &ProfilePayload{PinnedHashes: make([]string, maxPinnedHashes+1)}},
		{"profile bad pin hash", &ProfilePayload{PinnedHashes: []string{"short"}}},
		{"profile status too long", &ProfilePayload{Status: long(101)}},
		{"profile mood too long", &ProfilePayload{Mood: long(51)}},
		{"profile np too long", &ProfilePayload{NowPlaying: long(101)}},
		{"guestbook too long", &GuestbookPayload{Message: long(281)}},
		{"ack missing hash", &AckPayload{}},
		{"status too long", &StatusPayload{Status: long(101)}},
		{"mood too long", &StatusPayload{Mood: long(51)}},
		{"np too long", &StatusPayload{NowPlaying: long(101)}},
		{"sealed empty ciphertext", &SealedPayload{}},
		{"sealed unaligned", &SealedPayload{Ciphertext: make([]byte, crypto.SealedBlockSize+crypto.Overhead+1)}},
		{"sealed too short", &SealedPayload{Ciphertext: make([]byte, 100)}},
		{"delete missing hash", &DeletePayload{}},
		{"delete reason too long", &DeletePayload{TargetHash: hash64, Reason: long(101)}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.payload.Validate(); err == nil {
				t.Fatal("Validate accepted invalid payload")
			}
			if _, err := EncodePayload(tt.payload); err == nil {
				t.Fatal("EncodePayload accepted invalid payload")
			}
		})
	}
}

func TestRuneCountNotByteCount(t *testing.T) {
	// 2000 multibyte runes is valid content even though it exceeds 2000
	// bytes — limits are character counts.
	content := strings.Repeat("ᚷ", 2000)
	p := &ChatPayload{Content: content}
	if err := p.Validate(); err != nil {
		t.Fatalf("2000-rune content rejected: %v", err)
	}
	p.Content += "x"
	if err := p.Validate(); err == nil {
		t.Fatal("2001-rune content accepted")
	}
}

func TestDecodePayloadRejectsOversize(t *testing.T) {
	if err := DecodePayload(make([]byte, MaxPayloadSize+1), &ChatPayload{}); err == nil {
		t.Fatal("DecodePayload accepted oversize data")
	}
}

func TestDecodePayloadGarbage(t *testing.T) {
	for _, data := range [][]byte{nil, {0xC1}, {0xFF, 0xFF, 0xFF}, []byte("not msgpack at all")} {
		for mt := uint8(1); mt <= maxMsgType; mt++ {
			if LocalOnly(mt) {
				continue
			}
			p, err := NewPayload(mt)
			if err != nil {
				t.Fatal(err)
			}
			// must not panic; errors are expected and fine
			_ = DecodePayload(data, p)
		}
	}
}

func TestEnvelopePayloadIntegration(t *testing.T) {
	// Full path: payload -> envelope -> wire -> envelope -> payload.
	_, priv, err := crypto.GenerateIdentity()
	if err != nil {
		t.Fatal(err)
	}
	chat := &ChatPayload{Content: "the network is the community"}
	data, err := EncodePayload(chat)
	if err != nil {
		t.Fatal(err)
	}
	var channel [32]byte
	channel[0] = 0x11
	env, err := NewEnvelope(priv, MsgChat, channel, data)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := Decode(env.Encode())
	if err != nil {
		t.Fatal(err)
	}
	got := &ChatPayload{}
	if err := DecodePayload(decoded.Payload, got); err != nil {
		t.Fatal(err)
	}
	if got.Content != chat.Content {
		t.Fatal("content mismatch through full stack")
	}
}
