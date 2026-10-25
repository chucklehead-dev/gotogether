#!/usr/bin/env bash
# Explore the raft store and check every run against the models: the
# clients' history linearized whole (model/raftkv.qnt), the nodes'
# leadership in the order recorded (model/cluster.qnt). MUTATION= one of
# no-request-id, lease-read, slow-election, no-persist-vote, eager-v2
# shows what catches it.
set -euo pipefail
cd "$(dirname "$0")"
case "${MUTATION:-}" in
  "") run='TestFaithful$|TestClusterFaithful$' ;;
  no-request-id) run='TestPendingTransfers$' ;;
  lease-read) run='TestLeaseRead$' ;;
  slow-election) run='TestElectionAfterHeal$' ;;
  no-persist-vote) run='TestUnpersistedVote$' ;;
  eager-v2) run='TestRollingUpgrade$' ;;
  *) echo "unknown MUTATION=$MUTATION" >&2; exit 2 ;;
esac
echo "== raftkv ${MUTATION:+(mutation $MUTATION)}"
go test -count=1 -timeout 60m -run "$run" -v . | grep -E '^(--- |ok|FAIL)|runs, '
