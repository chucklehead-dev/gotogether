#!/usr/bin/env bash
# examples/payouts: a payouts service and its bank, two grist Apps, the
# bank a peer whose command the payouts' send processor calls at least
# once (grist.NewPeer). Both run in one simulation, each judged by its
# own model, and the two sides related by key (gristtest.Linked).
# MUTANT= freshKey (generated: each try under a new command id) or
# noDuplicateCheck (testdata/mutants: a payout's id taken twice) builds
# that mutant and shows what catches it; without one, the faithful pair
# through the bank's lost answers and both nodes' crashes.
set -euo pipefail
cd "$(dirname "$0")"
if [ "${SPANS:-0}" = 1 ]; then
  case "${MUTANT:-}" in
    "") run='TestDeclaredCallSpans$|TestSpanIdentityCorruption$' ;;
    freshKey) run='TestSpanMutants$/^freshKey$' ;;
    *) echo "SPANS=1 supports MUTANT=freshKey only" >&2; exit 2 ;;
  esac
elif [ -n "${MUTANT:-}" ]; then
  [ -d "testdata/mutants/$MUTANT" ] || [ "$MUTANT" = freshKey ] || { echo "unknown MUTANT=$MUTANT (freshKey, or testdata/mutants: $(ls testdata/mutants | tr '\n' ' '))" >&2; exit 2; }
  run="TestMutants\$/^$MUTANT\$"
else
  run='TestFaithful$'
fi
echo "== payouts ${MUTANT:+(mutant $MUTANT)}"
GOTOGETHER_CHECK=1 go test -short -count=1 -timeout 30m -run "$run" -v . | grep -E '^(--- |ok|FAIL)|runs, |as expected'
