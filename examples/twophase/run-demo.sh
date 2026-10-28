#!/usr/bin/env bash
# Prove the Choreo model's invariants over its whole state graph, then
# simulate two-phase commit and check the step logs against the model
# with gotogether. MUTANT=earlyCommit | impatient | volatileDecision
# injects a bug into the code; MODEL=early-decision into the model (the
# coordinator commits once any participant has prepared), which the
# graph refutes with a counterexample quint replays.
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
# the binary takes the mutation kebab-cased (noLocks: no-locks)
mutation=$(printf '%s' "${MUTANT:-}" | sed -E 's/([A-Z])/-\1/g' | tr '[:upper:]' '[:lower:]')
out=$(mktemp -d)
go build -o "$out/twophase-sim" ./cmd/twophase-sim
(cd ../.. && go build -o "$out/gotogether" ./cmd/gotogether)
echo "== the model alone: every reachable state ${MODEL:+(model $MODEL)}"
case "${MODEL:-}" in
"") "$out/gotogether" simulate -graph -binding twophase.binding.yaml ;;
early-decision)
  cp -r model "$out/model" && cp twophase.binding.yaml "$out/"
  sed -i.orig 's/ctx.messages.preparedIn() == PARTICIPANTS/ctx.messages.preparedIn().size() > 0/' "$out/model/twophase.qnt"
  code=0
  "$out/gotogether" simulate -graph -binding "$out/twophase.binding.yaml" -out "$out/cex.qnt" > "$out/graph" || code=$?
  grep -v '^         ' "$out/graph" # the states along the path left out
  if [ "$code" != 1 ]; then echo "the graph refuted nothing (exit $code)"; exit 1; fi
  echo "== quint replays the counterexample"
  (cd "$out" && quint test --main simulate --match gtg_ cex.qnt | grep -E 'ok|failed')
  exit 0 ;;
*) echo "MODEL=$MODEL: early-decision or nothing" >&2; exit 2 ;;
esac
echo "== two-phase commit ${MUTANT:+(mutant $MUTANT)}"
"$out/twophase-sim" -seeds "${SEEDS:-30}" -mutation "$mutation" -out "$out/runs"
if "$out/gotogether" validate -binding twophase.binding.yaml "$out/runs"/*.jsonl > "$out/report"; then
  echo "all runs conform"
else
  awk '/^actor/{if(b~/FAIL/)print b; b=$0; next}{b=b"\n"$0}END{if(b~/FAIL/)print b}' "$out/report" |
    grep -E "^actor|FAIL|not a transition|does not match|quint:" | head -24
  echo "step logs and report in $out"
  finish 1
fi
echo "step logs and report in $out"
finish 0
