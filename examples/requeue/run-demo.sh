#!/usr/bin/env bash
# The controller's model on its own, then its simulated runs checked
# against it with gotogether. First the model's state graph (simulate
# -graph): the faithful model is live, and the storm's (RESEND) fails with
# its lasso, which is what the storm's model must show (this script fails
# if it does not). Then 20 seeds of the controller, each run's steps
# validated: every run conforms and is reconciled at its settle.
# MUTANT=stampSpec | giveUp builds the simulator with that mutant's
# overlay (testdata/mutants/<name>: files replacing the package's of the
# same names), and the check that catches it fails some run (the script
# then exits 0: caught, as expected); stampSpec's
# runs are also validated against the storm's model, whose report shows
# the cycle they go round (hot cycle).
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
out=$(mktemp -d)
overlay=()
if [ -n "${MUTANT:-}" ]; then
  [ -d "testdata/mutants/$MUTANT" ] || { echo "unknown MUTANT=$MUTANT (testdata/mutants has $(ls testdata/mutants | tr '\n' ' '))" >&2; exit 2; }
  replace=""
  for f in "testdata/mutants/$MUTANT"/*.go; do
    replace="$replace${replace:+,}\"$PWD/$(basename "$f")\":\"$PWD/$f\""
  done
  echo "{\"Replace\":{$replace}}" > "$out/overlay.json"
  overlay=(-overlay "$out/overlay.json")
fi
go build "${overlay[@]}" -o "$out/requeue-sim" ./cmd/requeue-sim
(cd ../.. && go build -o "$out/gotogether" ./cmd/gotogether)

echo "== the faithful model: reconciled live"
"$out/gotogether" simulate -graph -binding requeue.binding.yaml
echo "== the storm's model (RESEND): a lasso keeps reconciled false"
if "$out/gotogether" simulate -graph -binding requeue.storm.binding.yaml -out "$out/storm.qnt"; then
  echo "the storm's model has no lasso: it should" >&2
  exit 1
fi
# quint replays the lasso: the path, and one turn of the cycle back to its state
quint test --main simulate --match 'gtg_' "$out/storm.qnt" | grep -E "ok |passing|failing"

echo "== the controller ${MUTANT:+(mutant $MUTANT)}"
"$out/requeue-sim" -seeds "${SEEDS:-20}" -out "$out/runs"
if [ "${MUTANT:-}" = stampSpec ]; then
  echo "-- against the storm's model: conforms, never settles, a hot cycle"
  "$out/gotogether" validate -binding requeue.storm.binding.yaml "$out/runs/seed1.jsonl" | grep -E "conformance|hot cycle" || true
fi
if "$out/gotogether" validate -binding requeue.binding.yaml "$out/runs"/*.jsonl > "$out/report"; then
  echo "all runs conform, each reconciled at its settle"
else
  # the first failing run: what was observed, and the state the model was in
  awk '/^actor/{if(b~/FAIL/){print b; done=1; exit} b=$0; next}{b=b"\n"$0}END{if(!done && b~/FAIL/)print b}' "$out/report" |
    grep -vE "module:|N/A"
  echo "step logs and report in $out"
  finish 1
fi
echo "step logs and report in $out"
finish 0
