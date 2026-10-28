#!/usr/bin/env bash
# examples/counters: grain's counter example on grist, explored in a
# simulation, every run checked against model/counters.qnt. MUTANT=
# blind or dispatchDirect builds the app with that mutant's overlay
# (testdata/mutants/) and shows what catches it; without one, the
# faithful app through commit faults and crashes at two isolation
# levels, at claim 1's configuration, through duplicated and late
# requests, and the welcome's duplicate after a crash (claims 2 and 3 of
# docs/grist.md §8.1, and claim 1's contrast; MUTANT=blind is claim 1).
#
# SERVE=1 instead builds cmd/counters (grist.Main), serves the app's HTTP
# API on a SQLite file, drives it with curl (a command, its retry under
# the same Idempotency-Key, a refusal, a reused key, a query), stops it
# (the server drains, the node quiets and serve records a settle) and
# validates the steps it recorded with counters.binding.yaml, its
# eventual vals judged on the settle; a retirement's payout goes to the
# bank, served beside the node behind grist.ServeCalls (its payments
# recorded into the same file under a recorder of their own).
#
# ROLLING=1 runs GR10 slice 2's rolling restart under load: simulated
# (gristsim.Cluster: nodes drained in turn behind a router whose view
# lags) and on three real pods of the binary behind a router polling
# /readyz; no client's try refused, no payout unknown, /metrics' lag at 0
# before each settle. MUTANT=noDrainDelay or stopNodeAtSignal
# shows what catches its mutants.
#
# SIZING=1 runs GR10 slice 6's sizing: builds cmd/counters and its sizing
# binary, cmd/counters-sizing, side by side, and runs `counters sizing`
# (grist.Main finds counters-sizing beside it) on the checked-in
# recording (testdata/sizing/steps.jsonl: 400 calls on the real tier),
# with the configuration mutant postRetries3 in force (a Gateway's three
# tries inside the client's three attempts on commands) at 60/s and a
# 3x30s burst: the chain ranks the retry layers, the simulation confirms
# (SEEDS=, default 6; -1 the chain alone), and sizing.yaml says why for
# every value.
set -euo pipefail
cd "$(dirname "$0")"
if [ -n "${SIZING:-}" ]; then
  dir=$(mktemp -d)
  trap 'rm -rf "$dir"' EXIT
  go build -o "$dir/counters" ./cmd/counters
  go build -o "$dir/counters-sizing" ./cmd/counters-sizing
  echo "== counters sizing, postRetries3 in force (gateway 3 x client 3 on commands), 60/s, a 3x30s burst"
  "$dir/counters" sizing -steps testdata/sizing/steps.jsonl -rate 60/s -burst 3x30s \
    -gateway-post 3 -client-attempts 3 -seeds "${SEEDS:-6}" -out "$dir/deploy/sizing.yaml"
  echo "== deploy/sizing.yaml"
  cat "$dir/deploy/sizing.yaml"
  exit
fi
if [ -n "${SERVE:-}" ]; then
  dir=$(mktemp -d)
  trap 'kill "${pid:-}" 2>/dev/null || true; rm -rf "$dir"' EXIT
  port=${PORT:-18080}
  go build -o "$dir/counters" ./cmd/counters
  "$dir/counters" serve -db "sqlite:$dir/counters.db" -addr "127.0.0.1:$port" -tenant t1 \
    -steps "$dir/steps.jsonl" -caller-header X-Caller -poll 50ms &
  pid=$!
  api="http://127.0.0.1:$port/t/t1"
  for _ in $(seq 100); do curl -fs "$api/queries/show-counter?counter=a" -H 'X-Caller: demo' >/dev/null && break; sleep 0.1; done
  post() { # post COMMAND KEY BODY: the status and the answer
    curl -s -w ' %{http_code}\n' -X POST "$api/commands/$1" -H 'X-Caller: demo' -H "Idempotency-Key: $2" -d "$3"; }
  echo "== create a under the name x (k1), then k1 again: the same receipt"
  post create-counter k1 '{"counter":"a","name":"x"}'
  post create-counter k1 '{"counter":"a","name":"x"}'
  echo "== create b under x (k2): 422 name-taken; k1 with other arguments: 422 idempotency-key-reused"
  post create-counter k2 '{"counter":"b","name":"x"}'
  post create-counter k1 '{"counter":"b","name":"y"}'
  echo "== increment a by 2 (k3), then show it"
  post increment-counter k3 '{"counter":"a","by":2}'
  curl -s -w ' %{http_code}\n' "$api/queries/show-counter?counter=a" -H 'X-Caller: demo'
  echo "== retire a (k5): the payout pays it through the bank, over HTTP on its loopback port"
  post retire-counter k5 '{"counter":"a"}'
  echo "== without a caller: 403"
  curl -s -w ' %{http_code}\n' -X POST "$api/commands/increment-counter" -H 'Idempotency-Key: k4' -d '{"counter":"a","by":1}'
  kill -INT "$pid"; wait "$pid" || true; pid=
  echo "== the steps the server recorded, validated with counters.binding.yaml"
  (cd ../.. && go run ./cmd/gotogether validate -binding examples/counters/counters.binding.yaml "$dir/steps.jsonl")
  exit
fi
if [ -n "${ROLLING:-}" ]; then
  echo "== counters: a rolling restart under load (simulated, then three real pods)"
  go test -count=1 -timeout 30m -run 'TestRollingRestart$|TestRealRollingRestart$' -v . | grep -E '^(--- |ok|FAIL)|runs, |restarted in turn'
  exit
fi
if [ -n "${MUTANT:-}" ]; then
  # a directory of testdata/mutants (its overlay), or a rule dropped (drop-<rule>, generated): TestMutants builds it and states what catches it
  [ -d "testdata/mutants/$MUTANT" ] || [[ $MUTANT == drop-* ]] || { echo "unknown MUTANT=$MUTANT (testdata/mutants has $(ls testdata/mutants | tr '\n' ' '))" >&2; exit 2; }
  run="TestMutants\$/^$MUTANT\$"
else
  run='TestFaithful$|TestFaithfulAtClaim1$|TestDuplicateRequests$|TestWelcomeDuplicate$'
fi
echo "== counters ${MUTANT:+(mutant $MUTANT)}"
GOTOGETHER_CHECK=1 go test -short -count=1 -timeout 30m -run "$run" -v . | grep -E '^(--- |ok|FAIL)|runs, |as expected'
