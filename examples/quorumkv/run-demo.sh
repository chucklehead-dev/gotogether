#!/usr/bin/env bash
# Explore the replicated store and check every run against the model,
# linearized key by key (each run's history is a client's: the
# explorations search its orders, as `gotogether validate -linearize-key
# key` does for a step log). MUTANT= one of
# w3, leak, noHandoff, blindRepair, repick shows what catches it.
set -euo pipefail
cd "$(dirname "$0")"
case "${MUTANT:-}" in
  "") run='TestFaithful$' ;;
  w3) run='TestQuorumWithOneDown$' ;;
  leak) run='TestLeakedPut$' ;;
  noHandoff) run='TestNoHandoff$' ;;
  blindRepair) run='TestBlindRepair$' ;;
  repick) run='TestRepick$' ;;
  *) echo "unknown MUTANT=$MUTANT" >&2; exit 2 ;;
esac
echo "== quorumkv ${MUTANT:+(mutant $MUTANT)}"
go test -count=1 -timeout 60m -run "$run" -v . | grep -E '^(--- |ok|FAIL)|runs, '
