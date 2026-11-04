#!/usr/bin/env bash
# Run the frozen consumer outside the checkout, using only published modules.
set -euo pipefail
fixture_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
for tool in go quint python3 sha256sum; do
  command -v "$tool" >/dev/null || { echo "missing required tool: $tool" >&2; exit 2; }
done
(cd "$fixture_dir" && sha256sum -c FREEZE.sha256)
run_dir="${LRU_RUN_DIR:-$(mktemp -d "${TMPDIR:-/tmp}/gotogether-lru-mbt.XXXXXX")}"
mkdir -p "$run_dir"
run_dir="$(cd "$run_dir" && pwd)"
consumer_dir="$run_dir/consumer"
if [ -e "$consumer_dir" ]; then
  echo "consumer destination already exists: $consumer_dir (choose a fresh LRU_RUN_DIR)" >&2
  exit 2
fi
mkdir -p "$consumer_dir/model"
cp "$fixture_dir/go.mod" "$fixture_dir/go.sum" "$fixture_dir/driver_test.go" "$consumer_dir/"
cp "$fixture_dir/model/lru.qnt" "$consumer_dir/model/"
export GOWORK=off GOFLAGS=-mod=readonly QUINT_BACKEND=rust
export LRU_EVIDENCE_DIR="$run_dir/traces"
(cd "$consumer_dir" && go mod edit -json) > "$run_dir/consumer-module.json"
python3 - "$run_dir/consumer-module.json" <<'PY'
import json, sys
with open(sys.argv[1]) as stream:
    module = json.load(stream)
if module.get("Replace"):
    raise SystemExit("baseline consumer must not contain checkout replacements")
if not any(x["Path"] == "github.com/chucklehead-dev/gotogether/cmd/gotogether" for x in module.get("Tool", [])):
    raise SystemExit("register the published CLI as a Go tool before freezing")
PY
(cd "$consumer_dir" && go mod tidy -diff)
(cd "$consumer_dir" && go tool gotogether skills install -dir "$run_dir/guidance") > "$run_dir/skills-install.txt"
(cd "$consumer_dir" && go test -count=1 -v .) | tee "$run_dir/faithful-test.txt"
(cd "$consumer_dir" && go vet .)
(cd "$consumer_dir" && go list -m -json github.com/chucklehead-dev/gotogether github.com/hashicorp/golang-lru/v2) > "$run_dir/dependencies.json"
printf 'faithful consumer and native traces retained: %s\n' "$run_dir"
