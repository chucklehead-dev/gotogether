# Shipping a grist app to Kubernetes

*About 30 minutes. You will: serve a grist app as a pod would, with its
identity, probes and metrics; send it SIGTERM and read its drain; budget
the drain into a grace period; run a rolling restart in simulation and
see the mutant that drops the drain's delay caught; watch a store that
goes back stop the app's effects until an operator resumes; check a
release against the deployed one before it rolls (`catalog -diff`); and
size the deployment from recorded steps (`sizing.yaml`).*

grist is Kubernetes-aware, never Kubernetes-bound: the app binary
imports no client-go, and everything below runs on a laptop. The app is
[examples/counters](../../examples/counters), grain's counter example on
grist (create a counter under a unique name, increment it, retire it;
a welcome mailed at least once, a payout paid at most once through a
bank).

**What is built.** GR10's slices 1 (identity, probes, the drain), 2
(graceful termination, the lagging router, `gristsim.Cluster`), 3 (store
failover and restore), 4 (versions that roll) and 6 (sizing from the
app's steps), with I25's operations basics (JSON lifecycle logs, request
metrics, `-db-file`). **Designed, not built**: generated manifests and
their lint (slice 8: `<app> manifests`, `grist/kube`), the kind tier
and `gotogether-kube` (slice 9), the canary gate (slice 10), the app
under load with an autoscaler (slice 7), the rest of configuration from
files (slice 5), sharding (slice 11). Their designs are in
[docs/grist-platform.md](../grist-platform.md) §9.

Outputs are pasted as they came, but for paths shortened, timestamps
kept, and long lines cut (`[...]`). It assumes the
[first tutorial](prove-it.md)'s setup.

## 1. Build the binary

An app's binary is `grist.Main(App, options...)`: `cmd/counters` here.

```sh
cd examples/counters
mkdir -p /tmp/pod/steps                  # serve -steps DIR/ does not make it (yet)
go build -o /tmp/pod/counters ./cmd/counters
go build -o /tmp/pod/counters-sizing ./cmd/counters-sizing   # section 7
export PATH=/tmp/pod:$PATH
counters -h        # catalog, verify, model, sizing, serve, resume, steps -export, prune
```

## 2. A pod: identity, probes, metrics

A node's identity is `GRIST_NODE`, else `POD_NAME` (the Downward API
sets it), else the host name, and a fresh incarnation on every start.
Its steps are recorded under `<app>/<node>/<incarnation>`, and with
`-steps` a directory, a file per node and start. Never the process id:
it is 1 in every container, and two pods recording as one node make
their merged step files unjudgeable (the `pidIdentity` mutant: "two
different records", no verdict).

```console
$ POD_NAME=counters-0 counters serve -db sqlite:/tmp/pod/counters.db -tenant t1 -steps /tmp/pod/steps/ -health-addr 127.0.0.1:8081 -grace 30s -caller-header X-Caller &
counters: node counters-0, incarnation 9fdf99eab086; drain needs a grace of 35s (delay 5s, shutdown 10s, settle 10s, flush 10s); -grace 30s leaves delay 4s, shutdown 8s, settle 8s, flush 10s; no memory limit (no cgroup memory.max)
{"time":"2026-10-28T18:36:14.598327329Z","level":"INFO","msg":"starting","app":"counters","node":"counters-0","incarnation":"9fdf99eab086","addr":"127.0.0.1:8080","health_addr":"127.0.0.1:8081","orchestrator":false,"grace":"30s","delay":"4s","shutdown":"8s","settle":"8s","flush":"10s","memory":"no memory limit (no cgroup memory.max)"}
counters health on http://127.0.0.1:8081 (/livez, /readyz, /metrics)
counters: dependency bank served on http://127.0.0.1:43365
{"time":"2026-10-28T18:36:14.608400444Z","level":"INFO","msg":"serving","app":"counters","node":"counters-0","incarnation":"9fdf99eab086","addr":"127.0.0.1:8080"}
counters serving on http://127.0.0.1:8080
```

The first line says who the node is and what its drain needs (section
3). In a pod (`KUBERNETES_SERVICE_HOST` set) `-health-addr` defaults to
`:8081` and a loopback `-addr` is refused; here both are given. In a
real deployment give the DSN as a file (`-db-file`, a mounted Secret),
so it shows in neither `ps` nor the pod spec.

The probes and metrics are on the side port. `/readyz` answers once the
store has and the node runs:

```console
$ curl -s -w " %{http_code}\n" 127.0.0.1:8081/readyz
ok
 200
$ curl -s -X POST 127.0.0.1:8080/t/t1/commands/create-counter -H 'X-Caller: me' -H 'Idempotency-Key: k1' -d '{"counter":"a","name":"x"}'
{"pos":1,"events":[{"tenant":"t1","pos":1}]}
$ curl -s 127.0.0.1:8081/metrics | grep -v '^#'
grist_ready 1
grist_draining 0
grist_api_requests_in_flight 0
grist_requests_total{kind="command",block="create-counter",code="200"} 1
[...]
grist_effects_in_flight 0
grist_relay_backlog 0
grist_processor_lag{processor="average",tenant="t1"} 0
grist_processor_lag{processor="welcome",tenant="t1"} 0
grist_processor_lag{processor="payout",tenant="t1"} 0
[...]
grist_store_up 1
grist_rewound{tenant="t1"} 0
```

`/livez` never touches the store: it fails only when a poller has been
on one trigger far longer than any handler takes. A liveness probe that
checked the database would restart every pod at once when the database
blinked.

## 3. SIGTERM: the drain, budgeted into the grace

Send the signal a kubelet sends:

```console
$ kill -TERM %1; sleep 1; curl -s -w " %{http_code}\n" 127.0.0.1:8081/readyz
draining
 503
```

and read the node's log as it stops:

```
mail welcome/t1/1: created x
{"time":"2026-10-28T18:36:14.752432746Z","level":"INFO","msg":"draining","app":"counters","node":"counters-0","incarnation":"9fdf99eab086","grace":"30s"}
{"time":"2026-10-28T18:36:18.753200743Z","level":"INFO","msg":"drain","app":"counters","node":"counters-0","incarnation":"9fdf99eab086","phase":"delay","took":"4.001s"}
{"time":"2026-10-28T18:36:18.753319698Z","level":"INFO","msg":"drain","app":"counters","node":"counters-0","incarnation":"9fdf99eab086","phase":"shutdown","took":"0s","cut":false}
{"time":"2026-10-28T18:36:18.774483061Z","level":"INFO","msg":"drain","app":"counters","node":"counters-0","incarnation":"9fdf99eab086","phase":"settle","took":"21ms","settled":true}
{"time":"2026-10-28T18:36:18.77474289Z","level":"INFO","msg":"drain","app":"counters","node":"counters-0","incarnation":"9fdf99eab086","phase":"flush","took":"0s","flushed":true}
{"time":"2026-10-28T18:36:18.774756207Z","level":"INFO","msg":"stopped","app":"counters","node":"counters-0","incarnation":"9fdf99eab086","uptime":"4.176s"}
```

The four phases (`grist.Drain`):
1. **delay**: `/readyz` fails at once, and the server keeps serving for
   a while, because the Service's endpoints take seconds to stop sending
   a terminating pod requests (`grist.DefaultDrainDelay`, 5 s, on an
   orchestrator, none elsewhere; counters' `Drain` asks for 5 s
   everywhere, so the laptop shows it);
2. **shutdown**: the server drains the requests in hand;
3. **settle**: once the processors are idle and the relay has caught
   up, a settle is recorded in the steps file (where the bindings'
   eventual vals are judged: section 4 of [the liveness tutorial](liveness.md));
4. **flush**: the node stops and every committed step is recorded.

`-grace` is the pod's `terminationGracePeriodSeconds`, and the phases
are budgeted backwards from it. The first line of serve said: the drain
needs 35 s, and `-grace 30s` leaves delay 4s, shutdown 8s, settle 8s,
flush 10s. Kubernetes' default grace is 30 s; before slice 1 the drain
took 30 s of it on its own, and a slow settle was killed mid-flush.
Compare:

```console
$ for g in 45s 10s; do POD_NAME=counters-1 timeout 2 counters serve -db sqlite:/tmp/pod/counters.db -addr 127.0.0.1:8090 -health-addr 127.0.0.1:8091 -grace $g 2>&1 | head -1; done
counters: node counters-1, incarnation 07dc4fde9c7c; drain needs a grace of 35s (delay 5s, shutdown 10s, settle 10s, flush 10s); -grace 45s fits it; no memory limit (no cgroup memory.max)
counters: node counters-1, incarnation 080525bcd530; drain needs a grace of 35s (delay 5s, shutdown 10s, settle 10s, flush 10s); -grace 10s leaves delay 1s, shutdown 2s, settle 2s, flush 5s; no memory limit (no cgroup memory.max)
```

The flush keeps its share first (at most half the grace). Give a grace
above what the drain needs: 45 s is what section 7's sizing advises.

The steps the pod recorded are judged by the same binding as every
simulated run:

```console
$ ls /tmp/pod/steps
counters.counters-0.9fdf99eab086.jsonl
$ gotogether validate -binding counters.binding.yaml /tmp/pod/steps/*.jsonl
input /tmp/pod/steps/counters.counters-0.9fdf99eab086.jsonl: native step log
[...]
actor counters/t1: 5 steps
  conformance: PASS
  invariant uniqueNames: PASS
  invariant averageAsOfTrigger: PASS
  invariant eventually everyPaymentAnswerServed: PASS
  invariant eventually everyMailAnswerServed: PASS
  invariant eventually averageEventuallyHandled: PASS
  invariant eventually welcomeEventuallyHandled: PASS
  invariant eventually payoutEventuallyHandled: PASS
  invariant eventually bankPayEventuallyServed: PASS
  judged by the Go evaluator, with no quint replay (GOTOGETHER_EVAL=quint replays it with quint)
RESULT: PASS (1 actor conforms to the model and satisfies its 8 invariants)
```

The eventual vals were judged because the drain recorded its settle.

## 4. A rolling restart, simulated

On a cluster a rolling restart sends each pod SIGTERM in turn while a
Service routes around them, and the router's view lags. `gristsim.Cluster`
simulates exactly that, under the model. counters' `rolling` workload
(`cluster_test.go`):

```go
var rolling = gristsim.Config{Tenants: []string{"t1"}, Nodes: 2, Clients: 2, Ops: 14, Alphabet: []string{"a", "b"}, Doubles: slowBank,
	Send:    []string{"create-counter", "increment-counter", "retire-counter", "retire-counter", "show-counter"},
	Cluster: &gristsim.Cluster{Rolling: true, EndpointLag: 3 * time.Second, Disruptions: 1, Drain: Drain}}
```

Two nodes behind a router whose view of them lags by up to 3 s, at most
one down at once (a PodDisruptionBudget of one), each drained as the
binary drains (`Drain`), a bank slow enough that a payout is in flight
at a signal. `TestRollingRestart` claims, at the store, that every run
conforms and settles; at the clients, that every call completed and none
had a try refused (`gristtest.NoClientErrors`); and that no payout's
outcome is unknown:

```console
$ go test -short -count=1 -run 'TestRollingRestart$' -v . | grep -E '^(--- |ok|FAIL)|runs, ' | cut -c1-260
    cluster_test.go:87: gotogether: TestRollingRestart#1: 3 runs, all conform (the Go evaluator's, none confirmed by quint); 3 settled; model actions taken 14 of 24 (never, of the binding's actions: bankPayReplayed, gristResumed, gristRewound, mailerSendReplay
    cluster_test.go:89: gotogether: TestRollingRestart#2: 3 runs, all conform (the Go evaluator's, none confirmed by quint); 3 settled; model actions taken 4 of 4; fault kinds faulted 1 of 17 reached; a sample, no proof: a failure 64% of runs show is missed at
    cluster_test.go:91: gotogether: TestRollingRestart#3: 3 runs, all conform (the Go evaluator's, none confirmed by quint); 3 settled; model actions taken 14 of 24 (never, of the binding's actions: bankPayReplayed, gristResumed, gristRewound, mailerSendReplay
--- PASS: TestRollingRestart (17.47s)
ok  	github.com/chucklehead-dev/gotogether/examples/counters	17.473s
```

Now the mutant `noDrainDelay`: the listener closed at the signal, no
delay. The router still sends the node requests for up to 3 s; their
tries are refused and retried under their keys, so the answers still
conform, but the clients saw errors:
`qtest.Some(qtest.Not(gristtest.NoClientErrors()))`. With the router's
lag off, the same mutant passes `NoClientErrors`: the delay exists for
the lag.

```console
$ MUTANT=noDrainDelay ./run-demo.sh | cut -c1-260
== counters (mutant noDrainDelay)
    mutants_test.go:208: gotogether: TestMutant#1: 4 runs, all conform (the Go evaluator's, none confirmed by quint); 4 settled; model actions taken 4 of 4; fault kinds faulted 1 of 17 reached; a sample, no proof: a failure 53% of runs show is missed at most 5
    mutants_test.go:210: gotogether: TestMutant#2: 4 runs, all conform (the Go evaluator's, none confirmed by quint); 4 settled; model actions taken 4 of 4; fault kinds faulted 0 of 17 reached; a sample, no proof: a failure 53% of runs show is missed at most 5
--- PASS: TestMutants (0.00s)
ok  	github.com/chucklehead-dev/gotogether/examples/counters	22.094s
```

The test passing *is* the catch: its claim was that some run had a
refused try (the summary line counts conformance, not that claim). The
other drain mutant, `stopNodeAtSignal` (the node's work cancelled at
SIGTERM), leaves a payout in flight at a signal unknown though the bank
was asked: never derive the node's context from `grist.Terminating`.

The same restart runs on three real pods of the binary behind a router
that polls `/readyz` every 200 ms (`TestRealRollingRestart`;
`ROLLING=1 ./run-demo.sh` runs both, about half a minute more).

## 5. A store that goes back

A managed database's failover can lose its last commits (an
asynchronously replicated tail); a restore puts an older log back.
Either way a tenant's log goes backwards under a running node. grist
sees it in its pollers and relay (`grist.ErrRewound`), records the
model step `gristRewound`, and stops the tenant's processors until an
operator says to go on: fail closed, because an effect replayed from a
rewound log may pay twice. `gristsim.Managed` makes the incident;
`TestRewindFailsClosed` loses the last two commits (the payout's
checkpoint among them) after the bank was paid:

```console
$ go test -short -count=1 -run 'TestRewindFailsClosed$' -v . | grep -E '^(--- |ok|FAIL)|runs|as expected|step ' | cut -c1-300
    incident_test.go:63: gotogether: TestRewindFailsClosed#1: 2 runs, 0 conform, 2 violate eventually everyPayoutOutcomeLogged; 2 settled; [...]
    incident_test.go:70: gotogether: as expected, some run showing a bankPay step the model cannot take: 2 of 2 runs (failing: 2 reject bankPay); the first, seed 1, no faults:
            step 13, bankPay(amount=0, counter=a, key=payout/t1/3, processor=payout, ref=payout/t1/3, trigger=3): the model's bankPay(processor = "payout", trigger = 3, key = "payout/t1/3", counter = "a", amount = 0, result = "ok", ref = "payout/t1/3") is disabled: in bankPay: ((processor == "payout
    incident_test.go:70: gotogether: TestRewindFailsClosed#2: 2 runs, 0 conform, 2 reject bankPay; [...]
--- PASS: TestRewindFailsClosed (21.33s)
ok  	github.com/chucklehead-dev/gotogether/examples/counters	21.341s
```

Read the two halves. Failing closed (`#1`), the payout is not paid
again; what the runs violate is the lost outcome itself
(`everyPayoutOutcomeLogged`: the tail took it), a loss the binding names
rather than a silent pass. With the fail-closed ablated (`#2`,
`Managed.FailOpen`) the processors go on after the rewind and the
payout calls the bank a second time: rejected at `bankPay`. On the real
tier the operator's command is:

```console
$ counters resume -db sqlite:/tmp/pod/counters.db -tenant t1
counters: tenant t1 resumed at head 2: every node's processors go on from their checkpoints once their relay reads it
```

Decide first: the effects of the triggers after the checkpoints run
again.

## 6. Versions that roll: `catalog -diff`

During a rolling update both releases' nodes serve one store. A release
that writes a new event version while old nodes run makes them refuse
its events. counters' release 2 renamed counter-created's `creator` to
`owner` (version 2) and, for this release, still writes version 1
(`grist.WritesVersion`). Before it ships, compare its catalog with the
deployed release's, kept in CI (`release1/catalog.json`, written by
release 1's `counters catalog`):

```console
$ counters catalog -diff release1/catalog.json
safe
  safe: event counter-created: this release reads version 2 and writes version 1, which the deployed nodes read (readers first)
    once it runs on every node, the next release drops WritesVersion and writes version 2
  safe: read model counter-names folds otherwise (its fingerprint changed): its reads refold from the start until they take their own snapshots, each release keeping its own
```

The mutant `eagerV2` writes version 2 at once. Build it (its file in
place of `counters.go`, by a `go build -overlay`) and compare:

```console
$ echo "{\"Replace\":{\"$PWD/counters.go\":\"$PWD/testdata/mutants/eagerV2/counters.go\"}}" > /tmp/pod/eager.json
$ go build -overlay /tmp/pod/eager.json -o /tmp/counters-eager ./cmd/counters && /tmp/counters-eager catalog -diff release1/catalog.json
readers first
  readers first: event counter-created: this release writes version 2, and the deployed nodes read it up to version 1: they would refuse its events (grist.NewerVersionError) until the rollout ends
    write version 1 in this release, grist.WritesVersion(1, func(v New) Old {...}) on its NewEvent, and drop it in the next, once no deployed node runs
  safe: read model counter-names folds otherwise (its fingerprint changed): its reads refold from the start until they take their own snapshots, each release keeping its own
$ echo $?
3
```

Exit 0 is safe, 3 readers first, 4 breaking: a CI gate. The simulated
counterpart is `gristtest.Upgrade(t, binding, release1.App, App, cfg)`,
section 4's cluster as a rollout from release 1's nodes to release 2's
(`TestRollingUpdate`, claiming `qtest.All(gristtest.ReadersFirst())`;
`eagerV2` shows its negation).

## 7. Sizing from the app's own steps

`<app> sizing` measures each command's, query's and call's service time
from a recording of the real tier and writes `deploy/sizing.yaml`, every
value with its reason, formula, margin and caveat. The CTMC (qmeta)
ranks the settings; a load simulation (qload) confirms each. counters
keeps a recording of 400 calls (`testdata/sizing/steps.jsonl`):

```console
$ counters sizing -steps testdata/sizing/steps.jsonl -rate 60/s -burst 3x30s -out /tmp/deploy/sizing.yaml
gateway.retries.POST: 0
  because tenant t1 commits one command at a time (μ 82/s, measured mean 12.2ms) at 37.9/s; recovery within 1m0s after a 3x30s burst: gateway 1 × client 50 1.000 (simulated: 6 of 6 seeds recover); gateway 2 × client 50 0.064 (simulated: 0 of 6 seeds recover); gateway 3 × client 50 2.2e-06; in force: gateway 1 × client 50; [...]
  tool: chain + qload (6 seeds); margin: its smallest failing burst 4.0x30s (+113/s at the tenant), from 6 seeds each
  in force: kept: it is the recommended setting
client.attemptTimeout: 300.2ms
  [...]
replicas.min: 2
  because the API at 60/s, 10 requests at once per pod (μ 92.7/s per request, measured over commands and queries): Size gives 1 recovering after a 3x30s burst (recovery 1.000 at 1) [...], and 2 spread over 3 zones keep 1 after losing one
  [...]
tenant.rateLimit: 69.1/s
  [...]
terminationGracePeriodSeconds: 45
  because drain delay 5s (endpoint lag 5s) + shutdown 10s (≥ p99.9 request 161ms) + settle 10s + flush 10s + margin 10s
  tool: sum
[...]
finding: POST retries at the Gateway multiply the client's (gateway 1 × client 50): the client's budget bounds only its own
finding: replicas never speed up a hot tenant: tenant t1 commits one at a time, recovery 1.000 at 1 to 8 replicas alike under gateway 1 × client 50 (one worker recovers under it); a per-tenant limit (69.1/s) does
finding: scaling on a lag a slow dependency causes only adds waiting pods: welcome runs one effect at a time per tenant at mailer.send's pace, and mailer.send 10x slower leaves 88 events, drained in 2.59s at 1 to 8 replicas alike
counters sizing: wrote /tmp/deploy/sizing.yaml
```

`counters sizing` runs `counters-sizing` beside it (`gristload.Main`),
so the app's own binary links no load simulator. The grace is the drain
of section 3 plus a margin. Give the configuration in force, and the
report says whether to keep it; with a Gateway retrying POSTs three
times inside the client's three attempts (counters' configuration
mutant `postRetries3`):

```console
$ counters sizing -steps testdata/sizing/steps.jsonl -rate 60/s -burst 3x30s -gateway-post 3 -client-attempts 3 -out /tmp/deploy3/sizing.yaml | head -4
counters sizing: wrote /tmp/deploy3/sizing.yaml
gateway.retries.POST: 0
  because tenant t1 commits one command at a time (μ 82/s, measured mean 12.2ms) at 37.9/s; recovery within 1m0s after a 3x30s burst: gateway 1 × client 3 1.000 (simulated: 6 of 6 seeds recover); gateway 2 × client 3 0.079 (simulated: 0 of 6 seeds recover); gateway 3 × client 3 3.0e-06 (simulated: 0 of 6 seeds recover); in force: gateway 3 × client 3; [...]
  tool: chain + qload (6 seeds); margin: its smallest failing burst 4.0x30s (+113/s at the tenant), from 6 seeds each
```

and its `in force:` line says `advised against: gateway 3 × client 3
recovers with 3.0e-06 by the chain, 0 of 6 seeds in simulation`. The
file's head:

```yaml
# generated by `counters sizing -steps testdata/sizing/steps.jsonl -rate 60/s -burst 3x30s` from 1772 steps (400 client calls; the busiest tenant t1, 77% of them; at most 1 in flight at once)
# load: 60/s, burst 3x30s, recovery within 1m0s at probability 0.99; in force: gateway 1 × client 50 on commands, per-try timeout 2s
# every value with its formula, tool, margin and caveat: the chain (qmeta) ranks, the simulation (qload) confirms, never a bare chain number (docs/grist-platform.md §8.5)
measured:
  create-counter: {kind: command, calls: 61, mean: 9.8ms, p50: 6.5ms, p99: 95.7ms, p999: 95.7ms, cv: 1.37, exponential: false}
  increment-counter: {kind: command, calls: 255, mean: 12.4ms, p50: 9.3ms, p99: 71.6ms, p999: 161ms, cv: 1.12, exponential: true}
  [...]
```

Sizing enters manifests only when asked (`-sizing`), with the manifests
themselves: designed, not built (slice 8).

## What you saw

| on the cluster | in grist | checked by |
|---|---|---|
| a pod's name | the node's identity, a step file per node and start | `pidIdentity` mutant: no verdict when two pods share one |
| readiness, liveness | `/readyz`, `/livez` on the side port | `TestRealRollingRestart` |
| SIGTERM, the grace | `grist.Drain`'s four phases, budgeted by `-grace` | the startup line; sizing's grace |
| a rolling restart | `gristsim.Cluster` | `gristtest.NoClientErrors`; `noDrainDelay`, `stopNodeAtSignal` |
| a failover, a restore | `grist.ErrRewound`, `<app> resume` | `TestRewindFailsClosed` |
| a rolling update | `grist.WritesVersion`, `catalog -diff` | `gristtest.Upgrade`, `gristtest.ReadersFirst`; `eagerV2` |
| requests, limits, replicas | `<app> sizing`, `deploy/sizing.yaml` | `TestSizing`; `postRetries3` |

## Read next

- [Validating production at volume](production-at-volume.md): judging
  the steps a deployment records.
- The grist guide, §9 "Running in production": [docs/grist-guide.md](../grist-guide.md#9-running-in-production),
  and §7.3 on versions in a rolling update.
- The grist skill and its versions reference: [skills/grist/SKILL.md](../../skills/grist/SKILL.md),
  [skills/grist/references/versions.md](../../skills/grist/references/versions.md).
- Sizing's mechanics: [skills/gotogether-metastability/references/sizing.md](../../skills/gotogether-metastability/references/sizing.md).
- The design, and what each slice still has to build: [docs/grist-platform.md](../grist-platform.md).

When you are done: `kill %1` if the server still runs, and `rm -rf
/tmp/pod /tmp/deploy /tmp/deploy3 /tmp/counters-eager`.
