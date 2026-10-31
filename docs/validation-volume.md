# Interacting-entity validation baseline

I19's partitioned validation checks independent entity histories. Transfers join
two accounts, so that shortcut cannot validate their histories independently.
This baseline measures the remaining case before changing production semantics.

`internal/qeval/interacting_test.go` separates ordinary transitions, tracked
transitions, full invariant evaluation, and full replay. Account counts are 10,
100 and 1,000; replay lengths are independently 100 and 1,000 transfers. Accounts
begin funded, and transfers alternate between two accounts. This isolates the
cost of surrounding state size; it does not represent broad interaction graphs.
Compilation and fixture construction are outside timed subbenchmarks.

`validate/interacting_bench_test.go` measures the warm passing validator path,
including reconstruction and reporting, using the existing whole-ledger binding.
It varies 10/100 accounts and 100/1,000 transfers independently. Opens and
deposits add two setup records per account. Quint shadow auditing is explicitly
disabled; compilation is warmed, and failure/artifact paths are not measured.
This is not a cold CLI or production throughput budget.

Run from the root with Go 1.26.1 and Quint 0.33.0 available:

```sh
GOWORK=off go test ./internal/qeval -run '^TestInteracting' -count=1
GOWORK=off go test ./internal/qeval -run '^$' -bench '^BenchmarkInteracting$' -benchmem -count=3
GOWORK=off go test ./validate -run '^$' -bench '^BenchmarkInteractingValidation$' -benchmem -count=3
GOMAXPROCS=1 GOWORK=off go test ./internal/qeval -run '^$' -bench '^BenchmarkInteracting/entities=1000/invariant$' -benchtime=1s -cpuprofile=/tmp/invariant.cpu -memprofile=/tmp/invariant.mem
go tool pprof -top -cum /tmp/invariant.cpu
go tool pprof -top -alloc_space /tmp/invariant.mem
```

Local exploratory measurements on a shared AMD EPYC 9V74 executor (Go 1.26.1,
Quint 0.33.0) found the following. Short timing runs identify costs, not a
statistically established speedup or a portable performance guarantee.

| accounts | transfer, µs | full bounds check, µs | transfer bytes | bounds-check bytes |
|---|---:|---:|---:|---:|
| 10 | 3.15 | 10.80 | 2,550 | 1,610 |
| 100 | 11.38 | 82.08 | 9,080 | 15,480 |
| 1,000 | 63.25 | 958.75 | 67,450 | 152,470 |

These are 100 ms exploratory runs. A separate single-process profiled invariant
run took 1.20 ms at 1,000 accounts, with 152,472 B and 3,004 allocations per
evaluation. About 81% of sampled allocated bytes came from `env.bind` and 13%
from slice cloning. Whole validation's initial one-iteration measurements ranged
from 2.4 ms (10 accounts/100 transfers) to 79 ms (100/1,000). Repeated local
measurements should precede a performance budget; nightly already discovers
root-module benchmarks automatically.

A whole-validator profile at 100 accounts/1,000 transfers (1,200 total records,
three 300 ms runs, `GOMAXPROCS=1`) measured 83–100 ms per check, approximately
32.1 MB and 377,834 allocations. Sampled allocation space attributed 39% to value
slice cloning and 25% to `env.bind`. This supports investigating both map copying
and per-entry evaluation allocation rather than extrapolating the microprofile's
81% share to the entire validator.

## Soundness boundary

The test-only prototype caches a deliberately entry-local bounds predicate for
each key. It starts from a full judgment, rechecks all actual written keys, and
compares its result with full evaluation at every prefix, including repairs and
unrelated writes after failures. Six named histories and 1,360 enumerated
boundary prefixes cover the bounded fixture. Removing write tracking makes the
tests fail. This is evidence about that fixture, not a generic inference rule.

Negative controls demonstrate why neither the action's source parameter nor its
write set alone is enough:

- A destination can exceed its bound while the source remains valid.
- A changed reserve can invalidate an untouched account's predicate, although
  every changed account still passes its own predicate.
- Whole-map replacement records a whole access and requires full reevaluation.
- New keys need a first judgment, and unchanged failures must remain failures.

`Footprint.Judging` supports partition factoring; it does not prove safe
incremental invalidation. Production checking, bindings, reports and verdicts
remain unchanged. There is no incremental speedup claim or new user option.

## Next decision

Full invariant evaluation dominates this fixture, but map copying still grows
with account count. First evaluate whether reducing measured per-entry evaluator
allocations improves ordinary full checks without changing semantics. Retain
this full oracle and the existing evaluator differential tests. Any later
incremental design must establish predicate dependencies, handle initial state,
domain changes and failed predicates, and fall back to full evaluation for
unsupported accesses. A wider interaction graph is needed before extrapolating
these results to general workloads.
