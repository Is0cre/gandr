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
