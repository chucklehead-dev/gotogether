#!/usr/bin/env bash
# Serve the ledger for real over gRPC with injected network faults, export
# the server's otelgrpc spans, and check them against the model with
# gotogether (converted by ledger.convert.yaml). MUTANT=noDedupe |
# noFundsCheck | splitTxn injects a bug; SEEDS picks how many loads.
# The simulated tier (every fault, crash and interleaving a qenv choice) is
# in the tests: go test ./... (DESIGN: examples/grpcledger/README.md).
set -euo pipefail
cd "$(dirname "$0")"
# finish FAILED: the demo's exit status (examples/README.md, "run-demo.sh"):
# 0 when it showed what the README says (every run conforms; under
# MUTANT=, some run fails its check), 1 when it did not
finish() {
  if [ -z "${MUTANT:-}" ]; then
    [ "$1" = 0 ] && exit 0
    echo "not as expected: a run of the faithful system failed its check" >&2
    exit 1
  fi
  if [ "$1" != 0 ]; then echo "mutant $MUTANT caught, as expected"; exit 0; fi
  echo "not as expected: no run caught mutant $MUTANT (every run conforms)" >&2
  exit 1
}
case "${MUTANT:-}" in
"" | noDedupe | noFundsCheck | splitTxn) ;;
*) echo "unknown MUTANT=$MUTANT (noDedupe, noFundsCheck, splitTxn)" >&2; exit 2 ;;
esac
# the binary takes the mutation kebab-cased (noLocks: no-locks)
mutation=$(printf '%s' "${MUTANT:-}" | sed -E 's/([A-Z])/-\1/g' | tr '[:upper:]' '[:lower:]')
out=$(mktemp -d)
go build -o "$out/ledger-real" ./cmd/ledger-real
go build -C ../.. -o "$out/gotogether" ./cmd/gotogether
status=0
for seed in $(seq 1 "${SEEDS:-3}"); do
  echo "== load $seed ${MUTANT:+(mutant $MUTANT)}"
  "$out/ledger-real" -seed "$seed" -mutation "$mutation" -out "$out/spans$seed.json"
  if "$out/gotogether" validate -binding ledger.binding.yaml -convert ledger.convert.yaml "$out/spans$seed.json" > "$out/report$seed"; then
    echo "conforms"
  else
    status=1
    grep -E "^actor|FAIL|not a transition|does not match|quint:|observed:" "$out/report$seed" | head -12
  fi
done
echo "spans and reports in $out"
finish $status
