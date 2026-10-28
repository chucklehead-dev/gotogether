#!/usr/bin/env bash
# webjobs: the recorded Bombadil journeys checked against the model (no
# browser needed: a Go client drives the real server too), or with LIVE=1
# Bombadil exploring the app for 45s (bombadil and a Chrome on PATH).
# MUTANT= one of readyEarly, doubleJob, cancelIgnored, cancelBroken,
# statusStale, commitAfterCancel, rerunFinished builds the server with that mutant's overlay
# (testdata/mutants/) and shows what catches it; without one, the
# faithful app, then cmd/webjobs answering a submission twice with one
# job.
set -euo pipefail
cd "$(dirname "$0")"
if [ -n "${MUTANT:-}" ]; then
  # a directory of testdata/mutants (its overlay): TestMutants builds it and states what catches it
  [ -d "testdata/mutants/$MUTANT" ] || { echo "unknown MUTANT=$MUTANT (testdata/mutants has $(ls testdata/mutants | tr '\n' ' '))" >&2; exit 2; }
  run="TestMutants\$/^$MUTANT\$"
else
  run='TestClient$|TestRecorded$|TestProbes$|TestClickWithNoOutcome$|TestTornTrace$|TestTimesKept$'
fi
short=-short
if [ -n "${LIVE:-}" ]; then
  short=
  [ -n "${MUTANT:-}" ] || run='TestLive$'
fi
echo "== webjobs ${MUTANT:+(mutant $MUTANT)}${LIVE:+, live}"
go test $short -count=1 -timeout 20m -run "$run" -v . | grep -E '^(\s*--- |ok|FAIL)|journey:|violat|pending'
[ -z "${MUTANT:-}" ] || exit 0

echo "== cmd/webjobs: one submission, sent twice"
dir=$(mktemp -d)
go build -o "$dir/webjobs" ./cmd/webjobs
"$dir/webjobs" -addr 127.0.0.1:18090 -steps "$dir/server.jsonl" 2> "$dir/log" &
pid=$!
trap 'kill $pid 2>/dev/null; rm -rf "$dir"' EXIT
for _ in $(seq 50); do curl -s 127.0.0.1:18090/ > /dev/null && break; sleep 0.1; done
for _ in 1 2; do
  answer=$(curl -s -X POST 127.0.0.1:18090/jobs -d '{"key": "demo-1"}')
  echo "$answer"
done
id=$(echo "$answer" | sed 's/.*"id":"\([^"]*\)".*/\1/')
sleep 1.5
curl -s "127.0.0.1:18090/jobs/$id"
kill -INT $pid; wait $pid || true
cut -c1-120 "$dir/server.jsonl"
