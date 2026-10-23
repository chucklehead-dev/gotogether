# lease: a lock, the store it guards, and clocks that lie

A lock grants leases in epochs; two holders contend for it and write to
a store while they hold it. The requirement (model/lease.qnt) is the
store's: once it has accepted a write of some epoch, it accepts none of
an older one, so a holder whose lease ended cannot overwrite its
successor. The model knows nothing of time. The code keeps time with
`qenv.Now`, which reads the clock of the process it runs in: with
`Sim.ClockFaults` each process's clock starts skewed and may jump, and
with `Sim.ClockDrift` it runs fast or slow, its sleeps with it.

| file | what it is |
|---|---|
| [model/lease.qnt](model/lease.qnt) | grants, accepted writes, and `noStaleWrite` (a ghost flag) |
| [lease.binding.yaml](lease.binding.yaml) | grants and writes; a write refused as stale is no step |
| [lease.go](lease.go) | the lock and the store as services (their own processes and clocks), the holders, `Run` |
| [lease_test.go](lease_test.go) | each variant explored with and without clock faults, and every run with one jump |

## What each test catches

| test | variant | clocks | verdict |
|---|---|---|---|
| `TestFencedConformsUnderClockFaults` | faithful: the store refuses an older epoch (a fencing token) | skews and jumps | every run conforms; every action and fault kind exercised |
| `TestNoFencingSafeWithTrueClocks` | `NoFencing`: the store accepts every write; the holder stops a margin before its lease ends, timed on its own clock | true | every run conforms: a test without clock faults passes the bug |
| `TestNoFencingBrokenByAClockJump` | `NoFencing` | jumps only | `Violates("noStaleWrite")` (37% of runs): the holder's clock went back, or the lock's forward |
| `TestAbsoluteExpiryBrokenBySkew` | `AbsoluteExpiry`: the holder compares the lock's expiry time with its own clock | true, then skews only | conforms, then `Violates("noStaleWrite")` (36%); `NoFencing` conforms under the same skews: timing a lease on one clock is immune to skew |
| `TestNoFencingBrokenByDrift` | `NoFencing`, then faithful | drift only, at 2%, then 25% | conforms at 2% (the margin left after a pause covers about 5%), `Violates("noStaleWrite")` at 25% (43% of runs): a slow clock under-measures the lease; the fenced store conforms at 25% |
| `TestOneClockJumpIsEnough` | `NoFencing`, then faithful | every run with one jump (`System.Exhaust`, 209 runs) | one jump breaks `NoFencing`; the fenced store survives each, the search complete |

The lesson is Kleppmann's: a lease checked against a clock is a
performance optimisation, and only a token the guarded resource checks
makes it safe. A test that runs with true clocks cannot tell the two
apart.

## Run it

```sh
go test .                                      # needs quint on PATH
./run-demo.sh                                  # 20 seeds, every run conforms
MUTATION=no-fencing ./run-demo.sh              # still conforms: the clocks are true
MUTATION=no-fencing CLOCKS=1 ./run-demo.sh     # a stale write after a clock fault
MUTATION=absolute-expiry CLOCKS=1 ./run-demo.sh
MUTATION=no-fencing CLOCKS=1 DRIFT=0.25 ./run-demo.sh   # skew and drift: the slow holder overstays
```
