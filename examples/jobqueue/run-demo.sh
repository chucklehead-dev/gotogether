#!/usr/bin/env bash
# Run the job queue with workers, slow jobs and a restart; check the run.
#   OUT=dir       where binaries and the step log go (default: a temp dir)
#   MUTANT=...  ackAnyLease | forgetDone (default: none)
set -uo pipefail
cd "$(dirname "$0")"
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
# exit status: that of the check against the guarantees (0 unless MUTANT found a bug)
exit $rc
