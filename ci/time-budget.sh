#!/usr/bin/env bash
# Fails a package whose tests took more than twice their time in
# ci/test-times.txt: a check that got slow (a search that lost its memo,
# a test that lost its -short) is found by the push that made it so, not
# by a job timing out later. Reads go test's output on stdin.
# Usage: go test ./... | tee out.txt; ci/time-budget.sh < out.txt
set -euo pipefail
root=$(cd "$(dirname "$0")/.." && pwd)
mod=github.com/chucklehead-dev/gotogether/
status=0
while read -r ok pkg secs _; do
  [ "$ok" = ok ] || continue
  pkg=${pkg#"$mod"}
  t=${secs%s}
  budget=$(awk -v p="$pkg" '$1 == p { print $2 }' "$root/ci/test-times.txt")
  [ -n "$budget" ] || continue
  if awk -v t="$t" -v b="$budget" 'BEGIN { exit !(t > 2 * b) }'; then
    echo "::error::$pkg took ${t}s, more than twice its ${budget}s (ci/test-times.txt): find what got slow, or update its line with the reason"
    status=1
  fi
done
exit $status
