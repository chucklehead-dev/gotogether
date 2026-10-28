# grist on a platform (GR10): Kubernetes-aware, never Kubernetes-bound

Status: design, 2026-10-28, for docs/ROADMAP.md's GR10; slice 1
(identity, probes, the drain) built 2026-10-28 (docs/HISTORY.md (grist §0.15)
says what building it changed, and what its acceptance got wrong), and
slice 2 (graceful termination, the router, `gristsim.Cluster`) the same
day (docs/HISTORY.md (grist §0.16), likewise). Nothing else below is built; every
proposed API is in ```text blocks (slice 2's are as built: §4 says
where it differs), and every name it
takes from an existing package (`qenv.Stalls`, `qmeta.CalibrateTimeout`,
`qnemesis.Target`, ...) exists today. Read docs/grist.md (§2.6
processors, §3.4 leases, §3.5 seams, §3.8 the real tier) and
docs/grist-instrumentation.md (GR3: declared calls; this note copies
its shape) first; "grist §x" and "GR3 §x" cite them, "DESIGN §x"
docs/DESIGN.md. Folded in: three reviewers' brainstorms (a newcomer
deploying `grist new`'s shop, a designer, a Go specialist), §11 says
what was taken and what was left. The maintainer decided the six open
questions on 2026-10-28 (§10); the sections below are written to the
decisions.

The maintainer's aims (2026-10-28): a grist application can become
Kubernetes-aware and Kubernetes-native (the downward API, projected
secrets and certificates, leader election, limits, disruption budgets,
autoscalers, the Gateway API, Kubernetes primitives as integrations),
none of it mandatory; and qload and qmeta estimate limits, retries,
timeouts and the like. Two lenses, each a section: **what can be
simulated and tested** (every cluster behaviour that can change an
app's correctness or liveness, §4) and **what an app can use to be a
better app** (§5).

The thesis, in one line: **a cluster is an environment, and qenv already
models environments as named choices.** Most of what Kubernetes does to
a process (kill it, pause it, skew its clock, cut its network, run two
versions of it) is a qenv fault today; what is missing is a handful of
choices (a graceful termination, a router that lags, a store that fails
over, a scaler that reacts late) and a core that serves well on a
platform without importing one. Kubernetes buys a grist app efficiency
and operability, never correctness: replicas are already safe without a
leader (grist §2.6's checkpoint compare-and-set).

## 1. Inventory: what grist does today at each seam

Read from grist/ (main.go, processor.go, relay.go, http.go, overhttp.go,
gristsim, gristtest) at 0da5013.

| seam | grist today | what a cluster does to it | gap |
| --- | --- | --- | --- |
| identity | the node's recorder is `<app>-<pid>` (main.go); store-side steps' process is the App's name | every container is pid 1: two pods, or two incarnations of one, share the id | **a real bug on Kubernetes** (below) |
| listen | `-addr` defaults to `127.0.0.1:8080` | a Service's endpoints reach the pod IP | a pod serving on loopback has no endpoints |
| health | none: no `/livez`, `/readyz` | probes decide routing and restarts | teams write probes that hit the database (a DB blip restarts every pod) |
| termination | `Main` stops on SIGTERM: server drain (`shutdownTimeout` 10 s), settle (`settleTimeout` 10 s), relay flush (`flushTimeout` 10 s), the node outliving the signal so an effect in flight keeps its ctx | SIGTERM while endpoints still route for 1-10 s; SIGKILL at `terminationGracePeriodSeconds`, default 30 s | stops accepting at once (refused connections in the lag); worst case 30 s = the default grace: SIGKILL lands in the flush, committed steps unrecorded in this file, the settle lost |
| replicas | `gristsim.Config.Nodes` and two real nodes are safe: every node polls every processor of every tenant, the checkpoint CAS picks one commit (`TestPayoutTwoNodes`) | N replicas | at-least-once effects run on several nodes (harmless only for `Keyed` calls); condition retries grow with N; every node's relay records the whole step log from its first row at every start (the durable relay cursor is grist §8's left item) |
| leadership | none needed; grist §3.4 designs leases **in the tenant's log**, fenced in the commit (slice 3, not built) | client-go's leader election is not a fence: a paused leader acts after its Lease expired | the tempting wrong tool is one import away |
| config, secrets | flags: `-db postgres:DSN` (in the pod spec, in `ps`); `OverHTTP`'s `http.Client` built once | Secrets and ConfigMaps as files, swapped atomically (`..data`), 30-90 s apart per pod; cert-manager rotating certificates | credentials in args; an open `*sql.DB` keeps old credentials; certificates cached until restart |
| decisions' inputs | an `Envelope` (`ID`, `Tenant`, `Caller`) and `Reads`; no clock, no config (GR3 §1) | a ConfigMap read by one pod and not yet by another | none today, by construction: keep it so (§3.6) |
| resources | nothing: `GOMEMLIMIT` unset; `GOMAXPROCS` is container-aware since Go 1.25 (the cgroup's CPU limit, rounded up, updated as it changes; grist needs Go 1.26.1) | memory limits (OOMKill: SIGKILL, no grace); CPU limits (CFS throttling: stalls of up to the 100 ms period) | read models fold from the log on every read (grist §2.2, slice 1), so memory and CPU per query grow with the log: a limit sized in week one OOMs in week four |
| metrics | none in grist; qrole exists (gauges u, v), unused by grist | HPA, KEDA, alerts | processor lag, relay backlog, oldest pending effect are not exposed |
| inbound HTTP | every command requires `Idempotency-Key` (400 without, http.go); 503 + `Retry-After` for an unknown outcome; `WriteTimeout` 2 m | Gateway/HTTPRoute and mesh timeouts and retries | a retried command is safe; the timeout chain is not ordered (a gateway timing out at 15 s turns a slow commit into a 504 and a retry); `-caller-header` trusts a header the Gateway forwards unless a filter strips it |
| outbound calls | `OverHTTP` (`callTimeout` 10 s), declared calls (GR3) | DNS, endpoint lag, NetworkPolicy, conntrack-pinned connections | already modelled: a reset is an unknown outcome, never a refusal |
| store | sqlstore on PostgreSQL or SQLite, `-poll` 250 ms | managed PostgreSQL failover (async replication loses a committed tail), CSI snapshot restore (the log rewinds), `max_connections` against replicas × pool; SQLite on a PVC (RWO with a surge deadlocks a rollout, RWX corrupts) | none of these is a fault grist or qenv can make |
| steps | `-steps FILE` (`qobs.PerStep`), on the container's filesystem | lost with the pod (emptyDir) or interleaved (a shared PV) | commit steps survive in the store's step log (grist §2.10), but only a relay records them, and no command exports them |
| simulation | gristsim: n crashable nodes, the first serving the API; crashes, commit faults, `rpc.*` faults; Clocks and Stalls available but unused | graceful kills, routing lag, mixed versions, zones, autoscaling | no graceful termination, no router, no hosts or zones, no rollout in gristsim |

The finding that matters most: **on Kubernetes every pod's node
records under the same recorder id** (`fmt.Sprintf("%s-%d", app.Name,
os.Getpid())` is `shop-1` in every container), so two pods' merged step
files share (recorder, seq) pairs, and `qtrace.Reconstruct` reports
each pair that differs as an error ("is two different records": its
guard against two logs numbering one recorder), so a multi-pod run gets
no verdict at all; a restarted container's incarnation collides with
its predecessor's the same way. The second:
**30 s of drain inside 30 s of grace**. Both are fixed in slice 1
without any Kubernetes code.

What must keep holding: replicas need no leader; every command is keyed;
a dependency's reset or 5xx is an unknown outcome; commit steps are
durable in the store; decisions see no clock and no configuration.

## 2. Options, weighed against the constraints

The constraints: the core free of heavy dependencies (grist is a
published module linked into production binaries: OTel and yaml.v3
only, no client-go); determinism under qenv (every choice through the
Chooser); the spec as ground truth (a cluster behaviour is a choice the
model judges, never a check loosened); a step the model must see
recorded at the effect's site; **nothing mandatory** (an app on a VM,
under systemd or on Nomad loses nothing).

**(a) client-go in grist.** Leases, informers, Events and the downward
API's files are all reachable without it except the API calls. Rejected
for the core: measured by the Go specialist, a binary electing a
leader through client-go is 37.6 MB (518 packages) where a minimal
client over net/http is 5.8 MB, and nothing the core needs requires the
API server (§3.5 shows leadership does not). A client-go adapter is the
choice of an app that watches objects (GR7's operators), in a module of
its own.

**(b) A platform seam in the core, stdlib only, with Kubernetes as one
reading of it.** The core learns what any orchestrator gives: an
identity (from the environment if present), health endpoints, a drain
schedule, files that change, a memory limit, gauges. Kubernetes' names
appear only as the environment variables and file paths it reads, all
optional. **Chosen.**

**(c) A module of its own for what is Kubernetes-shaped, and client-go
only in executables of its own.** `grist/kube`: manifests from the
catalog, the lint's rules, the sizing writer, in YAML and text only
(yaml.v3, already a core dependency). **No app binary ever links
client-go** (decision 4): whatever talks to the API server is its own
executable, reached as a subcommand by a PATH lookup, the pattern
`gotogether nav` already uses for gotogether-nav (cmd/gotogether/nav.go:
`exec.LookPath`, an install hint when the binary is missing). The
toolkit's `gotogether kube ...` execs `gotogether-kube`; an app's
`<app> kube ...` execs `<app>-kube` (or `grist-kube`). **Chosen.** The
alternative, a build tag (`-tags kube`) putting client-go into the same
binary on demand, was rejected: two build configurations, and the heavy
one easy to ship by mistake.

**(d) Simulating Kubernetes' components** (kube-scheduler, kubelet,
kube-proxy, a full API server). Rejected: an app sees outcomes (killed,
paused, terminated with a grace, not routed, routed late, scaled late,
its files changed), and qenv makes outcomes. Where an app talks to the
API server (GR7, the CR source of §5), kubesim's semantics, published by
H7, are the double, and kwok or kind the real tier.

**(e) A Kubernetes Lease as grist's leader election.** Rejected for
correctness, and also as an efficiency layer (§3.5, decision 1): it does
not fence (a paused or partitioned holder acts after expiry, as
examples/lease shows of every clock-trusting lease), it couples grist's
liveness to the control plane, and grist §3.4's lease in the tenant's
log fences in the commit with no API server at all.

**(f)** The API server as the event store and **(g)** admission webhooks
for HTTP commands are rejected too (§5.8: object size limits and
compaction; a duplicate of grist's own refusals with the API server in a
command's path).

## 3. The design: what the user writes

### 3.1 Layers

| layer | module | imports | what it is |
| --- | --- | --- | --- |
| a good server anywhere | grist (core) | stdlib, qobs, qrole | identity, `/livez` `/readyz` `/metrics` on a side port, the drain schedule, file sources, `GOMEMLIMIT`, gauges, the store-incident checks |
| the simulated cluster | qenv, qload (core); gristsim | as today | terminate, router, hosts and zones, store failover and restore, file projection, a delayed scaler (§4) |
| Kubernetes-shaped output | grist/kube (new module, no client-go) | grist, yaml.v3 | `<app> manifests`, the lint's rules (§5.2), `<app> sizing`'s writer |
| sizing | grist/gristload (a package of grist, linked only by the app's sizing binary, `cmd/<app>-sizing`: `func main() { gristload.Main(app) }`) | qload, qmeta (gristsim at slice 7) | `<app> sizing`'s engine: `grist.Main` runs `<app>-sizing` found beside the app's binary or on PATH, so the binary an operator deploys links none of it (slice 6, as built) |
| the toolkit against a cluster | gotogether-kube (new module, its own executable) | client-go, grist/kube | `gotogether kube`: the kind tier's driver (§7), the canary gate's analysis job (§5.1), the lint against a live cluster |
| the API server as a source | grist-kube or `<app>-kube` (GR7's module, its own executable and Deployment) | client-go, grist/client | CRs as commands sent to the app's HTTP API, status from its answers (§5.6) |

An app that wants none of it writes nothing: `grist.Main(App)` gains
health endpoints and a corrected drain, and nothing else changes. The
two executables are reached as subcommands:

```text
gotogether kube tier|canary|lint ...   # exec.LookPath("gotogether-kube"), else:
  gotogether kube: not installed (go install github.com/chucklehead-dev/gotogether/kube/cmd/gotogether-kube@latest)
shop kube ...                          # grist.Main: an unknown subcommand "kube" execs shop-kube, else grist-kube, else:
  shop kube: neither shop-kube nor grist-kube is on PATH (go install .../grist/gristkube/cmd/grist-kube@latest)
```

### 3.2 Identity

```text
// package grist (core): read once at start, every field optional
type Identity struct {
	Node        string // GRIST_NODE, else POD_NAME, else os.Hostname()
	Incarnation string // a random nonce per start: a restarted container keeps its pod name
	Namespace   string // POD_NAMESPACE
	Host        string // NODE_NAME (the Kubernetes node: a failure domain)
	Zone        string // GRIST_ZONE (the downward API cannot read a node's labels; the manifest passes it, or "")
	Config      string // a hash of the operational options and files in effect (§3.6; not in slice 1)
}
func NewIdentity() Identity // mints: a fresh Incarnation on every call, the rest from the environment
func WithIdentity(id Identity) ServeOption // tests and other orchestrators
```

The node's recorder id becomes `<app>/<node>/<incarnation>` (`-steps
DIR` writes `DIR/<app>.<node>.<incarnation>.jsonl`; a file path is used
as given, which a pod should not do: a restarted container opens the
file afresh, and the earlier start's steps are lost, so a pod gives
`-steps DIR`, and serve warns in a pod, `KUBERNETES_SERVICE_HOST` set,
when `-steps` names a file), the served dependencies' `<app>/<node>/<incarnation>/<dep>`;
relayed steps keep `RecorderID` (store and tenant: the same row is the
same record wherever relayed). The other fields belong on the
recorder's metadata, not on steps, but a recorder has no metadata until
G8's "a step's location separate from its incarnation", the core change
that puts them in reports (qnemesis's loss records then name pods):
slice 1 reads them and prints them on the startup line only, and
`Config` waits for §3.6's options. The generated manifest (slice 8) sets
the variables from the downward API (`fieldRef: metadata.name`,
`spec.nodeName`).

### 3.3 Lifecycle: probes and the drain schedule

`Main` serves, on a side port (`-health-addr`, default `:8081` when an
orchestrator is detected, else off):

- `/livez`: the node's loop is ticking (no poller has worked on one
  trigger past `liveBound`, 5 m, its clock restarted at each trigger and
  after each backoff from a store error: a deadlock or a stuck
  goroutine). **Never the store**: a database blip must not restart
  every pod.
- `/readyz`: the store answered once since start, the node started, not
  draining. **Never a dependency, never processor lag**: lag caused by a
  slow bank would take every pod out of the Service and turn a slow
  dependency into an outage. Today queries fold at the store's head on
  every read, so there is no catch-up to wait for; when H7's snapshots
  and caches land, readiness adds "caches loaded at the head read at
  start", and queries take a `Grist-Min-Position` header (a client's
  last receipt position) and wait for it, so read-your-writes holds
  across pods.
- `/metrics`: grist's gauges in Prometheus' text format, written with
  the standard library (no Prometheus client): per processor and tenant
  the lag (head minus checkpoint) and the oldest unhandled trigger's
  age; the relay's backlog; in-flight effects; the qrole gauges u and v
  of the API (§3.7). qrole's OTel `MetricsSink` stays the way to push
  them.

The drain becomes a schedule, each phase an option, the grace its
budget:

```text
// package grist (core)
type Drain struct {
	Delay    time.Duration // not ready at once, keep serving: the router's lag (default 5 s on an orchestrator, 0 otherwise)
	Shutdown time.Duration // the server drains its requests (default 10 s)
	Settle   time.Duration // the node quiets, a settle recorded (default 10 s)
	Flush    time.Duration // the relay records every committed step (default 10 s)
	Grace    time.Duration // -grace: the orchestrator's kill deadline; the phases shrink to fit, the flush's share first
}
func WithDrain(d Drain) ServeOption
```

On SIGTERM: `/readyz` fails at once; the server keeps serving for
`Delay` (endpoint propagation: the preStop `sleep` action, on by default
since Kubernetes 1.30, does the same outside the binary, and the
generated manifest uses one or the other, not both); then Shutdown,
Settle, Flush as today. Given `-grace`, `Main` budgets the phases from
the deadline backwards: the flush keeps its share first (at most half
the grace: a flush cut is unrecorded commits), the delay, the shutdown
and the settle share the rest in proportion (a settle skipped is an
unjudged eventual val, a request cut a client's retry), and a phase
that ends early passes its time on. It prints at start what the drain
needs and what each phase gets: `shop: node shop-0, incarnation
3f9a0c1e7b42; drain needs a grace of 35s (delay 5s, shutdown 10s,
settle 10s, flush 10s); -grace 30s leaves delay 4s, shutdown 8s, settle
8s, flush 10s; GOMEMLIMIT ...` (the shortfall shared, not taken from
the settle alone). `-addr` on loopback with `KUBERNETES_SERVICE_HOST`
set fails, naming `-addr :8080`.

### 3.4 Store incidents

Two incidents an event-sourced app meets in production and no test of
grist's sees: a managed PostgreSQL failing over with asynchronous
replication (the last committed transactions are gone: a client holds
a receipt for an event no longer in the log, a checkpoint points past
the head), and a restore from a CSI snapshot or backup (the log
rewinds: at-least-once and at-most-once effects run again). grist cannot
prevent either; it must **see** them, fail closed for effects, and the
simulation must make them (§4):

- Each node remembers the highest head it read per tenant; a head below
  it (or a checkpoint above the head) is `ErrRewound`: the tenant's
  pollers and relay stop, `/readyz` stays up for commands (the log is
  consistent from here on), and the error names the positions and the
  fix (`<app> resume -tenant t1 -from 812` after an operator decides).
  A rewind across a restart is seen only by a node that kept running;
  `<app> steps -export` (§7) gives an operator the comparison.
- SQLite in a Deployment is refused by the manifest generator and the
  lint (`-db sqlite:` means a StatefulSet of one replica, `Recreate`).
- The connection pool is an option (`-db-pool`), and the sizing rule
  `replicas_max × pool ≤ max_connections - reserve` is linted.

### 3.5 Replicas, leadership, sharding

Correctness never depends on who polls: two pollers race on the
checkpoint and one commit wins (grist §2.6). Leadership is efficiency:
fewer duplicate at-least-once effects, fewer condition retries, one
relay per tenant instead of N. The design keeps grist §3.4's lease **in
the tenant's log** (fenced in the commit, no API server) and adds the
assignment it lacked, without a leader:

- Each node heartbeats into the reserved tenant `grist` (grist §3.4
  already puts heartbeats there): `member{node, incarnation, until}`.
- A tenant's owner is the live member that ranks first for it by
  rendezvous hashing; a node polls and relays the tenants it owns, and
  every tenant whose owner's heartbeat is stale by `Lease.Takeover`.
- Owning is taking the tenant's log lease (a conditioned append of
  `grist.lease{node, term}`); every processor commit's condition
  includes "no newer term", so a deposed owner cannot advance a
  checkpoint whatever its clock says.
- The heartbeat is read through the store's clock (the commit's time,
  taken under the tenant's lock), never compared across nodes' clocks.

```text
grist.Main(App, grist.Shard(grist.Lease{Heartbeat: 2 * time.Second, Takeover: 10 * time.Second}))
```

Off by default; on, with a control-plane outage nothing changes (no
API server in the path); under split views two nodes poll a tenant for a
while, which is today's behaviour. A Kubernetes Lease is not offered
(decision 1). EndpointSlices are not needed for membership; GR4's peer
discovery may use them.

The reviewers disagreed here (the Go specialist proposed a Lease
elector over a minimal client as a hint, fenced through the store); the
maintainer decided for no Lease (decision 1): heartbeat appends cost the
store little, and in exchange the control plane is never in grist's path,
no RBAC is needed, and the same code runs off Kubernetes. The elector is
the recorded alternative, should heartbeats measure too costly.

### 3.6 Configuration, secrets, certificates

Three kinds, three rules:

- **Secrets and certificates** are files that change. A `FileSource`
  reads a projected volume and notices the atomic `..data` swap by
  polling the link (no fsnotify dependency); `-db-file PATH` reads the
  DSN from one and reopens the pool on an authentication failure;
  `OverHTTP` and `ServeCalls` take TLS from files through
  `GetClientCertificate`/`GetCertificate`, re-read on swap. `subPath`
  mounts never update, which the lint flags.
- **Operational options** (timeouts, poll intervals, tries, batch size)
  are per pod and may differ for a while during a ConfigMap update, as
  versions differ during a rollout. They do not change what a decision
  decides, only when and how often; their hash is `Identity.Config`, so
  a report names which pods ran which.
- **Anything a decision reads** (a feature flag, a limit, a price) is
  an event, never a file: a source's new version becomes a command
  (`grist.FromSource(src, SetConfig)`, its id `<source>/<content
  hash>`, so every pod sending it commits once by the marker), and the
  decision reads it through `Reads` like any read model. The model then
  judges decisions on the configuration the log says was in force, and
  two pods cannot decide under two flags at one position. This is the
  sketch's "config recorded in steps, declared like reads", made literal.

### 3.7 Resources and gauges

- `GOMEMLIMIT`: when unset, `Main` calls `debug.SetMemoryLimit` at 90%
  of the container's limit: `GRIST_MEMORY_LIMIT` if set (the manifest's
  `resourceFieldRef: limits.memory`, in bytes), else the cgroup's own
  `memory.max` (cgroup v2), which any container runtime gives and no
  orchestrator needs to pass. `GOGC` is left alone (never off), and
  `runtime.GOMAXPROCS` is never called. It logs the effective
  `GOMAXPROCS` beside the CPU limit; the generated manifest sets **no
  CPU limit** by default (throttling stalls the pollers and the API in
  100 ms periods, §4), a request from sizing.
- The API is a qrole server (`qrole.Serve`), each declared call a qrole
  call (`qrole.Call`), each processor and tenant a queue whose length is
  its lag: the u and v that retrystorm's alerts use, now for every grist
  app. Two cautions carried from docs/agents/metastability.md: a
  running system's v is only the waiting part (`Sample.Waiting`), and
  the alert regions come from a chain calibrated with `CalibrateTimeout`.

### 3.8 Traffic: Gateway, mesh, timeouts

Every command already carries an `Idempotency-Key`, so a Gateway's,
a mesh's or a client's retry of one is safe; what is not safe is the
product of retry layers (a storm) and timeouts in the wrong order (an
outer timeout shorter than the server's handling turns a commit into a
504, an unknown outcome and a retry). The design:

- A server-side request deadline (`-request-timeout`, default 25 s,
  below a Gateway's usual 30 s) bounding a command's condition and
  transient retries, answered 503 + `Retry-After` when exceeded (the
  outcome unknown, as today).
- The generated HTTPRoute (`<app> manifests -gateway`): one rule per
  route family (`/t/*/commands/*`, `/t/*/queries/*`), timeouts from
  sizing, retries only where sizing says the product of layers recovers
  (commands are safe to retry, not free to), a `RequestHeaderModifier`
  removing the caller header from client requests when `-caller-header`
  is used (the authenticating proxy sets it after).
- The lint checks the chain: client per-attempt timeout > Gateway
  request timeout > grist's request deadline > the longest declared
  call's `callTimeout` × tries it may spend inline (none: effects are
  asynchronous, so a command never waits on a dependency).

Tenant routing to an owner (grain's 503 + `Retry-After`) is not needed
for correctness (any node can serve any tenant against the shared
store) and is left to GR4's measurements.

### 3.9 Versions

A rolling update runs two versions on one log for minutes. The rules
(raftkv's lesson, DESIGN §47: a v2 that writes its new format at once
breaks the v1 nodes still running):

- `<app> catalog -diff deployed.json` classifies a change: **safe**
  (a new command, query or processor; a field added with a zero
  default), **readers first** (a new event type or a field a v1 fold
  must not ignore: ship the readers, then the writers behind a flag set
  after the rollout, §3.6), **breaking** (a renamed tag, a removed
  event: needs H7's upcasters). CI runs it against the deployed catalog.
- What H7 built, and what it did not: a field added to a version is
  taken by grist.lock as the same version (old events read it as zero,
  old nodes ignore it), so it is safe as this list says. A new version
  is not yet: appends write the newest at once, and a node still on the
  older version refuses its events by name (`grist.NewerVersionError`),
  so "readers first" has no API: the writers would need to write the
  older version until the rollout ends (`grist.WritesVersion(n)`, with a
  downcaster from the newest), which slice 4 builds with
  `gristtest.Upgrade` (ROADMAP H7's next item).
- `gristtest.Upgrade(t, v1, v2, cfg)` runs both Apps' nodes on one
  store in gristsim, rolling one node at a time (`Sim.Upgrades`), each
  version's steps judged by the same model (an event v1 does not know is
  v1's refusal to fold, named, never a crash).

## 4. Lens (a): what is simulated, and how

Every row is a named choice (option 0 nominal), so Explore samples it,
Exhaust bounds it, Hegel shrinks it away, Coverage says whether a run
met it, and a Script or a qnemesis journal names it.

| cluster behaviour | in qenv today | new | claim it serves |
| --- | --- | --- | --- |
| OOMKill, SIGKILL, node loss, spot reclaim | crash nemesis `nemesis.crash.<proc>` | hosts: processes sharing a failure domain, `nemesis.host.<h>` crashing all of them | at-least-once repeats, at-most-once losses: admitted, and named |
| eviction, preemption, rolling restart, scale-down | built (slice 2): `Sim.Terminations`, `nemesis.terminate.<proc>`, `Process.Terminate`, `Exit` | `nemesis.terminate.<proc>`: a context the process's tasks read (`qenv.Terminating(ctx)`), then a crash at the grace, then the restart; option `short` of `terminate.<proc>.grace` (node-pressure eviction's reduced grace) | drain within grace (one at least the longest call's timeout): no at-most-once outcome unknown, the settle recorded |
| endpoints lag (kube-proxy, Gateway, mesh) | built (slice 2): `qenv.NewRouter(s, name, lag, procs...)` | a router double: which endpoint of its view (a backend's incarnation) is a schedule choice `route.<name>`; its view lags by `lag` under `route.<name>.lag` (`stale`: a terminating backend still chosen, a ready one not yet), taking changes in the order they happened, every backend's in one sequence (an endpoint pipeline acts on the latest state, so no change overtakes another); a request to an endpoint gone, or to a listener closed when it was sent, is refused | no client sees an error the app could have avoided: `gristtest.NoClientErrors()`, over the tries (a refused try is retried under its key, so no call errs); `Completes()` |
| PodDisruptionBudget | built (slice 2): `Termination.Budget`, `Cluster.Disruptions` | terminate nemeses enabled only while fewer than that many nodes are unavailable (terminating, or down: drained and not yet replaced, crashed and not restarted; crashes are involuntary: not prevented, as a PDB does not prevent them, but counted, as a PDB counts unhealthy pods) | availability through a drain |
| CFS throttling, GC, node pressure | `Sim.Stalls` (`Pause`, `Slow`) | presets: `gristsim.Throttled(cpuLimit)` | leases and timeouts survive stalls |
| NTP skew, drift, VM pause | `Sim.Clocks` | none | §3.5's lease, as examples/lease |
| NetworkPolicy, zone partition | `Network.Partition`, `PartitionNemesis` | zones: a group of hosts, `nemesis.zone.<z>` cutting it off (and, as an option, the store with it) | `Completes()` and `Settles()` after the heal |
| DNS failure, conntrack to a dead pod | `rpc.<name>.request`, `.response` | none: a reset is an unknown outcome | GR3's call claims |
| mesh or Gateway retries and timeouts | `rpc.<name>.request` duplicate, `.delay` | a proxy double in front of the API with its own timeout and retry policy (`proxy.<name>`) | idempotency under duplicates; storms are qload's (§8) |
| rolling update, mixed versions | `Sim.Upgrades`, `Process.RestartAs` | `Cluster.Rollout` (maxSurge, maxUnavailable) driving them | §3.9 |
| ConfigMap and Secret updates | none | `qenv.NewProjection(s, name, versions...)`: a file whose content changes by nemesis, `nemesis.project.<name>`, per process at its own time | §3.6: decisions agree; a rotated secret causes no failure |
| certificate expiry | none (TLS is not simulated) | the projection's versions carry `NotAfter` in virtual time; a double's transport refuses an expired one | a cached certificate fails the eventual vals after expiry |
| managed PostgreSQL failover | `store.<name>.commit` (applied, answer lost) | `store.<name>.failover`: in-flight commits unknown, connections reset, read-only for a while; option `loseTail` (asynchronous replication: the last k commits gone) | `ErrRewound` raised; a receipt without its event named by the clients' binding |
| CSI snapshot restore, backup restore | none | `nemesis.restore.<store>`: the store back to an earlier commit | effects re-run: Keyed dependencies replay, unkeyed ones apply twice (`Rejects("<c>Served")`) |
| `max_connections` | none | `qenv.Store.Connections`: a begin beyond it waits or fails (`store.<name>.connect`) | sizing's pool rule |
| ephemeral storage | `qenv.Disk` (`ErrNoSpace`, `powerLoss`, `tornWrite`) | none | a steps file on a full disk |
| HPA, KEDA, VPA | none | in qload: `qload.Autoscaler` (metric, target, sync period, stabilization, startup delay) changing a server's workers over time; VPA's resize is a terminate | recovery with the scaler's delay (§8) |
| node NotReady, then pod deletion after the toleration (5 min) | partition plus crash | none: a Deployment's replacement runs beside the partitioned original (two nodes polling: safe by the CAS); a StatefulSet's waits, so SQLite's single writer holds | §3.4's StatefulSet rule |
| CSI attach and detach, RWO multi-attach while a pod moves | none | a restart delay on the moved process (`Process.RestartAfter`) | `within` bounds on recovery; the lint's StatefulSet rule |
| API server: watches, 410 relists, conflicts, informer lag, throttling, outages | kubesim (fleetrollout; published by H7): conflicts, lost responses, status lag | an unstructured kubesim: watches with resourceVersion and bookmarks, `kube.watch.gone` (410 after compaction), `kube.watch.lag` (informer staleness: a read of one's own write fails), `kube.request.throttled` (429 + `Retry-After`), finalizers and owner-reference GC, `nemesis.apiserver` (an outage, then a relist storm: a qload case) | only for apps using the API (§5.6, GR7): grist itself never calls it |

What gristsim gains is one optional field (slice 2 built `Grace`,
`EndpointLag`, `Disruptions`, and `Drain`, `Rolling` and `Pace`, which
this sketch did not name: docs/HISTORY.md (grist §0.16); the rest come in later
slices):

```text
gristsim.Config{Nodes: 3, Cluster: &gristsim.Cluster{
	Hosts: 2, Zones: 2,             // nodes spread round-robin (nemesis.host.*, nemesis.zone.*)
	Grace: 45 * time.Second,        // nemesis.terminate.node*: drain as Main does, then the kill
	EndpointLag: 3 * time.Second,   // every node serves the API behind a router (route.api, route.api.lag)
	Disruptions: 1,                 // a PDB of maxUnavailable 1
	Rollout: &gristsim.Rollout{To: appV2, MaxSurge: 1},
	Store: gristsim.Managed{Failover: true, LoseTail: 2, Restore: false},
}}
```

With `Cluster` set, every node serves the API (not only the first), the
node runs `Main`'s drain schedule (the same code: grist's `runDrain`,
the schedule, the settle on `qenv.Now` and `qenv.Sleep`, the stop and
the flush, each tier giving its server's delay and shutdown), and the
claims a deployment needs are ready-made (slice 2 built
`NoClientErrors` and its negation `ClientErrors`; `Deploy` waits for
the later slices' faults):

```text
// package gristtest
func Deploy(t *testing.T, sys qtest.System, c gristsim.Cluster) // the TestDeploy checklist, each a claim:
//   a rolling restart under load: Conforms, All(Completes), All(Settles), NoClientErrors
//   two replicas: Conforms; an unkeyed call's double duplicates are named (Some(Rejects("<c>Served")) where it applies twice)
//   a rotated secret: Settles (RecoversAfter("nemesis.project.<name>"))
//   v1 and v2 nodes: Conforms (Upgrade)
func NoClientErrors() qtest.Claim // no client call exhausted its tries or saw a refusal no model action explains
```

What is deliberately **not** simulated: the scheduler's placement
(only its outcome: pending pods are a smaller capacity in qload), the
kubelet's probe loop beyond its outcome (a liveness failure is a
terminate), CNI and CSI internals.

### 4.1 Liveness under the cluster

A probe failing under load (a restart storm, §8), a scaler's delay, a
cached certificate past expiry and a tenant stopped by `ErrRewound` are
liveness, judged by settles and `within` (DESIGN §48), and under load by
qload's recovery.

## 5. Lens (b): what an app can use

Ranked by what each gives a team for its cost.

### 5.1 A canary judged by the model

The unique thing grist can offer: **the spec gates the deploy**. v2
takes a share of the traffic (HTTPRoute `backendRefs` weights, or Argo
Rollouts' canary steps), both versions record (the identity says which
pod ran which version), and an analysis step validates the canary's
window with the app's bindings: a `Rejects` or `Mismatch` aborts the
rollout, beside the latency checks teams already have. A window never
settles, so it judges safety and the `within` obligations whose bound
fell inside it, not eventual vals; and a rejection names the step's pod,
so a v1 step's fault is not charged to v2.

```text
# generated by `shop manifests -rollouts`: an Argo Rollouts AnalysisTemplate, Job provider
- name: conforms
  provider: {job: {spec: {template: {spec: {containers: [{name: validate, image: gotogether-kube-shop:v2,
    args: [canary, -binding, shop.binding.yaml, -since, 10m, -pods, app=shop, -exit-on, reject,mismatch]}]}}}}}
```

The Job runs `gotogether-kube canary` (decision 6; HTTPRoute weights
for teams without Argo), in an image of the app's model and bindings
over gotogether-kube's (quint for failing runs' reports, H1's Go
evaluator for passing ones), never the production image. It reads a
window as a partial log (DESIGN §50) or, exact and dearer, the touched
tenants' whole logs (`<app> steps -export`, §7). A CronJob running the
same check hourly, its verdict a Kubernetes Event, is continuous
conformance in production.

### 5.2 Generated, annotated manifests

`<app> manifests` writes plain YAML (no Helm, no operator, kustomize-
friendly), committed and diffed in CI like the bindings
(`gristtest.Generated`), every field with a "because" naming its source:

```text
terminationGracePeriodSeconds: 45  # because: drain 5 + shutdown 10 + settle 10 + flush 10 + 10 margin (grist.Drain)
resources: {requests: {cpu: 250m, memory: 256Mi}}  # because: shop sizing, 2026-10-28 (deploy/sizing.yaml)
# no cpu limit: because throttling stalls the pollers in 100 ms periods (§3.7)
```

From the catalog and declared features: a Deployment (or a StatefulSet
of one for SQLite), the probes on the side port, the drain's preStop,
`GOMEMLIMIT` from the downward API, a PodDisruptionBudget sized against
the replicas (never `minAvailable` equal to them), topology spread
across zones and hosts, a NetworkPolicy whose egress is the catalog's
dependencies (`-dependency` URLs) and the store, the HTTPRoute of §3.8,
a native sidecar collector for steps (§7; an init container with
`restartPolicy: Always`, GA since Kubernetes 1.33, so it outlives the
app's flush), a migration Job (sqlstore's schema once, not N pods
racing at boot), HPA or a KEDA ScaledObject from sizing (§8). Validated
with kubeconform in grist/kube's tests.

`<app> manifests -lint DIR` (files, standard library and yaml.v3) and
`gotogether kube lint` (the same rules, against a live cluster's
objects) check hand-written or edited manifests, each finding naming the field and the fix:
loopback `-addr`; a liveness probe reaching the store; a grace below the
drain; `-caller-header` not stripped at the Gateway; retries on routes
whose product of layers sizing rejects; the timeout chain out of order;
a PDB that blocks drains; a CPU limit; no `GOMEMLIMIT`; secrets in env
or args; `subPath` secret mounts; replicas without spread; SQLite in a
Deployment; `replicas_max × pool > max_connections`; steps on a
container filesystem without a collector.

### 5.3 Scaling on what the app knows

KEDA's Prometheus scaler or an HPA on external metrics, fed by §3.3's
gauges, tuned by §8 before shipping. The rule sizing makes visible: **a
grist tenant is a serialization unit and a processor is sequential per
tenant**, so pods add API throughput and parallelism across tenants,
never a hot tenant's commit rate or a processor's rate on one tenant;
and scaling on a lag caused by a slow dependency adds pods that wait on
the same dependency. Lag-based scaling is right only with sharding
(§3.5) and a per-pod bottleneck; the HPA's target is otherwise the API's
u (qrole), not CPU.

### 5.4 Jobs for replays, rebuilds and backfills

`<app> replay -tenant t1 -processor restock -from 0` (a processor's
checkpoint moved back under its log lease, its effects keyed as before
so a Keyed dependency replays rather than repeats), `<app> rebuild`
(H7's snapshots), `<app> steps -export` (§7), each run first in gristsim
on a copy (`gristtest.Replay`: the backfilled events equal the on-time
ones), each idempotent by position so a retried Job is harmless.

### 5.5 Platform services as plain files

cert-manager certificates and external-secrets (or the Secrets Store
CSI driver) reach grist as projected files (§3.6), with rotation handled
and simulated; a dependency's SPIFFE ID, when mTLS is on, checked and
recorded as the callee's identity on the caller's step. The OTel
operator's auto-instrumentation stays useful beside grist for services
that are not grist apps, their spans converted to steps (`convert`,
GR3 slice 6).

### 5.6 Kubernetes as a command source (with GR7)

A CRD per command (`<app> manifests -crds`: the OpenAPI schema from the
argument struct; YAML, no client-go). The controller is **a separate
binary** (`grist-kube`, or the app's own `<app>-kube`), run as its own
Deployment and reached on a workstation as `<app> kube`; it is **a
client of the app's HTTP API** (grist/client): each custom resource
becomes a command with `Idempotency-Key` = uid + generation
(level-triggered: the latest spec, not every edit), and the outcome is
written from the answer as status conditions (`Applied`,
`Refused(short)`, `Unknown`) by server-side apply. Since custom
resources become commands over HTTP, the controller never lives in the
app's process, and its replicas need no Lease either: two of them send
one generation under one key, which the marker answers once, and apply
the same status. A `ValidatingAdmissionPolicy` (CEL) is generated only
from refusals that need no reads (the generator can tell: a branch of
the decision with no `Needs`), so admission never decides on stale
state. Watches are where kubesim's faults matter (410 relists redeliver
every object, which the marker dedupes).

Checked for a need of client-go inside the app, and how the separate
process covers each:
- **Objects feeding read models** (an informer's cache as state):
  covered. A read model folds the log, so a watched object enters as a
  command the controller sends (an event per change, id uid +
  resourceVersion), never as a cache a decision reads, which no model
  could judge.
- **Effects that write the API** (an Event, a finalizer removed, an
  owned object created): covered by GR3's declared calls. The
  controller serves a `kube` dependency (`grist.ServeCalls`, its site
  recorded there), and the app's effect calls it `OverHTTP`, `Keyed` by
  the effect key (server-side apply's field manager and the object's
  name make a retry idempotent).
- **Flagged**: such effects depend on the controller's availability
  (its outage an unknown outcome and a retry, as any dependency's), and
  a decision reading live cluster state has no place in this shape (by
  design, §3.6's rule); an app that needs it is GR7's question.

This is GR7's design to finish; GR10 fixes the process boundary, the
command-id rule and the admission rule.

### 5.7 SLOs from the model

An eventual val with a `within` bound ("every retirement paid within
30 s") is a simulation claim today; `<app> manifests -slo` writes the
same as a Prometheus recording rule over the gauges (oldest unhandled
trigger's age) and a canary check: one statement, three uses.

### 5.8 Not to do

etcd or the API server as the event store; leader election for
correctness, or a Kubernetes Lease at all (§3.5); admission webhooks for
HTTP commands; Kubernetes Events per refusal (Events are for rare
lifecycle facts: a rewind detected, a migration done, a slow catch-up);
client-go, Helm or an operator in grist's core; CTMC numbers as absolute
recommendations (§8.5); readiness on processor lag or a dependency's
health (§3.3); configuration a decision reads from a file (§3.6);
weaving client-go's leader election into qenv to simulate it as it is
(the wrong tool, §2e, and its goroutines and timers would need qgosim);
simulating the scheduler or the kubelet beyond the outcomes an app sees.

## 6. What the generator writes

| command | writes | judged by |
| --- | --- | --- |
| `<app> model` (today) | the model's skeleton and bindings | `gristtest.Generated` |
| `<app> manifests [-gateway] [-rollouts] [-keda] [-slo] [-sizing deploy/sizing.yaml]` | deploy/*.yaml, annotated | kubeconform; `gristtest.Generated`'s diff; `manifests -lint` clean |
| `<app> catalog -diff OLD` | safe / readers first / breaking, each change named | CI against the deployed catalog |
| `<app> sizing -steps FILES -rate R -burst 3x30s` (slice 6: runs `<app>-sizing`) | deploy/sizing.yaml (values, reasons, formula, tool, margin, the setting in force's verdict, caveat) and deploy/sizing/ (the chain's fields as SVG) | §8's confirmation runs (qload, the declared seeds) |
| `<app> steps -export` | the store's committed steps, as JSONL | the bindings |
| `<app> kube ...` | nothing in the app's binary: execs `<app>-kube` or `grist-kube` (§3.1, §5.6) | GR7 |

`manifests` reads `sizing.yaml` only when given it (decision 5): a recommendation
never enters a manifest silently, and each value it takes carries its
reason into the comment.

## 7. The real tier on a cluster

`gristtest.KubeTier` drives the cluster through `gotogether kube tier`
(gotogether-kube's driver, exec'd: the test skips with the install hint
when it is missing, so the app's test binary links no client-go either).
It runs the app's image on kind (kwok
cannot run an app's containers; it serves for scheduling-only questions
such as a PDB's effect on a node drain) with a PostgreSQL in the
cluster, the clients outside it through a Gateway (Envoy Gateway in
kind), and a qnemesis `Target` per pod:

```text
type Pod struct{ Namespace, Name string } // qnemesis.Target: Kill = delete --grace-period=0 --force,
                                          // Start = wait for the replacement Ready, Pause/Resume = SIGSTOP/SIGCONT via kubectl debug
func Evict(ctx context.Context, p Pod) error // the eviction API: graceful, PDB-respecting (429 when blocked)
```

qnemesis keeps its standard-library rule: the pod target lives in
gotogether-kube, which holds client-go (the eviction API, Events read
into the journal) and runs kubectl where a command is simpler, as
crossdb's kind scripts do.
Faults: evictions, kills, rolling restarts (`kubectl rollout restart`),
node drains (`kubectl drain` on a kind worker), the Gateway's retries
configured, a PostgreSQL failover (CloudNativePG's switchover, when the
operator is installed; skipped otherwise, said so).

**Steps.** The store's step log already holds every commit's step
(grist §2.10): `<app> steps -export -db ... -since T` writes them as a
JSONL file, so a pod's loss loses no commit. What only pods hold
(effects' caller steps, served dependencies' sites, settles) goes to a
native sidecar collector (OTLP, qobs's OTel recording) or to a PVC per
pod named by identity; never to the container's filesystem alone. The
tier merges both and validates with the same bindings
(`<app>.real.binding.yaml` where a third party's sites are missing, GR3
decision 4). Pod lifecycle facts (evicted, OOMKilled, restarted, scaled)
are read from the API's Events into a qnemesis journal, so a cluster's
real disruptions become a `qenv.Script` replayable in gristsim
("last Tuesday's node rotation against v2").

## 8. Sizing with qload and qmeta

The maintainer's addition: estimate resource requests, replica counts,
autoscaler thresholds, timeouts, retry policies, Gateway retries, grace
periods and lease durations **from the app's own service times**, with
**recovery as the criterion**: a setting is acceptable when the system,
so configured, recovers from every stressor it must survive (the spikes
and dips the team declares), and the margin is how far the nearest
failing setting is.

### 8.1 Inputs, measured not assumed

- **Service times per block** from the app's steps: a command's
  invocation and completion on the clients' recorder (`qobs.Invoke`,
  `Call.Done`), a commit's time on the relay, a declared call's
  interval on the caller's step (the dependency's latency), an effect's
  from its trigger's commit to its step. `qmeta.Measure` per block; its
  `Exponential()` says whether the chain's verdicts can be trusted
  (docs/agents/metastability.md: constant service made a queue longer
  than μ × timeout absorbing, where exponential service drains it).
- **Growth with the log**: the same measured at 1k, 10k, 100k events per
  tenant (a gristsim prologue fills the log), since queries fold from
  the log until H7's snapshots: memory and CPU per query are lines, not
  points.
- **CPU per request** from a real-tier profile (`go test -cpuprofile`
  on the tier, or the binary's `runtime/metrics`), not from qenv's
  virtual-time profile, which has no CPU.
- **Arrivals**: a declared rate and bursts (`-rate 40/s -burst
  3x30s`), qrole's gauges from production, or a SimKube trace's shape
  (sktrace, `qload.Piecewise`).
- **Policies in force**: grist/client's `MaxAttempts`, `RetryDelay`,
  `Budget` (a `qload.Retrier` as it stands: retrystorm's method, the
  library itself in the loop), the Gateway's and mesh's, `EffectTries`.

### 8.2 The three tiers, each checking the next

1. **The chain** (`qmeta.New` over `Params{Lambda, Mu, Workers,
   Timeout, Retries, Queue, Orbit, Backoff, RetryBudget}`), one per hop:
   the API (workers = replicas × per-pod concurrency, bounded by the
   pool), each hot tenant (workers = 1: its head lock), each processor
   on a tenant (workers = 1, μ from the effect's time). Seconds per
   configuration: it **ranks** settings and draws the recovery region
   (`Region`, `RecoveryWithin`), and its field (`SVG`) is the evidence
   a reviewer reads. Calibrated on simulated trajectories with
   `CalibrateTimeout` (λ held) before any number is quoted. (As built,
   calibration is `-calibrate`, tens of chain solves; without it the
   chain's numbers are quoted only as rankings, beside the simulation's
   seeds.)
2. **The simulation**: `qload` with the measured service
   (`Service.Draw`), the retry library in the loop, `qload.Autoscaler`
   for HPA; then the app itself under load (`gristsim.Load`: gristsim
   as a `qload.Experiment`, goodput counted from its own steps, checked
   against its model in the same run, as examples/checkout's load.go
   does). `qload.Recovers`/`Mostly` decide; `qhegel.FindTrigger` finds
   the smallest stressor that breaks the chosen setting, which is the
   margin.
3. **The cluster**: the trigger found, replayed on the kind tier with
   the generated manifests (§7): one run that confirms, not a search.

### 8.3 What each setting comes from

| setting | tool | criterion | caveat |
| --- | --- | --- | --- |
| replicas (min) | chain, then simulation | the smallest count whose region holds peak λ plus the spike, after losing one zone (PDB plus a zone outage) | per-tenant and per-processor limits do not scale with replicas: reported apart, with a per-tenant rate limit recommended where a hot tenant's λ nears its μ |
| client per-attempt timeout, Gateway timeout, `-request-timeout` | `CalibrateTimeout`, then simulation | above p99 under load, below the next layer out; reject one making a queue absorbing (queue > μ × timeout) | the chain's service is exponential: check with `Exponential()`, confirm in simulation |
| retries, backoff, budget (client, Gateway, mesh) | chain ranks, simulation confirms, FindTrigger margins | recovery from the declared bursts with layers composed (attempts multiply; a budget must cover every loop, DESIGN §11.6) | retrystorm's finding: backoff alone can recover worse than immediate retries under rejection |
| HPA target, stabilization, KEDA threshold | simulation only (a delayed control loop is not Markov in the chain's state) | recovery with the scaler's sync period, stabilization and pod start time; no oscillation N ↔ 2N | the pod start time is measured (kind), not assumed |
| CPU request | arithmetic | λ_peak × CPU per request / replicas + headroom | from a real profile; no CPU limit by default |
| memory limit, `GOMEMLIMIT` | measurement | peak heap per read model × the largest tenant's log at the horizon, plus batches | no queueing model; grows with the log until snapshots |
| pool size, `max_connections` | Little's law | pool ≥ λ × mean transaction time per pod; replicas_max × pool ≤ max_connections − reserve | arithmetic; the HPA's max included |
| `terminationGracePeriodSeconds`, drain delay | sum | grace ≥ drain delay (measured endpoint lag p99 on the tier) + p99.9 request + settle + flush + margin | the endpoint lag is the cluster's, measured once per cluster |
| lease heartbeat and takeover (§3.5) | arithmetic, then exploration | takeover > heartbeat + max stall (Stalls, GC) + drift × takeover; failover within a `within` bound | no chain: correctness is fenced, this is lag and duplicates |

### 8.4 The API

As built (slice 6; docs/HISTORY.md, grist §0.17), the generic helpers
in qmeta and qload (core), the catalog's part in grist/gristload:

```text
// package qmeta: Measure already gives a measured Service (Mean, CV, Exponential, Draw; now Quantile)
type Burst struct{ Rate, Duration float64 }
func RecoveryAfter(p Params, b Burst, healthy func(State) bool, within float64) float64
	// the probability of having been healthy within `within` after the burst, from the empty
	// system: the burst's end distribution (Chain.Distribution) weighed by each state's RecoveryWithin
func Size(p Params, b Burst, healthy func(State) bool, within, prob float64, most int) Sized
	// the fewest Workers whose RecoveryAfter is at least prob (bisected; brute-force tested)
func LimitFor(p Params, factor, duration float64, healthy func(State) bool, within, prob, hi float64) float64
	// the highest λ that recovers from factor × λ for duration: a hop no replica count speeds up
func TimeoutFor(p Params, data []Trajectory, s Service, b Burst, healthy func(State) bool, within, prob float64) Timeout
	// floor = p99.9 service + Erlang C's p99.9 wait at λ × (1+CV²)/2, raised for regular service to
	// the burst's backlog drain; then the shortest rung of floor × 2^(k/2) that recovers, the chain
	// calibrated first (CalibrateTimeout, λ held) when data are given
type Params struct{ ...; Unit int }  // the chain counts requests in units: ms service behind s timeouts
// package qload
func Layered(inner int, outer Retrier) Retrier  // a proxy's tries inside a client's attempts
// package client (grist/client)
func (c *Client) Policy() *Policy  // Do's own retry decision, a qload.Retrier by its methods
// package gristload (grist)
func Measure(app *grist.App, steps []qtrace.Step) (*Measured, error)
func Report(app *grist.App, steps []qtrace.Step, load Load) (*Sizing, error) // value, formula, tool, margin, caveat, evidence
func Main(app *grist.App)  // cmd/<app>-sizing's entry point; grist.Main's `sizing` runs it by name
```

`qload.Autoscaler` is slice 7's. Each value is printed with its
formula (§8.3's criterion column: the timeout's floor and ladder, the
retries ranked with layers multiplied, the grace and pool sums), the
tool that produced it, its margin, the verdict on the setting in force
(`inForce: advised against: ...`) and its caveat.
Never sized from the mean when the CV is far from 1 or the process runs
under a CPU limit. VPA in recommendation mode is a cross-check of the
memory row, never its source.

What the design said that building it corrected:

- **`Size(p, peak, ...)` "from every state the peak reaches".** A peak
  without a duration reaches every state (held long enough, any rate
  above capacity does), and a minimum over the reachable states is 0
  for nearly every setting (the orbit's bound is reachable with some
  tiny probability). Built: a `Burst` (rate and duration), and recovery
  as a probability, the burst's end distribution weighed by each state's
  chance to recover (`RecoveryAfter`): the chain's counterpart of the
  share of seeds `qload.Recovers` says recover.
- **`TimeoutFor` as "CalibrateTimeout's fit".** The fit is the chain's
  correction of its own timeout, not a timeout to set. Built: the floor
  is latency (p99.9 service plus Erlang C's p99.9 wait), the absorbing
  rule is the burst's backlog (grist's HTTP server has no queue bound:
  for regular service the timeout must outlast the backlog's drain),
  and the chain picks the shortest rung above the floor that recovers;
  calibration, when asked (`-calibrate`), scales the rungs. In counters
  the floor itself recovers: the timeout is latency's, and the chain's
  part is to veto one (none recovers: the retries keep the storm).
- **The chain at a grist app's scale.** μ × Workers × Timeout is the
  queue a timeout lets through; 82 commits/s behind a 2 s timeout is a
  queue of hundreds and an orbit of a thousand, too many states to
  solve per setting. `Params.Unit` counts requests in units (the drift
  kept, the noise lost: `TestUnitsKeepTheDrift`), chosen per hop and
  timeout so every setting of a hop is ranked on one state space; a hop
  whose burst stays under half its capacity is not solved at all (no
  queue builds to tip), and its reason says so.
- **FindTrigger for the margin.** grist does not depend on Hegel
  (qhegel is a module of its own), and the declared burst has one
  dimension to shrink: the margin is a bisection of the burst's factor
  over seeded simulations (`qload.Mostly`), the smallest failing burst.
  FindTrigger stays the search for a test (retrystorm's).
- **gristload "test-only by use" (or `grist.Main(App,
  gristload.Sizing())`).** Either links qload and qmeta into the app's
  binary. Built as decision 4's pattern: `<app> sizing` runs
  `<app>-sizing`, beside the binary or on PATH, with an install hint
  when absent; counters' `TestSizingStaysOutOfTheBinary` checks
  `go list -deps` of both.
- **CPU and memory from profiles.** gristload takes CPU per request
  (`-cpu`) and the peak heap (`-heap`) as measured inputs and does the
  arithmetic; it reads no pprof file (no dependency), and without them
  the rows are not sized. Growth with the log comes from the same steps
  (a command's duration against its commit's head, least squares), not
  from gristsim prologues: counters' create grew 8 to 11 ms over 500
  events.
- **A hot tenant's service.** No step records how long a tenant's head
  is held; the command's call is its upper bound, and on SQLite every
  tenant shares one writer, so the whole store is that hop.
- **The chain's limits are not the simulation's.** retrystorm's
  `TestSizedFromTheChain`: the chain's highest recovering rate for
  immediate retries is 7.2/s, where the simulation still recovers at 9;
  the ordering (budget above immediate above backoff alone) is what
  holds. The report quotes chain numbers as rankings beside the
  simulation's seeds.

### 8.5 What the chain can and cannot predict

It can: where the tipping point lies (which λ, which timeout, which
retry count leaves a stable storm), how policies rank, and an alert
region (qmeta's `Rules`) a running system can check. It cannot: absolute
times (its recovery times were days where the simulation's were
minutes, DESIGN §11.3; assert orderings and orders of magnitude);
non-exponential service; several request classes sharing a CPU; more
than one server (a request path of API → store → dependency is several
chains, not one); correlated failures, delayed control, CFS periods,
GC, memory; and anything beyond its orbit bound (keep it well above the
storm's size). So the report never prints a bare number: each
recommendation carries the tool that produced it, its margin (the
smallest failing stressor or the nearest failing setting), its caveat,
and the file of its evidence.

```text
# deploy/sizing.yaml (generated by `counters sizing -steps testdata/sizing/steps.jsonl -rate 60/s
#   -burst 3x30s -gateway-post 3 -client-attempts 3`: postRetries3 in force; abridged)
gateway:
  retries:
    POST:
      value: 0
      because: 'tenant t1 commits one command at a time (μ 82/s, measured mean 12.2ms) at 37.9/s;
        recovery within 1m0s after a 3x30s burst: gateway 1 × client 3 1.000 (simulated: 6 of 6 seeds
        recover); gateway 2 × client 3 0.079 (0 of 6); gateway 3 × client 3 3.0e-06 (0 of 6); ...'
      tool: chain + qload (6 seeds)
      margin: its smallest failing burst 4.0x30s (+113/s at the tenant), from 6 seeds each
      inForce: 'advised against: gateway 3 × client 3 recovers with 3.0e-06 by the chain, 0 of 6 seeds in simulation'
tenant:
  rateLimit:
    value: 69.1/s
    because: 'tenant t1''s commands commit one at a time (its head: workers 1 at any replica count)
      at μ 82/s; under gateway 3 × client 3 its recovery after the burst is 3.3e-04 at 1 to 8
      replicas alike: it would need 2 workers (qmeta.Size), which no replica count gives; ...'
terminationGracePeriodSeconds:
  value: 45
  because: drain delay 5s (endpoint lag 5s) + shutdown 10s (≥ p99.9 request 161ms) + settle 10s + flush 10s + margin 10s
```

## 9. Slices

Each is one agent's work, reviewed before the next; each acceptance
names the mutant it must catch (an overlay, `qtest.Mutants`), with
the faithful app's claim beside it, and its test fails with the feature
ablated.

1. **Done** (docs/HISTORY.md (grist §0.15)). **Identity, probes, the drain schedule (core, S-M).** `Identity`,
   recorder ids `<app>/<node>/<incarnation>`, `/livez` `/readyz`
   `/metrics` on a side port, `Drain` with `-grace`, the loopback
   refusal, `GOMEMLIMIT`, the startup line. **Example: counters**, two
   real nodes as two processes (`TestRealTwoPods`), merged.
   **Acceptance**: the mutant `pidIdentity` (both `counters-1`) gets
   Reconstruct's duplicate error, no verdict; the faithful run conforms;
   a budgeted run records its settle and flush where the unbudgeted one
   is still draining at the kill. As built (docs/HISTORY.md (grist §0.15)) that
   needs the phases scaled down (delay 0.5 s, shutdown 3 s, settle 2 s,
   flush 2 s, `-grace 2s`, a request trickling across the signal): with
   the defaults a `-grace 20s` run is never killed after a settle a
   budget could save (a settle needs the relay caught up, so the flush
   after it is nearly instant), only before one when delay and shutdown
   alone outrun the grace. The recorder's metadata (namespace, host,
   zone) waits for G8 and the generated 45 s grace for slice 8: slice 1
   prints the fields and the grace the drain needs on the startup line.
2. **Done** (docs/HISTORY.md (grist §0.16)). **Graceful termination and the
   router in qenv and gristsim (core, M).** `nemesis.terminate.<proc>`,
   `qenv.Terminating`, the router double (`route.<name>`,
   `route.<name>.lag`), `gristsim.Cluster`'s `Grace`, `EndpointLag`,
   `Disruptions`, every node serving the API. **Example: counters** (the
   at-most-once payout), simulated (`TestRollingRestart`) and on three
   real pods behind a router polling `/readyz` (`TestRealRollingRestart`).
   **Acceptance**: the mutant `stopNodeAtSignal` (the node's ctx
   cancelled at SIGTERM, the bug main.go's comment records) leaves an
   at-most-once outcome `grist.unknown` in some run under terminations
   within grace, where the faithful app records none and is
   `All(Completes())`; a router without lag (ablation) makes the
   endpoint-lag test pass the mutant `noDrainDelay`, which with lag
   fails `NoClientErrors`. As built (docs/HISTORY.md (grist §0.16)): no call of
   the mutant's fails, its refused tries being retried under their
   keys, so `NoClientErrors` counts the tries and the mutant's claim is
   `Some(ClientErrors())`; the lag's ablation holds once a try sent
   before the shutdown is served, as an `http.Server`'s shutdown serves
   its connections; `stopNodeAtSignal` needed `qenv.Service.Call` to
   stop at a cancelled context (it ran to its answer), and a payment in
   flight at a signal on its node: two nodes and a slow bank, 12 runs
   in 40. Two slice-1 defects found: `grist_processor_lag` never 0 for
   a caught-up processor while other events came, and a stopped poller
   counted as running (a cancelled node never idle).
3. **Store failover and restore (core, M).** `store.<name>.failover`
   with `loseTail`, `nemesis.restore.<store>`, `Store.Connections`;
   grist's `ErrRewound` and `<app> resume`. **Example: wallet-grist**
   (transfers, the ledger's Keyed posts). **Acceptance**: under
   `loseTail` the bindings name the incident (a position committed
   twice, or a receipt with no commit), never a silent pass; under a
   restore the faithful app stops the tenant's effects with
   `ErrRewound` (the run's outcome names it), and once resumed its
   effects replay at the Keyed ledger, where `ledgerNoDedupe` is
   `Some(Rejects("ledgerPostServed"))`; with `ErrRewound` ablated an
   at-most-once effect runs again unasked (two calls of one key,
   `Some(Rejects(...))` at its caller step).
4. **Versions (M, after H7's upcasting).** `<app> catalog -diff`,
   `gristtest.Upgrade`, `gristsim.Cluster.Rollout`. **Example:
   counters v2** (a field added to an event, a new event behind a
   flag). **Acceptance**: the mutant `eagerV2` (v2 appends the new
   event before the rollout ends) fails the v1 nodes' folds, named
   (`Some(Mismatch(...))` or the fold's refusal); the faithful
   readers-first rollout conforms; `catalog -diff` classifies each of a
   table of changes (golden test).
5. **Configuration and secrets (core, M).** `FileSource`, `-db-file`
   with reopen, TLS from files, `grist.FromSource` (configuration as
   commands), `qenv.NewProjection`, certificate expiry in virtual time.
   **Example: library** (a mailer over mTLS, a loan limit as a flag).
   **Acceptance**: the mutant `certOnce` (the client certificate read
   at start) fails `All(Settles())` after a rotation and the old
   certificate's expiry; the mutant `flagFromFile` (the decision reads
   the flag's file directly) is `Some(Rejects(...))` at the command
   when two nodes see two versions (the model decides on the logged
   flag); the faithful app conforms through both.
6. **Done** (docs/HISTORY.md (grist §0.17)). **Sizing, the chain and the arithmetic (grist/gristload, M).**
   Measurement from steps, per-hop chains, calibration, the arithmetic
   rows, `<app> sizing` with evidence. **Example: counters**, grist/client's
   budget as the `Retrier`. **Acceptance**: the configuration mutant
   `postRetries3` (Gateway 3 × client 3 on commands) is advised against,
   and `qload.Recovers` agrees: the recommended setting `Recovered()`,
   the mutant `StaysDegraded()` after the declared burst, in most of 6 seeds.
   As built (§8.4's corrections; docs/HISTORY.md (grist §0.17)): counters'
   `TestSizingAcceptance` at `-rate 60/s -burst 3x30s` (6 of 6 and 0 of
   6); at 40/s the busiest tenant's burst (76/s) stays under its
   measured 82/s and every setting recovers, the mutant's by the chain
   with 0.37: the declared load must reach the hop, and the tool says
   which hop it reaches. The sizing binary is `cmd/<app>-sizing`, run by
   `<app> sizing` (decision 4's pattern), its input a checked-in
   recording of the real tier (`testdata/sizing/steps.jsonl`), so the
   test is one machine's measurement, once. Sized: POST and GET
   retries, the per-try timeout, the minimum replicas (a zone lost), a
   hot tenant's rate limit, each processor's lag under a slow
   dependency, the pool, the grace, CPU and memory from given
   measurements, and a command's growth with the log; not the lease
   (slice 11) or the scaler (slice 7).
7. **The app under load and the scaler (qload + gristsim, M).**
   `gristsim.Load`, `qload.Autoscaler`, sizing's HPA rows. **Example:
   counters** (processors' lag as the queue). **Acceptance**: an HPA
   stabilizing slower than the spike rises `StaysDegraded()`, the
   recommended one `Recovered()`, Conforms through the storm.
8. **Manifests and the lint (grist/kube, M).** `<app> manifests`,
   annotations, `-crds`, the lint's rules (`manifests -lint`),
   kubeconform in tests, the shop of `grist new` given manifests
   (`TestNewIsGreen` extended). **Example: `grist new`'s scaffold**.
   **Acceptance**: each of a table of manifest mutants (one per lint
   rule: a liveness probe on the store, a grace below the drain, the
   caller header forwarded, ...) is reported naming its field and fix;
   the generated manifests are clean and valid; `go list -deps` of the
   scaffold's binary has no k8s.io package (a test).
9. **gotogether-kube and the kind tier (M-L).** The module and its
   executable, `gotogether kube` and `<app> kube` dispatching by PATH
   with the install hint (as `gotogether nav`), `KubeTier` driving it,
   the pod target, steps exported from the store and collected from a
   native sidecar, Events into a journal and a Script, `kube lint`
   against a live cluster. **Example: counters** on kind with
   PostgreSQL. **Acceptance**: slice 2's `stopNodeAtSignal` caught by a
   rolling restart under load on kind (a `grist.unknown` outcome after
   a graceful termination), the faithful image conforming and settling;
   the journal's disruptions replayed in gristsim; both dispatches
   tested with the executable absent (the hint) and present.
10. **The canary gate (gotogether-kube, S-M).** `gotogether-kube
    canary` over a window (a partial log, or the tenants' export), the
    AnalysisTemplate, the HTTPRoute-weights recipe. **Example: counters
    v2 on kind**. **Acceptance**: v2 = `wrongAmount` aborts the canary
    (`Rejects("bankPayServed")` in the window); the faithful v2 is
    promoted.
11. **Sharding (core, M-L, after grist slice 3's leases).** Member
    heartbeats, rendezvous ownership, takeover, the relay per owner (the
    durable relay cursor). **Example: wallet-grist with 3 nodes**.
    **Acceptance**: the mutant `trustLease` (a holder commits its
    checkpoint unconditioned, trusting the lease instead of the CAS) is
    `Some(Rejects(...))` at the processor's step under Stalls and Clocks
    (a trigger handled twice by two believed owners); the faithful app
    conforms; condition retries and duplicate effects
    measured lower than without sharding (stated, not asserted as a
    score).

GR7 owns §5.6 (the grist-kube controller as a client of the app's
API, admission from read-free refusals, kubesim's watch faults); slice
5's `FromSource` and slice 9's dispatch are the shapes it reuses.

Order: 1 and 2 first (they fix today's two bugs and every later tier
needs them), then 3, 6, 8, 9, 10, with 4 after H7 and 11 after grist
slice 3's leases; 5 and 7 alongside.

## 10. Decisions (the maintainer, 2026-10-28)

The maintainer took the recommendation on five and reshaped the sixth
(4); each is now the design, and the sections above are written to it.

1. **Kubernetes Leases: none, not even as a hint.** Membership as
   heartbeats in the reserved tenant `grist`, ownership by rendezvous,
   the fence in the log (§3.5). The Go specialist's elector over a
   minimal client is kept only as the recorded alternative.
2. **The drain:** 10 s phases kept, a 5 s wait for endpoints added, a
   45 s grace generated (by slice 8's manifests; until then the startup
   line says the 35 s the defaults need); `-grace` budgets anything
   shorter (§3.3).
3. **A rewind:** fail closed for effects (`ErrRewound`); the operator
   runs `<app> resume`; never an automatic resume, since an at-most-once
   effect would run again (§3.4).
4. **client-go isolated to a subcommand.** No app binary ever links
   client-go; grist core and grist/kube stay standard library and
   yaml.v3. Whatever talks to the API server is its own executable,
   reached by PATH lookup as `gotogether nav` reaches gotogether-nav:
   `gotogether kube` → `gotogether-kube`, `<app> kube` → `<app>-kube`
   or `grist-kube`, GR7's controller a client of the app's HTTP API
   (§2c, §3.1, §5.6). Rejected: a build tag (`-tags kube`): two build
   configurations, the heavy one easy to ship by mistake. No in-process
   need for client-go remains (§5.6's check); a decision reading live
   cluster state is flagged there for GR7.
5. **Sizing into manifests:** `<app> sizing` writes sizing.yaml with its
   evidence; `manifests` uses it only when given `-sizing` (§6).
6. **The canary:** an Argo Rollouts AnalysisTemplate with the Job
   provider, plus an HTTPRoute-weights recipe; no controller of our own
   (§5.1).

## 11. The reviewers' ideas: taken and left

- **Newcomer** (deploying `grist new`'s shop). Taken: the first month's
  bites (§1), no Lease for correctness, the cluster as fault kinds (§4),
  the lint and manifests with a "because" (§5.2), `gristtest.Deploy`
  and `NoClientErrors`, `sizing` with reasons (§8.5), `catalog -diff`
  and `Upgrade` (§3.9), the canary (§5.1), CRs as commands keyed by uid
  + generation, admission from read-free refusals (§5.6), KEDA on lag
  with its limit (§5.3), SLOs from eventual vals (§5.7), Events as a
  Script (§7). Left: `RequireIdempotencyKey` and a 428 (every command
  already needs the key: 400 without); `kube.endpoints.lag` (the lag is
  the router's, Gateway and mesh included).
- **Designer**. Taken: the mapping onto qenv's primitives (§4),
  failover and restore (§3.4), a minimum position once caches exist
  (§3.3), SQLite as a StatefulSet, native sidecars, rare Events only,
  the sizing table (§8.3), the "not to do" list (§5.8). Corrected:
  `nemesis.machine` is `nemesis.host` (qenv's `Machine` is gosim's
  platform interface; simulated machines are DESIGN §10.7's not-yet).
  Left: a load-correlated OOM nemesis (sizing measures heap instead) and
  `dns.*` kinds (`rpc.<name>.*` already makes them).
- **Go specialist**. Taken: the binary sizes (§2a), `memory.max`,
  `GOGC` and `GOMAXPROCS` left alone (§3.7), the drain order, the fence
  through the store (§3.5), the doubles and kubesim's gaps (§4), `Size`,
  `TimeoutFor`, the `Autoscaler`, formulas (§8.4). Left:
  `qmeta.ServiceOf` (`Measure` does it), a kubelet-restart fault (no
  outcome an app sees), EndpointSlices for discovery (GR4's), the
  in-cluster token re-read (only gotogether-kube and grist-kube call
  the API).
- **Where they disagreed**, the Lease: decided for none (decision 1),
  the specialist's elector the recorded alternative.
