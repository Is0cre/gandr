# Gandr Wire Protocol

Written from the implementation in `pkg/proto`. The code is normative;
this document describes it.

## Envelope

Every message on the wire, no exceptions:

```
[version:       1 byte  ]  0x01
[message_type:  1 byte  ]
[timestamp:     8 bytes ]  unix nanoseconds, int64 big-endian
[sender_pubkey: 32 bytes]  Ed25519 public key
[recipient:     32 bytes]  pubkey (DM/sealed), channel id (chat), 0x00*32 (broadcast)
[payload_len:   4 bytes ]  uint32 big-endian, max 65535
[payload:       variable]  MessagePack, schema per message type
[signature:     64 bytes]  Ed25519 over all preceding bytes
```

Header is 78 bytes; minimum message (empty payload) is **142 bytes**;
maximum is 65677 bytes. (The original design sketch said "minimum 112
bytes" — that figure does not survive the field arithmetic above and
the implementation uses 142.)

### Signing

```
signature = Ed25519Sign(sender_privkey,
    SHA256(version || message_type || timestamp || sender_pubkey ||
           recipient || payload_len || payload))
```

Receivers verify the signature before the payload reaches any
deserializer (`proto.Decode` enforces this ordering). Invalid signature
= drop silently. No error response, no log entry. Not configurable.

### Timestamp validation

Messages older than 120 seconds or more than 10 seconds in the future
are rejected (`proto.ValidateTimestamp`). This bounds replay windows.
Live-traffic freshness only; stored content is governed by the peer
policy `MaxMessageAge`.

### Content addressing

```
content_id = SHA256(version || message_type || timestamp || sender_pubkey || payload)
```

Recipient and signature are deliberately excluded. The content id is
the canonical object id: storage key, dedup key, reference target for
replies, deletes, and acks. Hash references in payloads are 64-char
lowercase hex.

## Message types

| type | name         | payload schema        | notes |
|------|--------------|-----------------------|-------|
| 0x01 | Chat         | `ChatPayload`         | channel or DM, content ≤ 2000 chars |
| 0x02 | Post         | `PostPayload`         | feed post, ≤ 2000 chars, ≤ 16 tags |
| 0x03 | Thread       | `ThreadPayload`       | title ≤ 200, body ≤ 10000 chars |
| 0x04 | Reply        | `ReplyPayload`        | parent hash required |
| 0x05 | PeerHello    | `HelloPayload`        | handshake step 1 |
| 0x06 | PeerAck      | `HelloAckPayload`     | handshake step 2 |
| 0x07 | PeerComplete | `HelloCompletePayload`| handshake step 3 |
| 0x08 | PeerPolicy   | `PeerPolicyPayload`   | handshake step 4, session-encrypted |
| 0x09 | Profile      | `ProfilePayload`      | latest timestamp supersedes |
| 0x0A | Guestbook    | `GuestbookPayload`    | ≤ 280 chars, signed by visitor |
| 0x0B | PeerIntro    | `PeerIntroPayload`    | vouched peers only |
| 0x0C | Ack          | `AckPayload`          | generic ack by hash |
| 0x0D | Status       | `StatusPayload`       | ephemeral, pruned at 24h |
| 0x0E | Block        | —                     | **local only, never on the wire** |
| 0x0F | Nickname     | —                     | **local only, never on the wire** |
| 0x10 | Sealed       | `SealedPayload`       | node-opaque, see SEALED.md |
| 0x11 | SealedAck    | `SealedAckPayload`    | delivery confirmation, no content |
| 0x12 | Delete       | `DeletePayload`       | author-only deletion |

Types 0x0E and 0x0F have no wire schema at all: `proto.NewEnvelope`
refuses to build them and `proto.Decode` refuses to accept them. A
conforming node cannot transmit a blocklist or nickname even by bug.

All character limits are rune counts, not byte counts. All strings must
be valid UTF-8.

## Deletion rules

A `Delete` is honored when the envelope sender matches the target
object's sender, or — for guestbook entries — when the sender is the
profile owner the entry was written to. Anything else is silently
ignored. There is no other moderation primitive.

## IPC protocol (gandrd ↔ gandr)

Unix socket, binary frames (`pkg/ipc`):

```
[magic:       1 byte ]  0x49 'I'
[type:        1 byte ]
[request_id:  4 bytes]  big-endian; 0 = unsolicited push
[payload_len: 4 bytes]  big-endian, max 1 MiB
[payload:     variable]
```

Client→daemon: `0x01 Send` (complete signed envelope), `0x02 Subscribe`
/ `0x03 Unsubscribe` (32-byte channel id), `0x04 Fetch` (32-byte hash),
`0x05 PeerList`, `0x06 Profile` (32-byte pubkey), `0x07 Trust`
(32-byte peer identity + 1-byte level), `0x08 Connect` (32-byte
yggdrasil node key; queues a federation attempt).
Daemon→client: `0x80 Incoming`, `0x81 Delivered`, `0x82 PeerUpdate`,
`0xFF Error` (msgpack `{m: string}`). Replies reuse the request's type
and id.

The user identity key lives in the client. The daemon receives only
complete signed envelopes and is architecturally ignorant of who its
users are.

## Channels

A channel id is `SHA256("gandr-channel:" + name)`. Knowing the name is
knowing the channel; channels are rendezvous points, not access
control.
