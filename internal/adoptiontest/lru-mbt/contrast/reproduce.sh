#!/usr/bin/env bash
# Replay one frozen native trace against the faithful module and an isolated source contrast.
set -euo pipefail
fixture_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
if [ "$#" -ne 1 ]; then
  echo "usage: $0 <artifact-directory-from-baseline-reproduce.sh>" >&2
  exit 2
fi
run_dir="$(cd -- "$1" && pwd)"
(cd "$fixture_dir" && sha256sum -c FREEZE.sha256)
export GOWORK=off GOFLAGS=-mod=readonly
trace="$run_dir/traces/TestForcedLRU/mbtGetPromotesTest/test_0.itf.json"
test -f "$trace"
probe="$run_dir/replay-probe"
if [ -e "$probe" ]; then
  echo "replay probe already exists: $probe (use a fresh baseline run)" >&2
  exit 2
fi
mkdir -p "$probe"
cp "$run_dir/consumer/go.mod" "$run_dir/consumer/go.sum" "$run_dir/consumer/driver_test.go" "$probe/"
cp "$fixture_dir/contrast/replay_probe_test.go.tmpl" "$probe/replay_probe_test.go"
export LRU_CAPTURED_TRACE="$trace"
export LRU_REPLAY_EXPECT=pass LRU_REPLAY_RESULT="$run_dir/faithful-replay.json"
(cd "$probe" && go test -count=1 -run '^TestCapturedReplay$' -v .) | tee "$run_dir/faithful-replay.txt"
library=$(cd "$probe" && go list -m -f '{{.Dir}}' github.com/hashicorp/golang-lru/v2)
changed="$run_dir/contrast-library"
python3 - "$library" "$changed" "$run_dir" "$trace" <<'PY'
import hashlib, json, pathlib, shutil, sys
original, copied, evidence, trace = map(pathlib.Path, sys.argv[1:])
shutil.copytree(original, copied)
relative = pathlib.Path('simplelru/lru.go')
source = (original / relative).read_bytes()
text = source.decode()
start = text.index('func (c *LRU[K, V]) Get(')
end = text.index('\nfunc ', start + 1)
method = text[start:end]
promotion = '\t\tc.evictList.MoveToFront(ent)\n'
assert method.count(promotion) == 1, 'expected exactly one Get hit promotion'
changed_text = text[:start] + method.replace(promotion, '', 1) + text[end:]
target = copied / relative
target.chmod(0o644)
target.write_text(changed_text)
sha = lambda b: hashlib.sha256(b).hexdigest()
record = {
  'library': 'github.com/hashicorp/golang-lru/v2@v2.0.7',
  'changedFiles': [relative.as_posix()],
  'change': 'omit Get hit recency promotion; Add and Peek remain unchanged',
  'removedLine': promotion.strip(),
  'originalSourceSha256': sha(source),
  'contrastSourceSha256': sha(target.read_bytes()),
  'capturedTraceSha256': sha(trace.read_bytes()),
}
(evidence / 'contrast-provenance.json').write_text(json.dumps(record, indent=2) + '\n')
PY
altmod="$run_dir/contrast.mod"
cp "$probe/go.mod" "$altmod"
cp "$probe/go.sum" "$run_dir/contrast.sum"
(cd "$probe" && go mod edit -modfile "$altmod" -replace "github.com/hashicorp/golang-lru/v2=$changed")
export LRU_REPLAY_EXPECT=state LRU_REPLAY_RESULT="$run_dir/contrast-replay.json"
(cd "$probe" && go test -modfile "$altmod" -count=1 -run '^TestCapturedReplay$' -v .) | tee "$run_dir/contrast-replay.txt"
python3 - "$fixture_dir" "$library" "$run_dir" "$trace" <<'PY'
import hashlib, json, pathlib, sys
fixture, library, evidence, trace = map(pathlib.Path, sys.argv[1:])
sha = lambda p: hashlib.sha256(p.read_bytes()).hexdigest()
record = json.loads((evidence / 'contrast-provenance.json').read_text())
assert sha(library / 'simplelru/lru.go') == record['originalSourceSha256'], 'module cache changed'
assert sha(trace) == record['capturedTraceSha256'], 'captured trace changed'
for line in (fixture / 'FREEZE.sha256').read_text().splitlines():
    digest, name = line.split('  ', 1)
    assert sha(fixture / name) == digest, name
    consumer_file = evidence / 'consumer' / name
    if consumer_file.is_file():
        assert sha(consumer_file) == digest, f'baseline consumer changed: {name}'
result = json.loads((evidence / 'contrast-replay.json').read_text())
assert result['verdict'] == 'state_mismatch' and result['kind'] == 'state'
assert result['action'] == 'Get' and result['step'] == 3
PY
cat "$run_dir/contrast-replay.json"
printf 'isolated Get contrast rejected; typed evidence retained: %s\n' "$run_dir/contrast-replay.json"
