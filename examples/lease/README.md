# lease: a lock, the store it guards, and clocks that lie

A lock grants leases in epochs; two holders contend for it and write to
a store while they hold it. The requirement (model/lease.qnt) is the
store's: once it has accepted a write of some epoch, it accepts none of
an older one, so a holder whose lease ended cannot overwrite its
successor. The model knows nothing of time. The code keeps time as a Go
program does: a process's wall clock (`qenv.Now`), which `Sim.Clocks`
skews and jumps, and its monotonic clock (`qenv.Mono`, `qenv.Since`: what
`time.Since` measures), which no skew or jump moves; a drift moves both,
and the process's sleeps with them.

| file | what it is |
|---|---|
| [model/lease.qnt](model/lease.qnt) | grants, accepted writes, and `noStaleWrite` (a ghost flag) |
| [lease.binding.yaml](lease.binding.yaml) | grants and writes; a write refused as stale is no step |
| [lease.go](lease.go) | the lock (its own process and clocks: it times leases on its monotonic clock) and the store as services, the holders, `Run` |
| [lease_test.go](lease_test.go) | each variant explored with and without clock faults, and every run with one jump |

## What each test catches

| test | variant | clocks | verdict |
|---|---|---|---|
| `TestFencedConformsUnderClockFaults` | faithful: the store refuses an older epoch (a fencing token) | skews, jumps and drift | every run conforms; every action and fault kind exercised |
| `TestNoFencingSafeWithTrueClocks` | `NoFencing`: the store accepts every write; the holder stops a margin before its lease ends, timed on its monotonic clock | true, every request on time | every run conforms: a test without clock faults or late messages passes the bug |
| `TestNoFencingBrokenByAPause` | `NoFencing`, then faithful | true, every request on time, a holder paused 3 s (`qenv.Stalls`, `nemesis.pause`) | `Violates("noStaleWrite")` (25 of 90 runs): the paused holder still trusts its lease; the fenced store refuses its old token |
| `TestNoFencingBrokenByALateWrite` | `NoFencing`, then faithful | true, a write delivered after its writer's timeout (`rpc.store.delay`) | `Violates("noStaleWrite")` (25 of 90 runs): the late write lands after the next holder's; the fenced store refuses its old token |
| `TestNoFencingImmuneToSkewAndJumps` | `NoFencing` | skews and jumps; then every run with one jump (`System.Exhaust`, 3209 runs; the first 100 under `-short`) | every run conforms: a monotonic clock sees neither |
| `TestWallIntervalBrokenByAClockJump` | `WallInterval`: `NoFencing` timing its lease on the wall clock (`Now().Sub`, as code whose times lost their monotonic reading does); then faithful | jumps; then every run with one jump | `Violates("noStaleWrite")` (half the runs; 306 of the 3209 with one jump, 9 of the first 100 that `-short` searches): the holder's clock went back; the fenced store survives each |
| `TestAbsoluteExpiryBrokenBySkew` | `AbsoluteExpiry`: the holder compares the lock's expiry time with its own wall clock | true, then skews only | conforms, then `Violates("noStaleWrite")` (36%); `WallInterval` conforms under the same skews: an interval on one clock is immune to skew |
| `TestNoFencingBrokenByDrift` | `NoFencing`, then faithful | drift only, at 2%, then 25% | conforms at 2% (the margin left after a pause covers about 5%), `Violates("noStaleWrite")` at 25% (30% of runs): a slow clock under-measures the lease; the fenced store conforms at 25% |

Each mutant is broken by the one fault its code trusts: a comparison of
two machines' clocks by a skew, a wall-clock interval by a jump, a
monotonic interval by a drift past its margin.

The lesson is Kleppmann's: a lease checked against a clock is a
performance optimisation, and only a token the guarded resource checks
makes it safe. A test that runs with true clocks cannot tell the two
apart.

## Run it

```sh
go test .                                      # needs quint on PATH
go test -short .                               # the per-push tier: 8 to 15 seeds an exploration, the first 100 runs with one jump
./run-demo.sh                                  # 20 seeds, every run conforms
MUTATION=no-fencing ./run-demo.sh              # still conforms: the clocks are true
MUTATION=no-fencing CLOCKS=1 ./run-demo.sh     # still conforms: skews and jumps do not move a monotonic clock
MUTATION=wall-interval CLOCKS=1 ./run-demo.sh  # a stale write after a jump
MUTATION=absolute-expiry CLOCKS=1 ./run-demo.sh
MUTATION=no-fencing DRIFT=0.25 ./run-demo.sh   # drift: the slow holder overstays
```
