#!/usr/bin/env bash
# Run the kvstore service under concurrent load, then check what it recorded
# against model/kv.qnt two ways: its model steps, and its plain OTel spans.
#   OUT=dir       where binaries and recordings go (default: a temp dir)
#   MUTANT=...    a mutant of the store, built with its overlay
#                 (testdata/mutants/<name>: files replacing kv/'s of the
#                 same names): staleCAS | revisionOnConflict | staleRead |
#                 lateRevisionOnConflict | staleSnapshot (default: none)
# Then a rotation demo: the step log rotated every 120 operations, each new
# file headed by the store's snapshot, and the last file validated alone.
# Exit status: 0 when every validation conforms, 1 when one does not (a
# mutant's run), 2 when the demo could not run.
set -uo pipefail
cd "$(dirname "$0")"
OUT=${OUT:-$(mktemp -d)}
mkdir -p "$OUT"
echo "# output in $OUT"
status=0
# check FILE...: validate, showing the report's verdict lines; a run that
# does not conform makes the demo's exit status 1
check() {
  "$OUT/gotogether" validate "$@" > "$OUT/report" || status=1
  grep -E 'input|lost-prefix|conformance|invariant|step [0-9]+ |quint:|expect:|observed:|obs:|RESULT' "$OUT/report"
}
(cd ../.. && go build -o "$OUT/gotogether" ./cmd/gotogether) || exit 2
overlay=()
if [ -n "${MUTANT:-}" ]; then
  [ -d "testdata/mutants/$MUTANT" ] || { echo "unknown MUTANT=$MUTANT (testdata/mutants has $(ls testdata/mutants | tr '\n' ' '))" >&2; exit 2; }
  replace=""
  for f in "testdata/mutants/$MUTANT"/*.go; do
    replace="$replace${replace:+,}\"$PWD/kv/$(basename "$f")\":\"$PWD/$f\""
  done
  echo "{\"Replace\":{$replace}}" > "$OUT/overlay.json"
  overlay=(-overlay "$OUT/overlay.json")
  echo "# mutant $MUTANT"
fi
go build "${overlay[@]}" -o "$OUT/kvserver" ./cmd/kvserver || exit 2
go build -o "$OUT/kvload" ./cmd/kvload || exit 2

"$OUT/kvserver" -addr 127.0.0.1:18080 -steps "$OUT/steps.jsonl" -spans "$OUT/spans.jsonl" &
srv=$!
for _ in $(seq 50); do curl -s -o /dev/null 127.0.0.1:18080/kv/a && break; sleep 0.1; done
"$OUT/kvload" -addr 127.0.0.1:18080 -clients 8 -ops 50 -keys a,b || { kill $srv; exit 2; }
kill -INT $srv; wait $srv

echo; echo "\$ gotogether validate -binding kv.binding.yaml steps.jsonl"
check -binding kv.binding.yaml -dir "$OUT/v-steps" "$OUT/steps.jsonl"
echo; echo "\$ gotogether validate -binding kv.binding.yaml -convert kv.convert.yaml spans.jsonl"
check -binding kv.binding.yaml -convert kv.convert.yaml -dir "$OUT/v-spans" "$OUT/spans.jsonl"

# Rotation: a new step log every 120 operations, each headed by the
# store's whole state (restore, the binding's snapshot). The last file
# alone has lost its prefix; its head is where it is judged from.
"$OUT/kvserver" -addr 127.0.0.1:18081 -steps "$OUT/rotated.jsonl" -rotate 120 &
srv=$!
for _ in $(seq 50); do curl -s -o /dev/null 127.0.0.1:18081/kv/a && break; sleep 0.1; done
"$OUT/kvload" -addr 127.0.0.1:18081 -clients 8 -ops 50 -keys a,b || { kill $srv; exit 2; }
kill -INT $srv; wait $srv
last=$(ls "$OUT"/rotated.*.jsonl | sort -t. -k2 -n | tail -1)
echo; echo "\$ gotogether validate -binding kv.binding.yaml $(basename "$last")    # the last rotated log alone"
check -binding kv.binding.yaml -dir "$OUT/v-rotated" "$last"
exit $status
