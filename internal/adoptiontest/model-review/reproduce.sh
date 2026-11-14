#!/usr/bin/env bash
# Review an actual model change with the current checkout's CLI.
set -euo pipefail
fixture_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(cd "$fixture_dir/../../.." && pwd)"
for tool in go quint git python3 sha256sum; do
  command -v "$tool" >/dev/null || { echo "missing required tool: $tool" >&2; exit 2; }
done
(cd "$fixture_dir" && sha256sum -c SOURCE.sha256)
run_dir="${MODEL_REVIEW_RUN_DIR:-$(mktemp -d "${TMPDIR:-/tmp}/gotogether-model-review.XXXXXX")}"
mkdir -p "$run_dir"
run_dir="$(cd "$run_dir" && pwd)"
if [ -e "$run_dir/inputs" ] || [ -e "$run_dir/gotogether" ]; then
  echo "evidence destination already contains a run: $run_dir (choose a fresh MODEL_REVIEW_RUN_DIR)" >&2
  exit 2
fi
mkdir "$run_dir/inputs"
cp -R "$fixture_dir/before" "$fixture_dir/after" "$run_dir/inputs/"
cp "$fixture_dir/SOURCE.sha256" "$run_dir/inputs/"
(cd "$repo_root" && GOWORK=off GOFLAGS=-mod=readonly go build -o "$run_dir/gotogether" ./cmd/gotogether)
go version -m "$run_dir/gotogether" > "$run_dir/cli-build.txt"
git -C "$repo_root" rev-parse HEAD > "$run_dir/checkout-revision.txt"
"$run_dir/gotogether" model-diff \
  -before "$run_dir/inputs/before/hello.binding.yaml" \
  -after "$run_dir/inputs/after/hello.binding.yaml" \
  -observe applied,count -states 100 -pairs 1000 -work 10000 \
  -json -report "junit=$run_dir/report.xml" -report "sarif=$run_dir/report.sarif" \
  > "$run_dir/report.json"
python3 - "$run_dir" <<'PY'
import json, pathlib, sys, xml.etree.ElementTree as ET
root = pathlib.Path(sys.argv[1])
report = json.loads((root / "report.json").read_text())
assert report["format"] == "gotogether.report/2"
assert report["command"] == "model-diff"
assert report["exit"] == 0 and report["verdict"] == "pass"
c = report["comparison"]
assert c["scope"] == "finite_observation_traces"
assert c["basis"] == "complete_go_graph_product"
assert c["status"] == "equivalent" and c["observe"] == ["applied", "count"]
assert c["pairLimit"] == 1000 and c["workLimit"] == 10000
for side, transitions in [("before", 24), ("after", 25)]:
    graph = c[side]
    assert graph["module"] == "hello"
    assert graph["complete"] and not graph.get("cut")
    assert graph["states"] == 8 and graph["starts"] == 1
    assert graph["transitions"] == transitions and graph["depth"] == 3
    assert graph["bound"] == {"states": 100, "depth": 0, "memoryBytes": 256 << 20}
    assert type(graph["memoryUsed"]) is int and 0 < graph["memoryUsed"] <= graph["bound"]["memoryBytes"]
    assert graph["memoryScope"] == "estimated_retained_graph_storage_excluding_compilation_temporary_evaluation_and_process_heap"
    assert graph["constants"] == [{"name": "REQUESTS", "value": "1.to(3)"}]
for name in ["added", "removed"]:
    direction = c[name]
    assert direction["status"] == "included" and not direction.get("witness")
    assert 0 < direction["explored"] <= c["pairLimit"]
    assert 0 < direction["work"] <= c["workLimit"]
# The auxiliary projections must agree; equivalence has two passing directions.
junit = ET.parse(root / "report.xml").getroot()
assert len(list(junit.iter("testcase"))) == 2
assert not list(junit.iter("failure")) and not list(junit.iter("skipped"))
sarif = json.loads((root / "report.sarif").read_text())
assert sarif["version"] == "2.1.0"
assert not sarif["runs"][0]["results"]
print("historical hello: equivalent observed state sequences; named done completion remains a separate obligation")
PY
(cd "$run_dir/inputs" && sha256sum -c SOURCE.sha256)
(cd "$fixture_dir" && sha256sum -c SOURCE.sha256)
printf 'model-review evidence retained: %s\n' "$run_dir"
