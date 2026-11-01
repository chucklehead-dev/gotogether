#!/usr/bin/env bash
# Freeze first; the public executor, not a replacement implementation, supplies observations.
set -euo pipefail
root=$(cd "$(dirname "$0")" && pwd)
export GOWORK=off GOTOGETHER_EVAL=quint QUINT_BACKEND=typescript
for tool in go quint gotogether python3 sha256sum; do
  command -v "$tool" >/dev/null || { echo "counted-values rehearsal requires $tool" >&2; exit 2; }
done
(cd "$root" && sha256sum -c FREEZE.sha256)
mkdir -p "$root/logs"
work=$(mktemp -d "$root/logs/run-XXXXXXXX")
project="$work/project"
mkdir -p "$project" "$work/reports" "$work/elsewhere"
python3 - "$root" "$project" <<'PY'
from pathlib import Path
import shutil, sys
source, target = map(Path, sys.argv[1:])
for line in (source / 'FREEZE.sha256').read_text().splitlines():
    name = line.split('  ', 1)[1]
    (target / name).parent.mkdir(parents=True, exist_ok=True)
    shutil.copyfile(source / name, target / name)
shutil.copyfile(source / 'FREEZE.sha256', target / 'FREEZE.sha256')
PY
cd "$project"
report() {
  local mode="$1" expected="$2" label="$3" steps="$4" results="$5" status=0
  shift 5
  gotogether validate -json "$@" > "$work/reports/$label.json" 2> "$work/reports/$label.stderr" || status=$?
  if [ "$status" -ne "$expected" ]; then
    cat "$work/reports/$label.stderr" >&2
    cat "$work/reports/$label.json" >&2
    echo "$label exit $status, expected $expected" >&2
    exit 1
  fi
  python3 "$root/check_evidence.py" "$mode" "$work/reports/$label.json" "$status" "$steps" "$results"
}
go run ./cmd/observe -out faithful.jsonl -results faithful-results.json
report faithful 0 faithful "$project/faithful.jsonl" "$project/faithful-results.json" -itf -binding counted.binding.yaml -dir "$work/faithful-check" -manifest faithful-run.json faithful.jsonl
go run ./cmd/inspect-itf -trace "$work/faithful-check/check-1/observed_counted_allTest.itf.json" -results faithful-results.json > "$work/reports/faithful-itf.json"
gotogether bundle -out "$work/faithful-bundle" faithful-run.json
cp faithful.jsonl faithful-results.json "$work/faithful-bundle/"
# This gate must pass before preparing any source contrast.
report faithful 0 gate-replay "$project/faithful.jsonl" "$project/faithful-results.json" -itf -replay "$work/faithful-bundle/run.json" -dir "$work/gate-replay"
go run ./cmd/inspect-itf -trace "$work/gate-replay/check-1/observed_counted_allTest.itf.json" -results faithful-results.json > "$work/reports/replay-itf.json"
cmp "$work/reports/faithful-itf.json" "$work/reports/replay-itf.json"
dependency=$(go list -m -f '{{.Dir}}' github.com/casselc/goo)
contrast_root=$(mktemp -d "${TMPDIR:-/tmp}/counted-contrast-XXXXXXXX")
trap 'python3 -c "import shutil,sys; shutil.rmtree(sys.argv[1])" "$contrast_root"' EXIT
python3 "$root/make_contrast.py" "$dependency" "$contrast_root/dependency"
mkdir -p "$work/contrast"
cp "$contrast_root/PROVENANCE.json" "$work/contrast/PROVENANCE.json"
cp go.mod contrast.mod
cp go.sum contrast.sum
go mod edit -modfile=contrast.mod -replace="github.com/casselc/goo=$contrast_root/dependency"
go run -modfile=contrast.mod ./cmd/observe -out contrast.jsonl -results contrast-results.json
report contrast 1 contrast "$project/contrast.jsonl" "$project/contrast-results.json" -binding counted.binding.yaml -dir "$work/contrast-check" -manifest contrast-run.json contrast.jsonl
gotogether nav -run contrast.jsonl -record '{"actor":"counted","recorder":"counted","seq":4}' -json > "$work/reports/contrast-record.json"
python3 - "$work/reports/contrast-record.json" <<'PYRECORD'
import json, sys
record = json.load(open(sys.argv[1]))
assert record['reference']['seq'] == 4 and record['action'] == 'lookup'
assert record['observations']['state'] == 'no_data'
assert record['arguments']['total'] == 0
assert json.loads(record['arguments']['groups']) == [18446744073709551615, 1]
PYRECORD
gotogether bundle -out "$work/contrast-bundle" contrast-run.json
cp contrast.jsonl contrast-results.json "$work/contrast-bundle/"
python3 - <<'PY'
import json
from pathlib import Path
rows = [json.loads(s) for s in Path('faithful.jsonl').read_text().splitlines()]
del rows[3]['quint.obs.count']
Path('missing.jsonl').write_text(''.join(json.dumps(r) + '\n' for r in rows))
PY
report missing 2 missing "$project/missing.jsonl" "$project/faithful-results.json" -binding counted.binding.yaml -dir "$work/missing-check" missing.jsonl
sha256sum -c FREEZE.sha256
# Relocation removes every original model/binding/input path.
cd "$work/elsewhere"
mv "$project" "$work/project-hidden"
test ! -e "$project"
for mode in faithful contrast; do
  expected=0
  if [ "$mode" = contrast ]; then expected=1; fi
  report "$mode" "$expected" "$mode-relocated" "$work/$mode-bundle/$mode.jsonl" "$work/$mode-bundle/$mode-results.json" -replay "$work/$mode-bundle/run.json" -dir "$work/$mode-relocated"
done
(cd "$work/project-hidden" && sha256sum -c FREEZE.sha256)
printf 'Counted-values rehearsal passed; fresh evidence: %s\n' "$work"
