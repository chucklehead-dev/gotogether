#!/usr/bin/env bash
# The migration walk-through (README.md), each stage's check run as a
# user runs it: `go tool gotogether` (a tool of this module: go get -tool
# github.com/chucklehead-dev/gotogether/cmd/gotogether), the steps of the
# audit logs by cmd/auditsteps, the legacy tests recorded by go test.
# Each check must say what the README's stage says it does (the draft's
# FAILs included): the demo exits non-zero when one does not.
#
# MUTANT= forgetsPriorRefunds (the legacy bug), replayCreatesCharge or
# staleLookup builds the handler with that mutant's overlay
# (testdata/mutants/) and runs the legacy tests, recorded, then the
# production day replayed on it: the legacy tests pass, the replay
# catches it, and the demo exits non-zero.
set -euo pipefail
cd "$(dirname "$0")"
out=$(mktemp -d)
trap 'rm -rf "$out"' EXIT
gt() { go tool gotogether "$@"; }
summary() { grep -E 'RESULT|FAIL|issue: error|reached by|spans that no rule|not an action|fits payments' | cut -c1-220 || true; }
# expect ok|fail CMD...: run it, show its summary, and stop the demo if
# its exit status is not the one the README states
expect() {
  local want=$1; shift
  local got=ok
  "$@" > "$out/log" 2>&1 || got=fail
  summary < "$out/log"
  if [ "$got" != "$want" ]; then
    echo "!! expected $want, got $got: $*" >&2
    cat "$out/log" >&2
    exit 1
  fi
}

if [ -n "${MUTANT:-}" ]; then
  [ -d "testdata/mutants/$MUTANT" ] || { echo "unknown MUTANT=$MUTANT (testdata/mutants has $(ls testdata/mutants | tr '\n' ' '))" >&2; exit 2; }
  replace=""
  for f in "testdata/mutants/$MUTANT"/*.go; do
    replace="$replace${replace:+,}\"$PWD/$(basename "$f")\":\"$PWD/$f\""
  done
  echo "{\"Replace\":{$replace}}" > "$out/overlay.json"
  echo "== mutant $MUTANT: the legacy tests, recorded (they pass, and their runs conform)"
  go test -overlay "$out/overlay.json" -count=1 -run '^TestLegacyTestsConform$' -v . | grep -E '^(--- |ok|FAIL)|runs given'
  echo "== mutant $MUTANT: the production day, replayed on it"
  go test -overlay "$out/overlay.json" -count=1 -run '^TestProductionDayReplayedConforms$' -v . | grep -E '^(--- |ok|FAIL)|RESULT|invariant|after step|not a transition|mismatch|quint:|observed:' | cut -c1-200
  exit "${PIPESTATUS[0]}"
fi

echo "== stage 1-2: the context map and the requirements, in model/payments.qnt"
sed -n '/^\/\/ Context:/,/^\/\/$/p' model/payments.qnt
grep -E '^\s*// req R[0-9]+:' model/payments.qnt | sed 's/^ *//'
go test -count=1 -run '^TestRequirementsHaveProvenance$' . > /dev/null && echo "every requirement has two kinds of source, or is assumed"

echo "== stage 3: the draft model on its own (its partial capture impossible)"
expect fail gt simulate -binding testdata/draft/payments.draft.yaml
echo "== stage 3: the model on its own"
expect ok gt simulate -binding payments.binding.yaml

echo "== stage 4: what the code records, against the draft and the model"
expect fail gt lint -binding testdata/draft/payments.draft.yaml -src ./auditsteps
expect ok gt lint -binding payments.binding.yaml -src ./auditsteps

echo "== stage 4: the staging spans, by the draft's rules and the model's"
expect ok gt convert -rules testdata/draft/payments.convert.yaml -out "$out/draft-spans.jsonl" testdata/spans/staging.otlp.json
expect ok gt validate -dir "$out/spans" -binding payments.binding.yaml -convert payments.convert.yaml testdata/spans/staging.otlp.json

echo "== stage 4: the legacy tests, recorded"
go test -count=1 -run '^TestLegacyTestsConform$' -v . | grep -E '^(--- |ok|FAIL)|runs given'

echo "== stage 4: the production log of 2026-10-02 (the legacy bug, before any change)"
go run ./cmd/auditsteps -out "$out/day.jsonl" testdata/logs/payments-2026-10-02.jsonl
expect fail gt validate -dir "$out/day" -binding payments.binding.yaml "$out/day.jsonl"
echo "== stage 4: an older log, rotated"
go run ./cmd/auditsteps -out "$out/old.jsonl" testdata/logs/payments-2026-09-30.jsonl
expect fail gt validate -dir "$out/old" -binding payments.binding.yaml "$out/old.jsonl"

echo "== stage 6: the fixed code, the production day replayed on it"
go test -count=1 -run '^TestProductionDayReplayedConforms$' -v . | grep -E '^(--- |ok|FAIL)'
