#!/usr/bin/env bash
# The examples' compiled models (ROADMAP Q0): every <stem>.model.json.gz
# under examples/ (gotogether.model/1 gzipped, DESIGN §113), beside its
# binding (validate.CompiledFor), and every example
# test that calls qtest.RequireModel. Nothing is listed here: the models
# and the tests are found, so a new one is checked as soon as it is
# checked in. The bindings that keep no model, and the tests that still
# need quint, are ci/needs-quint.txt's, each with its reason
# (internal/exampleapps' TestCompiledModels keeps both current).
#
# Usage:
#   ci/compiled-examples.sh check [MODEL...]
#                                   where quint is installed: each model is
#                                   current for its binding by meaning
#                                   (gotogether compile -check)
#   ci/compiled-examples.sh test    with quint taken off PATH: each example's
#                                   RequireModel tests, -short, every check
#                                   by its compiled model alone
#                                   (GOTOGETHER_COMPILED=1)
#   ci/compiled-examples.sh list    the models and the tests, as found
# CPU_TIMES= (test): a file for internal/cputime's lines, one per package.
set -euo pipefail
root=$(cd "$(dirname "$0")/.." && pwd)
cd "$root"

# models are the compiled models under examples/, sorted.
models() {
  find examples -name '*.model.json.gz' -not -path '*/node_modules/*' -not -path '*/.gosim/*' | LC_ALL=C sort
}

# binding_of is the binding a model at path was compiled from: the
# artifact records it, relative to its own directory.
binding_of() {
  local rel
  rel=$(gzip -dc "$1" | jq -r '.binding.path')
  if [ -z "$rel" ] || [ "$rel" = null ]; then
    echo "::error::$1 names no binding (.binding.path): is it gotogether compile's?" >&2
    exit 2
  fi
  echo "$(dirname "$1")/$rel"
}

# tests prints "<package dir> <Test>" for each test function (Test or
# Fuzz) whose body calls qtest.RequireModel, in examples' _test.go files,
# and a package's TestMutants where its TestMutant does: qtest.Mutants
# runs TestMutant in each mutant's binary, where it is judged.
tests() {
  find examples -name '*_test.go' -not -path '*/node_modules/*' -not -path '*/.gosim/*' -not -path '*/testdata/*' -print0 |
    xargs -0 awk '
      FNR == 1 { fn = "" }
      /^func (Test|Fuzz)[A-Za-z0-9_]*\(/ { fn = $2; sub(/\(.*/, "", fn) }
      /^func / && !/^func (Test|Fuzz)/ { fn = "" }
      /qtest\.RequireModel\(/ && fn != "" {
        d = FILENAME; sub(/\/[^\/]*$/, "", d); print d, fn
        if (fn == "TestMutant") print d, "TestMutants"
        fn = ""
      }' | LC_ALL=C sort -u
}

# module_of is the directory of the go.mod nearest above dir.
module_of() {
  local d=$1
  while [ ! -f "$d/go.mod" ]; do d=$(dirname "$d"); done
  echo "$d"
}

case "${1:-}" in
list)
  echo "models:"
  for m in $(models); do echo "  $m ($(wc -c < "$m") bytes, $(gzip -dc "$m" | wc -c) uncompressed) of $(binding_of "$m")"; done
  echo "tests:"
  tests | sed 's/^/  /'
  ;;
check)
  command -v quint >/dev/null || { echo "::error::compiled-examples.sh check compiles each model again: it needs quint on PATH"; exit 2; }
  bin=$(mktemp -d)/gotogether
  go build -o "$bin" ./cmd/gotogether
  status=0 n=0
  shift
  for m in ${*:-$(models)}; do
    b=$(binding_of "$m")
    n=$((n + 1))
    "$bin" compile -binding "$b" -o "$m" -check || status=1
  done
  [ "$n" -gt 0 ] || { echo "::error::no compiled model under examples/: is the walk wrong?"; exit 2; }
  exit $status
  ;;
test)
  # quint off PATH: every directory holding one, and with it the Node that
  # runs it; then there must be none
  path=
  IFS=: read -ra dirs <<< "$PATH"
  for d in "${dirs[@]}"; do
    [ -x "$d/quint" ] && continue
    path=${path:+$path:}$d
  done
  export PATH=$path
  if command -v quint >/dev/null; then echo "::error::quint is still on PATH ($(command -v quint)): these tests must need none"; exit 1; fi
  export GOTOGETHER_COMPILED=1
  exec_flag=()
  if [ -n "${CPU_TIMES:-}" ]; then
    go build -o "$(dirname "$CPU_TIMES")/cputime" ./internal/cputime
    export GOTOGETHER_CPU_TIMES=$CPU_TIMES
    exec_flag=(-exec "$(dirname "$CPU_TIMES")/cputime")
  fi
  status=0 n=0
  # one go test per package, its RequireModel tests by name: the package's
  # other tests need quint, and GOTOGETHER_STRICT fails a skip. Mutation
  # and coverage tests run woven (qweave), as the examples' CI step skips
  # them unwoven
  while read -r pkg names; do
    mod=$(module_of "$pkg")
    rel=./$(realpath --relative-to="$mod" "$pkg")
    echo "::group::$pkg: $names"
    n=$((n + 1))
    (cd "$mod" && go test "${exec_flag[@]}" -short -count=1 -timeout 30m -skip '^(TestMutationOf|TestCodeCoverage)' -run "^($(echo "$names" | tr ' ' '|'))\$" "$rel") || status=1
    echo "::endgroup::"
  done < <(tests | awk '{ t[$1] = t[$1] " " $2 } END { for (p in t) print p t[p] }' | LC_ALL=C sort)
  [ "$n" -gt 0 ] || { echo "::error::no example test calls qtest.RequireModel: is the walk wrong?"; exit 2; }
  exit $status
  ;;
*)
  echo "usage: ci/compiled-examples.sh check|test|list" >&2
  exit 2
  ;;
esac
