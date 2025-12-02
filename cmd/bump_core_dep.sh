#!/usr/bin/env bash

set -euo pipefail

# Get the latest commit hash from the ~/core repo
CORE_DIR="$HOME/core"
if [[ ! -d "$CORE_DIR" ]]; then
    echo "Error: Core repository not found at $CORE_DIR"
    exit 1
fi

echo "Getting latest commit hash from $CORE_DIR..."
LATEST_COMMIT=$(cd "$CORE_DIR" && git rev-parse HEAD)
echo "Latest commit hash: $LATEST_COMMIT"

# Get current timestamp for the go.mod version
TIMESTAMP=$(cd "$CORE_DIR" && git log -1 --format=%ct HEAD)
FORMATTED_TIME=$(date -u -d "@$TIMESTAMP" +"%Y%m%d%H%M%S" 2>/dev/null || date -u -r "$TIMESTAMP" +"%Y%m%d%H%M%S")

# Update .plzconfig core-revision
echo "Updating .plzconfig core-revision..."
sed -i.bak "s/^core-revision = .*/core-revision = $LATEST_COMMIT/" .plzconfig

# Update go.mod if it exists
if [[ -f "go.mod" ]]; then
    echo "Updating go.mod..."
    # Replace the version in go.mod with new timestamp and commit hash
    sed -i.bak "s|github.com/malonaz/core v0\.0\.0-[0-9]*-[a-f0-9]*|github.com/malonaz/core v0.0.0-$FORMATTED_TIME-${LATEST_COMMIT:0:12}|g" go.mod
    echo "Updated go.mod with version: v0.0.0-$FORMATTED_TIME-${LATEST_COMMIT:0:12}"
else
    echo "No go.mod file found, skipping go.mod update"
fi

# Clean up backup files
rm -f .plzconfig.bak go.mod.bak 2>/dev/null || true

echo "Successfully updated core dependency to commit: $LATEST_COMMIT"
