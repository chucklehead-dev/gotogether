#!/usr/bin/env bash
# examples/fleetrollout: the controller simulated (qenv/kubesim: status
# lag, lost responses, crashes), every run checked against model/fleet.qnt.
# MUTANT=ignoreObservedGeneration | inMemoryProgress runs the test that
# catches it; without one, the faithful controller, also under a
# production trace's workload. On three real kwok clusters: kwok-demo.sh
# (its own MUTANT=, KILL=, TRACE=). The exit status is go test's: 0 when
# every claim held, 1 when one did not, 2 for an unknown MUTANT.
set -euo pipefail
cd "$(dirname "$0")"
case "${MUTANT:-}" in
"") run='TestFaithfulConforms$|TestRolloutUnderProductionWorkload$|TestRolloutWithMeasuredPodTimes$' ;;
ignoreObservedGeneration) run='TestIgnoreObservedGenerationOverBudget$' ;;
inMemoryProgress) run='TestInMemoryProgress$' ;;
*) echo "unknown MUTANT=$MUTANT (ignoreObservedGeneration, inMemoryProgress)" >&2; exit 2 ;;
esac
echo "== fleetrollout ${MUTANT:+(mutant $MUTANT)}"
go test -short -count=1 -run "$run" -v . | grep -E '^(--- |ok|FAIL)|runs, |caught|expected'
