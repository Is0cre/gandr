#!/usr/bin/env bash
# Gandr node installer.
#
# Installs gandrd (daemon) and gandr (client) from dist/, creates the
# gandrd system user and group, writes a working config, generates a
# keyfile passphrase for unattended starts, and installs the systemd
# unit. Idempotent: never overwrites an existing config, passphrase,
# or identity.
#
# Usage:  sudo scripts/install.sh
#
# Uninstall:
#   systemctl disable --now gandrd
#   rm /usr/local/bin/gandrd /usr/local/bin/gandr
#   rm /etc/systemd/system/gandrd.service
#   rm -rf /etc/gandrd /var/lib/gandrd        # destroys node identity!
#   userdel gandrd
set -euo pipefail

PREFIX="${PREFIX:-/usr/local}"
BIN_DIR="$PREFIX/bin"
CONFIG_DIR=/etc/gandrd
STATE_DIR=/var/lib/gandrd
UNIT_PATH=/etc/systemd/system/gandrd.service
SERVICE_USER=gandrd
SERVICE_GROUP=gandrd

cd "$(dirname "$0")/.."

say()  { printf '  %s\n' "$*"; }
fail() { printf 'install.sh: %s\n' "$*" >&2; exit 1; }

[ "$(id -u)" -eq 0 ] || fail "must run as root (sudo scripts/install.sh)"
command -v systemctl >/dev/null || fail "systemd not found; install manually (see docs/SETUP.md)"

# --- locate or build binaries -------------------------------------------
case "$(uname -m)" in
    x86_64)          GOARCH=amd64 ;;
    aarch64|arm64)   GOARCH=arm64 ;;
    armv6l|armv7l)   GOARCH=arm   ;;
    *) fail "unsupported architecture: $(uname -m)" ;;
esac

DAEMON_BIN="dist/gandrd-linux-$GOARCH"
CLIENT_BIN="dist/gandr"

if [ ! -f "$DAEMON_BIN" ] || [ ! -f "$CLIENT_BIN" ]; then
    if command -v go >/dev/null && command -v make >/dev/null; then
        say "binaries missing — building"
        make build
    else
        fail "no binaries in dist/ and no Go toolchain; run 'make build' first"
    fi
fi
[ -f "$DAEMON_BIN" ] || fail "missing $DAEMON_BIN after build"
[ -f "$CLIENT_BIN" ] || fail "missing $CLIENT_BIN after build (client builds natively only)"

# --- binaries ------------------------------------------------------------
install -m 755 "$DAEMON_BIN" "$BIN_DIR/gandrd"
install -m 755 "$CLIENT_BIN" "$BIN_DIR/gandr"
say "installed $BIN_DIR/gandrd, $BIN_DIR/gandr"

# --- user, group, directories --------------------------------------------
if ! getent group "$SERVICE_GROUP" >/dev/null; then
    groupadd --system "$SERVICE_GROUP"
    say "created group $SERVICE_GROUP"
fi
if ! getent passwd "$SERVICE_USER" >/dev/null; then
    useradd --system --gid "$SERVICE_GROUP" --home-dir "$STATE_DIR" \
        --shell /usr/sbin/nologin "$SERVICE_USER"
    say "created system user $SERVICE_USER"
fi

install -d -m 750 -o root -g "$SERVICE_GROUP" "$CONFIG_DIR"
install -d -m 700 -o "$SERVICE_USER" -g "$SERVICE_GROUP" "$STATE_DIR"

# --- passphrase (unattended starts) ---------------------------------------
# Protects the keyfiles at rest with a machine-local secret. An attacker
# with root on the running box gets both anyway; this guards backups and
# pulled disks, not live compromise.
PASSFILE="$CONFIG_DIR/passphrase"
if [ ! -f "$PASSFILE" ]; then
    umask 077
    head -c 32 /dev/urandom | base64 | tr -d '\n=' > "$PASSFILE"
    chown root:"$SERVICE_GROUP" "$PASSFILE"
    chmod 640 "$PASSFILE"
    say "generated keyfile passphrase at $PASSFILE"
else
    say "keeping existing passphrase"
fi

# --- config ----------------------------------------------------------------
# Derived from the example, adjusted for the hardened systemd unit:
# ProtectSystem=strict makes /etc read-only for the service, so keyfiles
# live under $STATE_DIR (the unit's ReadWritePaths).
CONFIG="$CONFIG_DIR/config.toml"
if [ ! -f "$CONFIG" ]; then
    sed -e "s|^keyfile = .*|keyfile = \"$STATE_DIR/identity.key\"|" \
        -e "s|^# passphrase_file = .*|passphrase_file = \"$PASSFILE\"|" \
        scripts/config.example.toml > "$CONFIG"
    chown root:"$SERVICE_GROUP" "$CONFIG"
    chmod 640 "$CONFIG"
    say "wrote $CONFIG"
else
    say "keeping existing $CONFIG"
fi

# --- systemd unit ------------------------------------------------------------
install -m 644 scripts/gandrd.service "$UNIT_PATH"
systemctl daemon-reload
say "installed $UNIT_PATH"

# --- client socket access ------------------------------------------------------
# The IPC socket is 0660 gandrd:gandrd; group members may run the client.
if [ -n "${SUDO_USER:-}" ] && [ "$SUDO_USER" != "root" ]; then
    if id -nG "$SUDO_USER" | tr ' ' '\n' | grep -qx "$SERVICE_GROUP"; then
        say "$SUDO_USER already in group $SERVICE_GROUP"
    else
        usermod -aG "$SERVICE_GROUP" "$SUDO_USER"
        say "added $SUDO_USER to group $SERVICE_GROUP (re-login to take effect)"
    fi
fi

cat <<EOF

ᚷ  gandr installed.

next steps:
  1. edit $CONFIG
       [network] peers  — yggdrasil link-layer peers to reach the overlay
       [peering] seeds  — hex node keys of gandr nodes to federate with
  2. systemctl enable --now gandrd
  3. gandr            (re-login first if you were just added to '$SERVICE_GROUP')

the node identity is generated on first start, encrypted under
$PASSFILE — back up both $STATE_DIR/identity.key and the passphrase
file together, or the identity is unrecoverable.
EOF
