# Gandr — Project Reference

Federated, censorship-resistant communication network. Go 1.22+ (built
with 1.26). Two binaries: `gandrd` (node daemon), `gandr` (TUI client).
Transport exclusively over Yggdrasil (embedded in-process — no TUN, no
root, no external daemon). Identity = Ed25519 keypair, no accounts.
Signed binary protocol; no HTTP/REST/web, ever. This is infrastructure,
not a product. Code will be read by hostile security researchers —
write accordingly.

## Commands

```sh
make test          # full suite, includes 2-node yggdrasil integration (~40s)
make test-short    # skips yggdrasil integration tests
make build         # cross-compiled daemon + native client into dist/
make fuzz          # all fuzz targets, 30s each (scripts/fuzz.sh)
sudo make install  # scripts/install.sh — idempotent system install
```

Visual TUI inspection: `GANDR_DUMP_VIEW=/tmp/f.txt go test ./pkg/tui/ -run TestDumpView && cat /tmp/f.txt`

## Architecture (build order = dependency order)

1. `pkg/crypto` — ALL cryptography lives here, nowhere else. Ed25519
   sign/verify, Ed25519→X25519 conversion, XChaCha20-Poly1305, HKDF,
   Argon2id keyfiles, sealed messages (Noise X, 1024-byte zero padding,
   `content_len` field disambiguates padding, deniable mode = no inner
   signature). 95.2% coverage; the gap is provably-dead error branches,
   documented in the package doc.
2. `pkg/proto` — wire envelope (78-byte header + payload + 64-byte sig,
   min 142 bytes — the original spec said 112, which is wrong), 18
   message types, MessagePack payloads, rune-count limits, content id =
   SHA256(version‖type‖timestamp‖sender‖payload) (recipient excluded).
   Signature verified in `Decode` BEFORE payload deserialization.
   `MsgBlock`/`MsgNickname` (0x0E/0x0F) are local-only: unencodable AND
   undecodable on the wire by construction.
3. `pkg/network` — embedded yggdrasil-go core (`core.New` with
   ed25519-keyed self-signed cert). Ygg gives best-effort datagrams and
   the max envelope exceeds its 65535 MTU, so `mux.go` adds a 13-byte
   frame: fragmentation (max 4), ack/retransmit (250ms base, 5
   attempts, exp backoff), per-peer dedupe ring (1024 ids). Reliable,
   UNORDERED message delivery. Transport key ≠ identity key, always.
4. `pkg/federation` — 4-step handshake (HELLO→ACK→COMPLETE→POLICY),
   nonce echoes, ephemeral X25519 in signed envelopes, session key =
   HKDF(X25519(ephA,ephB), "gandr-session-v1"), direction-bound AAD
   (gandr-i2r-v1/r2i-v1) prevents reflection. Any violation = silent
   abort. Trust: untrusted/neutral/trusted/vouched (0x00–0x03), local
   only, never negotiated. Peer table memory-only by design.
5. `pkg/store` — content-addressed flat files `objects/<2hex>/<64hex>`,
   atomic temp+rename, prune by envelope timestamp, status msgs capped
   at 24h, corrupt objects self-delete on Get.
6. `pkg/identity` — encrypted keyfiles (salt‖nonce‖ct, Argon2id 3/64MiB/4).
7. `pkg/ipc` — Unix socket frames (magic 0x49, type, reqid, len, ≤1MiB).
   Requests 0x01–0x08 (Send/Sub/Unsub/Fetch/PeerList/Profile/Trust/
   Connect), pushes 0x80–0x82, error 0xFF. THE USER IDENTITY KEY LIVES
   IN THE CLIENT — the daemon only ever sees finished signed envelopes
   and is architecturally ignorant of its users. Socket is 0660
   gandrd:gandrd (docker.sock pattern).
8. `cmd/gandrd` — TOML config (BurntSushi), flood relay damped by
   storage dedup, per-peer RPM rate limit, delete = author-only (or
   profile owner for guestbook entries), hourly prune. Keyfiles MUST
   live under /var/lib/gandrd (systemd ProtectSystem=strict makes /etc
   read-only).
9. `pkg/clientdb` — client SQLite (mattn/go-sqlite3), application-layer
   encryption: every sensitive value XChaCha20-Poly1305 under
   HKDF(identity seed, "gandr-clientdb-v1") with table+rowkey AAD.
   Nicknames/blocklist NEVER leave this DB.
10. `pkg/tui` + `cmd/gandr` — BBS aesthetic. 4 local themes
    (theme.go: classic/midnight/paper/ice; palette.go styles rebuilt by
    applyTheme, no scattered colors), persisted in encrypted clientdb
    settings along with first-run entry-banner acceptance (banner.go —
    gates the app once per profile, GTFO exits, re-viewable from
    Settings→About). Big logo only on splash/About/banner; main view
    uses a compact 3-line header (status + tab line + rule) with a live
    traffic widget (stats.go — local byte totals only). 6 intent-based
    tabs: Messages (chat + sealed subview, `i` toggles), People
    (contacts + profile detail + own identity), Feed (dynamic-height
    stacked posts), Forum, Network (peers + diagnostics), Settings
    (8 sections, Appearance picks theme). Optional mouse (wheel, tab/
    sidebar clicks; --no-mouse), keyboard always complete. Terminal-
    safe glyphs with ASCII fallback (GANDR_ASCII=1 / TERM=dumb /
    non-UTF-8 locale). Overlays over content area only, IPC
    auto-reconnect (1→30s backoff, compose state survives), responsive:
    ≥120 full / 80–119 compact / 40–79 cyberdeck / <40 warning.
    Channel id = SHA256("gandr-channel:"+name).

## Conventions

- Stdlib preferred. Approved deps ONLY: yggdrasil-go, x/crypto,
  filippo.io/edwards25519 (stdlib's own vendored code, needed for
  Ed25519→X25519), msgpack, bubbletea+lipgloss (NOT bubbles —
  deliberately avoided), mattn/go-sqlite3 (client only), BurntSushi/toml.
  Justify any addition in README's dependency table.
- Invalid signature/timestamp/frame = drop SILENTLY. No error reply, no
  log. Non-negotiable.
- No logging of message content, peer identities, or who-talked-to-whom.
  Error logs only.
- Table-driven tests; fuzz every deserialization path; no panic() in
  library code; context.Context everywhere; errors wrapped with %w.
- Limits are rune counts, not bytes. Hash refs are 64-char lowercase hex.
- HKDF domain separators: gandr-sealed-v1, gandr-sealed-inner-v1,
  gandr-session-v1, gandr-clientdb-v1, gandr-i2r-v1, gandr-r2i-v1.
  Never reuse, never change.

## Display/protocol mappings (TUI)

Discrete trust levels map to display scores: vouched 0.80, trusted
0.45, neutral 0.20, untrusted 0.05. Badge thresholds: ≥0.6 vouched
(teal), ≥0.3 trusted, ≥0.1 neutral, else "[0.0x·new]". No fabricated
metrics: no "nodes propagated", no "behavior" score, no forum trust
gate (the network doesn't enforce one).

## Current state (2026-06-12)

All 10 build-order steps complete and tested end to end; two-daemon
full-stack test passes (federation over real yggdrasil loopback, chat/
profile/delete propagation through IPC clients). TUI redesign complete
per the BBS design prompt. install.sh added. ~11k lines.

Known deferrals: PeerIntro accepted but not auto-dialed; nickname
export/import; external-yggdrasil-daemon transport mode; wrapStyled is
clip-not-wrap for styled lines. NOTHING IS COMMITTED YET — git repo
initialized, zero commits.

Docs are written FROM the implementation (PROTOCOL/FEDERATION/SEALED/
SETUP.md in docs/) — keep them in sync when changing wire behavior.
