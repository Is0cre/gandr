#!/usr/bin/env bash
# Rotate ephemeral download mirrors.
#
# Operator-specific by design: this script intentionally ships empty of
# infrastructure details. Fill in your own mirror push targets (rsync
# over yggdrasil, onion service, IPFS pin, dead drop — your call).
# Mirrors are ephemeral; the GPG-signed SHA256SUMS is the source of
# truth, not the mirror.
set -euo pipefail
echo "mirror.sh: no mirrors configured (see comments)"
