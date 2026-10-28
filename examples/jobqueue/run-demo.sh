#!/usr/bin/env bash
# Run the job queue with workers, slow jobs and a restart; check the run.
#   OUT=dir       where binaries and the step log go (default: a temp dir)
#   MUTANT=...  ackAnyLease | forgetDone (default: none)
set -uo pipefail
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
"" | ackAnyLease | forgetDone) ;;
*) echo "unknown MUTANT=$MUTANT (ackAnyLease, forgetDone)" >&2; exit 2 ;;
esac
# the binary takes the mutation kebab-cased (noLocks: no-locks)
mutation=$(printf '%s' "${MUTANT:-}" | sed -E 's/([A-Z])/-\1/g' | tr '[:upper:]' '[:lower:]')
OUT=${OUT:-$(mktemp -d)}
mkdir -p "$OUT"
echo "# output in $OUT"
(cd ../.. && go build -o "$OUT/gotogether" ./cmd/gotogether) || exit 2
go run ./cmd/jobqueue -steps "$OUT/steps.jsonl" -mutation "$mutation" || exit 2
show() { grep -E 'issue|conformance|invariant|step [0-9]+ |quint:|observed:|RESULT|violated'; }
echo; echo "\$ gotogether validate -binding jobqueue.binding.yaml steps.jsonl"
"$OUT/gotogether" validate -binding jobqueue.binding.yaml -dir "$OUT/v" "$OUT/steps.jsonl" > "$OUT/verdict.txt"; rc=$?
show < "$OUT/verdict.txt"
echo; echo "\$ gotogether validate -binding jobqueue.binding.yaml -invariants exactlyOnce steps.jsonl"
# a violation here is the design's known limit, not a failure of the demo
"$OUT/gotogether" validate -binding jobqueue.binding.yaml -invariants exactlyOnce -dir "$OUT/v1" "$OUT/steps.jsonl" | show || true
# the check against the guarantees decides (finish): exactlyOnce's
# violation above is the design's known limit, not the demo's failure
finish $rc
