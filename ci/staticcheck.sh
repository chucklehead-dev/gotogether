#!/usr/bin/env bash
# Analyze the core with a pinned tool, without adding it to the runtime's
# module graph. Go's download/build caches make repeat installs inexpensive.
set -euo pipefail
cd "$(dirname "$0")/.."
tool_dir=$(mktemp -d)
trap 'rm -rf "$tool_dir"' EXIT
GOWORK=off GOFLAGS= GOBIN="$tool_dir" go install honnef.co/go/tools/cmd/staticcheck@v0.8.2
"$tool_dir/staticcheck" -version
if [ "$#" -eq 0 ]; then
  set -- ./...
fi
GOWORK=off GOFLAGS=-mod=readonly "$tool_dir/staticcheck" "$@"
