#!/usr/bin/env bash
# Explore the replicated store and check every run against the model,
# linearized key by key (a client history: the CLI's validate checks
# recorded order, so the demo runs the explorations). MUTATION= one of
# w3, leak, no-handoff, blind-repair, repick shows what catches it.
set -euo pipefail
cd "$(dirname "$0")"
case "${MUTATION:-}" in
  "") run='TestFaithful$' ;;
  w3) run='TestQuorumWithOneDown$' ;;
  leak) run='TestLeakedPut$' ;;
  no-handoff) run='TestNoHandoff$' ;;
  blind-repair) run='TestBlindRepair$' ;;
  repick) run='TestRepick$' ;;
  *) echo "unknown MUTATION=$MUTATION" >&2; exit 2 ;;
esac
echo "== quorumkv ${MUTATION:+(mutation $MUTATION)}"
go test -count=1 -timeout 60m -run "$run" -v . | grep -E '^(--- |ok|FAIL)|runs, '
