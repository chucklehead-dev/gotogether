# I/O specifications: the effects a step must make

Status: P0 and P1 built (2026-11-25): DESIGN §111 supersedes this note's
§2.2, §3.1–§3.3 (rules 1 and 2), §4.1 and §4.3 for what P1 built, and adds
`where:` to the binding's kinds. The rest (P2–P4) is the design still.
ROADMAP CW12; the maintainer's answers to §8's questions are in §8.1.

**Recommendation, in short.** State a step's outbound effects in the model
as a def per action (`payEffects(order): List[Effect]`, over a sum type the
model declares). Record each effect where it is made, as a record of its
own written at once (`qobs.Effect`, and qenv's doubles at the call
itself). Attribute it to its step by context: the open `qobs.Invoke` call,
else the task's next step, as qcover attributes code (§17). Compare the two
in the step's expect, so the judging path stays the same and quint and the
Go evaluator decide it. Asynchronous effects are not part of this first
form. These are an outbox drained later, or a call whose landing is a step
of another service. They stay steps of their own at their site, related to
their cause by a key. A later phase generalizes grist's declared calls
(`gristtest.Links`, the generated `<p><C>` rules) into a binding-level form
for them. The smallest valuable slice is synchronous effects under
simulation (§7, P1). Its acceptance is a planted checkout mutant that
conforms in every run today and is caught in every run that pays.

## 1. The problem

### 1.1 What Gobra does, and the analogue

Gobra's I/O specifications (after Penninckx, Jacobs and Piessens' I/O
permissions, used by Arquint et al. to verify protocol implementations
against Tamarin models) give each I/O operation a permission. Typical
operations are a send, a receive or a write. A permission is held at an
abstract place, and performing the operation consumes it and yields the
next place. The protocol model generates the permissions. A verified
function therefore performs only the I/O the model allows, in the model's
order. The permissions are *may* facts (safety). A *must* fact (the send
happens at all) needs obligations on top.

The runtime analogue checks, step by step, that what the code actually sent
during a step is what the model says that step sends. A step can update its
state correctly and still send the wrong message, send it twice, send
nothing, or write before it checks. Each of those is then caught where it
happens, not by a later invariant or a settle that may never come.

### 1.2 What the current checks see

| check | what it compares | why an effect slips past |
|---|---|---|
| a binding's `expect` (`binding/binding.go`, `Action.Expect`) | the model's post-state against the step's `obs.*` | an observation is what the code reports of its own state. A send is not state, unless the model keeps a ghost of it |
| invariants and `eventually` | the model's state along the trace | they see an effect only if some step records it and the model folds it into state. `eventually` is judged only on settled traces |
| `connect.Bind` `Returns` / `Observes` (`connect/bind.go`) | a method's results against a def or a pick | results are what the caller gets back, not what the method sent to its dependencies |
| lock invariants (`qenv.Guard`, `qenv/lockguard.go`) | a predicate over memory at every Unlock | internal consistency at publication, not the outward I/O |
| mutants (`qweave -mutate`'s `drop call`, `qweave/mutate.go`) | whether some check kills the mutant | a dropped send is killed only if a later check notices. Survivors are "often an unobserved effect" (docs/agents/gosim-weaving.md) |

The toolkit's present answer to effects is a rule from DESIGN §10.4: "steps
are recorded where effects happen". The effect is made a model action of
its own, recorded at its site (a commit, the provider), and related to its
cause by a ghost and a history invariant. That works where it is applied
completely (payd's `noDoubleCapture`, streams' reads). It leaves three gaps:
- **Partial payloads.** The site records fewer fields than the effect has.
- **No cause.** A site step does not say which step caused it. The model
  must then allow the effect at any time (checkout's `charge`, broadcast's
  `gossip`), and it cannot tell a duplicate the code made from one the
  network made.
- **Unrecorded effects.** Effects with no later reader are not recorded at
  all.

### 1.3 Evidence in the examples

| example | the effect | recorded today | what slips |
|---|---|---|---|
| checkout, the capture (`shop/shop.go` `pay`, `shop/provider.go` `Handle`) | `Capture(key "cap:<order>", order, amount qty×10)` over `qenv.Service` "provider" | `charge` at the provider's commit, with `order` alone (`provider.go:53`). The model's `charge(order)` has no amount and is enabled for any placed order at any time (`model/checkout.qnt`, "the provider may charge any placed order at any time") | A capture of the wrong amount (`o.Qty` for `o.Qty*10`) changes nothing the model reads. By reading, not run: it conforms in every run. `NoIdempotencyKey` is caught only after a lost response leads to a retry that charges twice (README table) |
| checkout, the outbox (`shop.go` `emit`, `RunRelay`) | an outbox row in the placing commit, then a broker publish | neither is a step | `DualWrite` (publish from a `qenv.Go` goroutine after the commit) is caught only by `settledCorrectly`, in "a one-step window, found at a higher crash rate" (README) |
| wallet, the ledger (`wallet.go` `Ledger.Consume`) | a posting of every move of a request | `post` at the ledger's commit, "its first move's" only (`p.Moves[0]`) | A transfer's posting whose second move is wrong or missing is judged by nothing. `postedApplied` compares the one move recorded |
| payd, the capture (`sim.go` `Processor`, `model/payd.qnt`) | `capture(key, amount)` at the processor | key and amount recorded (`sim.go:242`). The model keeps a count per key (`captured: str -> int`) and drops the amount | No property relates a capture's amount to its charge's. `noForwardKey` (the capture sent without its key) is caught when a fault makes a retry: "30 of 76 runs" within one fault |
| glomers broadcast (`broadcast/broadcast.go` `gossip`) | an RPC carrying `values` to a neighbour | `gossip` recorded by the sender just before the RPC, from the same variable (`broadcast.go:160-162`). The model allows "any subset of what it knows to any node at any time" (`model/broadcast.qnt`) | A node that never gossips, or forgets its backlog (`NoRetry`), is caught only by `convergedWhenSettled`, on a settled trace |
| twophase (`model/twophase.qnt`, `tpc/tpc.go`) | Choreo transitions carry `effects` (`Broadcast(ParticipantPrepared(..))`, `Broadcast(CoordinatorCommit)`) | the binding compares `stage` alone (`twophase.binding.yaml`). The code's messages are "the caller's" and unrecorded (`tpc.go` doc) | The model's effects are written and never compared |
| payouts / grist declared calls (`grist/calls.go`, docs/grist-instrumentation.md §3.3–3.4) | `bankPay` and its site `bankPayServed` | both ends, with request and response fields, cause and key | This is the **reference**. The generated rule `<p><C>` is the I/O spec of a (processor, call). It catches a wrong request at the site or the caller, a key applied twice, and an at-most-once call sent twice. What remains is effects outside declared calls, flagged only statically (qanalysis's quintraw, `//grist:raw`) |
| streams (`pipeline/pipeline.go`) | produced records | `publish` at the outbox delete, `read` at the consumer | Little slips. The effect is read back, and the consumer is "the one place every tier can record" (AGENTS gotchas). This is the case where today's pattern suffices |

Grist shows the idea works and pays: a request that does not follow from
its trigger is rejected (counters' `wrongAmount`). It does so only for
grist's own calls, by generating model variables (`<c>Records`, `<c>Calls`)
and rules from the catalog. The core has no equivalent for a plain Go
service.

### 1.4 The bug classes

An I/O spec of a step catches, at that step:
- **wrong payload:** the amount, the key, the recipient (checkout's capture
  amount, payd's missing key);
- **missing:** the step made no effect the model requires (a dropped
  `Publish`: qweave's `drop call`);
- **duplicated by the code:** two sends where one was due. A retry is still
  allowed when the binding says so (§4.1). A duplicate made by the network
  is the environment's and is not the step's (§3.3);
- **extra:** an effect of a declared kind the model does not list;
- **out of order within the step:** a publish before the write it
  announces, or a write before the check that guards it;
- **late:** an effect made after the step completed, from a goroutine the
  step started (`DualWrite`'s shape).

It does not catch effects of kinds the binding does not declare (they are
listed, §4.2), or effects made through channels nothing records: an unwoven
`go` statement, or a real client with no recording transport.

## 2. How the model states effects

### 2.1 Options

1. **A ghost of the step's effects.** Each action sets `sent' = [...]`, and
   every action frames it. This allows properties over effects in the model
   alone. Every action must assign it, though, and it multiplies the state
   graph (`simulate -graph`), because two states that differ only in their
   last step's effects become distinct.
2. **A def per action** (recommended): `pure def` or `def`
   `<action>Effects(params): List[Effect]`, evaluated in the step's
   post-state, as an `expect` and `Returns` are. It adds no variable, needs
   no framing and costs no state. Like Gobra's per-function I/O spec, it is
   a function of the step's inputs and the state.
3. **An obligations ledger** for effects that land later: the cause adds
   what it owes and the site's step discharges it (§2.3). It is the
   hand-written shape of payd's and wallet's ghosts, made regular.
4. **Choreo's transitions.** `Transition.effects` is already the I/O spec of
   a node's step (`model/choreo.qnt`). A projection per node is the Gobra
   and Tamarin route (§6).

### 2.2 The recommended form

The model declares its effects as a sum type and a def per action that
makes any:

```quint
type Effect =
  | Capture({ order: str, amount: int })
  | Publish({ topic: str, order: str, kind: str })

// what paying an order sends the provider: its amount, once (retries aside)
def payEffects(order: str): List[Effect] =
  [Capture({ order: order, amount: orders.get(order) * 10 })]
```

A def evaluated in the post-state sees what the step did. Models already
keep in state what a step decided (wallet's `moves`, checkout's `orders`).
An action whose effects depend on a choice made inside it must take that
choice as a parameter. This is the rule AGENTS.md already states for
expects: quint's replay draws an inner choice at random.

The binding maps recorded effects onto the model's constructors, as it maps
steps onto actions:

```yaml
effects:
  kinds:
    Capture:                         # the model's constructor
      from: rpc.provider.request     # the recorded effect kind (§3.2)
      fields: { order: '{{.order}}', amount: '{{.amount}}' }
      same: [key]                    # every try of one effect carries one key (§4.1)
    Publish:
      from: queue.payments.publish
      fields: { topic: '"payments"', order: '{{.order}}', kind: '{{.type}}' }
actions:
  pay:
    effects: payEffects              # default: <action>Effects, if the model has it
    match: retried                   # exact | retried | set (§4.1)
```

`fields` are templates over the effect record's fields, rendered by the
same renderer as `args` (`binding/binding.go`'s `templateFuncs`). An
action with no `effects` entry and no `<action>Effects` def is not
compared. The binding generator renders the comparison as one more
conjunct of the step's expect (`binding/generate.go`, `ExpectTemplate`):
`payEffects("o1") == [Capture({ order: "o1", amount: 20 })]`. Nothing new
is judged. The evaluator decides a passing trace and quint reports a
failing one (ARCHITECTURE §4, step 5).

Composition:
- **Binding YAML:** an optional top-level `effects:` and per-action
  `effects:`/`match:` keys, so `gotogether.binding/2` stays (an added
  optional field is no new major, DESIGN §63). The schema is regenerated
  (`schemas/`).
- **`connect.Bind`:** `.Emits(action, def, args...)`, the sibling of
  `Returns` and `Observes`. The step's context carries an effect scope. The
  doubles or fakes the implementation calls record into it, and the bound
  driver compares the scope's effects with `Step.Eval(def, ...)` after the
  call (§5).
- **`//quint:action`:** woven code records on return, so the effects the
  method made before returning are its task's next step's (§3.3). No
  directive change is needed.
- **Generated types:** `gotogether types` already turns a sum type into a Go
  struct tagged by variant (internal/typegen), so a fake can build expected
  effects from the model's type.
- **grist:** its generator keeps its own form. A declared call's caller
  step, site record and rule already cover the synchronous and the
  asynchronous halves for its calls. A later grist slice could generate
  core `effects:` entries for what is not a declared call (its raw effects),
  instead of only flagging them.

### 2.3 Effects that land later: owed, then discharged

An outbox row published by a relay, or a capture whose landing is the
provider's commit, is not made in the causing step. Its relation to the
cause is that the cause *owes* it, and some later step at the site
*discharges* it:

```quint
var owed: Set[Effect]           // owed, not yet seen at its site
var made: Effect -> int         // seen at its site, how often

action place(order: str, qty: int): bool = all {
  /* ... */
  owed' = owed.union(Set(Publish({ topic: "payments", order: order, kind: "placed" }))),
}
action published(e: Effect): bool = all {
  owed.contains(e) or made.keys().contains(e),   // may: only what some step owed
  owed' = owed.exclude(Set(e)), made' = made.put(e, if (made.keys().contains(e)) made.get(e) + 1 else 1),
  /* ... */
}
val allDischarged = owed == Set()                // must: an eventual val, judged on a settled trace
```

This splits Gobra's two facts. The guard of the discharge is the
permission: an effect no step owed is rejected at the site's step. The
eventual val is the obligation: what is owed is made by the settle. The
multiplicity follows the delivery. At most once is `made.get(e) <= 1` as an
invariant. At least once allows repeats of an identical effect. Exactly
once is both. The key that relates a cause to its site is a field of the
effect (an outbox key, an idempotency key, a grist effect key), never time
or span ancestry (docs/grist-call-spans.md: "trace parentage is not used to
invent the business cause").

Written by hand this is what payd's `captured`/`capturedCharged` and
wallet's `posted`/`allPosted` already are. The proposal (P3) makes it
regular:
- a vendored Quint module of polymorphic helpers (`owe`, `discharge`,
  `outstanding`), the first entry of CW6's environment library;
- a binding form naming a site action as the discharge of a kind, so the
  key-matching rules that `gristtest.Links` hand-codes for peers are
  declared rather than written.

The obligations live in the model, not in the generated replay module, so
`gotogether simulate` sees them too.

## 3. What records the actual effects

### 3.1 The effect record

An effect is a record line of the step log. It is not a model step. Like an
invocation, it is written when the effect is made, so a crash after the
send keeps it. It carries:
- `quint.effect`: the kind, for example `rpc.provider.request`;
- its fields as `quint.arg.*` scalars (collections as JSON text, as
  `qtrace.Step` keeps them);
- the cause, when it is known: `quint.call` (an open invocation's seq, the
  key that already pairs completions, `qtrace/step.go`) or the task it was
  made on (§3.3).

`qtrace.Reconstruct` takes effect records out of the steps, as it takes
invocations, settles and losses (`Record` in `qtrace/step.go`). It attaches
each one, in seq order, to its cause: the completion's `Step.Effects`, a
pending invocation's effects in `Trace.Pending`, or `Trace.Unattributed`.
The step's effects become part of its verdict key (the `Keyer`). Without
that, a cached verdict ("cached per instance, not per steps alone",
AGENTS.md) would answer for a trace whose effects differ.

A step must say whether its effects were observed. A step recorded with no
effect scope has "effects not observed", which differs from "observed, and
there were none". The difference matters for the same reason AGENTS.md
forbids -1 as "not observed". An action with an effects spec whose step
observed none is a binding error naming the fix, not a pass.

### 3.2 Where effects are recorded

The doubles record the *send*: the call as the code made it, before the
environment decides its fate.

| site | effect kind | fields | notes |
|---|---|---|---|
| `qenv.Service` `Call` (`qenv/rpc.go`) | `rpc.<name>.request` | the request, through `itf.Encode` of `Req` | A lost, duplicated or late request is a fault of the network: one send, one effect. A retry by the code is a second `Call`, a second effect |
| `qenv.Queue` `Publish` (`qenv/queue.go`) | `queue.<name>.publish` | the body | A publish cut off by a partition waits inside `Publish`. The effect is recorded once, at entry |
| simlog `Producer.Produce` (`qenv/simlog/simlog.go`) | `log.<name>.produce` | key, value | The idempotent producer's retries are the double's, not the code's |
| `qenv.Store` commit (`qenv/store.go`) | `store.<name>.write` | key, value, for the committed transaction's writes | Only a commit is an effect, since an aborted transaction made none. Only prefixes a binding declares are compared (P2) |
| qsql's `Options.OnCommit` changes (`qenv/qsql/qsql.go`) | `sql.<name>.change` | table, row | the changes payd already records as `charge` and `debit` |
| qhttp `Transport` (`qenv/qhttp/transport.go`) | `http.<host>.request` | method, path, chosen headers, body fields | an unmodified service's outbound calls (payd's processor client) |
| simgrpc, simvalkey, kubesim clients | `grpc.<service>.<method>`, `valkey.<name>.<command>`, `kube.<cluster>.<verb>` | the request | each double's own module, after the core's |
| code, anywhere | `qobs.Effect(ctx, kind, kv...)` | as given | for a dependency no double serves |
| the real tier | a recording `http.RoundTripper` (payd's `Recording` generalized into `qobs/qobshttp`); OTel client and producer spans through `convert` rules | as above | Within one process, a client span started under the step's span is in the step's scope: that is context propagation, not inferred cause |

A double records only when the run's context asks for effects: a binding
with an `effects:` section, carried through `internal/hooks` as the run's
environment is. Explorations that do not ask pay nothing.

### 3.3 Attribution

Three rules apply, the first that fits:

1. **The open invocation.** An effect made under the context of a
   `qobs.Invoke` call that has not completed is that call's. The context
   must carry the call (a scope from `Invoke`), and the doubles find it
   there. Goroutines started with `qenv.Go` inherit the context.
2. **The task's next step.** Otherwise the effect is the next step its task
   records, as DESIGN §17 attributes code: "steps are recorded where they
   take effect, at the end of the code that made them". This covers steps
   recorded at a commit (`Txn.OnCommit`, where the committing task records)
   and woven `//quint:action` records on return. It inherits §17's caveat.
   An effect whose task records no later step (a relay, setup, or a client
   whose step the service records) is "made by no step" and is listed, not
   compared.
3. **By key, at the site** (§2.3): an effect whose landing is another
   process's step is related by a field, never by attribution.

The hard cases:
- **An asynchronous effect from the step.** `DualWrite`'s `qenv.Go`
  publish inherits `PlaceOrder`'s context. If the step is an open
  invocation, the effect is attributed to it and recorded *after* its
  completion: a **late** effect, a mismatch unless the action's `match`
  allows late effects. If it is recorded at the commit (checkout's `place`
  is), rule 2 gives the effect to the goroutine's own task, which records
  no step, so the effect is "made by no step". Either way the report names
  it.
- **A crashed caller whose effect landed.** The effect record was written
  at the send, so it survives the crash. Its cause is a pending invocation
  (`Trace.Pending`) or a task frozen before its next step (DESIGN §13's
  interrupted steps). It is kept there, never dropped. If `qtest.ResolvePending`
  places the call as taken effect, its effects are compared. If it places
  the call as not taken, the effects are still a fact. Whether that rules
  the placement out is open (Q5).
- **Retries.** Every `Call` the code makes is an effect. The network's
  duplicates and the double's retransmissions are not. The binding's
  `match` (§4.1) decides how many tries of one effect a step may make.
- **Effects recorded at the service.** Effects a remote service makes in
  its handler are its own steps' (rule 2, in its process). The caller's
  effect is the request it sent. The link between the two is §2.3's key.
- **Order.** Scoped effects are recorded on the cause's own recorder, so
  their seqs order them within the step. No clock across recorders is
  needed. Lamport and domain clocks order steps (`order.*`) as before, and
  effects travel with their step.

## 4. Checking semantics

### 4.1 Matching

Per action, `match` says how the recorded list compares with the def's.
Recorded effects are normalized in Go, deterministically, before
rendering. The comparison is a plain Quint `==`.

| match | the recorded list is | catches | allows |
|---|---|---|---|
| `exact` (default) | as recorded, in order | missing, extra, duplicated, reordered, wrong payload | nothing else |
| `retried` | with consecutive identical effects collapsed into one. Identical includes every `same:` field, so a retry is the same request under the same key | missing, extra, reordered, wrong payload, a retry under a fresh key (`NoIdempotencyKey`, `freshKey`) | the code's own retries of one effect |
| `set` | deduplicated and sorted, compared as `Set` | missing, extra, wrong payload | order and repeats |

A `same:` field that differs between consecutive tries of one constructor's
effect is named in the message. That is the idempotency-key rule, checked
without the model knowing the key's encoding. Quint has no string
operations, so `"cap:" + order` is not the model's to compute. A field the
model does fix (the amount, the order) is compared whatever the tries.

### 4.2 What is not compared, said

- **Effects of kinds the binding does not map** are not compared. The
  report lists each one with its count ("not compared:
  `store.shop.write stock/*` ×14"). This makes the scope of the check a
  listed assumption (CW3), instead of an unstated one.
- **Effects made by no step, or from a goroutine outside the run,** are
  listed under the run, as `Recorder.Interrupted` steps are. An escaped
  effect follows qobs's strict rules: it fails the run that started its
  goroutine.
- **A step whose effects were not observed** (§3.1) is a binding error, not
  a pass.

### 4.3 Verdicts and messages

An effects mismatch is a mismatch of the step's expect. It is claimed with
`qtest.Mismatch("pay")` and shrunk like any other verdict. The report adds
the two lists and their first difference, in the style AGENTS.md asks for:
name the step, the action and its picks, the field, and the fix.

```text
step 7 (pay, seq 31, order="o1"): its effects are not the model's payEffects("o1"):
  made     [Capture({ order: "o1", amount: 2 })]         (rpc.provider.request at seq 29)
  expected [Capture({ order: "o1", amount: 20 })]
  first difference: effect 1, field amount: 2, expected 20
  Fix the request, or payEffects if the model is wrong; the effect's fields come from
  effects.kinds.Capture.fields in checkout.binding.yaml.

step 7 (pay, seq 31, order="o1"): effect 1 was tried twice under two keys (match: retried, same: [key]):
  try 1 key "cap:o1:1" (seq 29), try 2 key "cap:o1:2" (seq 33)
  A retry must send what the first try sent; a fresh key per try lets the provider apply both.

step 4 (place, seq 12, order="o1" qty=1): an effect was made after the step completed
(queue.payments.publish at seq 15, task "publish" started by the step):
  match: exact compares only effects made before seq 12's completion; if the design publishes
  later, state it as an owed effect (docs/io-specs.md §2.3) and judge it at its site.

step 9 (pay, seq 40): the step recorded no effect scope, and the binding gives pay effects:
  record it with qobs.Invoke (its context carries the scope), or drop effects for pay.
```

For owed effects (§2.3), the verdicts are the model's own:
- an effect no step owed is `Rejects("published")` at the site;
- an outstanding obligation fails the eventual val on a settled trace;
- a repeat beyond the delivery's multiplicity violates the invariant.

### 4.4 Quint or Go

The comparison is in the replay (§2.2), so quint and the evaluator judge
the same text. Linearize, hidden actions and every other search keep their
meaning: a candidate order's verdict includes its steps' effects. The
Go-side work is structural:
- attaching records in Reconstruct;
- normalizing by `match`;
- the issues of §4.2 (an effect whose cause is not in the input is no
  verdict: "an effect of invocation seq N, which the input lacks");
- the first-difference text.

A separate Go-side checker was considered. It would be faster to
prototype, but it would be a second judging path with its own semantics,
which ARCHITECTURE §4 exists to avoid.

## 5. Where it lives in the workflow

- **Trace validation.** The binding's `effects:` applies wherever steps are
  read (`gotogether validate`, `qtest.Record`), as long as the log has effect
  records. On the real tier that means a recording transport or convert
  rules over client spans (§3.2).
- **Explorations** (`qtest.System`). The doubles record effects
  automatically, so a simulated system needs only its model's defs and
  its binding's kinds. `Exhaust`, `Sweep` and Hegel's shrinking treat the
  verdict as any other.
- **connect MBT.** `.Emits(action, def)` on `connect.Bind`. The step's
  context carries the scope, the implementation's doubles or fakes record
  into it, and the driver compares after the method returns. Effects an
  implementation makes on its own goroutines after the call returns need
  CW11's primitive (wait for the implementation's next point) before they
  can be counted complete.
- **Mutants that prove it.** The checks below each have a mutant that
  survives without them:
  - `captureUnits`, planted in checkout: the capture's amount in units.
    Today it conforms in every run (§1.3). With `payEffects` it is
    `Mismatch("pay")` in every run that pays.
  - qweave's `drop call` over a `Publish` or `Call` statement: already
    woven; its survivors in `TestMutationOfTheShop` are the measure.
  - two new qweave operators, a **duplicated call statement** and **two
    adjacent call statements swapped**: the duplicate and reorder
    classes.
  - `qtest.MutateSteps` gains effect operations (drop, duplicate, swap,
    corrupt a field of an effect record): observation mutants that show
    the binding compares what it claims to, without a planted bug.
  - checkout's `NoIdempotencyKey` is killed by any retry (a lost request
    or a timeout), not only by a lost response after a commit.
  - payd's `noForwardKey` is a payload mismatch at the first capture, once
    payd has a scope (Q9).

  Each contrast forces its prerequisite and checks the faithful code under
  it, as AGENTS.md asks of planted mutants.
- **Cost.**
  - One record line per effect, written only when the binding asks.
  - A linear join in Reconstruct.
  - One list equality per step with effects, in the evaluator's fragment
    (lists of records). No state is added, since defs are not variables,
    so `simulate -graph` is unchanged.
  - The verdict cache key grows by the step's effects.
  - P1's acceptance measures checkout's exploration time with and without
    effects. Its exit criterion is that the difference is reported, not
    assumed.

## 6. Alternatives considered

- **Effects as observations** (`obs.sent` recorded by the code). This is
  what bindings can do today. The code then reports its own effect from
  the same variable it sent, which is broadcast's `gossip` record. A
  mutant that sends something else and records the intent passes. It is
  rejected because the site that sees the effect is the double, not the
  code. ROADMAP CW makes this a rule: "Effects are observed where they
  happen".
- **Effects as steps of their own, related by ghosts** (today's pattern,
  §1.2). It is sound and needs no new machinery. It is kept, and is §2.3's
  form for asynchronous effects. As the only form it leaves the three gaps
  of §1.2. Every model writes its own relation, often partially (wallet's
  first move, payd's count), and "what this step must send, now" waits for
  a settle.
- **Effects as their own model.** A second Quint module of the protocol
  between the service and each dependency, checked on the effect records
  alone. This is closer to a session type. It duplicates the service
  model's knowledge of what each step owes, and two models must then agree.
  It is not chosen. A single-dependency protocol (a client library's
  contract) can still be a binding of its own, over a `qobs.Effect` stream
  read as steps.
- **Protocol projection from a Choreo model, as Gobra does from Tamarin.**
  Choreo transitions already list their effects (`Transition.effects`,
  `Broadcast`, `Send`). Projected per node, they are each node's I/O spec,
  and twophase could compare its nodes' messages for free. It is the
  faithful analogue and the most attractive long-term form for
  message-passing models. Two things block it now:
  - Choreo's chosen transition is internal to `step_with`, so the binding
    would need the transition named (it is, per action, in twophase) and
    the effects exposed through a def, as `stage(n)` exposes state
    (AGENTS.md's Choreo gotcha).
  - Most of the examples are not Choreo models.

  It is P4, built on the same records and matching.
- **A Go-side effect checker with its own verdicts.** It was rejected in
  §4.4.
- **Generating the effect defs from the code.** It is rejected by CW's
  first rule ("never the model's transitions from the code's branches"):
  the spec must stay independent of what it checks.

## 7. Phased plan

| phase | scope | size | exit criterion |
|---|---|---|---|
| **P0** | Document today's pattern as a gotogether-patterns reference: effects as site steps with complete payloads, related by a ghost, and the gaps of §1.2. Plant `captureUnits` in checkout and record that it conforms, as a measured-once baseline. | S | The reference and the baseline in checkout's README |
| **P1** | Synchronous effects under simulation: the effect record and Reconstruct's attachment (rules 1 and 2), `qobs.Effect` and `Invoke`'s scope, recording in `qenv.Service` `Call`, `qenv.Queue` `Publish`, simlog `Produce` and qhttp `Transport`, the binding's `effects:` (kinds, defs, `exact`/`retried`/`set`) rendered into the expect, the verdict key, the report text, schema and API snapshots, and a fixture model in `testdata/`. Checkout gains a `pay` step (`qobs.Invoke` around `pay`) and `payEffects` | M | `captureUnits` is `Mismatch("pay")` in every run that pays. `NoIdempotencyKey` is killed in strictly more runs than today (both measured). The faithful shop conforms at every level. The `MutateSteps` effect operations are killed. The exploration cost is reported |
| **P2** | Commit writes as effects (`qenv.Store`, qsql's changes) of commit-recorded steps. `connect.Bind`'s `Emits`, with its static check (the def exists and returns a list of the model's effect type, internal/qeval's `TypeOf`). qweave's duplicate and swap operators | M | With a mechanism-level spec (the outbox row in the placing commit), `DualWrite` is caught at `place` in every placing run. A bound connect test catches a dropped send. Checkout's survivors under `TestMutationOfTheShop` are compared before and after |
| **P3** | Owed effects: key matching from cause to site declared in the binding, the vendored helpers module (CW6), recording on the real tier (a client `RoundTripper` in qobshttp, convert rules for client and producer spans) | L | Wallet's ledger with every move posted, and payouts' `Links`, expressed by binding and model alone. One real-tier example's effects come from its spans |
| **P4** | Choreo projection: twophase's transition effects compared with its nodes' sends | M | twophase's broadcasts are checked. A mutant that broadcasts the wrong decision is caught at its step |

Each phase is its own decision, as CW items are. Only P1 is needed to learn
whether adopters write effect defs, before P2–P4 are committed.

## 8. Open questions for the maintainer

1. **The def's state.** A def over the post-state, as `expect` and
   `Returns` use, is proposed. A Gobra-faithful pre-state form needs a
   replay slot before the action. Is post-state enough?
2. **Mechanism or interface.** Should an effects spec name internal effects
   (an outbox row in a commit), or only effects at the system's boundary (a
   capture, a publish)? The first catches `DualWrite` at once. The second
   stays a requirement rather than a design (gotogether-patterns'
   spec-level against mechanism-level models).
3. **The verdict's name.** Reuse `Mismatch` (an expect failed), or add a
   narrower claim, so that a test says it expects an effects mismatch and
   not a state one?
4. **Default attribution.** Should rule 2 (the task's next step) be on by
   default? It needs no annotation, but it can misattribute where a task
   records another component's step. The alternative is to attribute only
   explicit `Invoke` scopes and list everything else.
5. **Pending calls.** Do a pending invocation's recorded effects rule out
   `ResolvePending`'s "not taken" placement, or is a send without a landing
   consistent with either?
6. **Opt-in.** Should doubles record effects only when the run's binding
   declares kinds (§3.2's proposal), or always, for evidence and coverage
   (`Exercised` over effect kinds)?
7. **The format.** Effect records as an optional record kind in
   `gotogether.steps/1` (no new major by DESIGN §63's rule), or a new major
   so that an older reader refuses logs whose effects it would drop?
8. **grist.** Should grist's declared calls be re-expressed on core effects
   once P3 lands, or stay grist's own (its generator also writes the
   rules)?
9. **Unmodified services.** payd records nothing of its own. Its effects'
   scope would be the inbound request `qhttp.Server` serves, which is no
   model step of payd's model. Is a server-side scope an action the model
   must gain, or does the binding relate it to the client's `request` by
   the idempotency key (rule 3)?

### 8.1 Decided (maintainer, 2026-11-25)

1. **Post-state** is enough for P1. A pre-state form is added only when a
   real case needs it.
2. **Boundary effects by default**; an internal effect (an outbox row) may
   be named when a binding opts in, knowing it then specifies the design.
3. **Reuse `Mismatch`**, its message saying the effect, so existing
   `.Expect` claims work unchanged.
4. **Rule 2 on by default** (the task's next step), as coverage does, the
   report saying how each effect was attributed.
5. **A pending call's recorded effect rules out "not taken"**: an effect
   that landed means the call took effect.
6. **Doubles always record effects**: cheap, and evidence and coverage use
   them.
7. **The format: whatever shape is cleanest.** Backward compatibility is
   not a goal here: an optional record kind in `gotogether.steps/1` if that
   is natural, a new major if a cleaner shape needs one. No compatibility
   shims or dual readers for old logs.
8. **grist**: decided after P3, not now.
9. **Unmodified services**: effects are recorded at their adapters (qhttp,
   qsql), as payd's steps already are, and related to the client's request
   by its idempotency key (rule 3); the model gains no server-side action.

## 9. Relation to the roadmap

- **CW3, listed assumptions.** The kinds a binding does not compare are
  listed in every report (§4.2). An effects spec turns "the model says
  nothing of what we send" into a reviewed line.
- **CW5, Bind.** `Emits` completes `Returns` (a result against a def) and
  `Observes` (a result against a pick) with a third comparison: what the
  call sent. The static half checks the def's type, as it checks actions
  and picks.
- **CW6, the environment library.** Its doubles record the effects (§3.2).
  The obligations helpers are its first Quint module.
- **CW7, one entry.** The effects spec belongs to the model and binding, so
  connect, Hegel, the differential and explorations all check it, with no
  per-mode code.
- **CW8, causal links.** Its links (request, effect, adoption, display)
  need what P3 needs: a cause carried with the effect. P1's scope is the
  in-process half and P3's keys the cross-process half. One mechanism
  serves both.
- **CW9, one name for a broken variant.** `captureUnits` and its model-side
  twin (`payEffects` with the units) are a pair CW9 would name once.
- **CW10, into operation.** An effects mismatch is local to one step,
  cheap to check in a monitor and needs no settle. It is the first kind of
  production check that judges every request rather than waiting for
  quiescence.
- **CW11, asynchronous drivers.** Counting the effects a driven method
  makes on its own goroutines needs CW11's wait. P1 covers synchronous
  methods.
- **LI, lock invariants.** Both take an idea from Gobra. LI checks memory
  at the point a critical section publishes it. I/O specs check what
  leaves the process. Together they cover Gobra's two runtime-checkable
  halves (invariants at release, I/O by permission). Neither needs the
  other. A step that writes before it checks is visible to I/O specs when
  both are effects, and to LI when the check guards a lock.
