#!/usr/bin/env bash
# Exercise the installed command contract, including broken inputs and repairs.
set -euo pipefail
cd "$(dirname "$0")/.."
for tool in go quint python3; do
  command -v "$tool" >/dev/null || { echo "lint onboarding requires $tool on PATH" >&2; exit 2; }
done
scratch=$(mktemp -d "${TMPDIR:-/tmp}/gotogether-lint-onboarding.XXXXXX")
trap 'rm -rf "$scratch"' EXIT
go build -o "$scratch/gotogether" ./cmd/gotogether
mkdir "$scratch/source" "$scratch/simulation"
cat > "$scratch/source/app.go" <<'GO'
package app
import (
    "context"
    "github.com/chucklehead-dev/gotogether/qobs"
)
func record(ctx context.Context) {
    qobs.Record(ctx, "deposit", nil)
    qobs.Record(ctx, "withdraw", nil)
    qobs.Record(ctx, "transfr", nil)
}
GO
cat > "$scratch/simulation/app.go" <<'GO'
package app
import (
    "time"
    "github.com/chucklehead-dev/gotogether/qenv"
)
var _ = qenv.New
func pause() { time.Sleep(time.Second) }
GO
cat > "$scratch/broken.yaml" <<'YAML'
spec: missing.qnt
module: bank
actions:
  deposit:
    arg: {}
YAML

run_report() {
  local expected="$1" name="$2" status=0
  shift 2
  "$scratch/gotogether" lint -json "$@" > "$scratch/$name.json" || status=$?
  if [ "$status" -ne "$expected" ]; then
    echo "lint onboarding: $name exited $status, expected $expected" >&2
    cat "$scratch/$name.json" >&2
    exit 1
  fi
  python3 - "$scratch/$name.json" "$expected" <<'PY'
import json, sys
with open(sys.argv[1]) as stream:
    report = json.load(stream)
assert report["format"] == "gotogether.lint/1", report
assert report["exitCode"] == int(sys.argv[2]), report
assert report["checks"], report
PY
}

run_report 1 typo -binding examples/bank/bank.binding.yaml -src "$scratch/source"
python3 - "$scratch/typo.json" <<'PY'
import json, sys
report = json.load(open(sys.argv[1]))
findings = [f for f in report["findings"] if f["code"] == "source.action.unknown"]
assert len(findings) == 1, report
finding = findings[0]
assert finding["symbol"] == "transfr", finding
assert finding["path"].endswith("app.go") and finding["line"] == 9 and finding["column"] > 0, finding
assert finding["fix"] and finding["confidence"] == "syntax", finding
assert report["complete"], report
PY
python3 - "$scratch/source/app.go" <<'PY'
from pathlib import Path
import sys
path = Path(sys.argv[1])
path.write_text(path.read_text().replace('"transfr"', '"transfer"'))
PY
run_report 0 repaired -binding examples/bank/bank.binding.yaml -src "$scratch/source"

# An independent simulation scan still runs when a binding's schema is broken.
run_report 1 mixed -binding "$scratch/broken.yaml" -sim "$scratch/simulation"
python3 - "$scratch/mixed.json" <<'PY'
import json, sys
report = json.load(open(sys.argv[1]))
assert not report["complete"], report
assert any(f["code"] == "binding.schema" and f["line"] == 5 for f in report["findings"]), report
assert any(f["code"] == "simulation.wall_clock" and f["line"] == 7 and f["fix"] for f in report["findings"]), report
assert any(c["name"] == "binding.model" and c["status"] == "blocked" and c["reason"] for c in report["checks"]), report
assert any(c["name"] == "simulation.syntax" and c["status"] == "fail" for c in report["checks"]), report
PY
python3 - "$scratch/simulation/app.go" <<'PY'
from pathlib import Path
import sys
path = Path(sys.argv[1])
path.write_text(path.read_text().replace('    "time"\n', '').replace('time.Sleep(time.Second)', ''))
PY
run_report 0 simulation-repaired -sim "$scratch/simulation"

# Tool availability is operational evidence, not an authored model defect.
run_report 2 missing-tool -binding examples/bank/bank.binding.yaml -quint "$scratch/not-installed"
python3 - "$scratch/missing-tool.json" <<'PY'
import json, sys
report = json.load(open(sys.argv[1]))
assert not report["complete"], report
assert any(c["name"] == "binding.model" and c.get("operational") for c in report["checks"]), report
PY
run_report 0 conversion -convert examples/kvstore/kv.convert.yaml
printf 'lint onboarding: source repair, simulation repair, blocked dependencies, tool failure and conversion passed\n'
