#!/usr/bin/env bash
# examples/crossdb: the provider's real reconcile logic simulated (lost
# responses, crashes, throttling, an eventually consistent cloud), every
# run checked against model/crossdb.qnt. MUTANT=generatedExternalName |
# noClientToken | passwordFromCreate runs the test that catches it;
# without one, the faithful provider. KIND=1 runs it on kind with
# Crossplane instead (kind/demo.sh: Docker, kind, kubectl and helm; its
# own DROP=, KILL=). The exit status is go test's: 0 when every claim
# held, 1 when one did not, 2 for an unknown MUTANT.
set -euo pipefail
cd "$(dirname "$0")"
[ -z "${KIND:-}" ] || exec kind/demo.sh "$@"
case "${MUTANT:-}" in
"") run='TestFaithfulConforms$|TestLateCreateCoveredByCreationGrace$|TestStampsRoundTrip$' ;;
generatedExternalName) run='TestGeneratedExternalName$' ;;
noClientToken) run='TestNoClientTokenDuplicates$' ;;
passwordFromCreate) run='TestPasswordFromCreate$' ;;
*) echo "unknown MUTANT=$MUTANT (generatedExternalName, noClientToken, passwordFromCreate)" >&2; exit 2 ;;
esac
echo "== crossdb ${MUTANT:+(mutant $MUTANT)}"
go test -short -count=1 -run "$run" -v . | grep -E '^(--- |ok|FAIL)|runs, |caught|expected'
