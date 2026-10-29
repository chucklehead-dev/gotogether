#!/usr/bin/env bash
# inventory-grist: two competing checkouts, one SKU, cancellation and fulfillment.
# MUTANT=freshKey allocates twice after a lost reply; missingRelease drops a
# cancellation; authorizeCancelled ships after inventory accepted cancellation.
# The demo exits 0 when the selected bug is caught, 1 on a failed claim,
# and 2 for an unknown MUTANT=. TRIAL_EVIDENCE saves replayable protocol JSONL.
set -euo pipefail
cd "$(dirname "$0")"
case "${MUTANT:-}" in
  '') run='^(TestFaithful|TestDecisionRaces|TestLostReservationReply|TestReservationCrash|TestCapacity|TestCancellationOvertakesReserve)$' ;;
  freshKey|missingRelease) run="^TestMutants\$/^${MUTANT}\$" ;;
  authorizeCancelled) run='^TestInventoryMutants$/^authorizeCancelled$' ;;
  *) echo "unknown MUTANT=$MUTANT (freshKey, missingRelease, authorizeCancelled)" >&2; exit 2 ;;
esac
GOTOGETHER_CHECK=1 go test -short -count=1 -timeout 30m -run "$run" -v . |
  grep -E '^(--- |ok|FAIL)|runs, |as expected|protocol:|protocol history:'
