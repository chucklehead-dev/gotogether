#!/usr/bin/env bash
# The toolkit's own mutation testing: weave a core package's mutants into
# one test binary (qweave -mutate), run its tests once per mutant
# (GOTOGETHER_MUTANT=<id>, JOBS at a time), and list the survivors: code its tests do not
# constrain. A survivor in a search or a scheduler is a feature whose
# test would pass with it turned off (the seventh review's most frequent
# lesson). Usage: ci/mutate-core.sh qtrace [max-survivors]
set -euo pipefail
pkg=${1:?a core package, e.g. qtrace}
max=${2:-}
root=$(cd "$(dirname "$0")/.." && pwd)
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
go run -C "$root/qweave" ./cmd/qweave -C "$root" -mutate -coverpkg "github.com/chucklehead-dev/gotogether/$pkg" \
  go test -c -o "$work/t" "./$pkg/" >/dev/null
cd "$root/$pkg"
GOTOGETHER_MUTANT=list "$work/t" -test.run '^$' 2>&1 | grep '^qmut: mutant' > "$work/list" || true
# each mutant's tests, JOBS at a time (default 1): a survivor prints its line
export work
awk '{ id = $3; $1 = $2 = $3 = ""; sub(/^ +/, ""); print id "\t" $0 }' "$work/list" |
  xargs -d '\n' -P "${JOBS:-1}" -I{} sh -c '
    id=$(printf "%s" "$1" | cut -f1); rest=$(printf "%s" "$1" | cut -f2-)
    if GOTOGETHER_MUTANT=$id timeout 300 "$work/t" -test.short -test.failfast >/dev/null 2>&1; then
      echo "survived: $id $rest"
    fi' _ {} | sort -n -k2 > "$work/survived" || true
cat "$work/survived"
n=$(wc -l < "$work/list")
s=$(wc -l < "$work/survived")
echo "$pkg: $s of $n mutants survived its tests"
if [ -n "$max" ] && [ "$s" -gt "$max" ]; then
  echo "more than $max: kill them with tests, or raise the bar with the reason" >&2
  exit 1
fi
