#!/usr/bin/env bash
# Run the broadcast cluster and the checkout saga on
# it with gosim's own seeds (no qenv choices, no model), then run the tests
# that make every choice from qenv and check the runs against the models.
#   SEEDS=1-5     gosim's seeds for the first part
#   FULL=1        include the Hegel test (minutes: shrinking against the model)
#   MUTANT=...    readOwnOnly | noRetry (broadcast), manifestBeforeTable |
#                 noRotationLock (edgepublish), lockWithoutWrite |
#                 noIdempotencyKey (checkout): the test that catches it alone
# The exit status is go test's: 0 when every claim held (a mutant caught
# where the README says), 1 when one did not, 2 for an unknown MUTANT.
set -euo pipefail
cd "$(dirname "$0")"
if [ -n "${MUTANT:-}" ]; then
  short=-short
  case "$MUTANT" in
  readOwnOnly) pkg=./broadcast run='TestReadOwnOnlyRejected$' ;;
  noRetry) pkg=./broadcast run='TestHegelFindsNoRetry$' short= ;;
  manifestBeforeTable | noRotationLock) pkg=./edgepublish run='TestMutantsOnGosim$' ;;
  lockWithoutWrite | noIdempotencyKey) pkg=./checkout run='TestMutantsOnGosim$' ;;
  *) echo "unknown MUTANT=$MUTANT (readOwnOnly, noRetry, manifestBeforeTable, noRotationLock, lockWithoutWrite, noIdempotencyKey)" >&2; exit 2 ;;
  esac
  echo "== gosim (mutant $MUTANT)"
  go test $short -count=1 -run "$run" -v "$pkg" 2>&1 | grep -E '^(--- |    host_test|ok|FAIL|PASS)'
  exit
fi
echo; echo "\$ gosim test -seeds=${SEEDS:-1-5} ./broadcast/cluster ./checkout"
go run github.com/chucklehead-dev/gosim/cmd/gosim test -seeds="${SEEDS:-1-5}" -v ./broadcast/cluster ./checkout 2>&1 | grep -v "unknown linkname"
echo; echo "\$ go test -v ./..."
short=-short
[ -n "${FULL:-}" ] && short=
go test $short -count=1 -v ./... 2>&1 | grep -E '^(=== RUN|--- |    host_test|ok|FAIL|PASS)'
