#!/usr/bin/env bash
# Explore the wallet and check every run against the model. MUTANT= one
# of noDedupe, unlockedRecord, noValidation, readThenWrite,
# writeSkew, noOutbox, replicaRead shows what catches it, and the first
# counterexample; the faithful wallet runs linearized, under every fault
# family too.
set -euo pipefail
cd "$(dirname "$0")"
case "${MUTANT:-}" in
  "") run='TestFaithful$|TestAllFaults$' ;;
  noDedupe) run='TestNoDedupeWithinOneFault$' ;;
  unlockedRecord) run='TestUnlockedRecord$' ;;
  noValidation) run='TestInvalidRequests$' ;;
  readThenWrite) run='TestReadThenWrite$' ;;
  writeSkew) run='TestWriteSkew' ;;
  noOutbox) run='TestNoOutbox$' ;;
  replicaRead) run='TestReplicaRead$' ;;
  *) echo "unknown MUTANT=$MUTANT" >&2; exit 2 ;;
esac
echo "== wallet ${MUTANT:+(mutant $MUTANT)}"
go test -count=1 -timeout 30m -run "$run" -v . | grep -E '^(--- |ok|FAIL)|runs, |quint: |observed:|expect: |obs: |G2'
