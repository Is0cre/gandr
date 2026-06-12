package proto

import (
	"encoding/hex"
	"errors"
	"fmt"
	"unicode/utf8"

	"github.com/vmihailenco/msgpack/v5"

	"github.com/gandr-net/gandr/pkg/crypto"
)

// Payload is implemented by every message payload schema. Validate
// enforces type-specific structural limits; it does not perform any
// cryptographic checks.
type Payload interface {
	Validate() error
}

// ErrUnknownPayloadType is returned by NewPayload for message types
// without a payload schema.
var ErrUnknownPayloadType = errors.New("proto: no payload schema for message type")

// EncodePayload serializes a payload to MessagePack, enforcing the
// protocol payload size limit. The payload is validated first.
func EncodePayload(p Payload) ([]byte, error) {
	if err := p.Validate(); err != nil {
		return nil, err
	}
	b, err := msgpack.Marshal(p)
	if err != nil {
		return nil, fmt.Errorf("proto: encoding payload: %w", err)
	}
	if len(b) > MaxPayloadSize {
		return nil, ErrPayloadTooLarge
	}
	return b, nil
}

// DecodePayload deserializes MessagePack data into p and validates it.
// It is safe to call on hostile input: it never panics, returning an
// error instead.
func DecodePayload(data []byte, p Payload) (err error) {
	// The msgpack library is not documented panic-free on adversarial
	// input; a decoder fed by the network must hold the no-panic
	// guarantee unconditionally.
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("proto: payload decode panic: %v", r)
		}
	}()
	if len(data) > MaxPayloadSize {
		return ErrPayloadTooLarge
	}
	if err := msgpack.Unmarshal(data, p); err != nil {
		return fmt.Errorf("proto: decoding payload: %w", err)
	}
	return p.Validate()
}

// NewPayload returns an empty payload struct for the given wire message
// type, ready to be filled by DecodePayload.
func NewPayload(msgType uint8) (Payload, error) {
	switch msgType {
	case MsgChat:
		return &ChatPayload{}, nil
	case MsgPost:
		return &PostPayload{}, nil
	case MsgThread:
		return &ThreadPayload{}, nil
	case MsgReply:
		return &ReplyPayload{}, nil
	case MsgPeerHello:
		return &HelloPayload{}, nil
	case MsgPeerAck:
		return &HelloAckPayload{}, nil
	case MsgPeerComplete:
		return &HelloCompletePayload{}, nil
	case MsgPeerPolicy:
		return &PeerPolicyPayload{}, nil
	case MsgProfile:
		return &ProfilePayload{}, nil
	case MsgGuestbook:
		return &GuestbookPayload{}, nil
	case MsgPeerIntro:
		return &PeerIntroPayload{}, nil
	case MsgAck:
		return &AckPayload{}, nil
	case MsgStatus:
		return &StatusPayload{}, nil
	case MsgSealed:
		return &SealedPayload{}, nil
	case MsgSealedAck:
		return &SealedAckPayload{}, nil
	case MsgDelete:
		return &DeletePayload{}, nil
	default:
		return nil, ErrUnknownPayloadType
	}
}

// validateString enforces UTF-8 validity and a maximum rune count.
func validateString(field, s string, maxRunes int) error {
	if !utf8.ValidString(s) {
		return fmt.Errorf("proto: %s is not valid UTF-8", field)
	}
	if utf8.RuneCountInString(s) > maxRunes {
		return fmt.Errorf("proto: %s exceeds %d characters", field, maxRunes)
	}
	return nil
}

// validateHash checks a content hash reference: 64 lowercase hex chars.
// Empty is allowed when optional is true.
func validateHash(field, s string, optional bool) error {
	if s == "" {
		if optional {
			return nil
		}
		return fmt.Errorf("proto: %s is required", field)
	}
	if len(s) != 64 {
		return fmt.Errorf("proto: %s is not a valid content hash", field)
	}
	if _, err := hex.DecodeString(s); err != nil {
		return fmt.Errorf("proto: %s is not valid hex: %w", field, err)
	}
	return nil
}

// ChatPayload is the body of MsgChat (0x01).
type ChatPayload struct {
	ChannelID [32]byte `msgpack:"c"` // all-zero for DM (recipient field used instead)
	Content   string   `msgpack:"m"` // max 2000 chars
	ReplyTo   string   `msgpack:"r"` // hash of parent message, empty otherwise
}

// Validate implements Payload.
func (p *ChatPayload) Validate() error {
	if err := validateString("chat content", p.Content, 2000); err != nil {
		return err
	}
	return validateHash("chat reply_to", p.ReplyTo, true)
}

// PostPayload is the body of MsgPost (0x02).
type PostPayload struct {
	Content string   `msgpack:"m"` // max 2000 chars, Gandr markup
	ReplyTo string   `msgpack:"r"`
	Tags    []string `msgpack:"t"`
}

// Validate implements Payload.
func (p *PostPayload) Validate() error {
	if err := validateString("post content", p.Content, 2000); err != nil {
		return err
	}
	if err := validateHash("post reply_to", p.ReplyTo, true); err != nil {
		return err
	}
	return validateTags(p.Tags)
}

// ThreadPayload is the body of MsgThread (0x03).
type ThreadPayload struct {
	Title    string   `msgpack:"i"` // max 200 chars
	Content  string   `msgpack:"m"` // max 10000 chars, Gandr markup
	Category string   `msgpack:"g"`
	Tags     []string `msgpack:"t"`
}

// Validate implements Payload.
func (p *ThreadPayload) Validate() error {
	if err := validateString("thread title", p.Title, 200); err != nil {
		return err
	}
	if err := validateString("thread content", p.Content, 10000); err != nil {
		return err
	}
	if err := validateString("thread category", p.Category, 50); err != nil {
		return err
	}
	return validateTags(p.Tags)
}

// ReplyPayload is the body of MsgReply (0x04).
type ReplyPayload struct {
	ParentHash string `msgpack:"p"` // hash of parent — chat, post, or thread
	Content    string `msgpack:"m"` // max 2000 chars
}

// Validate implements Payload.
func (p *ReplyPayload) Validate() error {
	if err := validateHash("reply parent", p.ParentHash, false); err != nil {
		return err
	}
	return validateString("reply content", p.Content, 2000)
}

// maxTags bounds the tag list on posts and threads.
const maxTags = 16

func validateTags(tags []string) error {
	if len(tags) > maxTags {
		return fmt.Errorf("proto: more than %d tags", maxTags)
	}
	for _, t := range tags {
		if err := validateString("tag", t, 50); err != nil {
			return err
		}
	}
	return nil
}

// HelloPayload is the body of MsgPeerHello (0x05), step 1 of the
// federation handshake.
type HelloPayload struct {
	Capabilities uint32   `msgpack:"c"`
	Nonce        [32]byte `msgpack:"n"` // cryptographically random
	UserAgent    string   `msgpack:"u"` // e.g. "gandrd/0.1.0"
}

// Validate implements Payload.
func (p *HelloPayload) Validate() error {
	return validateString("hello user_agent", p.UserAgent, 64)
}

// HelloAckPayload is the body of MsgPeerAck (0x06), step 2.
type HelloAckPayload struct {
	Capabilities  uint32   `msgpack:"c"`
	Nonce         [32]byte `msgpack:"n"` // responder's random nonce
	EchoNonce     [32]byte `msgpack:"e"` // initiator's nonce echoed
	SessionPubkey [32]byte `msgpack:"s"` // responder's ephemeral X25519 pubkey
	UserAgent     string   `msgpack:"u"`
}

// Validate implements Payload.
func (p *HelloAckPayload) Validate() error {
	return validateString("hello_ack user_agent", p.UserAgent, 64)
}

// HelloCompletePayload is the body of MsgPeerComplete (0x07), step 3.
type HelloCompletePayload struct {
	EchoNonce     [32]byte `msgpack:"e"` // responder's nonce echoed
	SessionPubkey [32]byte `msgpack:"s"` // initiator's ephemeral X25519 pubkey
}

// Validate implements Payload.
func (p *HelloCompletePayload) Validate() error { return nil }

// PeerPolicyPayload is the body of MsgPeerPolicy (0x08), step 4,
// exchanged after session encryption is established.
type PeerPolicyPayload struct {
	MaxMessageAge  uint32 `msgpack:"a"` // seconds — reject content older than this
	MaxPayloadSize uint32 `msgpack:"p"` // bytes
	RateLimitRPM   uint16 `msgpack:"r"` // messages per minute willing to relay
	TrustLevel     uint8  `msgpack:"t"`
}

// Validate implements Payload.
func (p *PeerPolicyPayload) Validate() error {
	if p.TrustLevel > TrustVouched {
		return fmt.Errorf("proto: unknown trust level %#x", p.TrustLevel)
	}
	return nil
}

// ProfileTheme is the visual theme block of a profile.
type ProfileTheme struct {
	BgColor     string `msgpack:"b"` // hex e.g. "#1a1a2e"
	FgColor     string `msgpack:"f"`
	AccentColor string `msgpack:"a"`
	Font        string `msgpack:"o"` // "mono" | "sans" | "serif" | "pixel"
	Layout      string `msgpack:"l"` // "centered" | "left" | "terminal"
}

func validateHexColor(field, s string) error {
	if s == "" {
		return nil
	}
	if len(s) != 7 || s[0] != '#' {
		return fmt.Errorf("proto: %s is not a #rrggbb color", field)
	}
	if _, err := hex.DecodeString(s[1:]); err != nil {
		return fmt.Errorf("proto: %s is not a #rrggbb color", field)
	}
	return nil
}

// Validate checks theme fields; empty values mean "terminal default".
func (t *ProfileTheme) Validate() error {
	if err := validateHexColor("theme bg", t.BgColor); err != nil {
		return err
	}
	if err := validateHexColor("theme fg", t.FgColor); err != nil {
		return err
	}
	if err := validateHexColor("theme accent", t.AccentColor); err != nil {
		return err
	}
	switch t.Font {
	case "", "mono", "sans", "serif", "pixel":
	default:
		return fmt.Errorf("proto: unknown theme font %q", t.Font)
	}
	switch t.Layout {
	case "", "centered", "left", "terminal":
	default:
		return fmt.Errorf("proto: unknown theme layout %q", t.Layout)
	}
	return nil
}

// ProfileLink is a single labelled link on a profile.
type ProfileLink struct {
	Label string `msgpack:"l"`
	URL   string `msgpack:"u"` // clearnet, yggdrasil, onion, whatever
}

// Profile list bounds.
const (
	maxProfileLinks  = 10
	maxPinnedHashes  = 10
)

// ProfilePayload is the body of MsgProfile (0x09). The latest profile
// message from a pubkey supersedes all previous ones.
type ProfilePayload struct {
	DisplayName  string        `msgpack:"d"` // announced, not unique, not verified
	Bio          string        `msgpack:"b"` // max 500 chars, Gandr markup
	Theme        ProfileTheme  `msgpack:"e"`
	Links        []ProfileLink `msgpack:"k"`
	PinnedHashes []string      `msgpack:"p"` // content hashes of pinned posts
	Status       string        `msgpack:"s"` // "currently doing..."
	Mood         string        `msgpack:"m"` // old school mood field
	NowPlaying   string        `msgpack:"n"` // listening/reading/playing
	UpdatedAt    int64         `msgpack:"t"`
}

// Validate implements Payload.
func (p *ProfilePayload) Validate() error {
	if err := validateString("profile display_name", p.DisplayName, 64); err != nil {
		return err
	}
	if err := validateString("profile bio", p.Bio, 500); err != nil {
		return err
	}
	if err := p.Theme.Validate(); err != nil {
		return err
	}
	if len(p.Links) > maxProfileLinks {
		return fmt.Errorf("proto: more than %d profile links", maxProfileLinks)
	}
	for _, l := range p.Links {
		if err := validateString("link label", l.Label, 50); err != nil {
			return err
		}
		if err := validateString("link url", l.URL, 200); err != nil {
			return err
		}
	}
	if len(p.PinnedHashes) > maxPinnedHashes {
		return fmt.Errorf("proto: more than %d pinned hashes", maxPinnedHashes)
	}
	for _, h := range p.PinnedHashes {
		if err := validateHash("pinned hash", h, false); err != nil {
			return err
		}
	}
	if err := validateString("profile status", p.Status, 100); err != nil {
		return err
	}
	if err := validateString("profile mood", p.Mood, 50); err != nil {
		return err
	}
	return validateString("profile now_playing", p.NowPlaying, 100)
}

// GuestbookPayload is the body of MsgGuestbook (0x0A). Signed by the
// visitor, cryptographically attributed, public.
type GuestbookPayload struct {
	TargetPubkey [32]byte `msgpack:"p"` // whose profile this is for
	Message      string   `msgpack:"m"` // max 280 chars
}

// Validate implements Payload.
func (p *GuestbookPayload) Validate() error {
	return validateString("guestbook message", p.Message, 280)
}

// PeerIntroPayload is the body of MsgPeerIntro (0x0B). Sent only to and
// accepted only from TrustVouched peers.
type PeerIntroPayload struct {
	IntroducedPubkey  [32]byte `msgpack:"p"` // identity pubkey of introduced node
	IntroducedYggAddr [16]byte `msgpack:"a"` // Yggdrasil address
	VoucherSignature  [64]byte `msgpack:"s"` // introducer signs introduced pubkey + own pubkey
	Timestamp         int64    `msgpack:"t"`
}

// Validate implements Payload.
func (p *PeerIntroPayload) Validate() error { return nil }

// AckPayload is the body of MsgAck (0x0C).
type AckPayload struct {
	TargetHash string `msgpack:"h"` // content hash being acknowledged
}

// Validate implements Payload.
func (p *AckPayload) Validate() error {
	return validateHash("ack target", p.TargetHash, false)
}

// StatusPayload is the body of MsgStatus (0x0D). Ephemeral; nodes prune
// after 24 hours.
type StatusPayload struct {
	Status     string `msgpack:"s"` // max 100 chars
	Mood       string `msgpack:"m"` // max 50 chars
	NowPlaying string `msgpack:"n"` // max 100 chars
}

// Validate implements Payload.
func (p *StatusPayload) Validate() error {
	if err := validateString("status", p.Status, 100); err != nil {
		return err
	}
	if err := validateString("mood", p.Mood, 50); err != nil {
		return err
	}
	return validateString("now_playing", p.NowPlaying, 100)
}

// SealedPayload is the body of MsgSealed (0x10). Opaque to nodes: they
// see an ephemeral key, a nonce, a deniability flag, and a padded
// ciphertext. Nothing else.
type SealedPayload struct {
	EphemeralPubkey [32]byte `msgpack:"e"` // sender's ephemeral X25519 pubkey
	Nonce           [24]byte `msgpack:"n"` // XChaCha20 nonce
	Deniable        bool     `msgpack:"d"` // if true, no inner signature
	Ciphertext      []byte   `msgpack:"c"` // padded to 1024-byte blocks
}

// Validate implements Payload. It checks only what a relaying node is
// entitled to check: block-aligned padding and sane size.
func (p *SealedPayload) Validate() error {
	inner := len(p.Ciphertext) - crypto.Overhead
	if inner < crypto.SealedBlockSize {
		return errors.New("proto: sealed ciphertext too short")
	}
	if inner%crypto.SealedBlockSize != 0 {
		return errors.New("proto: sealed ciphertext not block aligned")
	}
	return nil
}

// SealedAckPayload is the body of MsgSealedAck (0x11): a delivery
// confirmation carrying no content, signed by the recipient.
type SealedAckPayload struct {
	MessageHash [32]byte `msgpack:"h"` // SHA256 of the sealed message
}

// Validate implements Payload.
func (p *SealedAckPayload) Validate() error { return nil }

// DeletePayload is the body of MsgDelete (0x12). Only valid when the
// envelope sender matches the original message sender.
type DeletePayload struct {
	TargetHash string `msgpack:"h"` // hash of message to delete
	Reason     string `msgpack:"r"` // optional, max 100 chars
}

// Validate implements Payload.
func (p *DeletePayload) Validate() error {
	if err := validateHash("delete target", p.TargetHash, false); err != nil {
		return err
	}
	return validateString("delete reason", p.Reason, 100)
}
