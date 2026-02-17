#!/usr/bin/env bash
# Notarize a macOS binary via Apple's notary service.
# Usage: scripts/notarize-binary.sh <binary-path>
#
# Requires: APPLE_ID, APPLE_APP_SPECIFIC_PASSWORD, APPLE_TEAM_ID

set -euo pipefail

BINARY="$1"
BASENAME=$(basename "$BINARY")
ZIP_PATH="/tmp/${BASENAME}.zip"

if [ -z "${APPLE_ID:-}" ] || [ -z "${APPLE_APP_SPECIFIC_PASSWORD:-}" ] || [ -z "${APPLE_TEAM_ID:-}" ]; then
  echo "Apple notarization credentials not set, skipping"
  exit 0
fi

if [ ! -f "$BINARY" ]; then
  echo "Error: Binary not found: $BINARY"
  exit 1
fi

echo "Creating zip for notarization..."
ditto -c -k --keepParent "$BINARY" "$ZIP_PATH"

echo "Submitting for notarization..."
xcrun notarytool submit "$ZIP_PATH" \
  --apple-id "$APPLE_ID" \
  --password "$APPLE_APP_SPECIFIC_PASSWORD" \
  --team-id "$APPLE_TEAM_ID" \
  --wait

echo "Stapling ticket..."
# Note: standalone binaries can't be stapled directly,
# but the notarization is recorded by Apple's servers.
# Gatekeeper will check online.

# Clean up
rm -f "$ZIP_PATH"

echo "Notarized: $BINARY"
