#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CONFIG="${SCRIPT_DIR}/codespacegen.json"

while IFS= read -r profile; do
  echo "Generating for profile: $profile"
  codespacegen \
    -output "${SCRIPT_DIR}/${profile}" \
    -name "$profile" \
    -language "$profile" \
    -service app \
    -workspace-folder /workspace \
    -headless \
    -force
done < <(jq -r '.langs[].profileName' "$CONFIG")
