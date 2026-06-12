#!/usr/bin/env bash
# Run every fuzz target for a bounded time. Used by `make fuzz`.
set -euo pipefail
cd "$(dirname "$0")/.."

FUZZTIME="${FUZZTIME:-30s}"

run() {
    local pkg="$1" target="$2"
    echo "== fuzz $pkg $target ($FUZZTIME)"
    go test "$pkg" -run='^$' -fuzz="^${target}\$" -fuzztime="$FUZZTIME"
}

run ./pkg/crypto FuzzDecryptKeyfile
run ./pkg/crypto FuzzOpen
run ./pkg/crypto FuzzVerify
run ./pkg/crypto FuzzPublicKeyToX25519
run ./pkg/proto  FuzzDecode
run ./pkg/proto  FuzzDecodePayload
run ./pkg/ipc    FuzzReadFrame
