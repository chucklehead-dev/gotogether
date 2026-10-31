#!/usr/bin/env bash
# Frozen real-library contract, fresh observations and a parent-planted contrast.
set -euo pipefail
root=$(cd "$(dirname "$0")" && pwd)
export GOWORK=off
for tool in go quint gotogether python3 sha256sum; do
  command -v "$tool" >/dev/null || { echo "Goo Read rehearsal requires $tool on PATH" >&2; exit 2; }
done
(cd "$root" && sha256sum -c FREEZE.sha256)
mkdir -p "$root/logs"
work=$(mktemp -d "$root/logs/run-XXXXXXXX")
project="$work/project"
mkdir -p "$project" "$work/reports" "$work/relocated" "$work/other-cwd"
# Run a disposable copy, so relocation can remove every original input/model
# path without renaming the checked-in frozen artifact or a caller's project.
python3 - "$root" "$project" <<'PY'
from pathlib import Path
import shutil, sys
source, target = map(Path, sys.argv[1:])
for line in (source / 'FREEZE.sha256').read_text().splitlines():
    name = line.split('  ', 1)[1]
    (target / name).parent.mkdir(parents=True, exist_ok=True)
    shutil.copyfile(source / name, target / name)
shutil.copyfile(source / 'FREEZE.sha256', target / 'FREEZE.sha256')
shutil.copytree(source / 'contrast', target / 'contrast', ignore=shutil.ignore_patterns('__pycache__'))
PY
cd "$project"
run_go() {
  local expected="$1" label="$2" status=0
  shift 2
  GOTOGETHER_KEEP="$project/evidence/$label" go test "$@" -run '^TestConforms$' -count=1 -v ./... > "$work/reports/$label-go.txt" 2>&1 || status=$?
  if [ "$status" -ne "$expected" ]; then
    cat "$work/reports/$label-go.txt" >&2
    echo "$label go test exited $status, expected $expected" >&2
    exit 1
  fi
}
report() {
  local mode="$1" expected="$2" label="$3" steps="$4" status=0
  shift 4
  local command="$1"
  shift
  gotogether "$command" -json "$@" > "$work/reports/$label.json" 2> "$work/reports/$label.stderr" || status=$?
  if [ "$status" -ne "$expected" ]; then
    cat "$work/reports/$label.stderr" >&2
    cat "$work/reports/$label.json" >&2
    echo "$label exited $status, expected $expected" >&2
    exit 1
  fi
  python3 "$root/contrast/check_report.py" "$mode" "$work/reports/$label.json" "$status" "$steps"
}
# Flags precede any positional input; Go's flag parser stops at that input.
run_go 0 faithful
faithful="$project/evidence/faithful/TestConforms/steps.jsonl"
report graph 0 graph '' simulate -binding reader.binding.yaml -graph -states 100000 -depth 0
report faithful 0 faithful "$faithful" validate -binding reader.binding.yaml -dir "$project/check-faithful" -manifest "$project/faithful.json" "$faithful"
gotogether bundle -out "$work/relocated/faithful" faithful.json
cp "$faithful" "$work/relocated/faithful/steps.jsonl"
# Never edit GOMODCACHE: Go 1.27 refuses overlays there. A temporary exact
# production-package copy is selected only by an alternate contrast modfile.
dependency=$(go list -m -f '{{.Dir}}' github.com/casselc/goo)
python3 - "$dependency" "$project" <<'PY'
from pathlib import Path
import hashlib, json, shutil, sys
source, project = map(Path, sys.argv[1:])
provenance = json.loads((project / 'contrast/PROVENANCE.json').read_text())
original = source / provenance['file']
assert hashlib.sha256(original.read_bytes()).hexdigest() == provenance['originalSHA256']
contrast = project / 'contrast/model.go.txt'
assert hashlib.sha256(contrast.read_bytes()).hexdigest() == provenance['contrastSHA256']
expected = original.read_bytes().replace(b'dec.KnownFields(true)', b'dec.KnownFields(false)', 1)
assert original.read_bytes().count(b'dec.KnownFields(true)') == 1
assert contrast.read_bytes() == expected
copy = project / 'contrast/dependency'
(copy / 'subjectfacts').mkdir(parents=True)
for name in ('go.mod', 'go.sum'):
    shutil.copyfile(source / name, copy / name)
for file in (source / 'subjectfacts').glob('*.go'):
    if not file.name.endswith('_test.go'):
        shutil.copyfile(file, copy / 'subjectfacts' / file.name)
for name in ('go.mod', 'go.sum'):
    shutil.copyfile(project / name, project / 'contrast' / ('contrast.' + name.split('.')[1]))
(project / 'contrast/overlay.json').write_text(json.dumps({'Replace': {str(copy / 'subjectfacts/model.go'): str(contrast)}}))
PY
go mod edit -modfile=contrast/contrast.mod -replace="github.com/casselc/goo=$project/contrast/dependency"
run_go 1 contrast -modfile=contrast/contrast.mod -overlay contrast/overlay.json
contrast="$project/evidence/contrast/TestConforms/steps.jsonl"
report contrast 1 contrast "$contrast" validate -binding reader.binding.yaml -dir "$project/check-contrast" -manifest "$project/contrast.json" "$contrast"
gotogether bundle -out "$work/relocated/contrast" contrast.json
cp "$contrast" "$work/relocated/contrast/steps.jsonl"
python3 - "$faithful" "$project/missing.jsonl" <<'PY'
import json, sys
records = [json.loads(line) for line in open(sys.argv[1])]
removed = False
for record in records:
    if record.get('quint.action') == 'read' and record.get('quint.arg.caseID') == 0:
        assert 'quint.obs.accepted' in record
        del record['quint.obs.accepted']
        removed = True
assert removed
with open(sys.argv[2], 'w') as output:
    for record in records:
        output.write(json.dumps(record) + '\n')
PY
report missing 2 missing "$project/missing.jsonl" validate -binding reader.binding.yaml -dir "$project/check-missing" "$project/missing.jsonl"
sha256sum -c FREEZE.sha256
# All manifests' original model, binding, input and driver paths disappear.
# Replay reads captured history; it does not execute Goo again.
cd "$work/other-cwd"
mv "$project" "$work/project-hidden"
test ! -e "$project"
for mode in faithful contrast; do
  expected=0
  if [ "$mode" = contrast ]; then expected=1; fi
  report "$mode" "$expected" "$mode-relocated" "$work/relocated/$mode/steps.jsonl" validate -replay "$work/relocated/$mode/run.json" -dir "$work/replay-$mode"
done
(cd "$work/project-hidden" && sha256sum -c FREEZE.sha256)
printf 'Goo Read rehearsal passed; fresh evidence: %s\n' "$work"
