#!/usr/bin/env bash
# examples/glomers: each challenge's nodes on the Maelstrom simulator,
# their steps checked against the challenge's model (README.md's table).
# MUTANT= one of CounterOnly, NoIncarnation, FixedIncarnation (unique ids),
# ReadOwnOnly, NoRetry (broadcast), Overwrite, GossipOnChange (grow-only
# counter), BlindAppend, PollSkips (kafka) runs the test that catches it;
# without one, every challenge's faithful nodes. Under real Maelstrom:
# maelstrom.sh. The exit status is go test's: 0 when every claim held,
# 1 when one did not, 2 for an unknown MUTANT.
set -euo pipefail
cd "$(dirname "$0")"
short=-short
case "${MUTANT:-}" in
"") pkgs=./...; run='TestUniqueUnderPartitionAndRestart$|TestBroadcastConverges$|TestCounterCountsEveryAdd$|TestLogKeepsEverySend$|TestEchoAndLamport$' ;;
CounterOnly | NoIncarnation | FixedIncarnation) pkgs=./uniqueids; run='TestMutants$|TestIDEncodingInjective$' ;; # each of the three, by both models
ReadOwnOnly) pkgs=./broadcast; run='TestReadOwnOnlyRejected$' ;;
NoRetry) pkgs=./broadcast; run='TestNoRetryDoesNotConverge$|TestHegelShrinksNoRetry$'; short= ;;
Overwrite) pkgs=./gcounter; run='TestOverwriteRejected$' ;;
GossipOnChange) pkgs=./gcounter; run='TestGossipOnChangeDoesNotConverge$' ;;
BlindAppend) pkgs=./kafka; run='TestBlindAppendLosesSends$' ;;
PollSkips) pkgs=./kafka; run='TestPollSkipsRejected$' ;;
*) echo "unknown MUTANT=$MUTANT (CounterOnly, NoIncarnation, FixedIncarnation, ReadOwnOnly, NoRetry, Overwrite, GossipOnChange, BlindAppend, PollSkips)" >&2; exit 2 ;;
esac
echo "== glomers ${MUTANT:+(mutant $MUTANT)}"
go test $short -count=1 -run "$run" -v $pkgs | grep -E '^(--- |ok|FAIL)|runs, |caught|expected'
