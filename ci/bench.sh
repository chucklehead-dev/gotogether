#!/usr/bin/env bash
# The core's benchmarks, compared with an earlier commit's by benchstat:
# the nightly's guard against a hot path getting slower (recording a
# step, Reconstruct, the scheduler, a fault choice, the doubles), which
# no test fails on.
#
# Both commits are measured in one job, on one machine, interleaved (a
# run of each package's benchmarks at the base, then at HEAD, COUNT
# times): numbers from another night's runner, another CPU, would differ
# by more than a regression. Every package of the root module with a
# Benchmark function is measured, so a new benchmark joins on its own; one
# the base lacks is reported, not compared.
#
# Usage:
#   ci/bench.sh run BASE OUT     # OUT/old.txt, OUT/new.txt, OUT/compare.txt, OUT/compare.csv
#   ci/bench.sh check CSV        # the regressions in benchstat's CSV; exit 1 if any
# COUNT (6), BENCHTIME (1s), BENCH (.) choose the runs; SEC (25) and MEM
# (10) are the thresholds in percent: a significant change (benchstat's
# p < 0.05) past them in sec/op, or in B/op or allocs/op, is a
# regression. Time on a shared runner moves by 10-20% between runs;
# allocations do not, so their threshold is lower.
set -euo pipefail

cmd=${1:-}
case "$cmd" in
run)
  base=${2:?usage: ci/bench.sh run BASE OUT}
  out=${3:?usage: ci/bench.sh run BASE OUT}
  root=$(cd "$(dirname "$0")/.." && pwd)
  mkdir -p "$out"
  out=$(cd "$out" && pwd)
  count=${COUNT:-6}
  benchtime=${BENCHTIME:-1s}
  bench=${BENCH:-.}
  export GOWORK=off GOTOGETHER_CHECK=
  cd "$root"
  # the root module's packages with a benchmark (go list leaves out the
  # nested modules: the examples, qhegel, grist, ...)
  mapfile -t listed < <(go list -f '{{.Dir}}' ./...)
  pkgs=()
  for dir in "${listed[@]}"; do
    if grep -qs '^func Benchmark' "$dir"/*_test.go; then
      pkgs+=("${dir#"$root"/}")
    fi
  done
  echo "benchmarks of ${#pkgs[@]} packages, $base against HEAD, $count runs each"
  git worktree add --detach -f "$out/base" "$base" >/dev/null
  trap 'git -C "$root" worktree remove --force "$out/base"' EXIT
  mkdir -p "$out/bin"
  : >"$out/old.txt"
  : >"$out/new.txt"
  for p in "${pkgs[@]}"; do
    name=${p//\//_}
    go test -c -o "$out/bin/new-$name.test" "./$p"
    if [ -d "$out/base/$p" ]; then
      (cd "$out/base" && go test -c -o "$out/bin/old-$name.test" "./$p") || echo "::warning::$p does not build at $base: not compared"
    fi
  done
  for i in $(seq "$count"); do
    for p in "${pkgs[@]}"; do
      name=${p//\//_}
      # each in its package's directory, as go test runs it (testdata)
      if [ -x "$out/bin/old-$name.test" ]; then
        (cd "$out/base/$p" && "$out/bin/old-$name.test" -test.run '^$' -test.bench "$bench" -test.benchmem -test.count 1 -test.benchtime "$benchtime") >>"$out/old.txt"
      fi
      (cd "$root/$p" && "$out/bin/new-$name.test" -test.run '^$' -test.bench "$bench" -test.benchmem -test.count 1 -test.benchtime "$benchtime") >>"$out/new.txt"
    done
    echo "run $i of $count done"
  done
  benchstat old="$out/old.txt" new="$out/new.txt" | tee "$out/compare.txt"
  benchstat -format csv old="$out/old.txt" new="$out/new.txt" >"$out/compare.csv" 2>/dev/null
  ;;
check)
  csv=${2:?usage: ci/bench.sh check CSV}
  # benchstat's CSV: a "pkg:" line, then per unit a header row
  # (,sec/op,CI,sec/op,CI,vs base,P) and a row per benchmark whose "vs
  # base" is a percentage when the change is significant, "~" when not
  awk -F, -v sec="${SEC:-25}" -v mem="${MEM:-10}" '
    /^pkg: / { pkg = substr($0, 6); next }
    $1 == "" { unit = ($6 == "vs base") ? $2 : ""; next } # a section of one commit alone compares nothing
    unit != "" && $1 != "" && $1 != "geomean" && $6 ~ /^\+[0-9.]+%$/ {
      pct = substr($6, 2, length($6) - 2) + 0
      limit = (unit == "sec/op") ? sec : mem
      if (pct > limit) {
        printf "::error::%s %s: %s %s (%s): past the %s%% threshold\n", pkg, $1, unit, $6, $7, limit
        bad++
      }
    }
    END { exit bad > 0 }
  ' "$csv"
  ;;
*)
  echo "usage: ci/bench.sh run BASE OUT | check CSV" >&2
  exit 2
  ;;
esac
