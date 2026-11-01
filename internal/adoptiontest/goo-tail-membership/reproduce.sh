#!/usr/bin/env bash
# Frozen model/observer, actual library returns, then one isolated source contrast.
set -euo pipefail
root=$(cd "$(dirname "$0")" && pwd)
export GOWORK=off GOTOGETHER_EVAL=quint QUINT_BACKEND=rust
for tool in go quint gotogether python3 sha256sum; do
  command -v "$tool" >/dev/null || { echo "tail-membership rehearsal requires $tool" >&2; exit 2; }
done
(cd "$root" && sha256sum -c FREEZE.sha256)
python3 -m unittest discover -s "$root" -p 'check_evidence_test.py'
mkdir -p "$root/logs"
work=$(mktemp -d "$root/logs/run-XXXXXXXX")
project="$work/project"
mkdir -p "$project" "$work/reports" "$work/elsewhere"
python3 - "$root" "$project" <<'PYCOPY'
from pathlib import Path
import shutil, sys
source, target = map(Path, sys.argv[1:])
for line in (source / 'FREEZE.sha256').read_text().splitlines():
    name = line.split('  ', 1)[1]
    (target / name).parent.mkdir(parents=True, exist_ok=True)
    shutil.copyfile(source / name, target / name)
shutil.copyfile(source / 'FREEZE.sha256', target / 'FREEZE.sha256')
PYCOPY
cd "$project"
report() {
  local mode="$1" expected="$2" label="$3" steps="$4" native="$5" status=0
  shift 5
  gotogether validate -json "$@" > "$work/reports/$label.json" 2> "$work/reports/$label.stderr" || status=$?
  if [ "$status" -ne "$expected" ]; then
    cat "$work/reports/$label.stderr" "$work/reports/$label.json" >&2
    echo "$label exit $status, expected $expected" >&2
    exit 1
  fi
  python3 "$root/check_evidence.py" "$mode" "$work/reports/$label.json" "$status" "$steps" "$native" "$project/testdata/cases.json"
}
go test ./...
quint test model/membership.qnt > "$work/reports/model-tests.txt"
go run ./cmd/observe -out faithful.jsonl -results faithful-native.json
report faithful 0 faithful "$project/faithful.jsonl" "$project/faithful-native.json" -binding membership.binding.yaml -dir "$work/faithful-check" -manifest faithful-run.json faithful.jsonl
gotogether bundle -out "$work/faithful-bundle" faithful-run.json
cp faithful-native.json "$work/faithful-bundle/"
report faithful 0 gate-replay "$project/faithful.jsonl" "$project/faithful-native.json" -replay "$work/faithful-bundle/run.json" -dir "$work/gate-replay"
# A passing native/transport/replay gate precedes creating any changed source.
dependency=$(go list -m -f '{{.Dir}}' github.com/casselc/goo)
contrast_root=$(mktemp -d /tmp/tail-membership-contrast-XXXXXXXX)
trap 'python3 -c "import shutil,sys; shutil.rmtree(sys.argv[1])" "$contrast_root"' EXIT
python3 "$root/make_contrast.py" "$dependency" "$contrast_root/dependency"
cp "$contrast_root/PROVENANCE.json" "$work/reports/PROVENANCE.json"
cp go.mod contrast.mod
cp go.sum contrast.sum
go mod edit -modfile=contrast.mod -replace="github.com/casselc/goo=$contrast_root/dependency"
go run -modfile=contrast.mod ./cmd/observe -out contrast.jsonl -results contrast-native.json
report contrast 1 contrast "$project/contrast.jsonl" "$project/contrast-native.json" -binding membership.binding.yaml -dir "$work/contrast-check" -manifest contrast-run.json contrast.jsonl
gotogether bundle -out "$work/contrast-bundle" contrast-run.json
cp contrast-native.json "$work/contrast-bundle/"
python3 - <<'PYMISSING'
import json
from pathlib import Path
rows = [json.loads(line) for line in Path('faithful.jsonl').read_text().splitlines()]
row = next(r for r in rows if r.get('quint.ref') == 'case:before-mutation')
del row['quint.obs.members']
Path('missing.jsonl').write_text('\n'.join(json.dumps(r) for r in rows) + '\n')
PYMISSING
report missing 2 missing "$project/missing.jsonl" "$project/faithful-native.json" -binding membership.binding.yaml -dir "$work/missing-check" missing.jsonl
# Remove every original input/model location and replay from another working dir.
mv "$project" "$work/project-hidden"
project="$work/project-hidden"
cd "$work/elsewhere"
for mode in faithful contrast; do
  expected=0
  if [ "$mode" = contrast ]; then expected=1; fi
  report "$mode" "$expected" "$mode-relocated" "$work/$mode-bundle/files/inputs/$mode.jsonl" "$work/$mode-bundle/$mode-native.json" -replay "$work/$mode-bundle/run.json" -dir "$work/$mode-relocated"
done
(cd "$project" && sha256sum -c FREEZE.sha256)
printf 'Tail-membership rehearsal passed; fresh evidence: %s\n' "$work"
