# Gandr Federation

Written from the implementation in `pkg/federation` and `pkg/network`.

## Transport

All inter-node traffic runs over the Yggdrasil overlay. gandrd embeds a
yggdrasil-go core in-process: no TUN device, no root, no external
daemon. A node's transport address is its Yggdrasil node key, which is
**a different keypair from its Gandr identity** — transport and
identity are never the same key, and the handshake binds them.

Yggdrasil delivers best-effort unordered datagrams. A 13-byte transport
sublayer (`pkg/network/mux.go`) adds fragmentation (max envelope
exceeds the 65535 MTU by 142 bytes → 2 fragments), acknowledgement,
retransmission (250ms base, 5 attempts, exponential backoff), and
dedupe. Result: reliable message delivery, unordered, which is all the
envelope layer needs.

The header's final byte is an **epoch**: a random nonzero value fixed
for the lifetime of the sending process. Connections are keyed by node
key, so without it a restarted peer's new handshake would be demuxed
into the dead connection and silently swallowed. A frame bearing a
different nonzero epoch than the one the connection was established
with retires the old connection and surfaces a fresh one to the
acceptor. Senders predating the field emit 0x00, which never triggers
a reset. Epochs ride inside Yggdrasil's encrypted sessions, so only
the genuine peer can reset its own connection.

## Handshake

Four steps. Every step is a signed envelope. Any invalid signature,
wrong nonce echo, stale timestamp, mid-handshake identity switch, or
unexpected message type aborts the handshake with **silence** — the
violating party learns nothing.

```
A → B   PeerHello     capabilities, nonce_A, user agent
B → A   PeerAck       capabilities, nonce_B, echo(nonce_A), B's ephemeral X25519 key
A → B   PeerComplete  echo(nonce_B), A's ephemeral X25519 key
A ↔ B   PeerPolicy    encrypted with the session key (initiator sends first)
```

Nonce echoes prove liveness in both directions. Ephemeral session keys
travel inside signed envelopes, binding them to the identities. The
session key is:

```
key = HKDF-SHA256(X25519(eph_A, eph_B), info="gandr-session-v1")
```

All post-handshake traffic is `nonce(24) || XChaCha20-Poly1305(envelope)`
with direction-bound AAD (`gandr-i2r-v1` / `gandr-r2i-v1`, preventing
reflection), inside Yggdrasil's own transport encryption — double
encrypted. Identity keys sign; they never encrypt.

## Trust levels

| level | value | grants |
|-------|-------|--------|
| untrusted | 0x00 | bootstrap contact only, no relay |
| neutral   | 0x01 | relay public content (default for new peers) |
| trusted   | 0x02 | relay private channel invites |
| vouched   | 0x03 | send and receive peer introductions |

New peers start neutral (configurable down to untrusted). Trust is
assigned locally by the operator; it is never negotiated and never
synchronized. The peer table is memory-only and rebuilt from live
handshakes at every start — a seized node carries no peer history.

## Relay and limits

Valid content envelopes are stored (content-addressed, deduplicating)
and flooded to all neutral-or-better peers except the origin. The
storage dedup check is the flood damper: a message already stored is
not re-relayed. Per-peer rate limiting (`rate_limit_rpm`) and payload
size limits are enforced before any processing. Exceeding them means
silent drops, not errors.

## Discovery

Bootstrap is explicit: the operator configures seed nodes as hex
Yggdrasil node keys plus yggdrasil link-layer peers to reach the
overlay. `PeerIntro` messages are accepted only from vouched peers;
v0.1 records them but does not yet auto-dial. There is no public peer
registry and no enumerable peer list, by design.
