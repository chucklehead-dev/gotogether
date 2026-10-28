#!/usr/bin/env bash
# examples/bank: every workflow on one small bank, each test showing which
# check catches which bug (README.md's table). MUTANT=LostDeposit or
# SelfTransfer runs the tests that catch that mutant (the bank's Mutation
# values, planted by the tests themselves); without one, the faithful
# bank's tests. The exit status is go test's: 0 when every claim held
# (a mutant caught where the README says), 1 when one did not, 2 for an
# unknown MUTANT.
set -euo pipefail
cd "$(dirname "$0")"
short=-short
case "${MUTANT:-}" in
"") run='TestModelDrivesBank$|TestHegelRunsConformToModel$|TestHegelVariesModelTraces$|TestRecordedRunConforms$|TestSanitizedRunConforms$|TestSlogStepsConform$' ;;
LostDeposit) run='TestModelDrivenCatchesOnlyDivergence$|TestRecordedRunCatchesLostDeposit$|TestOTelSpansConvertedAndChecked$|TestTellersAsASystem$' ;;
SelfTransfer) run='TestHegelCatchesSelfTransfer$|TestTellersAsASystem$' short= ;; # Hegel's shrink: about a minute
*) echo "unknown MUTANT=$MUTANT (LostDeposit, SelfTransfer)" >&2; exit 2 ;;
esac
echo "== bank ${MUTANT:+(mutant $MUTANT)}"
go test $short -count=1 -run "$run" -v . | grep -E '^(--- |ok|FAIL)|runs, |caught|expected'
