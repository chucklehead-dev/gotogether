#!/usr/bin/env bash
# Repository contracts that need Go and Bash, before native model checks.
# Keep this roster shared by local development and the CI dependency gate.
set -euo pipefail
cd "$(dirname "$0")/.."
if [ "$#" -ne 0 ]; then
  echo "usage: ci/preflight.sh (no arguments)" >&2
  exit 2
fi

export GOWORK=off GOFLAGS=-mod=readonly GOTOGETHER_STRICT=1
# Checking a snapshot must not regenerate it, including when the caller was
# deliberately updating API/schema/golden files in an earlier command.
for name in "${!GOTOGETHER_UPDATE_@}"; do
  unset "$name"
done
unset GOTOGETHER_MAIN GOTOGETHER_TUTORIAL_HELP GOTOGETHER_HELLO_REPLAY
# Help error-path subprocesses must not inherit a persistent report destination.
unset GOTOGETHER_REPORT_DIR

# Check source files without descending into hidden generated trees (.gosim,
# .git, caches). Preserve filename boundaries and never rewrite source/artifacts.
unformatted=$(find . -type d -name '.*' ! -path . -prune -o -type f ! -name '.*' -name '*.go' -print0 | xargs -0 gofmt -l)
if [ -n "$unformatted" ]; then
  printf '%s\n' "$unformatted" >&2
  echo "repository formatting differs: run gofmt on the listed files" >&2
  exit 1
fi

# These packages inspect repository source, formats, documentation and installed
# skills. They do not execute model runs or require Quint, Node or other native
# test services. Full suites still run them; this is an early failure gate.
guards=(
  ./internal/pkgstate
  ./internal/apisurface
  ./internal/ciconfig
  ./internal/docsnippets
  ./internal/exampleapps
  ./internal/formats/...
  ./schemas
  ./skills
  ./qlint
)
go test -count=1 -timeout 5m "${guards[@]}"

# Include the complete overview as well as individual commands and tutorial
# freshness. These tests inspect arguments or execute only the CLI's help path.
cli_tests='^Test(Usage.*|.*Help.*|UnknownCommand.*|CommandNeeds|TutorialCommandsAreCurrent|TutorialCommandGuardControls|TutorialShellControls)$'
go test -count=1 -timeout 5m ./cmd/gotogether -run "$cli_tests"
