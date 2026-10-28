# Validating production at volume

*About 20 minutes. You will: judge a day of a legacy service's
production log against its model and find a bug its tests miss; make a
day of 10⁴ steps and see why judging it whole does not scale; judge it
charge by charge (`partition:`), every failing charge reported; then
compile the model once and judge the day with no quint or Node at all
(`-compiled`).*

The service is [examples/migrate-payments](../../examples/migrate-payments):
a legacy payment handler (authorize with idempotency keys, partial
capture, refunds, voids) with 12 passing tests, a production audit log,
and no model until the migration wrote one. Its README tells the
migration in stages; this tutorial takes its stage 4 ("the first
verdicts, before any change") to production volume.

Outputs are pasted as they came, but for paths shortened and long lines
cut (`[...]`). It assumes the [first tutorial](prove-it.md)'s setup.
Here the CLI is a tool of the example's module (`go get -tool`), so
`go tool gotogether` runs the version the module requires.

## 1. A day of production, judged

The handler writes an audit record per request. The migration's one
adapter, `auditsteps`, reads them as steps (an operation's record as
its model action, its result as the outcome):

```console
$ cd examples/migrate-payments
$ go run ./cmd/auditsteps -out /tmp/oct02.jsonl testdata/logs/payments-2026-10-02.jsonl && go tool gotogether validate -binding payments.binding.yaml /tmp/oct02.jsonl
15 steps
input /tmp/oct02.jsonl: native step log
[...]
actor payments: 15 steps
  conformance: PASS
  invariant refundsWithinCapture: FAIL
    violated after record /tmp/oct02.jsonl:12
      record:   /tmp/oct02.jsonl:12, 2026-10-02T09:00:31.37Z, process payments-1
      observed: refund(amount=500, id=ch_3)
      quint:    refund("ch_3", 500, "ok")
      the step changed, in the model's state:
        charges["ch_3"].refunded: 700 -> 1200
      model state after the step: 2 variables (-v prints it)
  invariant captureWithinAuthorization: PASS
  invariant oneChargePerKey: PASS
  invariant voidedNothingCaptured: PASS
  [...]
RESULT: FAIL (1 of 1 actor: 1 violates refundsWithinCapture)
```

Every step is one the model allows (`conformance: PASS`), and requirement
R5, "refunds never exceed the capture", fails after the log's line 12:
`ch_3` refunded 700, then 500, of 1000 (the state diff: 700 -> 1200). The handler compared each refund with
the capture and forgot the earlier ones. No legacy test refunds twice,
so all 12 pass; the log is what caught it. (The README's stage 6 fixes
it; the legacy code is kept as the mutant `forgetsPriorRefunds`.)

## 2. A day at volume

That log is 15 steps. A real day is 10⁴ or 10⁵. Make one: 3,334
charges, each authorized, captured (600 of 1000) and refunded in part,
and one, `ch_2000`, refunded 900. Save this as `day.sh`:

```sh
# a synthetic day: n charges, each authorized, captured (600 of 1000) and
# refunded in part (300); charge 2000's refund is 900, past its capture
n=${1:-3334}
seq=0
rec() { seq=$((seq+1)); printf '{"time":"2026-10-02T09:00:00Z","level":"INFO","msg":"%s","audit_seq":%d,"instance":"payments-1",%s}\n' "$1" "$seq" "$2"; }
for i in $(seq 0 $((n-1))); do
  rec charge.authorize "\"charge_id\":\"ch_$i\",\"result\":\"ok\",\"idempotency_key\":\"ord-$i\",\"amount\":1000"
  rec charge.capture "\"charge_id\":\"ch_$i\",\"result\":\"ok\",\"amount\":600"
  if [ "$i" = 2000 ]; then r=900; else r=300; fi
  rec charge.refund "\"charge_id\":\"ch_$i\",\"result\":\"ok\",\"amount\":$r,\"refunded\":$r"
done
```

```console
$ bash day.sh > /tmp/audit.jsonl && wc -l /tmp/audit.jsonl && go run ./cmd/auditsteps -out /tmp/day.jsonl /tmp/audit.jsonl
10002 /tmp/audit.jsonl
10002 steps
```

Judged whole, a log costs the sum of the states it passes through:
after every step, every invariant over every charge so far. Time the
first 600 steps, then the first 3000:

```console
$ head -n 600 /tmp/day.jsonl > /tmp/day600.jsonl && time go tool gotogether validate -binding payments.binding.yaml /tmp/day600.jsonl | tail -1; head -n 3000 /tmp/day.jsonl > /tmp/day3000.jsonl && time go tool gotogether validate -binding payments.binding.yaml /tmp/day3000.jsonl | tail -1
RESULT: PASS (1 actor conforms to the model and satisfies its 4 invariants)

real	0m2.351s
[...]
RESULT: PASS (1 actor conforms to the model and satisfies its 4 invariants)

real	0m46.928s
[...]
```

Five times the steps, twenty times the time: quadratic. The whole day
would take minutes, a 10⁵-step day hours, and judged whole a log stops
at its first failure, hiding every later one.

## 3. Charge by charge: `partition:`

The charges are independent: no requirement relates one charge to
another. A binding that says so judges each charge's steps alone, in
parallel. Add one line to a copy of the binding:

```console
$ sed 's/^module: payments$/module: payments\npartition: id/' payments.binding.yaml > payments.day.binding.yaml && time go tool gotogether validate -binding payments.day.binding.yaml /tmp/day.jsonl
input /tmp/day.jsonl: native step log
[...]
actor payments, partitioned by id: 3333 of 3334 partitions pass (9999 steps), each judged alone; the model checked to factor by id on this run
actor payments id=ch_2000: 3 steps
  conformance: PASS
  invariant refundsWithinCapture: FAIL
    violated after record /tmp/day.jsonl:6004
      record:   /tmp/day.jsonl:6004, 2026-10-02T09:00:00Z, process payments-1
      observed: refund(amount=900, id=ch_2000)
      quint:    refund("ch_2000", 900, "ok")
      the step changed, in the model's state:
        charges["ch_2000"].refunded: 0 -> 900
      model state after the step: 2 variables (-v prints it)
  invariant captureWithinAuthorization: PASS
  invariant oneChargePerKey: PASS
  invariant voidedNothingCaptured: PASS
  instance: AMOUNTS = Set(1000, 300, 600), IDS = Set("ch_0"), KEYS = Set("ord-0"), (AMOUNTS, IDS, KEYS read by no step or val of this run: every partition replayed with the first one's)
  [...]
RESULT: FAIL (1 of 3334 actors: 1 violates refundsWithinCapture)

real	0m6.093s
[...]
```

The whole day in about 6 seconds, quint's compile of the model
included, and the failing charge reported with its own three steps and
the line of the log (`day.jsonl:6004`). Every failing charge is
reported, not only the first (`TestProductionDayAtVolume` plants two).

"The model checked to factor by id on this run" is not taken on trust.
A partition is sound only if no step touches another charge's state.
validate checks it on every run: R2's lookup of a key's charge goes
through an index (`byKey`), not a scan of every charge, so it factors. A
run where a step touches another charge's entries (the mutant
`replayCreatesCharge`: one key, two charges) is refused, naming the step
and the entry: judge that log whole. See [docs/DESIGN.md](../DESIGN.md)
§66 for the check.

## 4. Compiled: no quint, no Node

A pipeline that only validates (a canary, a CronJob over yesterday's
logs, an incident laptop) should not need Node and quint for a passing
run. Compile the model once:

```console
$ go tool gotogether compile -binding payments.day.binding.yaml -o payments.model.json
compiled payments.day.binding.yaml's model payments (quint 0.33.0, 1 sources) to payments.model.json: gotogether validate -binding payments.day.binding.yaml -compiled payments.model.json FILES judges runs with no quint
```

and judge the day with a PATH that has neither quint nor node on it:

```console
$ time env PATH="$(go env GOROOT)/bin:/usr/bin:/bin" go tool gotogether validate -binding payments.day.binding.yaml -compiled payments.model.json /tmp/day.jsonl
input /tmp/day.jsonl: native step log
[...]
issue: info compiled: judged by the Go evaluator alone, by the compiled model payments.model.json (compiled with quint 0.33.0): no quint process, and no sample of its passes judged by quint
actor payments, partitioned by id: 3333 of 3334 partitions pass (9999 steps), each judged alone; the model checked to factor by id on this run
actor payments id=ch_2000: 3 steps
  conformance: PASS
  invariant refundsWithinCapture: FAIL
  [...]
  judged by the Go evaluator alone, by a compiled model (validate -compiled): no quint replay, none to sample it against
  [...]
RESULT: FAIL (1 of 3334 actors: 1 violates refundsWithinCapture)

real	0m1.379s
[...]
```

1.4 seconds for 10⁴ steps, the same verdict. What it gives up, the
report says: no quint replay of a failure, and no sample of the passes
judged by quint as well. A run the Go evaluator cannot decide is `NO
VERDICT (needs quint)`, never a pass; validate that one without
`-compiled`.

The artifact records the hashes of the binding and of every source. Edit
the model and the compiled one is refused, with the fix:

```
gotogether: compiled model payments.model.json is stale: model/payments.qnt changed since it was compiled (sha256 1d92dd9a1c94, the artifact's 55161681a2f5): compile it again (gotogether compile -binding payments.day.binding.yaml -o payments.model.json)
```

`go tool gotogether doctor -binding payments.day.binding.yaml -compiled
payments.model.json` says the same before a pipeline runs. Clean up:

```sh
rm -f payments.day.binding.yaml payments.model.json /tmp/oct02.jsonl /tmp/audit.jsonl /tmp/day*.jsonl
```

## What you saw

| | judged whole | `partition: id` | `partition: id` and `-compiled` |
|---|---|---|---|
| 600 steps | 2.3 s | | |
| 3,000 steps | 47 s | | |
| 10⁴ steps | minutes (quadratic) | 6.1 s, quint's compile included | 1.4 s, no quint or Node |
| failures reported | the first | every partition's | every partition's |
| a failure's report | quint replay | quint replay | the Go evaluator's alone |

## Read next

- The CI reference of the gotogether-start skill: compiled models,
  `NO VERDICT (needs quint)`, `doctor`: [skills/gotogether-start/references/ci.md](../../skills/gotogether-start/references/ci.md).
- Partitioning a model's state so its logs can be judged by entity
  (pattern 9, abstraction and cost): [skills/gotogether-patterns/references/patterns.md](../../skills/gotogether-patterns/references/patterns.md).
- The migration skill, whose walk-through this example is: [skills/gotogether-migration/SKILL.md](../../skills/gotogether-migration/SKILL.md).
- A grist app's production steps (the same bindings, every pod's files):
  the grist guide, §9.7: [docs/grist-guide.md](../grist-guide.md#97-validating-productions-steps).
- [docs/DESIGN.md](../DESIGN.md) §64 (a model compiled for the evaluator
  alone) and §66 (validation at volume by entity).
