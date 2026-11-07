#!/usr/bin/env bash
# Mutate a core package and test every test-bearing descendant package.
# JOBS bounds concurrent mutants; MEM_MB caps each test process (default 3072).
# Each mutant gets 300 seconds per suite. Usage: ci/mutate-core.sh qenv [max-survivors]
set -euo pipefail
: "${1:?a core package, e.g. qtrace}"
root=$(cd "$(dirname "$0")/.." && pwd)
exec go run -C "$root" ./ci/mutatecore "$root" "$@"
