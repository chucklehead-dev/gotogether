# raftkv: etcd's raft library under faults, judged by two models

A three-node key-value store on etcd's raft library
(`go.etcd.io/raft/v3`, the library itself, unchanged), run in qenv
([raftkv.go](raftkv.go)). Each node is a crashable process with a
write-ahead log on a Store. Nodes exchange raft's messages over a network
that loses, delays and duplicates them, and only the leader serves
clients. Clients put, get, compare-and-set and transfer amounts between
keys. Each request carries an id (the client and a sequence number), so a
retry applies once. Raft's own election randomness, which it draws from
`crypto/rand`, is drawn from the simulation ([entropy.go](entropy.go)), so
every run replays.

It is the example where the toolkit's newer checks meet a real
consensus library. Leadership is checked by its own model: at most one
leader a term, and a leader within a bound of the network healing
(`within:`). The clients' calls are checked linearized whole (a
transfer touches two keys, so no partition by key helps). A client that
gives up leaves its call pending, and the search places it where it
took effect, if it did. Clock drift is the fault a lease read assumes
away, and the cluster also goes through a rolling upgrade.

| file | what it is |
|---|---|
| [model/raftkv.qnt](model/raftkv.qnt) | the store as clients must see it: `write`, `read`, `cas`, `transfer` (whether it took effect is `lastOk`), the probe's `observe`; `converged` an eventual val |
| [model/cluster.qnt](model/cluster.qnt) | leadership as the nodes record it: `campaign`, `lead`, `stepDown`, `stop` (a crash), the test's `isolate` and `heal`, and each node's state machine (`boot`, `apply`); `electionSafety` an invariant, `hasLeader`, `healed`, `behind` and `caughtUp` vals |
| [raftkv.binding.yaml](raftkv.binding.yaml) | a read's value and a cas's or transfer's answer against the model's; `eventually: [converged]` |
| [cluster.binding.yaml](cluster.binding.yaml) | `invariants: [electionSafety]`, `eventually: [hasLeader]`, `within:` a leader 300ms after a heal, and a restarted node caught up 1s after it booted behind |
| [node.go](node.go) | a node: its raft loop (the RawNode's only user), its senders, the services' handlers, the leadership steps it records |
| [storage.go](storage.go) | the write-ahead log: hard state, snapshot and entries, persisted before any message is sent |
| [sm.go](sm.go) | the state machine, its sessions (a retry answered as the first time), the log's two formats (v1 text, v2 binary) |
| [client.go](client.go) | the clients: calls recorded when invoked and answered, retries with backoff, following the leader's hint |
| [seams.go](seams.go) | where the node leaves its process: the peers' network, the clients', the log; their simulated implementations |
| [realnet.go](realnet.go), [boltwal.go](boltwal.go), [cmd/raftkv-node](cmd/raftkv-node/main.go) | the real tier: the same node over HTTP on TCP, its log a bbolt file |
| [raftkv_test.go](raftkv_test.go) | one `qtest.System` per configuration, explored or searched exhaustively |

A run records one of two histories (`Config.Record`). The clients' calls
are checked linearized, so their order is a search's. A bounded-response
obligation is judged along a trace in time order, and leadership steps
would only enlarge that search, so the nodes' leadership is a trace of
its own, recorded at each node after its hard state is persisted.

## What each test catches

| test | mutant | check | verdict |
|---|---|---|---|
| `TestFaithful` | none | 10 seeds, one message fault in twenty, a node crash about every second; linearized | every run conforms, settles and completes every call, and every node converges |
| `TestClusterFaithful` | none | the same faults, the cluster's history | no term has two leaders; the settled cluster has one |
| `TestElectionAfterHeal` | `SlowElection`: an election timeout ten times as long | every node cut off from the others for 2.5 s, then healed; no other fault | the faithful cluster elects within 300 ms of the heal in every run. The mutant still elects (its `eventually hasLeader` holds), but too late: every run violates `within 300ms after healed: hasLeader` |
| `TestPendingTransfers` | none, then `NoRequestID`: a retry is a new request | clients give up after five attempts at one message fault in ten | with `Linearize` and `ResolvePending` every run conforms (8 of the 12 leave calls pending). `Linearize` alone takes a pending call as having no effect: 3 of 12 runs fail (on a read or `converged`). `NoRequestID` applies a retried transfer twice, which no placement explains: 6 of 12 fail |
| `TestLeaseRead` | `LeaseRead`: reads answered on the leader's lease (`ReadOnlyLeaseBased`) | the leadership moved to n1, then n1 cut off for 400 ms; `Exhaust` over every combination of the three clocks' drifts (none, fast, slow at 0.8), 27 runs | the faithful leader (`ReadOnlySafe`) conforms in all 27; the mutant reads stale in 3 |
| `TestRollingUpgrade` | `EagerV2`: v2 code writes the v2 log format at once | nodes upgraded one every 500 ms while clients write; a full rollout, and one paused after two nodes | the faithful cluster conforms in both and ends on the v2 log after the full one. The mutant, paused, leaves the v1 node skipping entries it cannot read: 8 of 20 runs violate `converged` |
| `TestUnpersistedVote` | `NoPersistVote`: the hard state written without its vote | the leader deposed every 300 ms, crashes and late messages at high rates | no faithful run has two leaders in a term; 5 of 50 mutant runs do |
| `TestCatchUp` | `NoSnapshotReport`: a snapshot lost on the way is never reported to raft | a follower down while the clients write past a compaction, restarted, and its snapshot lost by a 200 ms cut; the cluster's history | the faithful follower catches up within 1 s in all 20 runs; the mutant's never does in 18 of 20 (`within 1s after behind: caughtUp`), though the cluster settles with a leader |
| `TestLostSnapshots` | none | twelve operations a client, one message fault in ten, compaction every 16 entries | every run converges: a lost snapshot is reported to raft and retried (the first version never reported one, and 2 of these 40 runs left a follower behind for good) |
| `TestRealNemesis`, `TestRealNoRequestID` (`RAFTKV_REAL=1`, toxiproxy) | none; `NoRequestID` | the real cluster under qnemesis (crashes, pauses, isolates, lost client responses), seeded schedules | every faithful run conforms and settles, both histories; some `NoRequestID` run fails as in the simulation ([below](#under-a-nemesis)) |
| `TestRealCluster` (`RAFTKV_REAL=1`) | none | three `raftkv-node` processes on localhost; the leader killed (`kill -9`) and restarted on its log while clients write | the cluster catches the node up (every `/status` agrees); the clients' history conforms linearized, settles and completes; the nodes' logs with the harness's settle conform to the cluster's model and settle (`eventually hasLeader` judged); without the settle they do not settle. `TestRealNodeExitsWithParent`: a node whose parent is killed exits |
| `TestLinearizeScale` | none | four clients, fifty operations each, every history linearized whole | all 5 runs conform within a budget of 50000 orders, 20 to 60 s a history (most of it in planning each candidate: ROADMAP S2) |

## What the runs taught

**Raft's lease is a clock assumption, and drift alone does not break it
here.** Raft's pre-vote has a follower refuse to elect while it is in the
leader's lease, and its quorum check has the leader step down. Each is
timed on the node's own clock. An election, though, is won by the node
whose timer fires first in true time, which under drift is the node with
the fastest clock. A slow leader, the one whose lease outlasts its
followers', came only from a leadership transfer (`Deployment.TransferLeader`,
etcd's move-leader). Cutting off whichever node led found no stale read
in any of the 27 drift combinations, since its clock was the fast one;
the transfer to n1 first, then the cut, found 3.

**A rolling upgrade can hide the bug it causes.** The upgraded code
writing its new format at once makes the v1 node skip entries. A full
rollout, though, restarts that node on v2, which replays its log and
repairs the state. A test of the full rollout as first written caught it
in 1 run of 32, and later in none (only when the skipped entries were
compacted into a snapshot before the upgrade). A rollout paused after two
nodes, as one pauses to watch a canary, catches it in 8 of 20.

**Raft waits for a lost snapshot forever unless told.** A node far
behind gets a snapshot; when it is lost (a failed call, or delivered to a
node that crashed before stepping it), the leader pauses that peer until
the application reports the snapshot failed (`ReportSnapshot`). The
first version reported nothing, and a review found 2 runs in 40 whose
follower never caught up: only the probe's `converged` after the settle
showed it.

**Two faults in the toolkit, found by this example.** A wait timed on
a drifting clock was rounded down to no time at all, so raft's tick loop
spun at one instant until the step limit; it now rounds up (qenv,
`TestClockDriftWaitsReachTheirPoint`). A linearization search that the
Go evaluator judged and that ran out of budget was searched again with
quint, at the same budget and with the same no-verdict result, at minutes a run.
It now stays no verdict (validate; DESIGN, the evaluator's
confirmation).

## Real tier

The node's code also runs as a process of its own
([cmd/raftkv-node](cmd/raftkv-node/main.go)): raft's messages and the
clients' requests over HTTP on TCP ([realnet.go](realnet.go)), its log a
bbolt file fsynced at every commit ([boltwal.go](boltwal.go)). It is the
same `Node`: the simulation's seams are interfaces (`PeerNet`, `KVNet`,
`WAL`, in [seams.go](seams.go)) whose simulated implementations are the
qenv Services and Store, and outside a simulation `qenv.Go`, `Sleep`,
`WaitFor` and `Mono` are goroutines and real time. The state the loop,
the senders and the handlers share is under one lock, never held across
a seam, so the simulated schedules are the ones they were.

```sh
RAFTKV_REAL=1 go test -run TestRealCluster -v .    # builds the binary, -race builds it with the detector
go build ./cmd/raftkv-node
raftkv-node -name n1 -incarnation 1 -listen 127.0.0.1:7001 \
  -peer n2=127.0.0.1:7002 -peer n3=127.0.0.1:7003 -wal data/n1 -steps logs/n1.1.jsonl
```

Each node serves `POST /raft` (its peers), `POST /kv` (clients, through
`HTTPNet` and the simulation's `Client.Do`, so a real client carries
`NoRequestID`), `GET /status` (applied index, term, state, leader, code
and log versions) and `GET /state` (its keys: the probe's `observe`).
`-mutation` takes the mutants' names, `-version v2` is the upgraded code,
and `-exit-with-parent` makes a node exit when its stdin reaches EOF: given
a pipe from its harness, it dies with it (a kill -9, a test binary's
`-timeout`, whose panic runs no cleanup), instead of holding its port and
its log's lock.
A node records the cluster's history (boot, apply, campaign, lead,
stepDown) into its `-steps` log, a new one per incarnation (its recorder
`<name>.<incarnation>`, every node's process `cluster`); the clients
record theirs. Both are checked by the bindings the simulation uses. The
harness records the cluster's settle (`qobs.Settled`, in a log of its
own, after the nodes agree and before they stop), without which the
cluster model's `eventually hasLeader` is not judged.

### Under a nemesis

[nemesis_test.go](nemesis_test.go) runs the real cluster under
`qnemesis`: each node a `qnemesis.Process` (`-exit-with-parent`, a
`PIDFile`), its peer links through toxiproxy (a proxy per direction,
given to the nodes as `-peer`), the clients' links through proxies of
their own, so an isolate cuts a node off from its peers only, as in the
simulation. A seeded `qnemesis.Random` schedule crashes, pauses and
isolates nodes (three faults, one every 800ms, each undone after
300ms) while four client connections in five lose their responses. The
nemesis records `stop`, `isolate` and `heal` in the cluster's process
(one actor's trace, one process), then `Stop` heals and restarts, the
nodes agree, the probe observes and the harness settles. Each run's
clients' history is judged by `raftkv.binding.yaml` (linearized,
pending calls placed) and the nodes' and nemesis's steps by
`cluster.binding.yaml`, the bindings the simulation uses, the
`within 300ms` bound included (no run broke it, with `-race` either).

| test | mutant | claim | measured |
|---|---|---|---|
| `TestRealNemesis` | none | every run's clients' history conforms and settles; the cluster's conforms and settles | 33 of 33 runs |
| `TestRealNoRequestID` | `NoRequestID` (the client's) | some run fails as the simulation's does (a read, cas or transfer mismatched, or `converged` violated); the cluster's history conforms | 22 of 24 runs at this loss, 2 of 20 without lost responses: 3 runs a PR (a miss chance of 2%), 5 nightly (0.14%) |
| `TestRealFaultsInSimulation` | none | a real run's journal (`Journal.Script`) re-made in the simulation: every fault happens, the run conforms | |

The nodes leave `Process.Recorders` unset. Named, a crash declares the
killed incarnation's tail lost, and a lost tail voids the run's settle:
of 8 runs with Recorders, the 4 with a crash did not settle. A raftkv
node needs no such declaration, since it records each cluster step before
its effect can be seen (a lead or a campaign before any message of its
term goes out).

A failed faithful run's log names its journal and the command that
re-makes its faults in the simulation:

```sh
RAFTKV_REAL_JOURNAL=<run>/journal.jsonl go test -run TestRealFaultsInSimulation -v .
```

The script's crashes, pauses and partitions are qenv's nemeses of the
same names, each after the steps the nodes had recorded; the simulated
pause and cut last as the real ones did (`Config.Stalls`,
`Config.CutLength`), and what the script cannot make as it happened is
logged (`Journal.Unreproducible`). The clients' lost responses are no
qenv nemesis: they are left out.

```sh
RAFTKV_REAL=1 go test -race -run TestReal -v .   # toxiproxy-server on PATH: go install github.com/Shopify/toxiproxy/v2/cmd/server@v2.12.0, renamed
```

Simulated only: clock skew (Go reads the time through the vDSO, which
libfaketime does not reach) and the rolling upgrade. Raft's election
randomness is crypto/rand's, so a real run does not replay; its seed
replays its schedule.

## Running it

```sh
go test ./...            # about 7 minutes; -short skips the slow searches
./run-demo.sh            # the faithful explorations
MUTATION=lease-read ./run-demo.sh   # or no-request-id, slow-election, no-persist-vote, eager-v2, no-snapshot-report
```
