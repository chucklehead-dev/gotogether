#!/usr/bin/env bash
# webjobs: the recorded Bombadil journeys checked against the model (no
# browser needed: a Go client drives the real server too), or with LIVE=1
# Bombadil exploring the app for 45s (bombadil and a Chrome on PATH).
# MUTATION= one of ready-early, double-job, cancel-ignored, cancel-broken,
# status-stale, commit-after-cancel builds the server with that mutant's overlay
# (testdata/mutants/) and shows what catches it; without one, the
# faithful app, then cmd/webjobs answering a submission twice with one
# job.
set -euo pipefail
cd "$(dirname "$0")"
case "${MUTATION:-}" in
  "") run='TestClient$|TestRecorded$|TestProbes$|TestClickWithNoOutcome$|TestTornTrace$|TestTimesKept$' ;;
  ready-early) run='TestMutants$/^readyEarly$' ;;
  double-job) run='TestMutants$/^doubleJob$' ;;
  cancel-ignored) run='TestMutants$/^cancelIgnored$' ;;
  cancel-broken) run='TestMutants$/^cancelBroken$' ;;
  status-stale) run='TestMutants$/^statusStale$' ;;
  commit-after-cancel) run='TestMutants$/^commitAfterCancel$' ;;
  *) echo "unknown MUTATION=$MUTATION" >&2; exit 2 ;;
esac
short=-short
if [ -n "${LIVE:-}" ]; then
  short=
  [ -n "${MUTATION:-}" ] || run='TestLive$'
fi
echo "== webjobs ${MUTATION:+(mutation $MUTATION)}${LIVE:+, live}"
go test $short -count=1 -timeout 20m -run "$run" -v . | grep -E '^(\s*--- |ok|FAIL)|journey:|violat|pending'
[ -z "${MUTATION:-}" ] || exit 0

echo "== cmd/webjobs: one submission, sent twice"
dir=$(mktemp -d)
go build -o "$dir/webjobs" ./cmd/webjobs
"$dir/webjobs" -addr 127.0.0.1:18090 -steps "$dir/server.jsonl" 2> "$dir/log" &
pid=$!
trap 'kill $pid 2>/dev/null; rm -rf "$dir"' EXIT
for _ in $(seq 50); do curl -s 127.0.0.1:18090/ > /dev/null && break; sleep 0.1; done
for _ in 1 2; do
  curl -s -X POST 127.0.0.1:18090/jobs -d '{"key": "demo-1"}'
done
sleep 1.5
curl -s 127.0.0.1:18090/jobs/j1
kill -INT $pid; wait $pid || true
cut -c1-120 "$dir/server.jsonl"
