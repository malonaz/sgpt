#!/usr/bin/env bash
# Regenerates docs/src/assets/screenshots/*.svg from scripted TUI scenes.
# Requires `freeze` (go install github.com/charmbracelet/freeze@latest).
set -euo pipefail
cd "$(dirname "$0")"
frames=$(mktemp -d)
trap 'rm -rf "$frames"' EXIT

GOFLAGS=-mod=mod go run . -out "$frames" >/dev/null
for frame in "$frames"/*.ansi; do
  name=$(basename "$frame" .ansi)
  freeze "$frame" \
    --output "../src/assets/screenshots/$name.svg" \
    --theme dracula --window --padding 24,32 \
    --font.family "JetBrains Mono" --font.size 13 --line-height 1.25 \
    --background "#0b0f19" --border.radius 12
done
