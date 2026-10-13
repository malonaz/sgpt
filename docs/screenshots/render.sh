#!/usr/bin/env bash
# Regenerates the site's docs/src/assets/screenshots/*.svg from scripted TUI
# scenes. Requires `freeze` (go install github.com/charmbracelet/freeze@latest).
set -euo pipefail
cd "$(dirname "$0")"
# The scenes the site embeds; the harness renders many more for development.
scenes=(diff-review read-and-answer shell help)
frames=$(mktemp -d)
trap 'rm -rf "$frames"' EXIT

plz build //docs/screenshots >/dev/null
../../plz-out/bin/docs/screenshots/screenshots -out "$frames" >/dev/null
for name in "${scenes[@]}"; do
  freeze "$frames/$name.ansi" \
    --output "../src/assets/screenshots/$name.svg" \
    --theme dracula --window --padding 24,32 \
    --font.family "JetBrains Mono" --font.size 13 --line-height 1.25 \
    --background "#0b0f19" --border.radius 12
done
