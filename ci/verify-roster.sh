#!/usr/bin/env bash
# The nightly verifier's two rosters, from ci/verify-alone.txt: the grist
# apps' slow proofs run in jobs of their own (one group per runner, each
# with its measured budget), every other tracked binding in the shared
# roster. Every tracked binding is in exactly one (internal/ciconfig
# checks it).
#   ci/verify-roster.sh shared   the shared roster's bindings, one per line
#   ci/verify-roster.sh alone    the groups as a job matrix (JSON)
set -euo pipefail
cd "$(dirname "$0")/.."
alone=ci/verify-alone.txt
# a group's line: its name, its job's minutes, then its bindings
groups() { grep -v '^[[:space:]]*\(#\|$\)' "$alone"; }
case "${1:-}" in
shared)
  groups | awk '{ for (i = 3; i <= NF; i++) print $i }' | sort -u > "${TMPDIR:-/tmp}/verify-alone.$$"
  git ls-files 'examples/*binding*.yaml' 'examples/**/*binding*.yaml' | sort -u | grep -vxF -f "${TMPDIR:-/tmp}/verify-alone.$$" || true
  rm -f "${TMPDIR:-/tmp}/verify-alone.$$"
  ;;
alone)
  groups | awk 'BEGIN { printf "{\"include\":[" }
    { printf "%s{\"name\":\"%s\",\"minutes\":%d,\"bindings\":\"", (NR > 1 ? "," : ""), $1, $2
      for (i = 3; i <= NF; i++) printf "%s%s", (i > 3 ? " " : ""), $i
      printf "\"}" }
    END { print "]}" }'
  ;;
*)
  echo "usage: ci/verify-roster.sh shared|alone" >&2
  exit 2
  ;;
esac
