#!/usr/bin/env bash
# Reproducible build: same source, same binary.
# Requires Go >= 1.24. -trimpath strips local paths; ldflags strip
# debug symbols and stamp the version.
set -euo pipefail
cd "$(dirname "$0")/.."
make build
echo "artifacts in dist/:"
sha256sum dist/*
