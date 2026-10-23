#!/usr/bin/env bash
# Simulate the lock, the store and its holders, then check the step logs
# against the model with gotogether.
#   MUTATION=no-fencing | absolute-expiry   the store without fencing
#   CLOCKS=1                                clock faults (skews, jumps)
#   DRIFT=0.25                              with CLOCKS, clocks run fast or slow by this fraction
# Without CLOCKS every variant conforms: only a clock fault shows the bug.
set -euo pipefail
cd "$(dirname "$0")"
out=$(mktemp -d)
go build -o "$out/lease-sim" ./cmd/lease-sim
(cd ../.. && go build -o "$out/gotogether" ./cmd/gotogether)
echo "== lease ${MUTATION:+(mutation $MUTATION)} ${CLOCKS:+(clock faults)}"
"$out/lease-sim" -seeds "${SEEDS:-20}" -mutation "${MUTATION:-}" ${CLOCKS:+-clocks} -drift "${DRIFT:-0}" -out "$out/runs"
if "$out/gotogether" validate -binding lease.binding.yaml "$out/runs"/*.jsonl > "$out/report"; then
  echo "all runs conform"
else
  # the first failing run: what was observed, and the state the model was in
  awk '/^actor/{if(b~/FAIL/){print b; done=1; exit} b=$0; next}{b=b"\n"$0}END{if(!done && b~/FAIL/)print b}' "$out/report" |
    grep -vE "module:|N/A"
  echo "step logs and report in $out"
  exit 1
fi
echo "step logs and report in $out"
