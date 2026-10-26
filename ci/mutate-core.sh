#!/usr/bin/env bash
# The toolkit's own mutation testing: weave a core package's mutants into
# one test binary (qweave -mutate), run its tests once per mutant
# (GOTOGETHER_MUTANT=<id>, JOBS at a time), and list the survivors: code its tests do not
# constrain. A survivor in a search or a scheduler is a feature whose
# test would pass with it turned off (the seventh review's most frequent
# lesson). Usage: [JOBS=n] [MEM_MB=n] [GOTOGETHER_CHECK=1] ci/mutate-core.sh qtrace [max-survivors]
#
# Each run's address space is capped at MEM_MB (default 3072): a mutant
# that allocates without end (qsweep's Cover with its loop bound flipped
# takes the 16 GB of a runner in seconds) fails with "out of memory" and
# counts as killed, as one that hangs does; uncapped, it took the whole
# runner down ("The runner has received a shutdown signal") and every
# mutant's result with it. JOBS x MEM_MB stays below the machine's memory.
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
if [ ! -s "$work/list" ]; then
  echo "$pkg: no mutants woven: is it a core package qweave may mutate (not qmut, qrun, qcover, internal/hooks)?" >&2
  exit 1
fi
mem=$(( ${MEM_MB:-3072} * 1024 ))
n=$(wc -l < "$work/list")
# the tests pass unmutated, under the same cap, or every mutant would be
# "killed". A mutant id past the last switches none on by the fast path:
# with GOTOGETHER_MUTANT unset every mutation site looks up its run
# (qrun), which made qenv's tests twenty times slower (225 s, not 11).
( ulimit -v "$mem"; GOTOGETHER_MUTANT=$n "$work/t" -test.short >/dev/null 2>&1 ) ||
  { echo "$pkg: its tests fail unmutated (with MEM_MB=${MEM_MB:-3072})" >&2; exit 1; }
# each mutant's tests, JOBS at a time (default 1): a survivor prints its
# line; a mutant whose tests run past 300 s (a loop that never ends)
# counts as killed, as a test that hangs fails
export work mem
awk '{ id = $3; $1 = $2 = $3 = ""; sub(/^ +/, ""); print id "\t" $0 }' "$work/list" |
  xargs -d '\n' -P "${JOBS:-1}" -I{} sh -c '
    id=$(printf "%s" "$1" | cut -f1); rest=$(printf "%s" "$1" | cut -f2-)
    if (ulimit -v "$mem"; GOTOGETHER_MUTANT=$id exec timeout 300 "$work/t" -test.short -test.failfast) >/dev/null 2>&1; then
      echo "survived: $id $rest"
    fi' _ {} | sort -n -k2 > "$work/survived"
cat "$work/survived"
s=$(wc -l < "$work/survived")
echo "$pkg: $s of $n mutants survived its tests"
if [ -n "$max" ] && [ "$s" -gt "$max" ]; then
  echo "more than $max: kill them with tests, or raise the bar with the reason" >&2
  exit 1
fi
