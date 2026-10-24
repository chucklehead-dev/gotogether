#!/usr/bin/env bash
# The toolkit's own mutation testing: weave a core package's mutants into
# one test binary (qweave -mutate), run its tests once per mutant
# (GOTOGETHER_MUTANT=<id>), and list the survivors: code its tests do not
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
n=0 s=0
while read -r _ _ id rest; do
  n=$((n + 1))
  if GOTOGETHER_MUTANT=$id timeout 300 "$work/t" -test.short -test.failfast >/dev/null 2>&1; then
    s=$((s + 1))
    echo "survived: $id $rest"
  fi
done < "$work/list"
echo "$pkg: $s of $n mutants survived its tests"
if [ -n "$max" ] && [ "$s" -gt "$max" ]; then
  echo "more than $max: kill them with tests, or raise the bar with the reason" >&2
  exit 1
fi
