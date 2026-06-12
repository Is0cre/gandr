# Running a Gandr Node

Assumes Linux. Yggdrasil does **not** need to be installed — gandrd
embeds its own Yggdrasil node (no TUN, no root needed for the overlay).

## Build

```sh
git clone <repo> && cd gandr
make build          # daemon (pure Go) + client (CGO for sqlite)
```

Binaries land in `dist/`. Verify releases against the GPG-signed
`SHA256SUMS`.

## Node (gandrd)

The installer does everything below in one idempotent step — binaries,
the `gandrd` system user and group, a hardened config, a generated
keyfile passphrase for unattended starts, and the systemd unit:

```sh
sudo scripts/install.sh     # or: sudo make install
$EDITOR /etc/gandrd/config.toml
sudo systemctl enable --now gandrd
```

It never overwrites an existing config, passphrase, or identity.
**Back up `/var/lib/gandrd/identity.key` and `/etc/gandrd/passphrase`
together** — without both, the node identity is unrecoverable.

Manual equivalent, if you prefer to see every step:

```sh
sudo groupadd --system gandrd
sudo useradd --system --gid gandrd --home /var/lib/gandrd gandrd
sudo mkdir -p /etc/gandrd /var/lib/gandrd
sudo cp scripts/config.example.toml /etc/gandrd/config.toml
sudo cp dist/gandrd-linux-amd64 /usr/local/bin/gandrd
$EDITOR /etc/gandrd/config.toml
```

Note: the systemd unit uses `ProtectSystem=strict`, so keyfiles must
live under `/var/lib/gandrd` (the installer's config does this); a
keyfile path under `/etc` will fail to generate on first start.

Config essentials:

- `[network] peers` — yggdrasil link-layer peers (e.g. public peers
  from the yggdrasil peer list, or a direct `tcp://host:port` to a
  friend's node). Without at least one peer or inbound listener you are
  not on the overlay.
- `[peering] seeds` — hex-encoded yggdrasil node keys of Gandr nodes to
  federate with. Get them from operators you trust; there is no public
  registry, deliberately.
- `[identity] passphrase_file` — one-line file, mode 600, for
  unattended starts. Interactive starts may type it instead;
  `GANDRD_PASSPHRASE` also works.

First start generates the node identity and yggdrasil transport key,
both encrypted at rest:

```sh
sudo cp scripts/gandrd.service /etc/systemd/system/
sudo systemctl enable --now gandrd
```

The daemon writes no access logs, no message logs, no peer identity
logs. Error output only. There is no admin interface — the config file
and the Unix socket are the entire operational surface.

## Client (gandr)

The daemon socket is mode 0660 `gandrd:gandrd`; the installer adds the
invoking user to the `gandrd` group (re-login for it to take effect).
Then:

```sh
gandr --socket /var/run/gandrd/gandr.sock
```

First run generates *your* identity (separate from the node's) under
`~/.local/share/gandr/`, encrypted with your passphrase. Then:

```
/join general            # join a channel by name
/help                    # everything else
```

Nicknames (`n` on any sender, or `/nick`), notes, and blocks are stored
only in your local encrypted database. They never touch the network.

## Hardware

Anything that runs Linux and Go: a $5 VPS, a Raspberry Pi, a cyberdeck.
gandrd at idle uses a few tens of MB of RAM. Storage nodes
(`capabilities.storage = true`) should provision disk for
`limits.max_message_age` worth of objects.

## What a seized node yields

Content-addressed envelopes of public messages (already public),
opaque sealed blobs (undecryptable), an encrypted identity key, and an
encrypted transport key. No user table, no peer history (memory-only),
no logs. The architecture, not the operator's diligence, guarantees
this.
