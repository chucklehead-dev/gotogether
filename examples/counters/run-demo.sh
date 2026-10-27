#!/usr/bin/env bash
# examples/counters: grain's counter example on grist, explored in a
# simulation, every run checked against model/counters.qnt. MUTATION=
# blind or dispatch-direct builds the app with that mutant's overlay
# (testdata/mutants/) and shows what catches it; without one, the
# faithful app through commit faults and crashes at three isolation
# levels, and the welcome's duplicate after a crash (claims 2 and 3 of
# docs/grist.md §8.1; MUTATION=blind is claim 1).
set -euo pipefail
cd "$(dirname "$0")"
case "${MUTATION:-}" in
  "") run='TestFaithful$|TestWelcomeDuplicate$' ;;
  blind) run='TestMutants$/^blind$' ;;
  dispatch-direct) run='TestMutants$/^dispatchDirect$' ;;
  *) echo "unknown MUTATION=$MUTATION" >&2; exit 2 ;;
esac
echo "== counters ${MUTATION:+(mutation $MUTATION)}"
GOTOGETHER_CHECK=1 go test -short -count=1 -timeout 30m -run "$run" -v . | grep -E '^(--- |ok|FAIL)|runs, |as expected'
