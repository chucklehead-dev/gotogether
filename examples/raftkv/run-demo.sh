#!/usr/bin/env bash
# Explore the raft store and check every run against the models: the
# clients' history linearized whole (model/raftkv.qnt), the nodes'
# leadership in the order recorded (model/cluster.qnt). MUTANT= one of
# noRequestId, leaseRead, slowElection, noPersistVote, eagerV2,
# noSnapshotReport
# shows what catches it.
set -euo pipefail
cd "$(dirname "$0")"
case "${MUTANT:-}" in
  "") run='TestFaithful$|TestClusterFaithful$' ;;
  noRequestId) run='TestPendingTransfers$' ;;
  leaseRead) run='TestLeaseRead$' ;;
  slowElection) run='TestElectionAfterHeal$' ;;
  noPersistVote) run='TestUnpersistedVote$' ;;
  eagerV2) run='TestRollingUpgrade$' ;;
  noSnapshotReport) run='TestCatchUp$' ;;
  *) echo "unknown MUTANT=$MUTANT" >&2; exit 2 ;;
esac
echo "== raftkv ${MUTANT:+(mutant $MUTANT)}"
go test -count=1 -timeout 60m -run "$run" -v . | grep -E '^(--- |ok|FAIL)|runs, '
