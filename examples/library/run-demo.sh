#!/usr/bin/env bash
# library: a lending library on grist, explored in a simulation, every run
# checked against model/library.qnt through the generated bindings.
# MUTATION=<name> builds the app with that mutant's overlay
# (testdata/mutants/) and shows what catches it: no-copy-check,
# stale-limit, no-dedupe, wrong-receipt.
set -euo pipefail
cd "$(dirname "$0")"
case "${MUTATION:-}" in
  "") run='TestFaithful$|TestFaithfulAtClients$|TestScarce$|TestGreedy$|TestReceiptOnce$|TestGenerated$' ;;
  no-copy-check) run='TestMutants$/^noCopyCheck$' ;;
  stale-limit) run='TestMutants$/^staleLimit$' ;;
  no-dedupe) run='TestMutants$/^noDedupe$' ;;
  wrong-receipt) run='TestMutants$/^wrongReceipt$' ;;
  *) echo "unknown MUTATION=$MUTATION" >&2; exit 2 ;;
esac
go run ./cmd/library model -check
GOTOGETHER_CHECK=1 go test -short -count=1 -run "$run" -v . | grep -E '^(--- |ok|FAIL)|runs, '
