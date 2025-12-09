#!/usr/bin/env bash
set -euo pipefail

# Base directories
SRC_DIR="plz-out/gen"
DEST_DIR="genproto"

# Define file mappings: "source_path:dest_dir"
# The filename from source will be appended to dest_dir
declare -a FILES=(
    # Proto libraries
    "chat/v1/chat.pb.go:chat/v1"
    "chat/v1/chat_aip.go:chat/v1"

    "chat/chat_service/v1/chat_service.pb.go:chat/chat_service/v1"
    "chat/chat_service/v1/chat_service_grpc.pb.go:chat/chat_service/v1"
)

# Collect all directories we'll be writing to
declare -A ACTIVE_DIRS
for entry in "${FILES[@]}"; do
    IFS=':' read -r src dest_dir <<< "$entry"
    ACTIVE_DIRS["$DEST_DIR/$dest_dir"]=1
done

# Clean up: delete all files except BUILD.plz, and delete BUILD.plz in inactive directories
if [[ -d "$DEST_DIR" ]]; then
    # Delete all non-BUILD.plz files
    find "$DEST_DIR" -type f ! -name "BUILD.plz" -delete

    # Delete BUILD.plz files in directories that are not active
    while IFS= read -r build_file; do
        dir=$(dirname "$build_file")
        if [[ ! -v ACTIVE_DIRS["$dir"] ]]; then
            rm -f "$build_file"
            echo "🗑 Removed unused $build_file"
        fi
    done < <(find "$DEST_DIR" -type f -name "BUILD.plz")

    # Remove empty directories
    find "$DEST_DIR" -type d -empty -delete
fi

# Process each file
for entry in "${FILES[@]}"; do
    IFS=':' read -r src dest_dir <<< "$entry"

    src_path="$SRC_DIR/$src"
    filename="$(basename "$src")"
    dest_path="$DEST_DIR/$dest_dir/$filename"

    # Create destination directory
    mkdir -p "$DEST_DIR/$dest_dir"

    # Check if it's a model file (needs sed rewriting)
    if [[ "$filename" == *.model.go ]]; then
        sed 's/"proto\//"github.com\/malonaz\/core\/genproto\//g' "$src_path" > "$dest_path"
    else
        cp -f "$src_path" "$dest_path"
    fi

    echo "✓ Copied $src → $dest_dir/$filename"
done

# Linting files
echo "Linting files..."
plz lint > /dev/null 2>&1

echo "✅ Regenerated all files!"
