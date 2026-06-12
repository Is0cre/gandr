# Gandr — Claude Code Prompt
# Federated Sovereign Communication Network
# Version 2.0 — Complete Specification

---

## What This Is

Gandr is a federated, censorship-resistant communication network.

The name is Old Norse — a sending, a magical transmission carried by invisible force. The rune is the logo. No further explanation needed.

**Non-negotiable design principles:**

- No central authority. No company. No killswitch. Ever.
- Identity = Ed25519 keypair. You are your key. No accounts. No registration.
- Single static Go binary. One artifact. Reproducible build. GPG signed.
- Transport over Yggdrasil overlay network exclusively.
- Signed binary protocol. No HTTP. No REST. No browser. No web interface. Ever.
- Federation via mutual peering and web of trust.
- No moderation. Social sanction only.
- No telemetry. No logs. No analytics. No crash reporting. No auto-update.
- Runs on cheap VPS, SBCs, Raspberry Pi, cyberdecks.
- Designed and hosted outside EU jurisdiction.
- The node is architecturally ignorant of its users. Seizure yields nothing.

---

## Binaries

```
gandrd        node daemon (server)
gandr         client TUI
gandr-lora    LoRa bridge (separate binary, separate audit scope, v2)
```

No Python client. No web client. No mobile app in v1.
Go only. One language. One binary per role. Clean audit surface.

`gandr` (client) never touches the network directly.
It talks to `gandrd` via Unix socket only.
Network code lives exclusively in `gandrd`.

```
/var/run/gandrd/gandr.sock
```

---

## Tech Stack

- **Language:** Go 1.22+
- **Transport:** Yggdrasil only (github.com/yggdrasil-network/yggdrasil-go)
- **Crypto:** Ed25519, X25519, ChaCha20-Poly1305, XChaCha20-Poly1305, HKDF, Argon2id
- **Serialization:** MessagePack for payloads, fixed binary envelope
- **TUI:** Bubble Tea
- **Client storage:** SQLite3 encrypted with identity key derivative
- **Node storage:** Content-addressed flat files
- **Config:** TOML
- **Build:** Reproducible, cross-compiled, GPG signed releases

**Approved external dependencies only:**
- yggdrasil-go
- bubbletea + lipgloss
- golang.org/x/crypto (X25519, ChaCha20, Argon2id, HKDF)
- vmihailenco/msgpack
- mattn/go-sqlite3 (client only)

No other external dependencies without explicit justification.
Standard library preferred everywhere it suffices.

---

## Repository Structure

```
/
├── cmd/
│   ├── gandrd/         node daemon entrypoint
│   └── gandr/          client TUI entrypoint
├── pkg/
│   ├── proto/          protocol definitions, message types, serialization, fuzz tests
│   ├── crypto/         key generation, sign, verify, seal, open, encrypt, decrypt
│   ├── network/        Yggdrasil integration, raw send/receive
│   ├── store/          content-addressed storage, prune, retrieve
│   ├── federation/     handshake, peering, trust management, peer table
│   ├── identity/       keypair management, encrypted keyfile, profile
│   ├── ipc/            Unix socket protocol between gandrd and gandr
│   └── tui/            Bubble Tea UI components
├── docs/
│   ├── PROTOCOL.md     wire protocol spec (written from implementation)
│   ├── FEDERATION.md   federation and peering spec
│   ├── SEALED.md       sealed message spec
│   └── SETUP.md        node operator guide
├── scripts/
│   ├── build.sh        reproducible build script
│   ├── release.sh      sign and publish
│   └── mirror.sh       rotate ephemeral download mirrors
├── Makefile
└── README.md
```

---

## Wire Protocol

### Message Envelope

All messages. No exceptions.

```
[version:       1 byte  ]  protocol version, currently 0x01
[message_type:  1 byte  ]  see message types
[timestamp:     8 bytes ]  unix nanoseconds, int64 big-endian
[sender_pubkey: 32 bytes]  Ed25519 public key
[recipient:     32 bytes]  pubkey for DM/sealed, channel_id for public, 0x00*32 for broadcast
[payload_len:   4 bytes ]  uint32 big-endian, max 65535
[payload:       variable]  MessagePack encoded, type-specific schema
[signature:     64 bytes]  Ed25519 over all preceding bytes
```

Minimum message size: 112 bytes.

### Signing

```
signature = Ed25519Sign(
    sender_privkey,
    SHA256(version || message_type || timestamp || sender_pubkey || recipient || payload_len || payload)
)
```

**Receivers MUST verify signature before deserializing payload.**
Invalid signature = drop silently. No error response. No logging. Nothing.
This is not optional. This is not configurable.

### Timestamp Validation

Reject messages older than 120 seconds or more than 10 seconds in the future.
Prevents replay attacks.
Clock skew tolerance: 10 seconds.

### Message Types

```go
const (
    MsgChat         uint8 = 0x01  // chat message to channel or DM
    MsgPost         uint8 = 0x02  // feed post
    MsgThread       uint8 = 0x03  // forum thread
    MsgReply        uint8 = 0x04  // reply to thread, post, or chat
    MsgPeerHello    uint8 = 0x05  // federation handshake initiation
    MsgPeerAck      uint8 = 0x06  // federation handshake response
    MsgPeerComplete uint8 = 0x07  // federation handshake completion
    MsgPeerPolicy   uint8 = 0x08  // peering policy exchange
    MsgProfile      uint8 = 0x09  // user profile (latest supersedes all prior)
    MsgGuestbook    uint8 = 0x0A  // guestbook entry on a profile
    MsgPeerIntro    uint8 = 0x0B  // introduce a peer via web of trust
    MsgAck          uint8 = 0x0C  // generic acknowledgement
    MsgStatus       uint8 = 0x0D  // ephemeral status update
    MsgBlock        uint8 = 0x0E  // block keypair — local only, never transmitted
    MsgNickname     uint8 = 0x0F  // petname — local only, never transmitted
    MsgSealed       uint8 = 0x10  // sealed message, node-opaque
    MsgSealedAck    uint8 = 0x11  // sealed delivery confirmation, no content
    MsgDelete       uint8 = 0x12  // signed deletion notice for own content
)
```

### Content Addressing

Every message is identified by:
```
SHA256(version || message_type || timestamp || sender_pubkey || payload)
```

This hash is the canonical ID. Stored by hash. Retrieved by hash.
Deduplication is automatic. Same message received twice = one stored object.

---

## Federation Handshake

Four-step. All steps signed. Silence on invalid signatures.

### Step 1 — MsgPeerHello (0x05)

Node A → Node B

```go
type HelloPayload struct {
    Capabilities uint32    // capability bitmask
    Nonce        [32]byte  // cryptographically random
    UserAgent    string    // "gandrd/0.1.0"
}
```

Capabilities bitmask:
```go
const (
    CapChat    uint32 = 0x0001
    CapFeed    uint32 = 0x0002
    CapForum   uint32 = 0x0004
    CapStorage uint32 = 0x0008  // willing to store content beyond local users
    CapRelay   uint32 = 0x0010  // willing to relay for other nodes
    CapSeed    uint32 = 0x0020  // bootstrap seed node
)
```

### Step 2 — MsgPeerAck (0x06)

Node B → Node A

B verifies A's signature. Invalid = drop silently.

```go
type HelloAckPayload struct {
    Capabilities  uint32
    Nonce         [32]byte  // B's random nonce
    EchoNonce     [32]byte  // A's nonce echoed — proves B received HELLO
    SessionPubkey [32]byte  // B's ephemeral X25519 pubkey
    UserAgent     string
}
```

### Step 3 — MsgPeerComplete (0x07)

Node A → Node B

```go
type HelloCompletePayload struct {
    EchoNonce     [32]byte  // B's nonce echoed — proves A received ACK
    SessionPubkey [32]byte  // A's ephemeral X25519 pubkey
}
```

Both nodes now:
- X25519 key exchange between ephemeral session pubkeys
- Derive shared session key via HKDF-SHA256
- All subsequent session traffic: XChaCha20-Poly1305 with derived key
- Identity keys used only for signing, never encryption

### Step 4 — MsgPeerPolicy (0x08)

Exchanged after session encryption established.

```go
type PeerPolicyPayload struct {
    MaxMessageAge  uint32  // seconds — reject content older than this
    MaxPayloadSize uint32  // bytes
    RateLimitRPM   uint16  // messages per minute willing to relay
    TrustLevel     uint8   // see trust levels below
}
```

Trust levels:
```go
const (
    TrustUntrusted uint8 = 0x00  // bootstrap contact only, no relay
    TrustNeutral   uint8 = 0x01  // relay public content (default for new peers)
    TrustTrusted   uint8 = 0x02  // relay private channel invites
    TrustVouched   uint8 = 0x03  // receive peer introductions, introduce to others
)
```

All new peers start at TrustNeutral.
Trust increases through time, uptime, and behavior.
Only TrustVouched peers receive MsgPeerIntro messages.

### Peer Discovery

Bootstrap: hardcoded seed node Yggdrasil addresses in binary.
Organic: MsgPeerIntro from TrustVouched peers only.
No public peer registry. No enumerable peer list. Ever.

```go
type PeerIntroPayload struct {
    IntroducedPubkey   [32]byte  // identity pubkey of introduced node
    IntroducedYggAddr  [16]byte  // Yggdrasil address
    VoucherSignature   [64]byte  // introducer signs introduced pubkey + their own pubkey
    Timestamp          int64
}
```

Recipient decides independently whether to attempt handshake.
Trust extended to introduced peer proportional to trust in introducer.

---

## Identity System

### Key Generation

First run:
1. Generate Ed25519 keypair — permanent identity
2. Derive Yggdrasil address from pubkey
3. Encrypt keyfile with ChaCha20-Poly1305, key derived via Argon2id from passphrase
4. Store encrypted keyfile
5. Never ask for passphrase again until restart

```go
type Identity struct {
    PrivateKey  ed25519.PrivateKey
    PublicKey   ed25519.PublicKey
    DisplayName string    // announced to network, not unique, not verified
    CreatedAt   int64
}
```

Keyfile encryption:
```go
// key derivation
salt := random(32)
key  := argon2.IDKey(passphrase, salt, 3, 64*1024, 4, 32)
// encryption
nonce      := random(24)
ciphertext := XChaCha20Poly1305.Seal(key, nonce, identity_bytes)
// stored: salt || nonce || ciphertext
```

---

## Nickname / Petname System

**This is a critical feature. Specified precisely.**

Nicknames are PURELY LOCAL.
They NEVER leave the client.
They are NEVER transmitted.
They are NEVER stored on nodes.
They are NEVER shared with peers.

```go
type Nickname struct {
    Pubkey    [32]byte  // identity pubkey of the person
    Name      string    // what you call them
    Note      string    // private note, visible only to you
    AddedAt   int64
    TrustHint uint8     // your local trust signal for this person
}
```

Stored in client's local encrypted SQLite database.

**Display priority:**
1. Your nickname for this pubkey (if set)
2. Their announced DisplayName from latest MsgProfile
3. First 12 chars of hex-encoded pubkey

**UX requirements:**
- `n` key on any message → set/edit nickname for sender instantly
- Nickname visible everywhere that pubkey appears: chat, feed, forum, profile, guestbook
- People list in sidebar shows nicknames
- Fuzzy search across nicknames and notes
- Quick-add: select any message sender → "Add nickname" with one keypress
- Export nicknames as encrypted file (for moving between devices)
- Import nicknames from encrypted file

The nickname is how you build your world inside Gandr.
The network doesn't name people for you. You do.

---

## Sealed Messages

Sealed messages are completely opaque to nodes.
The node is a dumb relay for fixed-size encrypted blobs.
It sees: recipient pubkey, block size, timing. Nothing else.
Not sender identity. Not message size. Not content.
A fully compromised node cannot read sealed messages.
This is architecturally guaranteed, not policy.

### Crypto — Noise X Pattern

```
sender has:    recipient's Ed25519 identity pubkey
               own Ed25519 identity keypair

1. Convert recipient Ed25519 pubkey → X25519 pubkey
2. Generate ephemeral X25519 keypair (fresh per message)
3. X25519 ECDH: ephemeral_privkey × recipient_x25519_pubkey → shared_secret
4. HKDF-SHA256(shared_secret, "gandr-sealed-v1") → encryption_key
5. Encrypt plaintext with XChaCha20-Poly1305
6. Pad ciphertext to nearest 1024-byte block boundary with random bytes
7. Sign entire sealed payload with sender identity key (outer envelope as normal)
```

Decryption:
```
recipient has: own X25519 privkey (derived from Ed25519 identity key)
               ephemeral pubkey from message

1. X25519 ECDH: own_x25519_privkey × ephemeral_pubkey → shared_secret
2. HKDF-SHA256(shared_secret, "gandr-sealed-v1") → encryption_key
3. XChaCha20-Poly1305 decrypt
4. Strip padding
5. Verify inner signature if present (non-deniable mode)
```

### Payload

```go
type SealedPayload struct {
    EphemeralPubkey [32]byte  // sender's ephemeral X25519 pubkey
    Nonce           [24]byte  // XChaCha20 nonce
    Deniable        bool      // if true, no inner signature
    Ciphertext      []byte    // padded to 1024-byte blocks
    // inner plaintext structure after decryption:
    // [sender_pubkey:     32 bytes] (always present)
    // [inner_signature:   64 bytes] (present if !Deniable)
    // [message_type:       1 byte ] (chat, post, etc)
    // [actual_content:  variable  ]
    // [padding:         variable  ] zeros to 1024-byte block boundary
}
```

### Deniable Mode

When `Deniable = true`:
- No inner signature
- Recipient can read the message
- Recipient cannot cryptographically prove to anyone who sent it
- Outer signature proves outer keypair sent *something* to recipient
- Inner content is unsigned — plausible deniability on content

### Delivery Confirmation

```go
// MsgSealedAck 0x11
type SealedAckPayload struct {
    MessageHash [32]byte  // SHA256 of the sealed message — no content
    // signed by recipient identity key
    // sender knows delivery happened
    // node learns nothing except timing
}
```

### New Message Type

```go
MsgSealed    uint8 = 0x10
MsgSealedAck uint8 = 0x11
```

---

## Profile System

MsgProfile (0x09). Latest message from a pubkey supersedes all previous profiles.
Profile is stored and propagated like any other content — no special infrastructure.

```go
type ProfilePayload struct {
    DisplayName   string         // announced name, not unique, not verified
    Bio           string         // max 500 chars, Gandr markup
    Theme         ProfileTheme
    Links         []ProfileLink
    PinnedHashes  []string       // content hashes of pinned posts
    Status        string         // "currently doing..."
    Mood          string         // old school mood field
    NowPlaying    string         // listening/reading/playing
    UpdatedAt     int64
}

type ProfileTheme struct {
    BgColor     string  // hex e.g. "#1a1a2e"
    FgColor     string  // hex
    AccentColor string  // hex
    Font        string  // "mono" | "sans" | "serif" | "pixel"
    Layout      string  // "centered" | "left" | "terminal"
}

type ProfileLink struct {
    Label string
    URL   string  // clearnet, yggdrasil, onion, whatever
}
```

### Gandr Markup

Safe subset. No HTML. No scripts. No arbitrary formatting.
Client renders. Server never interprets.

```
**bold**
*italic*
`inline code`
```code block```
[label](url)
---              horizontal rule
> blockquote
```

Nothing else. Ever.

### Guestbook (MsgGuestbook 0x0A)

```go
type GuestbookPayload struct {
    TargetPubkey [32]byte  // whose profile this is for
    Message      string    // max 280 chars
}
```

Signed by visitor. Cryptographically attributed.
Owner can publish a signed MsgDelete for any guestbook entry on their profile.
Public. Permanent unless owner deletes.

---

## Content Types

### Chat (MsgChat 0x01)

```go
type ChatPayload struct {
    ChannelID [32]byte  // channel identifier, 0x00*32 for DM (use recipient field)
    Content   string    // max 2000 chars
    ReplyTo   string    // hash of parent message if reply, empty otherwise
}
```

### Feed Post (MsgPost 0x02)

```go
type PostPayload struct {
    Content  string    // max 2000 chars, Gandr markup
    ReplyTo  string    // hash of parent post if reply
    Tags     []string  // optional
}
```

### Forum Thread (MsgThread 0x03)

```go
type ThreadPayload struct {
    Title    string    // max 200 chars
    Content  string    // max 10000 chars, Gandr markup
    Category string
    Tags     []string
}
```

### Reply (MsgReply 0x04)

```go
type ReplyPayload struct {
    ParentHash string  // hash of parent — works for chat, post, or thread
    Content    string  // max 2000 chars
}
```

### Status (MsgStatus 0x0D)

```go
type StatusPayload struct {
    Status     string  // max 100 chars
    Mood       string  // max 50 chars
    NowPlaying string  // max 100 chars
}
```

Ephemeral — nodes prune after 24 hours. Not stored long term.

### Delete (MsgDelete 0x12)

```go
type DeletePayload struct {
    TargetHash string  // hash of message to delete
    Reason     string  // optional, max 100 chars
}
```

Only valid if sender_pubkey matches original message sender_pubkey.
Nodes that receive a valid MsgDelete remove the referenced object.
Cannot delete other people's content.

---

## Storage

### Node Storage

Content-addressed. No relational database. No user tables.
Node is deliberately ignorant of user identity.

```
/var/lib/gandrd/
├── objects/
│   ├── ab/
│   │   └── ab3f7c9d...  (first 2 bytes of hash as dir, full hash as filename)
│   └── ...
├── peers/
│   └── (encrypted peer state)
├── channels/
│   └── (channel metadata only, not membership)
└── config.toml
```

Prune policy: objects older than MaxMessageAge are deleted during nightly prune.
Storage nodes (CapStorage) use longer retention via config.
No object is retained indefinitely by default.

### Client Storage

Local SQLite3, encrypted with key derived from identity key.

```sql
CREATE TABLE nicknames (
    pubkey      BLOB PRIMARY KEY,  -- 32 bytes
    name        TEXT NOT NULL,
    note        TEXT,
    added_at    INTEGER NOT NULL,
    trust_hint  INTEGER DEFAULT 1
);

CREATE TABLE profile_cache (
    pubkey      BLOB PRIMARY KEY,
    data        BLOB NOT NULL,      -- serialized ProfilePayload
    fetched_at  INTEGER NOT NULL,
    msg_hash    TEXT NOT NULL
);

CREATE TABLE content_cache (
    hash        TEXT PRIMARY KEY,
    data        BLOB NOT NULL,
    msg_type    INTEGER NOT NULL,
    fetched_at  INTEGER NOT NULL
);

CREATE TABLE blocklist (
    pubkey      BLOB PRIMARY KEY,
    added_at    INTEGER NOT NULL,
    reason      TEXT
);

CREATE TABLE channels (
    channel_id  BLOB PRIMARY KEY,
    name        TEXT,
    joined_at   INTEGER NOT NULL,
    last_seen   TEXT
);

CREATE TABLE sealed_inbox (
    msg_hash    TEXT PRIMARY KEY,
    data        BLOB NOT NULL,      -- decrypted plaintext
    sender      BLOB NOT NULL,      -- 32 bytes pubkey
    received_at INTEGER NOT NULL,
    read        INTEGER DEFAULT 0
);
```

Blocklist is local only. Never transmitted. Never shared.
Nicknames are local only. Never transmitted. Never shared.

---

## Node Configuration

```toml
[identity]
keyfile = "/etc/gandrd/identity.key"

[network]
listen_port = 4242
# yggdrasil config path or "embedded"
yggdrasil_config = "/etc/yggdrasil/yggdrasil.conf"

[peering]
seed_node       = false
max_peers       = 200
trust_new_peers = "neutral"   # untrusted|neutral|trusted|vouched

# hardcoded seed nodes (Yggdrasil addresses)
seeds = [
    "200:xxxx:xxxx:xxxx::1",  # primary seed, Philippines
    "200:yyyy:yyyy:yyyy::1",  # secondary seed, Iceland
]

[capabilities]
chat    = true
feed    = true
forum   = true
storage = false   # true on dedicated storage nodes
relay   = true
seed    = false   # true on bootstrap nodes

[limits]
max_payload_size = 65535     # bytes
max_message_age  = 604800    # 7 days in seconds
rate_limit_rpm   = 600       # per peer
max_connections  = 500

[ipc]
socket = "/var/run/gandrd/gandr.sock"

[logging]
level = "error"   # debug|info|warn|error
file  = ""        # empty = stderr only
# NO access logs. NO message logs. NO peer identity logs.
# Error logs only. By design.
```

---

## IPC Protocol (gandrd ↔ gandr)

Unix socket. Binary protocol. Same envelope format as network protocol
but with IPC-specific message types.

```go
const (
    IPCSend        uint8 = 0x01  // client → daemon: send a message
    IPCSubscribe   uint8 = 0x02  // client → daemon: subscribe to channel/feed
    IPCUnsubscribe uint8 = 0x03  // client → daemon: unsubscribe
    IPCFetch       uint8 = 0x04  // client → daemon: fetch content by hash
    IPCPeerList    uint8 = 0x05  // client → daemon: get peer list
    IPCProfile     uint8 = 0x06  // client → daemon: get/set profile
    IPCIncoming    uint8 = 0x80  // daemon → client: incoming message
    IPCDelivered   uint8 = 0x81  // daemon → client: delivery confirmation
    IPCPeerUpdate  uint8 = 0x82  // daemon → client: peer status changed
    IPCError       uint8 = 0xFF  // daemon → client: error response
)
```

Client connects to socket on start. Daemon streams IPCIncoming messages.
Client sends IPCSend for outgoing. Clean separation — client has zero network code.

---

## Client TUI

### Requirements

- Terminal-native. Must work in 80x24 minimum.
- Must work at 40x20 for cyberdeck/small screen deployments.
- Color mode and no-color mode (auto-detect from $TERM).
- Must work over SSH.
- No mouse required. Keyboard driven entirely.
- Bubble Tea for rendering.

### Layout

```
┌─────────────────────────────────────────────────────────┐
│ GANDR  ᚷ          peers:12  [your_name]  [●connected]   │
├────────────────┬────────────────────────────────────────┤
│ CHANNELS       │                                        │
│ ▶ general      │  [content area]                        │
│   tech         │                                        │
│   random       │                                        │
│                │                                        │
│ PEOPLE         │                                        │
│   mara         │                                        │
│   aira         │                                        │
│   john         │                                        │
│                │                                        │
│ ──────────     │                                        │
│ [feed]         │                                        │
│ [forum]        ├────────────────────────────────────────┤
│ [sealed: 2]    │ > _                                    │
└────────────────┴────────────────────────────────────────┘
```

Sealed message count shown in sidebar with unread indicator.
People list shows nicknames (your local names) not announced display names unless no nickname set.

### Key Bindings

```
Tab / Shift+Tab    cycle panels
j / k              scroll up/down (vim style)
Enter              select / send
Esc                back / cancel / close
n                  set/edit nickname for selected sender
p                  view profile of selected sender
g                  view guestbook of selected sender
r                  reply to selected message
s                  send sealed message to selected sender
d                  toggle deniable on sealed compose
/                  search
?                  help
q                  quit (with confirm)
```

### Commands

```
/nick <pubkey|nickname> <new_name>    set nickname
/note <pubkey|nickname> <text>        add/update private note
/block <pubkey|nickname>              block locally
/unblock <pubkey|nickname>            unblock
/trust <pubkey|nickname> <level>      set trust level (neutral/trusted/vouched)
/profile                              view your own profile
/profile <pubkey|nickname>            view someone's profile
/set name <text>                      set your display name
/set status <text>                    set status
/set mood <text>                      set mood
/set np <text>                        set now playing
/set bio <text>                       set bio
/set theme bg <hex>                   set background color
/set theme fg <hex>                   set foreground color
/set theme accent <hex>               set accent color
/set theme font <mono|sans|serif|pixel>
/set theme layout <centered|left|terminal>
/peers                                list connected peers with trust levels
/connect <yggdrasil-addr>             manually connect to a peer
/seal <pubkey|nickname>               open sealed compose to recipient
/export nicknames <path>              export encrypted nickname file
/import nicknames <path>              import encrypted nickname file
/help                                 full command reference
```

### Profile View

```
╔══════════════════════════════════════════╗
║  byte_me                                 ║
║  200:ab3f::1                             ║
╠══════════════════════════════════════════╣
║  building things that shouldn't exist.   ║
║  älvdalen → manila.                      ║
╠══════════════════════════════════════════╣
║  mood: caffeinated                       ║
║  listening: wardruna                     ║
║  status: somewhere in the pacific        ║
╠══════════════════════════════════════════╣
║  LINKS                                   ║
║  kernelcraft.net                         ║
╠══════════════════════════════════════════╣
║  GUESTBOOK  (12 entries)                 ║
║  ┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄  ║
║  mara: miss you, come home               ║
║  aira: check the sensor data lol         ║
╚══════════════════════════════════════════╝
  [s]eal  [g]uestbook  [n]ickname  [Esc]back
```

Rendered using ProfileTheme colors if set. Falls back to terminal defaults.
Terminal layout mode (default) uses box-drawing chars as above.

---

## Systemd Unit

```ini
[Unit]
Description=Gandr Node Daemon
After=network.target yggdrasil.service
Wants=yggdrasil.service

[Service]
Type=simple
ExecStart=/usr/local/bin/gandrd --config /etc/gandrd/config.toml
Restart=always
RestartSec=10
User=nobody
Group=nogroup
NoNewPrivileges=true
ProtectSystem=strict
ProtectHome=true
PrivateTmp=true
PrivateDevices=true
CapabilityBoundingSet=
AmbientCapabilities=
SystemCallFilter=@system-service
ReadWritePaths=/var/lib/gandrd
RuntimeDirectory=gandrd

[Install]
WantedBy=multi-user.target
```

---

## Build System

### Reproducible Builds

Same source = same binary. Always. Anyone can verify.

```makefile
VERSION    := $(shell git describe --tags --always --dirty)
BUILD_DATE := $(shell date -u +'%Y-%m-%dT%H:%M:%SZ')
LDFLAGS    := -s -w -X main.Version=$(VERSION) -X main.BuildDate=$(BUILD_DATE)
GOFLAGS    := -trimpath

.PHONY: build sign release

build:
	GOOS=linux   GOARCH=amd64 go build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o dist/gandrd-linux-amd64    ./cmd/gandrd/
	GOOS=linux   GOARCH=arm64 go build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o dist/gandrd-linux-arm64    ./cmd/gandrd/
	GOOS=linux   GOARCH=arm   go build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o dist/gandrd-linux-arm      ./cmd/gandrd/
	GOOS=darwin  GOARCH=arm64 go build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o dist/gandrd-darwin-arm64   ./cmd/gandrd/
	GOOS=windows GOARCH=amd64 go build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o dist/gandrd-windows-amd64.exe ./cmd/gandrd/
	GOOS=linux   GOARCH=amd64 go build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o dist/gandr-linux-amd64     ./cmd/gandr/
	GOOS=linux   GOARCH=arm64 go build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o dist/gandr-linux-arm64     ./cmd/gandr/
	GOOS=linux   GOARCH=arm   go build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o dist/gandr-linux-arm       ./cmd/gandr/
	GOOS=darwin  GOARCH=arm64 go build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o dist/gandr-darwin-arm64    ./cmd/gandr/
	GOOS=windows GOARCH=amd64 go build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o dist/gandr-windows-amd64.exe ./cmd/gandr/

sign:
	sha256sum dist/* > dist/SHA256SUMS
	gpg --detach-sign --armor dist/SHA256SUMS

release: build sign
	gh release create $(VERSION) dist/* --title "$(VERSION)"
	bash scripts/mirror.sh
```

---

## Security Requirements

**These are requirements not suggestions.**

- No logging of IP addresses or Yggdrasil addresses in production builds
- No logging of message content. Ever. Under any circumstance.
- No logging of who talked to whom
- Server stores content by hash only — no user-content mapping
- Seizure of server yields: encrypted blobs, content hashes, peer Yggdrasil addresses. Nothing actionable.
- Block list: local to client, never transmitted, never visible to nodes
- Nicknames: local to client, never transmitted, never visible to nodes
- All inter-node traffic: Yggdrasil encryption + session layer encryption (double encrypted)
- Invalid signatures: dropped silently, no error response, no log entry
- Timestamp enforcement: non-negotiable, not configurable
- Rate limiting: per peer, enforced in daemon
- Sealed messages: node stores opaque blobs only, cannot decrypt, cannot be compelled to produce content it does not have
- The daemon has no admin interface, no management API, no web UI — config file and Unix socket only

---

## What Not To Build

**Never. Not in v1. Not in v2. Not ever.**

- Web interface
- REST API
- GraphQL
- Browser client
- OAuth or any SSO
- Email verification
- Phone verification
- Age verification
- Real name requirements
- Central user database
- Analytics of any kind
- Crash reporting
- Automatic updates
- Advertising hooks
- Recommendation algorithm
- Engagement optimization
- Read receipts without explicit per-conversation opt-in
- Python client (deferred indefinitely — attack surface, supply chain risk)
- Admin dashboard
- Moderation tools (social sanction only)
- Content flagging system
- Appeal process (there is no moderator to appeal to)

---

## Build Order

Build in this exact sequence.
Each step must work, be tested, and be reviewed before proceeding to the next.
Do not skip ahead. Do not parallelize. Crypto bugs found late are protocol bugs forever.

### 1. pkg/crypto

Key generation. Sign. Verify. Seal. Open. Encrypt. Decrypt.
Ed25519, X25519, ChaCha20-Poly1305, XChaCha20-Poly1305, HKDF, Argon2id.
Keyfile encrypt/decrypt with Argon2id KDF.

**Unit tests: exhaustive.**
**Fuzz tests: deserialization paths.**
**Do not proceed until crypto package has 100% test coverage.**

### 2. pkg/proto

Message envelope. All message type structs. MessagePack serialization.
Serialize → deserialize roundtrip tests for every message type.
Fuzz test the deserializer — feed it garbage, make sure it never panics.
Sign and verify integration with pkg/crypto.

### 3. pkg/network

Yggdrasil integration. Raw send and receive.
Two instances on localhost communicating over Yggdrasil loopback.
Prove bytes go in one end and come out the other correctly.

### 4. pkg/federation

Handshake: HELLO → HELLO_ACK → HELLO_COMPLETE → PEER_POLICY.
Two nodes. Successful peer establishment.
Session encryption verified.
Trust level assignment.

**This is the proof of concept milestone.**
Two gandrd instances peering successfully = the protocol works.

### 5. pkg/store

Content-addressed storage.
Store by hash. Retrieve by hash. Prune by age.
Deduplication test: same content twice = one stored object.

### 6. pkg/identity

Keypair management.
Encrypted keyfile: write, read, wrong passphrase fails correctly.
Profile serialize/deserialize.

### 7. cmd/gandrd

Daemon tying network + federation + store + identity together.
Reads config. Starts Yggdrasil. Accepts peer connections. Handles all message types.
Exposes Unix socket for IPC.
Runs as systemd service correctly.

### 8. pkg/ipc

IPC protocol between gandrd and gandr.
Client connects to socket. Daemon streams incoming messages.
Client sends outgoing messages. Daemon routes to network.

### 9. cmd/gandr

TUI client.
Connects to gandrd via Unix socket.
Chat first. Then feed. Then forum. Then profiles. Then guestbook. Then sealed.
Nicknames from day one — they are foundational not an add-on.

### 10. docs/

PROTOCOL.md written from the implementation, not ahead of it.
FEDERATION.md same.
SEALED.md same.
SETUP.md: how to run a node. Assumes Linux. Assumes Yggdrasil installed.

---

## Notes for Claude Code

- Go 1.22+ only
- Standard library preferred. Every external dependency is a liability.
- Approved dependencies listed above. No others without explicit justification.
- Every exported function and type has a doc comment
- Table-driven tests everywhere
- Fuzz tests for all deserialization paths
- No global state
- context.Context passed everywhere for clean cancellation
- Structured errors using fmt.Errorf with %w wrapping
- No panic() in library code — return errors
- The code will be read by security researchers. Write as if they are hostile and looking for weaknesses.
- Start with pkg/crypto. Prove it. Then move.
- When in doubt, do less. A smaller correct implementation beats a larger buggy one.
- The name is Gandr. The rune is ᚷ (Gebo). The daemon is gandrd. The client is gandr.

---

## Final Note

This is infrastructure, not a product.
It has no business model because it doesn't need one.
It has no terms of service because there is no service.
It has no privacy policy because it doesn't collect data to have a policy about.

The network is the community that runs it.
The protocol is the only authority.
The keypair is the only identity.

Build it right. Build it once.
# Gandr — TUI Design Prompt
# For Claude with full capabilities (claude-opus-4-5 or latest)
# Scope: complete terminal UI implementation

---

## What You Are Building

The Gandr TUI client (`gandr`) — a terminal-based interface for the
Gandr federated sovereign network. This is the only interface. There is
no web client. There is no mobile app. This IS the product.

The aesthetic is BBS — bulletin board systems of 1990-1995. Green
phosphor on black. ANSI art. Box-drawing characters. The kind of
interface that rewarded the people who sought it out. Not nostalgia for
its own sake — this aesthetic communicates exactly what Gandr is: not
a product, not a platform, a place you find.

---

## Tech Stack

- **Language:** Go 1.22+
- **TUI library:** Bubble Tea (github.com/charmbracelet/bubbletea)
- **Styling:** Lip Gloss (github.com/charmbracelet/lipgloss)
- **Font requirement:** Monospace only. Share Tech Mono preferred in
  terminal environments that support font config. Falls back to any
  monospace. All layout uses box-drawing characters that work in any
  terminal with UTF-8.
- **Color:** 256-color ANSI. Primary palette: green phosphor on black.
  Full palette defined below.
- **Target terminals:** Any 256-color UTF-8 terminal. Must degrade
  gracefully to 16-color. Must work over SSH. Must work at 80x24
  minimum. Must work at 40x20 for cyberdeck/small screen.

---

## Color Palette

```go
// Primary phosphor palette
ColorBG         = "#0a0a0a"  // near-black background
ColorBGSurface  = "#050505"  // slightly lighter surface
ColorBGHover    = "#001200"  // hover state
ColorBGActive   = "#001a00"  // active/selected state

ColorGreen1     = "#00ff41"  // brightest — your messages, active elements
ColorGreen2     = "#00cc33"  // message body text
ColorGreen3     = "#009926"  // secondary text, sidebar items
ColorGreen4     = "#006619"  // dim text, borders, hints
ColorGreen5     = "#004d14"  // very dim — timestamps, addresses
ColorGreenDark  = "#003300"  // borders
ColorGreenDark2 = "#002200"  // subtle dividers
ColorGreenDark3 = "#001a00"  // background accents
ColorGreenDark4 = "#001200"  // deepest accent

ColorTeal       = "#00ffaa"  // trusted/vouched peers, special elements
ColorBright     = "#66ff88"  // ANSI art bright highlights

// Semantic
ColorYou        = "#00ff41"  // your own messages/identity
ColorTrusted    = "#00ffaa"  // vouched peers
ColorNeutral    = "#009926"  // neutral trust peers
ColorNew        = "#006619"  // new/untrusted nodes
ColorSealed     = "#006619"  // sealed message indicator
ColorMention    = "#00ffaa"  // @mentions
ColorCode       = "#66ff88"  // inline code
ColorDanger     = "#ff4136"  // errors only — used sparingly
```

---

## ANSI Art Header

This is the centerpiece. Rendered at the top of every screen.
The word GANDR in block ASCII art with gradient shading from dim to
bright, the rune ᚷ (Gebo) centered above or beside it.

Exact render (copy verbatim into Go string literal):

```
░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░
░░▓▓▓▓░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░▓▓▓▓░░
░▓▓████▓░░▓███▓░░▓██████████▓░░▓████▓░░░ᚷ░░░▓████▓░░▓██████████▓░░▓███▓░░▓████▓░
░██████████████████████████████████████░░░░░████████████████████████████████████░
░██████████████████████████████████████░░░░░████████████████████████████████████░
░▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓░░
░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░
                              ── a sending ──
```

Color the header using lipgloss with the gradient:
- Outermost ░ characters: ColorGreenDark (dim)
- ▓ border characters: ColorGreen4
- ██ block characters inner: ColorGreen2
- ██ block characters brightest row: ColorBright
- ᚷ rune: ColorTeal
- "── a sending ──" tagline: ColorGreen5, letter-spacing via spaces

The header is fixed — never scrolls, never changes, always visible.
Height: 9 lines. Width: auto-center to terminal width, min 80 chars.

If terminal width < 80: render compact header:
```
▓ ᚷ GANDR ▓
─ a sending ─
```

---

## Status Bar

One line, always visible, below header:

```
◈ GANDR v0.1.0 · YGGDRASIL    ⬡ peers: 12 · nodes: 847    identity: byte_me · trust: vouched
```

Colors:
- Left section: ColorGreen4
- Center section: "peers:" ColorGreen4, count ColorGreen1
- Right section: "identity:" ColorGreen4, name ColorGreen1,
  "trust:" ColorGreen4, level color based on trust:
    - vouched: ColorTeal
    - trusted: ColorGreen1
    - neutral: ColorGreen3
    - untrusted: ColorGreen4

Separator: `·` in ColorGreenDark

---

## Tab Bar

Below status bar. ANSI art tab selectors.

Each tab has:
1. Small ANSI art icon (3 lines of box-drawing chars)
2. Label in ALL CAPS, letter-spaced

Tab icons (3-line box art, 4 chars wide):

```
CHAT:           FEED:           FORUM:          SEALED:         PEERS:          PROFILE:
┌──┐            ┌──┐            ┌──┐            ┌──┐            ┌──┐            ┌──┐
│▓▓│            │░▓│            │▓░│            │◈◈│            │⬡⬡│            │██│
└──┘            └──┘            └──┘            └──┘            └──┘            └──┘
```

Tab states:
- Inactive: ColorGreen4 art, ColorGreen4 label
- Hover: ColorGreen3 art, ColorGreen3 label, ColorBGHover background
- Active: ColorGreen1 art, ColorGreen1 label, ColorBGActive background,
  bottom border line in ColorGreen1

Unread badge: top-right of tab, small box:
```
┤3├  (ColorGreen1 text, ColorGreenDark border)
```

Tab order: CHAT · FEED · FORUM · SEALED · PEERS · PROFILE
PROFILE tab right-aligned (pushed to far right with spacer).

Key bindings for tabs: 1-6 or Tab/Shift+Tab to cycle.

---

## Layout

```
┌─────────────────────────────────────────────────────────────────────┐
│  [ANSI HEADER - 9 lines]                                            │
├─────────────────────────────────────────────────────────────────────┤
│  [STATUS BAR - 1 line]                                              │
├─────────────────────────────────────────────────────────────────────┤
│  [TAB BAR - 4 lines]                                                │
├───────────────────┬─────────────────────────────────────────────────┤
│  SIDEBAR          │  MAIN CONTENT                                   │
│  (160px / 20%)    │  (remaining width)                              │
│                   │                                                 │
│  - channels       │  [channel header - 1 line]                      │
│  - people         │  [message area - scrollable]                    │
│  - network        │  [input area - 2 lines]                         │
│                   │                                                 │
└───────────────────┴─────────────────────────────────────────────────┘
```

Sidebar visibility: hidden on < 80 col, toggle with `\` key.

---

## Sidebar

### Channels section

```
 CHANNELS
 ▶ # general          7
   # tech
   # random
   # ph-mesh
   # nordic
```

- Section heading: ColorGreen5, letter-spaced
- Active channel: `▶ ` prefix in ColorGreen1, name ColorGreen1,
  ColorBGActive background
- Inactive: ColorGreen3, no prefix
- Unread count: right-aligned, ColorGreenDark border box

### People section

```
 PEOPLE
 ● mara
 ● aira
 ○ john
 ○ shiva
```

- Online indicator: `●` ColorGreen1 (online), `○` ColorGreen4 (away/offline)
- Names: your nicknames if set, announced name if not, truncated pubkey
  if neither
- Truncated pubkey format: `~ab3f` (tilde + first 4 hex chars)

### Network section

```
 NETWORK
 ygg: 200:ab3f::1
 score ██████░░  0.73
```

- Your Yggdrasil address, ColorGreen5
- Trust score: 8-pip bar, filled pips ColorGreen2, empty ColorGreenDark2
- Score number: ColorGreen3

---

## Chat View

### Channel Header

```
 # general    topic: building things that shouldn't exist
```

ColorGreen4 label, ColorGreen3 topic. Full-width separator line below.

### Message Format

```
 byte_me  [you]  02:17
 finishing gandrd first. pkg/crypto tests passing
```

Fields:
- Nickname: ColorTrusted if vouched, ColorGreen1 if you,
  ColorGreen3 if neutral, ColorGreen4 if new/unknown
- Trust badge: small inline box
  - `[you]` ColorGreen1
  - `[vouched]` ColorTeal
  - `[trusted]` ColorGreen2
  - `[0.12·new]` ColorGreen5
- Timestamp: ColorGreen5
- Body: ColorGreen2, mentions in ColorTeal, inline code in ColorBright

### Sealed Message Display

```
 mara  ⊕ sealed  02:21
 [ press s to open · deniable ]
```

- Left border: 2px line in ColorGreen4
- Everything slightly dimmed (0.7 opacity equivalent)
- ⊕ indicator: ColorGreen4
- `deniable` label if applicable
- Body placeholder italic, ColorGreen4

### Dividers

```
 ── 2026-06-12 ──────────────────────────────
 ── now ──────────────────────────────────────
```

ColorGreenDark, centered date/label, dashes fill to terminal width.

### Input Area

```
 byte_me@general ❯ _
 n=nickname · p=profile · s=seal · r=reply · ?=help
```

- Prompt: ColorGreen4 username, ColorGreen3 `@channel`, ColorGreen1 `❯`
- Cursor: blinking block `▋`
- Hint line: ColorGreen5, key letters ColorGreen4

### Compose — Sealed Message

When composing a sealed message, the input area transforms:

```
 ┌── SEALED TO: mara ──────────────────────── [D]=deniable ──┐
 │ byte_me ❯ _                                                │
 └────────────────────────────────────────────────────────────┘
```

Border in ColorGreen4, "SEALED TO:" and recipient in ColorTeal,
`[D]=deniable` toggle indicator.

---

## Feed View

Feed posts in reverse chronological order.

```
 ── FEED ─────────────────────────────────────────────────────

 byte_me  [you]  2026-06-12 02:17
 ╔════════════════════════════════════════════════════════════╗
 ║ pkg/crypto passing. moving to pkg/proto tomorrow.          ║
 ║ gandr is becoming real.                                    ║
 ╚════════════════════════════════════════════════════════════╝
   ↩ 2 replies    ♦ 4 nodes propagated

 mara  [vouched]  2026-06-12 01:44
 ╔════════════════════════════════════════════════════════════╗
 ║ arkin smiled today. or gas. hard to tell at 3 weeks        ║
 ╚════════════════════════════════════════════════════════════╝
   ↩ 5 replies    ♦ 12 nodes propagated
```

Post box: ColorGreenDark border, ColorGreen2 content
Reply count and propagation stats: ColorGreen4
`♦` propagation icon: ColorGreen4

---

## Forum View

```
 ── FORUM ────────────────────────────────────────────────────

 ┌ [TECH] ────────────────────────────────────────────────────┐
 │ Yggdrasil routing at scale — theory and practice           │
 │ byte_me · 3 replies · 6 nodes · 2026-06-11                 │
 └────────────────────────────────────────────────────────────┘

 ┌ [GENERAL] ─────────────────────────────────────────────────┐
 │ First impressions — found this via kernelcraft.net         │
 │ node_7f3a · 0 replies · 2 nodes · 2026-06-12               │
 │ [trust: 0.12 · new node — read-only posting until 0.3]     │
 └────────────────────────────────────────────────────────────┘
```

Category badge: ColorGreen4 border, ColorGreen3 text
Thread title: ColorGreen1 if unread, ColorGreen3 if read
Meta line: ColorGreen5
Trust warning for low-trust nodes: ColorGreen4 italic

Thread view (opened):
```
 ┌ TECH ──────────────────────────────────────────────────────┐
 │ Yggdrasil routing at scale                                 │
 │ byte_me · 2026-06-11                                       │
 └────────────────────────────────────────────────────────────┘

 byte_me  [you]  ── OP ──────────────────────────────────────
 The spanning tree convergence at 10k nodes is manageable...
 ────────────────────────────────────────────────────────────

 node_7f3a  [neutral]  ── reply 1 ──────────────────────────
 What happens during a partition longer than the replay window?
 ────────────────────────────────────────────────────────────
```

---

## Sealed Inbox View

```
 ── SEALED INBOX ─────────────────────────────────────────────
 ⊕ from mara · 02:21 · deniable
 ⊕ from aira · 01:55

 ── OPENED ───────────────────────────────────────────────────
 ✓ from john · 2026-06-11 · read
```

Unopened: ColorTeal indicator, ColorGreen2 text
Opened: ColorGreen4 indicator, ColorGreen4 text
`s` to open selected, decryption happens locally, content displayed
inline in ColorGreen2.

---

## Peers View

```
 ── PEERS ────────────────────────────────────────────────────

 200:ab3f::1  mara  [vouched]  ██████████  0.94  ↑↓ 2.1kB
 200:c291::1  aira  [trusted]  ████████░░  0.81  ↑↓ 847B
 200:7f3a::1  ~7f3a [neutral]  ████░░░░░░  0.41  ↑↓ 312B
 200:ff2a::1  ~ff2a [new]      █░░░░░░░░░  0.12  ↑↓ 44B

 ── TRUST SCORE BREAKDOWN ────────────────────────────────────
 uptime ████████  longevity ██████  behavior ██████  vouchers ████

 [c]=connect  [d]=disconnect  [v]=vouch  [b]=ban  [n]=nickname
```

Score bar: 10 pips, filled ColorGreen2, empty ColorGreenDark2
Traffic indicator: ColorGreen4
Breakdown bars: section labels ColorGreen5, bars ColorGreen3

---

## Profile View

Your own profile and viewing others'.

```
 ╔══════════════════════════════════════════════════════════════╗
 ║  byte_me                                            ᚷ       ║
 ║  200:ab3f::1                                                 ║
 ╠══════════════════════════════════════════════════════════════╣
 ║  building things that shouldn't exist.                       ║
 ║  älvdalen → manila.                                          ║
 ╠══════════════════════════════════════════════════════════════╣
 ║  mood:      caffeinated                                      ║
 ║  listening: wardruna — helvegen                              ║
 ║  status:    somewhere in the pacific                         ║
 ╠══════════════════════════════════════════════════════════════╣
 ║  LINKS                                                       ║
 ║  → kernelcraft.net                                           ║
 ╠══════════════════════════════════════════════════════════════╣
 ║  GUESTBOOK  (12 entries)                                     ║
 ║  ┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄    ║
 ║  mara: miss you, come home soon                              ║
 ║  aira: sensor data is looking good btw                       ║
 ╚══════════════════════════════════════════════════════════════╝
   [s]eal  [g]uestbook entry  [n]ickname  [e]dit (own only)
```

Box: double-line border ColorGreen2
Name: ColorTeal, rune ᚷ right-aligned ColorTeal
Address: ColorGreen5
Bio: ColorGreen3
Field labels: ColorGreen5 right-padded to align, values ColorGreen3
Section dividers: ╠══╣ ColorGreen2
Guestbook entries: ColorGreen3 name, ColorGreen2 message
Action bar: ColorGreen5 keys, ColorGreen4 labels

Profile theme respects the ProfileTheme fields — BgColor/FgColor/AccentColor
from the profile data override the default green palette for that profile
only. The surrounding UI stays green.

---

## Nickname Quick-Set Overlay

Triggered by `n` key on any message. Renders as an inline overlay
(not a modal — no position:fixed) just below the selected message:

```
 ┌── SET NICKNAME ──────────────────────────────────────────┐
 │ pubkey: db53d0e9...                                       │
 │ current: (none)                                          │
 │                                                          │
 │ nickname ❯ _                                             │
 │ note     ❯ _                  (optional, private)        │
 │                                                          │
 │ [Enter]=save  [Esc]=cancel                               │
 └──────────────────────────────────────────────────────────┘
```

Border: ColorGreen4
Labels: ColorGreen5
Input: ColorGreen1
Key hints: ColorGreen5

---

## Trust Score Display

Inline in messages and peer list. Two formats:

Short (in messages):
```
[vouched]    ColorTeal border+text
[trusted]    ColorGreen2 border+text
[neutral]    ColorGreen3 border+text
[0.12·new]   ColorGreen5 border+text
[you]        ColorGreen1 border+text
```

Bar (in peers view, profile):
```
████████░░  0.81
```
Filled: ColorGreen2, empty: ColorGreenDark2, number: ColorGreen3

Score component breakdown (peers view only):
```
uptime ████████  longevity ██████  behavior ██████  vouchers ████
```
Labels: ColorGreen5, bars: ColorGreen3 (uptime), ColorGreen2 (behavior),
ColorGreen4 (longevity), ColorTeal (vouchers)

---

## Error States

Minimal. No popups. Inline in the relevant area.

Connection lost:
```
 ── CONNECTION LOST ── retrying in 5s ── [r]=retry now ──────
```
ColorGreen4, pulsing (alternate between ColorGreen4 and ColorGreen5
every 500ms using a Bubble Tea tick command)

Decryption failed (sealed message):
```
 ⊗ decryption failed — message may not be for this identity
```
ColorGreen4

Low trust warning (forum posting attempt):
```
 ✗ trust score 0.12 — reach 0.30 to post. current: read-only
```
ColorGreen4

---

## Key Bindings — Complete

```
Navigation
  1-6          switch tabs (chat/feed/forum/sealed/peers/profile)
  Tab          next tab
  Shift+Tab    previous tab
  j / ↓        scroll down
  k / ↑        scroll up
  g            scroll to top
  G            scroll to bottom
  \            toggle sidebar
  Esc          back / cancel / close overlay

In any message list
  n            set nickname for sender under cursor
  p            view profile of sender under cursor
  s            send sealed message to sender under cursor
  r            reply to message under cursor
  Enter        expand/open selected item

Chat specific
  /            focus input (also any printable char)
  Enter        send message

Feed specific
  Enter        expand post and replies
  n            new post

Forum specific
  Enter        open thread
  n            new thread (requires trust >= 0.30)

Sealed inbox
  Enter / s    open and decrypt selected sealed message
  d            compose deniable sealed message

Peers
  c            connect to new peer (prompts for Yggdrasil addr)
  v            vouch for selected peer
  b            ban selected peer (with confirmation)
  n            set nickname for selected peer

Profile
  e            edit your own profile (only available on your profile)

Global
  ?            show help overlay
  q            quit (with confirm: "quit gandr? [y/N]")
  Ctrl+C       force quit
```

---

## Startup Sequence

On launch before IPC connection established:

```
░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░
░  ᚷ  GANDR                   ░
░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░

identity passphrase: ▋

```

After passphrase entered, identity loaded:

```
gandr: identity loaded — you are db53d0e9...

connecting to gandrd at /var/run/gandrd/gandr.sock
```

If gandrd not running:
```
gandr: gandrd not running
start with: sudo systemctl start gandrd
or:         gandrd --config /etc/gandrd/config.toml
```

If socket path non-default, flag:
```
gandr --socket /path/to/gandr.sock
```

---

## Responsive Behavior

### >= 120 columns, >= 40 rows
Full layout. Full header. Full sidebar. All features.

### 80-119 columns, 24-39 rows
Compact header (3 lines). Sidebar visible but narrower (12 chars).
Tab labels hidden, icons only.

### 40-79 columns (cyberdeck mode)
Minimal header (2 lines: just `▓ ᚷ GANDR ▓`).
No sidebar. Tabs as single-char icons in a row.
Input takes full width.
Message metadata compressed to single line.

### < 40 columns
Render warning:
```
terminal too narrow
min 40 columns
current: 38
```

---

## Implementation Notes for Claude Code

### Bubble Tea model structure

```go
type Model struct {
    // Layout
    width      int
    height     int
    activeTab  Tab
    showSidebar bool

    // Content models (each tab has its own sub-model)
    chat    ChatModel
    feed    FeedModel
    forum   ForumModel
    sealed  SealedModel
    peers   PeersModel
    profile ProfileModel

    // Overlays (rendered on top of active tab)
    overlay     OverlayType  // none, nickset, help, quit confirm
    overlayModel tea.Model

    // IPC state
    ipc         *ipc.Client
    connected   bool
    retryIn     int

    // Identity
    identity    *identity.Identity
    nicknames   *nickname.Store
}
```

### Rendering order

1. Header (fixed, always rendered first)
2. Status bar
3. Tab bar
4. Main content area (sidebar + active tab content)
5. Overlay (if active — renders over content area only, not header/tabs)

### ANSI art rendering

Store the header art as a raw string constant. Use lipgloss to apply
colors character by character based on character type:
- `░` → ColorGreenDark
- `▓` → ColorGreen4
- `█` inner rows → ColorGreen2
- `█` brightest row → ColorBright
- `ᚷ` → ColorTeal

Use lipgloss `Style.Foreground(lipgloss.Color(hex))` for each color.

### Scrollable areas

Implement a Viewport (bubbletea/bubbles viewport) for:
- Message list (chat)
- Feed posts
- Forum thread list
- Forum thread content
- Sealed inbox
- Peer list

Each viewport tracks its own scroll position. Switching tabs preserves
scroll position.

### Nickname store

```go
type Store struct {
    db *sql.DB  // local encrypted sqlite
}

func (s *Store) Get(pubkey [32]byte) (*Nickname, error)
func (s *Store) Set(pubkey [32]byte, name, note string) error
func (s *Store) Delete(pubkey [32]byte) error
func (s *Store) List() ([]*Nickname, error)
func (s *Store) Search(query string) ([]*Nickname, error)
```

Nickname lookups happen on every message render. Cache in memory
(map[pubkey]Nickname) refreshed on Set/Delete. Never a round-trip to
sqlite on render.

### Trust score display

```go
func TrustBadge(score float64, isYou bool) string
func TrustBar(score float64, width int) string
func TrustColor(score float64) lipgloss.Color
```

Thresholds:
```
isYou            → "[you]" ColorGreen1
score >= 0.6     → "[vouched]" ColorTeal
score >= 0.3     → "[trusted]" ColorGreen2
score >= 0.1     → "[neutral]" ColorGreen3
score < 0.1      → fmt.Sprintf("[%.2f·new]", score) ColorGreen5
```

### IPC connection management

The client runs a background goroutine that maintains the IPC
connection to gandrd. On disconnect: exponential backoff retry
(1s, 2s, 4s, 8s, max 30s). Status bar shows retry countdown.
On reconnect: re-subscribe to all active channels, re-request peer list.
The user never loses their compose state during reconnect.

### Profile theme application

When rendering a profile, extract ProfileTheme from the ProfilePayload.
Create a temporary lipgloss style override using the theme's colors.
Apply only to the profile box content — everything outside stays
in the default green palette. Restore default styles after profile
render.

---

## What Not To Build

- No mouse support (keyboard only)
- No images (text only)
- No web rendering, no HTML, no markdown renderer — Gandr markup only
- No notification popups or toast messages — status bar updates only
- No sound
- No animations beyond the connection-lost pulse and cursor blink
- No settings screen — config is in config.toml, not the UI
- No onboarding wizard
- No help tooltips — the hint bar at input is sufficient
- No color themes — the green phosphor palette is the identity
  (Profile themes affect profile view only)

---

## Final Note

This interface should feel like connecting to something that has been
running in a basement in the Philippines since before the web was a
thing. Clean. Purposeful. Not pretty in the modern sense — beautiful
in the way that a well-used tool is beautiful.

The person using this chose to be here. Respect that choice with
an interface that doesn't patronize them.
