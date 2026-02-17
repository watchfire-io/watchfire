#!/usr/bin/env bash
# Code sign a macOS binary with hardened runtime.
# Usage: scripts/codesign.sh <binary-path>
#
# Requires CODESIGN_IDENTITY environment variable.

set -euo pipefail

BINARY="$1"

if [ -z "${CODESIGN_IDENTITY:-}" ]; then
  echo "CODESIGN_IDENTITY not set, skipping code signing"
  exit 0
fi

if [ ! -f "$BINARY" ]; then
  echo "Error: Binary not found: $BINARY"
  exit 1
fi

echo "Signing $BINARY..."
codesign --sign "$CODESIGN_IDENTITY" \
  --options runtime \
  --timestamp \
  --force \
  "$BINARY"

echo "Verifying signature..."
codesign --verify --verbose=2 "$BINARY"
echo "Signed: $BINARY"
