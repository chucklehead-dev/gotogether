#!/usr/bin/env bash
# Prerequisites: Go matching go.mod; Python3, quint and gotogether on PATH.
# CLI: go install github.com/chucklehead-dev/gotogether/cmd/gotogether@v0.0.0-20261030211344-50b59b7cdb62
set -eu
cd "$(dirname "$0")"
export GOWORK=off GOFLAGS=-mod=mod
out="logs/reproduction/run-$(date -u +%Y%m%dT%H%M%S)-$$"
mkdir -p "$out"
# Each invocation owns its reports, fresh manifests, and generated artifacts.
python3 - "$out/overlay.json" <<'PY'
import json,pathlib,sys
root=pathlib.Path.cwd()
json.dump({'Replace': {str(root/'service/charge.go'):str(root/'contrast/charge.go.txt')}},open(sys.argv[1],'w'))
PY
expect_exit() {
 expected="$1"; destination="$2"; shift 2
 status=0
 "$@" > "$destination" 2> "$destination.stderr" || status=$?
 if [ "$status" -ne "$expected" ]; then
  printf 'Expected exit %s, got %s: %s\n' "$expected" "$status" "$*" >&2
  cat "$destination.stderr" >&2
  exit 1
 fi
}
check_report() {
 python3 contrast/check_report.py "$1" "$2"
}
go test -count=1 -v -run '^TestRecord$' ./... > "$out/baseline-test.txt" 2>&1
cp logs/faithful.jsonl "$out/baseline.jsonl"
cp logs/prefix.jsonl "$out/prefix.jsonl"
quint test model/payments.qnt > "$out/quint.txt"
gotogether simulate -graph -binding payments.binding.yaml > "$out/model.txt"
expect_exit 0 "$out/baseline.json" gotogether validate -json -binding payments.binding.yaml -require-finish -dir "$out/artifacts/baseline" "$out/baseline.jsonl"
check_report pass "$out/baseline.json"
expect_exit 0 "$out/prefix.json" gotogether validate -json -binding payments.binding.yaml -dir "$out/artifacts/prefix" "$out/prefix.jsonl"
check_report prefix "$out/prefix.json"
expect_exit 1 "$out/prefix-finish.json" gotogether validate -json -binding payments.binding.yaml -require-finish -dir "$out/artifacts/prefix-finish" "$out/prefix.jsonl"
check_report incomplete "$out/prefix-finish.json"
python3 - "$out/baseline.jsonl" "$out/missing-observation.jsonl" <<'PY'
import json,sys
rows=[json.loads(line) for line in open(sys.argv[1])]
changed=0
for row in rows:
 if row.get('quint.action')=='finish':
  del row['quint.obs.balance'];changed+=1
assert changed==1
with open(sys.argv[2],'w') as f:
 for row in rows:f.write(json.dumps(row)+'\n')
PY
expect_exit 2 "$out/missing-observation.json" gotogether validate -json -binding payments.binding.yaml -dir "$out/artifacts/missing" "$out/missing-observation.jsonl"
check_report missing "$out/missing-observation.json"
GOFLAGS="-mod=mod -overlay=$out/overlay.json" go test -count=1 -v -run '^TestRecord$' ./... > "$out/contrast-test.txt" 2>&1
cp logs/faithful.jsonl "$out/contrast.jsonl"
expect_exit 1 "$out/contrast.json" gotogether validate -json -binding payments.binding.yaml -manifest "$out/manifest.json" -dir "$out/artifacts/contrast" "$out/contrast.jsonl"
check_report mismatch "$out/contrast.json"
expect_exit 1 "$out/replay.json" gotogether validate -json -replay "$out/manifest.json" -dir "$out/artifacts/replay"
check_report mismatch "$out/replay.json"
ONBOARDING_OVERLAY="$out/overlay.json" gotogether shrink -binding payments.binding.yaml -actions contrast/actions.json -out "$out/shrunk" -dir "$out/artifacts/shrink" -baseline 1 -reruns 1 -budget 12 -- ./contrast/rerun.sh '{actions}' '{steps}' > "$out/shrink.txt" 2>&1
expect_exit 1 "$out/shrunk-replay.json" gotogether validate -json -replay "$out/shrunk/manifest.json" -dir "$out/artifacts/shrunk-replay"
check_report mismatch "$out/shrunk-replay.json"
python3 - "$out/shrunk/actions.json" <<'PY'
import json,sys
assert json.load(open(sys.argv[1])) == ['charge']
PY
sha256sum -c FREEZE.sha256 > "$out/freeze.txt"
sha256sum -c contrast/PROVENANCE.sha256 > "$out/contrast-provenance.txt"
printf 'Verified baseline, pending prefix, completion requirement, missing evidence, source mismatch, replay and shrunk replay: %s\n' "$out"
