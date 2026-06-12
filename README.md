# Gandr ᚷ

A federated, censorship-resistant communication network.

The name is Old Norse — a sending, a magical transmission carried by
invisible force.

- No central authority. No company. No killswitch.
- Identity = Ed25519 keypair. You are your key. No accounts.
- Two binaries: `gandrd` (node daemon), `gandr` (terminal client).
- Transport over the Yggdrasil overlay exclusively — embedded, no
  external daemon, no TUN, no root.
- Signed binary protocol. No HTTP, no REST, no browser. Ever.
- Federation via mutual peering and local trust. No moderation;
  social sanction only.
- No telemetry, no logs of people, no analytics, no auto-update.
- The node is architecturally ignorant of its users. Seizure yields
  nothing actionable.

## Status

v0.1 — core protocol complete and tested end to end:

- `pkg/crypto` — Ed25519/X25519, XChaCha20-Poly1305, HKDF, Argon2id,
  sealed messages (Noise X), encrypted keyfiles. Fuzzed.
- `pkg/proto` — signed binary envelope, all 18 message types,
  MessagePack payload schemas, content addressing. Fuzzed.
- `pkg/network` — embedded Yggdrasil transport with a small reliability
  sublayer (fragment/ack/retransmit/dedupe).
- `pkg/federation` — 4-step signed handshake, session encryption,
  trust table. Two real nodes peer over Yggdrasil in the test suite.
- `pkg/store` — content-addressed object store, atomic writes, age
  pruning.
- `pkg/identity` — encrypted identity keyfiles.
- `pkg/ipc` + `cmd/gandrd` — the daemon; full two-daemon
  federation test: chat, profiles, deletes propagate end to end.
- `pkg/clientdb` + `pkg/tui` + `cmd/gandr` — terminal client in full
  BBS dress: green phosphor palette, ANSI block-art header, six tabs
  (chat / feed / forum / sealed / peers / profile), sidebar, trust
  badges and bars, inline overlays, sealed compose with deniable mode,
  nicknames (local-only petnames) on every surface, IPC auto-reconnect
  with exponential backoff. Responsive from 120-column full layout
  down to 40x20 cyberdeck mode.

## Build

```sh
make test    # full suite, includes two-node yggdrasil integration tests
make build   # cross-compiled daemon + native client into dist/
make fuzz    # all fuzz targets, 30s each
```

See `docs/SETUP.md` to run a node, `docs/PROTOCOL.md`,
`docs/FEDERATION.md`, and `docs/SEALED.md` for the wire details.

## Dependencies

Every dependency is a liability; this is the complete list and why:

| module | why |
|--------|-----|
| `yggdrasil-network/yggdrasil-go` | the overlay transport (mandated) |
| `golang.org/x/crypto` | X25519, XChaCha20-Poly1305, Argon2id, HKDF |
| `filippo.io/edwards25519` | Ed25519→X25519 conversion for sealed messages; same code vendored in Go's stdlib, which does not export it |
| `vmihailenco/msgpack` | payload serialization (mandated) |
| `charmbracelet/bubbletea`, `lipgloss` | client TUI (mandated) |
| `mattn/go-sqlite3` | client-local storage (mandated; client only) |
| `BurntSushi/toml` | config format mandated as TOML; stdlib has no TOML |

Client data is encrypted at the application layer (XChaCha20-Poly1305
keyed from the identity key, row-bound AAD) rather than SQLCipher,
which would require a CGO fork outside this list.

## Final note

This is infrastructure, not a product. It has no business model because
it doesn't need one. It has no terms of service because there is no
service. The network is the community that runs it. The protocol is the
only authority. The keypair is the only identity.
