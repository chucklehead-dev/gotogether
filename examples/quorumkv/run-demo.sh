#!/usr/bin/env bash
# Explore the replicated store and check every run against the model,
# linearized key by key (each run's history is a client's: the
# explorations search its orders, as `gotogether validate -linearize-key
# key` does for a step log). MUTATION= one of
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
