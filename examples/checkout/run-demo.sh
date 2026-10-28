#!/usr/bin/env bash
# Simulate the shop under each isolation level, then check the step logs
# against the model with gotogether. MUTANT=noLocks | lockWithoutWrite |
# noIdempotencyKey | dualWrite injects a bug; ISOLATIONS picks levels. A
# mutant is caught when some level's runs fail their check.
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
"" | noLocks | lockWithoutWrite | noIdempotencyKey | dualWrite) ;;
*) echo "unknown MUTANT=$MUTANT (noLocks, lockWithoutWrite, noIdempotencyKey, dualWrite)" >&2; exit 2 ;;
esac
# the binary takes the mutation kebab-cased (noLocks: no-locks)
mutation=$(printf '%s' "${MUTANT:-}" | sed -E 's/([A-Z])/-\1/g' | tr '[:upper:]' '[:lower:]')
out=$(mktemp -d)
go build -o "$out/checkout-sim" ./cmd/checkout-sim
(cd ../.. && go build -o "$out/gotogether" ./cmd/gotogether)
status=0
for iso in ${ISOLATIONS:-serializable snapshot read-committed}; do
  echo "== $iso ${MUTANT:+(mutant $MUTANT)}"
  "$out/checkout-sim" -seeds "${SEEDS:-30}" -isolation "$iso" -mutation "$mutation" -crash-rate "${CRASH_RATE:-0.05}" -out "$out/$iso"
  # one actor per seed: the report lists each run's verdict
  if "$out/gotogether" validate -binding checkout.binding.yaml "$out/$iso"/*.jsonl > "$out/$iso.report"; then
    echo "all runs conform"
  else
    status=1
    # the failing runs' verdicts
    awk '/^actor/{if(b~/FAIL/)print b; b=$0; next}{b=b"\n"$0}END{if(b~/FAIL/)print b}' "$out/$iso.report" |
      grep -E "^actor|FAIL|not a transition|does not match|quint:" | head -24
  fi
done
echo "step logs and reports in $out"
finish $status
