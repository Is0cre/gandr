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
