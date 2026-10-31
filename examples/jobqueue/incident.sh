#!/usr/bin/env bash
# Rehearse a planted stale-ack incident using native reports and replay.
set -euo pipefail
cd "$(dirname "$0")"
for tool in go quint jq; do
  command -v "$tool" >/dev/null || { echo "incident.sh requires $tool on PATH" >&2; exit 2; }
done
OUT=${OUT:-$(mktemp -d)}
mkdir -p "$OUT"
OUT=$(cd "$OUT" && pwd)
bundle="$OUT/capture"
moved="$OUT/moved-capture"
if [ -e "$bundle" ] || [ -e "$moved" ]; then
  echo "choose a fresh OUT: existing incident evidence will not be overwritten" >&2
  exit 2
fi
mkdir -p "$OUT/bin" "$bundle/model" "$bundle/reports"
(cd ../.. && go build -o "$OUT/bin/gotogether" ./cmd/gotogether)
go build -o "$OUT/bin/jobqueue-incident" ./cmd/jobqueue-incident
cp jobqueue.binding.yaml "$bundle/"
cp model/jobqueue.qnt "$bundle/model/"
"$OUT/bin/jobqueue-incident" -out "$bundle"

# Exit one also means no verdict: require the native verdict, not just the code.
expect() {
  local code=$1 verdict=$2 report=$3
  shift 3
  local got=0
  "$@" > "$report" || got=$?
  if [ "$got" -ne "$code" ]; then
    echo "unexpected exit $got (wanted $code): $report" >&2
    return 2
  fi
  jq -e --arg verdict "$verdict" --argjson code "$code" \
    '.verdict == $verdict and .exit == $code' "$report" >/dev/null
}
for case_name in faithful incident repaired; do
  code=0; verdict=pass
  if [ "$case_name" = incident ]; then code=1; verdict=fail; fi
  expect "$code" "$verdict" "$bundle/reports/$case_name.json" \
    "$OUT/bin/gotogether" validate -binding "$bundle/jobqueue.binding.yaml" \
    -manifest "$bundle/$case_name.run.json" -dir "$bundle/work/$case_name" -json \
    "$bundle/$case_name.jsonl"
done
jq -e '.actors | length == 1' "$bundle/reports/incident.json" >/dev/null
jq -e '.actors[0].failures[0] | .check == "conformance" and .kind == "disabled" and
  .record.actor == "jobqueue" and .record.seq == 4 and .record.step == 5 and
  .record.action == "ack" and .record.outcome == "ok"' "$bundle/reports/incident.json" >/dev/null
jq -se '[.[] | [.model.binding.sha256, .model.spec.sha256]] | unique | length == 1' \
  "$bundle/faithful.run.json" "$bundle/incident.run.json" "$bundle/repaired.run.json" >/dev/null

ref=$(jq -c '.actors[0].failures[0].record | {actor, recorder, seq}' "$bundle/reports/incident.json")
"$OUT/bin/gotogether" nav -run "$bundle/incident.jsonl" -record "$ref" -json > "$bundle/reports/record.json"
"$OUT/bin/gotogether" nav -binding "$bundle/jobqueue.binding.yaml" -run "$bundle/incident.jsonl" \
  -at 4 -json > "$bundle/reports/cursor.json"
jq -e 'has("verdict") | not' "$bundle/reports/cursor.json" >/dev/null

identity() {
  jq -cS '.actors[0].failures[0] | {check, kind, quint,
    record: (.record | {actor, recorder, seq, step, action, args, outcome})}' "$1"
}
before=$(identity "$bundle/reports/incident.json")
# Removing the original path and changing cwd prevents accidental path fallback.
mv "$bundle" "$moved"
(cd "$OUT" && expect 1 fail "$moved/reports/replay.json" \
  "$OUT/bin/gotogether" validate -replay "$moved/incident.run.json" \
  -manifest "$moved/replayed.run.json" -dir "$moved/work/replay" -json)
after=$(identity "$moved/reports/replay.json")
if [ "$before" != "$after" ]; then echo "replay changed native failure identity" >&2; exit 1; fi
jq -e --slurpfile original "$moved/incident.run.json" \
  '.verdict.verdict == $original[0].verdict.verdict and
   .verdict.failing == $original[0].verdict.failing and
   ([.verdict.actors[] | {actor, verdict, action}] ==
    [$original[0].verdict.actors[] | {actor, verdict, action}])' "$moved/replayed.run.json" >/dev/null

# A complete header with no observations has no verdict, despite the same exit.
head -n 1 "$moved/incident.jsonl" > "$moved/empty.jsonl"
expect 1 'no verdict' "$moved/reports/empty.json" "$OUT/bin/gotogether" validate \
  -binding "$moved/jobqueue.binding.yaml" -dir "$moved/work/empty" -json "$moved/empty.jsonl"
# A harmless byte change must be refused by the manifest hash guard.
cp "$moved/incident.jsonl" "$OUT/original-incident.jsonl"
trap 'cp "$OUT/original-incident.jsonl" "$moved/incident.jsonl"' EXIT
printf '\n' >> "$moved/incident.jsonl"
expect 2 error "$moved/reports/tampered.json" "$OUT/bin/gotogether" validate \
  -replay "$moved/incident.run.json" -json
cp "$OUT/original-incident.jsonl" "$moved/incident.jsonl"
trap - EXIT
rm "$OUT/original-incident.jsonl"
mv "$moved/incident.jsonl" "$OUT/original-incident.jsonl"
trap 'mv "$OUT/original-incident.jsonl" "$moved/incident.jsonl"' EXIT
expect 2 error "$moved/reports/missing.json" "$OUT/bin/gotogether" validate \
  -replay "$moved/incident.run.json" -json
mv "$OUT/original-incident.jsonl" "$moved/incident.jsonl"
trap - EXIT
printf 'Incident rehearsal verified. Native evidence: %s\n' "$moved"
printf 'TUI: gotogether-nav -binding %q -run %q\n' "$moved/jobqueue.binding.yaml" "$moved/incident.jsonl"
