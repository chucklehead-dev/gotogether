#!/usr/bin/env bash
# Build the woven demo and the gotogether CLI, lint the binding, then run every
# scenario. Each run emits its model steps three ways, and each is validated
# against edgePublish.qnt:
#   spans.jsonl   OTLP/JSON through the OTel SDK      -> reconstructed, replayed
#   steps.jsonl   native step log (no OTel)           -> reconstructed, replayed
#   itf/*.json    model-level ITF, binding applied in-process -> replayed directly
#   OUT=dir            where binaries, traces and generated modules go (default: a temp dir)
#   QUINT_BACKEND      quint test backend: rust (default) or typescript
#   VERBOSE=1          print the full reports, not just the verdicts
#   MUTANT=manifestFirst | noLock   that bug's scenario alone (default: every scenario)
# happy and rotation-race must conform; ambiguous-seal (the model's known
# findings F1/F3), manifest-first and no-lock must not (README's table): the
# exit status is 1 when any scenario's verdict is not the expected one, 2
# when the demo could not run.
set -uo pipefail
cd "$(dirname "$0")"
case "${MUTANT:-}" in
"") ;;
manifestFirst) SCENARIOS=manifest-first ;;
noLock) SCENARIOS=no-lock ;;
*) echo "unknown MUTANT=$MUTANT (manifestFirst, noLock)" >&2; exit 2 ;;
esac
OUT=${OUT:-$(mktemp -d)}
mkdir -p "$OUT"
export QUINT_BACKEND=${QUINT_BACKEND:-rust}
echo "# output in $OUT"
(cd ../.. && go build -o "$OUT/gotogether" ./cmd/gotogether) || exit 2
go run github.com/DataDog/orchestrion go build -o "$OUT/demo" ./cmd/demo || exit 2
echo; echo "\$ gotogether lint -binding binding.yaml -src publisher"
"$OUT/gotogether" lint -binding binding.yaml -src publisher || exit 2

status=0
verdict() { # file -> one line: conformance, failing step, invariants, exit status
  local name=$1; shift
  # -v: a failing record's line says its step in the replay, which the
  # three inputs share (each names its record its own way: a span, a
  # line, none for ITF)
  local out; out=$("$OUT/gotogether" validate -v "$@" 2>&1); local rc=$?
  case "$sc" in
    happy | rotation-race) [ $rc -eq 0 ] || status=1 ;;
    *) [ $rc -ne 0 ] || status=1 ;;
  esac
  [ -n "${VERBOSE:-}" ] && echo "$out"
  printf '  %-9s %-28s exit %d  %s\n' "$name" "$(grep -m1 -o 'input .*: [a-zA-Z/ -]*$' <<<"$out" | sed 's/^input [^:]*: //')" "$rc" \
    "$(awk '{ sub(/^ +/, "") }
      /^(conformance:|invariant .*: (FAIL|PASS))/ { print; next }
      /^step [0-9]+ is not a transition/ { print $1 " " $2 " disabled"; next }
      /^after step [0-9]+ the model.s state does not match/ { print $2 " " $3 " state mismatch"; next }
      /^violated after step [0-9]+/ { print; next }
      /^record [^ ]+ is not a transition/ { k = "disabled"; next }
      /^after record .* does not match/ { k = "state mismatch"; next }
      /^violated after record / { k = "violated"; next }
      k != "" && /^record: .* of the replay/ { match($0, /step [0-9]+ of the replay/); n = substr($0, RSTART, RLENGTH - 14)
        print (k == "violated" ? "violated after " n : n " " k); k = "" }' <<<"$out" | paste -sd ';' -)"
}

for sc in ${SCENARIOS:-happy rotation-race ambiguous-seal manifest-first no-lock}; do
  echo; echo "=== scenario $sc"
  d="$OUT/$sc"; mkdir -p "$d"
  "$OUT/demo" -scenario "$sc" -out "$d/spans.jsonl" -steps "$d/steps.jsonl" -itf "$d/itf" -binding binding.yaml | grep -E '^scenario|^wrote'
  verdict "otel"  -binding binding.yaml -dir "$d/v-otel"  "$d/spans.jsonl"
  verdict "steps" -binding binding.yaml -dir "$d/v-steps" "$d/steps.jsonl"
  verdict "itf"                         -dir "$d/v-itf"   "$d"/itf/*.itf.json
done
exit $status
