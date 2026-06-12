// Package proto defines the Gandr wire protocol: the fixed binary
// message envelope, every message type and its MessagePack payload
// schema, signing, and content addressing.
//
// The envelope layout is fixed and versioned:
//
//	[version:       1 byte  ]  protocol version, currently 0x01
//	[message_type:  1 byte  ]
//	[timestamp:     8 bytes ]  unix nanoseconds, int64 big-endian
//	[sender_pubkey: 32 bytes]  Ed25519 public key
//	[recipient:     32 bytes]  pubkey, channel id, or all-zero broadcast
//	[payload_len:   4 bytes ]  uint32 big-endian, max 65535
//	[payload:       variable]  MessagePack, type-specific schema
//	[signature:     64 bytes]  Ed25519 over all preceding bytes
package proto

// Version is the current protocol version byte.
const Version uint8 = 0x01

// Message types. The values are wire format; never renumber.
const (
	MsgChat         uint8 = 0x01 // chat message to channel or DM
	MsgPost         uint8 = 0x02 // feed post
	MsgThread       uint8 = 0x03 // forum thread
	MsgReply        uint8 = 0x04 // reply to thread, post, or chat
	MsgPeerHello    uint8 = 0x05 // federation handshake initiation
	MsgPeerAck      uint8 = 0x06 // federation handshake response
	MsgPeerComplete uint8 = 0x07 // federation handshake completion
	MsgPeerPolicy   uint8 = 0x08 // peering policy exchange
	MsgProfile      uint8 = 0x09 // user profile (latest supersedes all prior)
	MsgGuestbook    uint8 = 0x0A // guestbook entry on a profile
	MsgPeerIntro    uint8 = 0x0B // introduce a peer via web of trust
	MsgAck          uint8 = 0x0C // generic acknowledgement
	MsgStatus       uint8 = 0x0D // ephemeral status update
	MsgBlock        uint8 = 0x0E // block keypair — local only, never transmitted
	MsgNickname     uint8 = 0x0F // petname — local only, never transmitted
	MsgSealed       uint8 = 0x10 // sealed message, node-opaque
	MsgSealedAck    uint8 = 0x11 // sealed delivery confirmation, no content
	MsgDelete       uint8 = 0x12 // signed deletion notice for own content
)

// maxMsgType is the highest assigned message type.
const maxMsgType = MsgDelete

// Capability bitmask values exchanged during the federation handshake.
const (
	CapChat    uint32 = 0x0001
	CapFeed    uint32 = 0x0002
	CapForum   uint32 = 0x0004
	CapStorage uint32 = 0x0008 // willing to store content beyond local users
	CapRelay   uint32 = 0x0010 // willing to relay for other nodes
	CapSeed    uint32 = 0x0020 // bootstrap seed node
)

// Trust levels assigned to federation peers.
const (
	TrustUntrusted uint8 = 0x00 // bootstrap contact only, no relay
	TrustNeutral   uint8 = 0x01 // relay public content (default for new peers)
	TrustTrusted   uint8 = 0x02 // relay private channel invites
	TrustVouched   uint8 = 0x03 // receive and send peer introductions
)

// LocalOnly reports whether a message type must never be transmitted
// over the network. Block and nickname records exist only inside the
// client's local store; a conforming implementation drops them at every
// network boundary.
func LocalOnly(msgType uint8) bool {
	return msgType == MsgBlock || msgType == MsgNickname
}
