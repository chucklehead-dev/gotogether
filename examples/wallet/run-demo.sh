#!/usr/bin/env bash
# Explore the wallet and check every run against the model. MUTATION= one
# of no-dedupe, read-then-write, write-skew, no-outbox, replica-read shows
# what catches it; the faithful wallet runs linearized, under every fault
# family too.
set -euo pipefail
cd "$(dirname "$0")"
case "${MUTATION:-}" in
  "") run='TestFaithful$|TestAllFaults$' ;;
  no-dedupe) run='TestNoDedupeWithinOneFault$' ;;
  read-then-write) run='TestReadThenWrite$' ;;
  write-skew) run='TestWriteSkew' ;;
  no-outbox) run='TestNoOutbox$' ;;
  replica-read) run='TestReplicaRead$' ;;
  *) echo "unknown MUTATION=$MUTATION" >&2; exit 2 ;;
esac
echo "== wallet ${MUTATION:+(mutation $MUTATION)}"
go test -count=1 -timeout 30m -run "$run" -v . | grep -E '^(--- |ok|FAIL)|runs, '
