#!/usr/bin/env bash
# Run the pipeline on each backend (simulated with faults, in-process
# Kafka, fake Kinesis), then check every run's step log against the model
# with gotogether. MUTANT=commitFirst | noDedupe | nonIdempotentProducer
# | offByOne injects a bug; BACKENDS picks backends; RUNS the runs each.
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
"" | commitFirst | noDedupe | nonIdempotentProducer | offByOne) ;;
*) echo "unknown MUTANT=$MUTANT (commitFirst, noDedupe, nonIdempotentProducer, offByOne)" >&2; exit 2 ;;
esac
# the binary takes the mutation kebab-cased (noLocks: no-locks)
mutation=$(printf '%s' "${MUTANT:-}" | sed -E 's/([A-Z])/-\1/g' | tr '[:upper:]' '[:lower:]')
out=$(mktemp -d)
go build -o "$out/streams-demo" ./cmd/streams-demo
go build -C ../.. -o "$out/gotogether" ./cmd/gotogether
status=0
for backend in ${BACKENDS:-sim kafka kinesis}; do
  runs=${RUNS:-20}
  [ "$backend" = sim ] || runs=${RUNS:-2}
  echo "== $backend ${MUTANT:+(mutant $MUTANT)}"
  "$out/streams-demo" -backend "$backend" -runs "$runs" -mutation "$mutation" -out "$out/$backend"
  # one actor per run: the report lists each run's verdict
  if "$out/gotogether" validate -binding streams.binding.yaml "$out/$backend"/*.jsonl > "$out/$backend.report"; then
    echo "all runs conform"
  else
    status=1
    awk '/^actor/{if(b~/FAIL/)print b; b=$0; next}{b=b"\n"$0}END{if(b~/FAIL/)print b}' "$out/$backend.report" |
      grep -E "^actor|FAIL|not a transition|does not match|quint:" | head -24
  fi
done
echo "step logs and reports in $out"
finish $status
