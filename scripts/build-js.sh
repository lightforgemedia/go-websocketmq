#!/bin/bash
set -e

# Get the root directory of the project
ROOT_DIR=$(pwd)

# Source and destination paths
SRC_PATH="$ROOT_DIR/client/src/client.js"
DIST_DIR="$ROOT_DIR/dist"
FULL_JS_PATH="$DIST_DIR/websocketmq.js"
MIN_JS_PATH="$DIST_DIR/websocketmq.min.js"

# Ensure dist directory exists
mkdir -p "$DIST_DIR"

# Copy the full JS file
cp "$SRC_PATH" "$FULL_JS_PATH"

# Minify the JS file using minify tool
minify -o "$MIN_JS_PATH" "$SRC_PATH"

# Print success message
SRC_SIZE=$(wc -c < "$SRC_PATH")
MIN_SIZE=$(wc -c < "$MIN_JS_PATH")
REDUCTION=$((100 - (MIN_SIZE * 100 / SRC_SIZE)))

echo "JavaScript build complete:"
echo "  Source:   $SRC_PATH ($SRC_SIZE bytes)"
echo "  Full:     $FULL_JS_PATH ($SRC_SIZE bytes)"
echo "  Minified: $MIN_JS_PATH ($MIN_SIZE bytes, $REDUCTION% reduction)"
