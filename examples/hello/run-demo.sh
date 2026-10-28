#!/usr/bin/env bash
# Simulate the counter and its retrying client, then check the step logs
# against the model with gotogether. MUTANT=noDedupe builds the simulator
# with that mutant's overlay (testdata/mutants/noDedupe: files replacing
# the package's of the same names), the bug planted by hand.
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
go build "${overlay[@]}" -o "$out/hello-sim" ./cmd/hello-sim
(cd ../.. && go build -o "$out/gotogether" ./cmd/gotogether)
echo "== hello ${MUTANT:+(mutant $MUTANT)}"
"$out/hello-sim" -seeds "${SEEDS:-20}" -out "$out/runs"
if "$out/gotogether" validate -binding hello.binding.yaml "$out/runs"/*.jsonl > "$out/report"; then
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
