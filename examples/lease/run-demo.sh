#!/usr/bin/env bash
# Simulate the lock, the store and its holders, then check the step logs
# against the model with gotogether.
#   MUTANT=noFencing | wallInterval | absoluteExpiry   the store without fencing
#   CLOCKS=1                                clock faults (skews, jumps)
#   DRIFT=0.25                              clocks run fast or slow by this fraction
# With true clocks every variant conforms: only a clock fault shows the bug,
# and only the one each variant trusts (NoFencing: drift; wallInterval: a
# jump; absoluteExpiry: a skew). A MUTANT= no run catches exits 1.
set -euo pipefail
cd "$(dirname "$0")"
# finish FAILED: the demo's exit status (examples/README.md, "run-demo.sh"):
# 0 when it showed what the README says (every run conforms; under
# MUTANT=, some run fails its check), 1 when it did not
finish() {
  if [ -z "${MUTANT:-}" ]; then
    [ "$1" = 0 ] && exit 0
    echo "not as expected: a run of the faithful system failed its check" >&2
    exit 1
  fi
  if [ "$1" != 0 ]; then echo "mutant $MUTANT caught, as expected"; exit 0; fi
  echo "not as expected: no run caught mutant $MUTANT (every run conforms)" >&2
  exit 1
}
case "${MUTANT:-}" in
"" | noFencing | wallInterval | absoluteExpiry) ;;
*) echo "unknown MUTANT=$MUTANT (noFencing, wallInterval, absoluteExpiry)" >&2; exit 2 ;;
esac
# the binary takes the mutation kebab-cased (noLocks: no-locks)
mutation=$(printf '%s' "${MUTANT:-}" | sed -E 's/([A-Z])/-\1/g' | tr '[:upper:]' '[:lower:]')
out=$(mktemp -d)
go build -o "$out/lease-sim" ./cmd/lease-sim
(cd ../.. && go build -o "$out/gotogether" ./cmd/gotogether)
echo "== lease ${MUTANT:+(mutant $MUTANT)} ${CLOCKS:+(clock faults)}"
"$out/lease-sim" -seeds "${SEEDS:-20}" -mutation "$mutation" ${CLOCKS:+-clocks} -drift "${DRIFT:-0}" -out "$out/runs"
if "$out/gotogether" validate -binding lease.binding.yaml "$out/runs"/*.jsonl > "$out/report"; then
  echo "all runs conform"
else
  # the first failing run: what was observed, and the state the model was in
  awk '/^actor/{if(b~/FAIL/){print b; done=1; exit} b=$0; next}{b=b"\n"$0}END{if(!done && b~/FAIL/)print b}' "$out/report" |
    grep -vE "module:|N/A"
  echo "step logs and report in $out"
  finish 1
fi
echo "step logs and report in $out"
finish 0
