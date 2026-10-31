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
source="$OUT/source"
bundle="$OUT/capture"
moved="$OUT/moved-capture"
if [ -e "$source" ] || [ -e "$bundle" ] || [ -e "$moved" ]; then
  echo "choose a fresh OUT: existing incident evidence will not be overwritten" >&2
  exit 2
fi
mkdir -p "$OUT/bin" "$source/reports"
(cd ../.. && go build -o "$OUT/bin/gotogether" ./cmd/gotogether)
go build -o "$OUT/bin/jobqueue-incident" ./cmd/jobqueue-incident
"$OUT/bin/jobqueue-incident" -out "$source"

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
  expect "$code" "$verdict" "$source/reports/$case_name.json" \
    "$OUT/bin/gotogether" validate -binding "$PWD/jobqueue.binding.yaml" \
    -manifest "$source/$case_name.run.json" -dir "$source/work/$case_name" -json \
    "$source/$case_name.jsonl"
done
jq -e '.actors | length == 1' "$source/reports/incident.json" >/dev/null
jq -e '.actors[0].failures[0] | .check == "conformance" and .kind == "disabled" and
  .record.actor == "jobqueue" and .record.seq == 4 and .record.step == 5 and
  .record.action == "ack" and .record.outcome == "ok"' "$source/reports/incident.json" >/dev/null
jq -se '[.[] | [.model.binding.sha256, .model.spec.sha256]] | unique | length == 1' \
  "$source/faithful.run.json" "$source/incident.run.json" "$source/repaired.run.json" >/dev/null

ref=$(jq -c '.actors[0].failures[0].record | {actor, recorder, seq}' "$source/reports/incident.json")
"$OUT/bin/gotogether" nav -run "$source/incident.jsonl" -record "$ref" -json > "$source/reports/record.json"
"$OUT/bin/gotogether" nav -binding "$PWD/jobqueue.binding.yaml" -run "$source/incident.jsonl" \
  -at 4 -json > "$source/reports/cursor.json"
jq -e 'has("verdict") | not' "$source/reports/cursor.json" >/dev/null

# Export all three native manifests without pre-copying models or bindings.
for case_name in faithful incident repaired; do
  "$OUT/bin/gotogether" bundle -out "$bundle/$case_name" "$source/$case_name.run.json"
done
cp -R "$source/reports" "$bundle/reports"

identity() {
  jq -cS '.actors[0].failures[0] | {check, kind, quint,
    record: (.record | {actor, recorder, seq, step, action, args, outcome})}' "$1"
}
before=$(identity "$bundle/reports/incident.json")
# Removing the original path and changing cwd prevents accidental path fallback.
mv "$bundle" "$moved"
rm -rf "$source"
incident_manifest="$moved/incident/run.json"
incident_log="$moved/incident/$(jq -r '.inputs[0].path' "$incident_manifest")"
incident_binding="$moved/incident/$(jq -r '.model.binding.path' "$incident_manifest")"
(cd "$OUT" && expect 1 fail "$moved/reports/replay.json" \
  "$OUT/bin/gotogether" validate -replay "$incident_manifest" \
  -manifest "$moved/replayed.run.json" -dir "$moved/work/replay" -json)
after=$(identity "$moved/reports/replay.json")
if [ "$before" != "$after" ]; then echo "replay changed native failure identity" >&2; exit 1; fi
jq -e --slurpfile original "$incident_manifest" \
  '.verdict.verdict == $original[0].verdict.verdict and
   .verdict.failing == $original[0].verdict.failing and
   ([.verdict.actors[] | {actor, verdict, action}] ==
    [$original[0].verdict.actors[] | {actor, verdict, action}])' "$moved/replayed.run.json" >/dev/null

# A complete header with no observations has no verdict, despite the same exit.
head -n 1 "$incident_log" > "$moved/empty.jsonl"
expect 1 'no verdict' "$moved/reports/empty.json" "$OUT/bin/gotogether" validate \
  -binding "$incident_binding" -dir "$moved/work/empty" -json "$moved/empty.jsonl"
# A harmless byte change must be refused by the manifest hash guard.
cp "$incident_log" "$OUT/original-incident.jsonl"
trap 'cp "$OUT/original-incident.jsonl" "$incident_log"' EXIT
printf '\n' >> "$incident_log"
expect 2 error "$moved/reports/tampered.json" "$OUT/bin/gotogether" validate \
  -replay "$incident_manifest" -json
cp "$OUT/original-incident.jsonl" "$incident_log"
trap - EXIT
rm "$OUT/original-incident.jsonl"
mv "$incident_log" "$OUT/original-incident.jsonl"
trap 'mv "$OUT/original-incident.jsonl" "$incident_log"' EXIT
expect 2 error "$moved/reports/missing.json" "$OUT/bin/gotogether" validate \
  -replay "$incident_manifest" -json
mv "$OUT/original-incident.jsonl" "$incident_log"
trap - EXIT
printf 'Incident rehearsal verified. Native evidence: %s\n' "$moved"
printf 'TUI: gotogether-nav -binding %q -run %q\n' "$incident_binding" "$incident_log"
