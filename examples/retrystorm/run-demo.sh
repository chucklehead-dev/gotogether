#!/usr/bin/env bash
# The retry storm report, and a check of the retry budget's recorded steps.
#   OUT=dir       where the report, binaries and the step log go (default: a temp dir)
#   LAMBDA=9      base load (requests/s)
#   MUTANT=...  uncapped | depositOnRetry (default: none)
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
# the binary takes the mutation kebab-cased (noLocks: no-locks)
mutation=$(printf '%s' "${MUTANT:-}" | sed -E 's/([A-Z])/-\1/g' | tr '[:upper:]' '[:lower:]')
OUT=${OUT:-$(mktemp -d)}
mkdir -p "$OUT"
echo "# output in $OUT"
(cd ../.. && go build -o "$OUT/gotogether" ./cmd/gotogether) || exit 2
go run ./cmd/retrystorm-report -out "$OUT/report" -lambda "${LAMBDA:-9}" -steps "$OUT/steps.jsonl" -mutation "$mutation" || exit 2
echo; echo "\$ gotogether validate -binding retrystorm.binding.yaml steps.jsonl"
"$OUT/gotogether" validate -binding retrystorm.binding.yaml -dir "$OUT/v" "$OUT/steps.jsonl" > "$OUT/verdict.txt"; rc=$?
grep -E 'issue|conformance|invariant|step [0-9]+ |quint:|observed:|RESULT|violated' "$OUT/verdict.txt"
finish $rc
