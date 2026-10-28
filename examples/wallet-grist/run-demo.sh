#!/usr/bin/env bash
# examples/wallet-grist: examples/wallet's bank on grist, explored in a
# simulation, every run checked against model/wallet.qnt. MUTATION= one of
# no-validation, read-then-write, no-outbox, ledger-no-dedupe or
# replica-read builds the app with that mutant's overlay
# (testdata/mutants/) and shows what catches it; without one, the faithful
# bank through commit faults, crashes and a lossy API and ledger, at its
# races' shapes, through duplicated requests and invalid ones.
#
# SERVE=1 instead builds cmd/wallet (grist.Main), serves the app's HTTP
# API on a SQLite file, drives it with curl (a transfer and its retry
# under the same Idempotency-Key, refusals, a read), stops it and
# validates the steps it recorded with wallet.binding.yaml.
set -euo pipefail
cd "$(dirname "$0")"
if [ -n "${SERVE:-}" ]; then
  dir=$(mktemp -d)
  trap 'kill "${pid:-}" 2>/dev/null || true; rm -rf "$dir"' EXIT
  port=${PORT:-18081}
  go build -o "$dir/wallet" ./cmd/wallet
  "$dir/wallet" serve -db "sqlite:$dir/wallet.db" -addr "127.0.0.1:$port" -tenant t1 \
    -steps "$dir/steps.jsonl" -caller-header X-Caller -poll 50ms &
  pid=$!
  api="http://127.0.0.1:$port/t/t1"
  for _ in $(seq 100); do curl -fs "$api/queries/read?account=a" -H 'X-Caller: demo' >/dev/null && break; sleep 0.1; done
  post() { # post COMMAND KEY BODY: the answer and the status
    curl -s -w ' %{http_code}\n' -X POST "$api/commands/$1" -H 'X-Caller: demo' -H "Idempotency-Key: $2" -d "$3"; }
  echo "== transfer 3 from c to a (k1), then k1 again: the same receipt"
  post transfer k1 '{"src":"c","dest":"a","amount":3}'
  post transfer k1 '{"src":"c","dest":"a","amount":3}'
  echo "== transfer 3 more from c (k2): 422 insufficient-funds; to its own source (k3): 422 invalid"
  post transfer k2 '{"src":"c","dest":"b","amount":3}'
  post transfer k3 '{"src":"a","dest":"a","amount":1}'
  echo "== a deposit (k4), then read a"
  post deposit k4 '{"account":"a","amount":2}'
  curl -s -w ' %{http_code}\n' "$api/queries/read?account=a" -H 'X-Caller: demo'
  sleep 0.5 # the ledger's posts and their checkpoints
  kill -INT "$pid"; wait "$pid" || true; pid=
  echo "== the steps the server recorded, validated with wallet.binding.yaml"
  (cd ../.. && go run ./cmd/gotogether validate -binding examples/wallet-grist/wallet.binding.yaml "$dir/steps.jsonl")
  exit
fi
case "${MUTATION:-}" in
  "") run='TestFaithful$|TestRaces$|TestDuplicateRequests$|TestInvalidRequests$|TestPostedOnce$' ;;
  no-validation) run='TestMutants$/^noValidation$' ;;
  read-then-write) run='TestMutants$/^readThenWrite$' ;;
  no-outbox) run='TestMutants$/^noOutbox$' ;;
  ledger-no-dedupe) run='TestMutants$/^ledgerNoDedupe$' ;;
  replica-read) run='TestMutants$/^replicaRead$' ;;
  *) echo "unknown MUTATION=$MUTATION" >&2; exit 2 ;;
esac
echo "== wallet-grist ${MUTATION:+(mutation $MUTATION)}"
GOTOGETHER_CHECK=1 go test -short -count=1 -timeout 30m -run "$run" -v . | grep -E '^(--- |ok|FAIL)|runs, |as expected'
