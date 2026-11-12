#!/usr/bin/env bash
# Mutate a core package and test every test-bearing descendant package.
# JOBS bounds concurrent mutants; MEM_MB caps each test process (default 3072).
# Each mutant gets 300 seconds per suite.
#   ci/mutate-core.sh qenv [max-survivors]        the whole campaign, its max applied
#   ci/mutate-core.sh -shard i/n qenv             shard i of n (0 <= i < n): no max here
#   ci/mutate-core.sh aggregate qenv max n dir    the max applied once to every shard's
#                                                 survivors summed (ci/mutatecore/README.md)
set -euo pipefail
: "${1:?a core package, e.g. qtrace}"
root=$(cd "$(dirname "$0")/.." && pwd)
if [ "$1" = aggregate ]; then
  [ "$#" -eq 5 ] || { echo "usage: ci/mutate-core.sh aggregate <package> <max> <shards> <evidence-dir>" >&2; exit 2; }
  dir=$5
  # go run -C changes directory: name the evidence from the caller's
  [[ "$dir" = /* ]] || dir=$PWD/$dir
  exec go run -C "$root" ./ci/mutatecore aggregate "$2" "$3" "$4" "$dir"
fi
exec go run -C "$root" ./ci/mutatecore "$root" "$@"
