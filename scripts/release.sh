#!/usr/bin/env bash
# Sign and publish a release. Requires gpg key and gh auth.
set -euo pipefail
cd "$(dirname "$0")/.."
make release
