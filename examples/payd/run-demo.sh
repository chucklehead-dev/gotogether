#!/usr/bin/env bash
# payd, unmodified, explored in a simulation and run for real on SQLite,
# every run checked against the model. MUTATION= one of precheck,
# no-forward-key, pending-conflict builds payd with that mutant's overlay
# (testdata/mutants/) and shows what catches it; without one, the faithful
# service under every fault at three isolation levels, then for real, then
# cmd/payd answering one key twice with one charge.
set -euo pipefail
cd "$(dirname "$0")"
case "${MUTATION:-}" in
  "") run='TestFaithful$|TestOneFault$|TestReal$' ;;
  precheck) run='TestMutants$/^precheck$' ;;
  no-forward-key) run='TestMutants$/^noForwardKey$' ;;
  pending-conflict) run='TestMutants$/^pendingConflict$' ;;
  *) echo "unknown MUTATION=$MUTATION" >&2; exit 2 ;;
esac
echo "== payd ${MUTATION:+(mutation $MUTATION)}"
GOTOGETHER_CHECK=1 go test -count=1 -timeout 30m -run "$run" -v . | grep -E '^(--- |ok|FAIL)|runs, |charges for|violat'
[ -z "${MUTATION:-}" ] || exit 0

echo "== cmd/payd: one key, twice"
dir=$(mktemp -d)
go build -o "$dir/payd" ./cmd/payd
"$dir/payd" -addr localhost:18080 -db "$dir/payd.db" 2> "$dir/log" &
pid=$!
trap 'kill $pid 2>/dev/null; rm -rf "$dir"' EXIT
for _ in $(seq 50); do curl -s localhost:18080/v1/charges/none > /dev/null && break; sleep 0.1; done
for _ in 1 2; do
  curl -s -X POST localhost:18080/v1/charges -H 'Idempotency-Key: demo-1' -d '{"account": "acct_1", "amount": 250}'
  echo
done
