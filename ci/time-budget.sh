#!/usr/bin/env bash
# Fails a package whose tests cost more than twice their CPU seconds in
# ci/test-times.txt: a check that got slow (a search that lost its memo,
# a test that lost its -short) is found by the push that made it so, not
# by a job timing out later. CPU time, not wall time: CI runs a module's
# packages at once on a few cores, and a package's wall time there is its
# neighbours' as much as its own.
#
# Reads the lines internal/cputime appends (go test -exec), one per test
# binary: "<package dir> <cpu seconds> <wall seconds>".
# Usage:
#   go build -o "$RUNNER_TEMP/cputime" ./internal/cputime   # -C ../.. from an example
#   GOTOGETHER_CPU_TIMES="$RUNNER_TEMP/cpu.txt" go test -exec "$RUNNER_TEMP/cputime" ./...
#   ci/time-budget.sh < "$RUNNER_TEMP/cpu.txt"
# TIMES= names another budget file (the script's test does).
set -euo pipefail
root=$(cd "$(dirname "$0")/.." && pwd)
times=${TIMES:-$root/ci/test-times.txt}
status=0
lines=0
while read -r dir cpu wall _; do
  [ -n "$dir" ] || continue
  if [ "$dir" = ok ] || [ "$dir" = "?" ] || [ "$dir" = FAIL ] || [ "$dir" = "---" ]; then
    echo "::error::ci/time-budget.sh reads internal/cputime's lines, not go test's output: run go test with -exec cputime and GOTOGETHER_CPU_TIMES=file, and pass that file"
    exit 2
  fi
  lines=$((lines + 1))
  # a package's directory relative to the repository, as test-times.txt names it
  pkg=${dir#"$root"/}
  [ "$dir" = "$root" ] && pkg=.
  budget=$(awk -v p="$pkg" '$1 == p { print $2 }' "$times")
  [ -n "$budget" ] || continue
  if awk -v t="$cpu" -v b="$budget" 'BEGIN { exit !(t > 2 * b) }'; then
    echo "::error::$pkg's tests took ${cpu} CPU seconds (${wall}s wall), more than twice its ${budget} (ci/test-times.txt, CPU seconds of the test binary and the quint processes it starts): find what got slow (a test that lost -short, a search that lost its memo), or, if the cost is deliberate, set its line to the new CPU time with the reason"
    status=1
  fi
done
if [ "$lines" = 0 ]; then
  echo "::error::ci/time-budget.sh read no CPU times: was go test run with -exec cputime and GOTOGETHER_CPU_TIMES set to the file passed here?"
  exit 2
fi
exit $status
