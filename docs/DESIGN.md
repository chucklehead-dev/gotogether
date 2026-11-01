# Design: model-based testing and runtime conformance for Go against Quint

The structure as it is now (the modules and their dependency rule, the
core's layers, the data flow of a check, the formats) is explained in
[ARCHITECTURE.md](ARCHITECTURE.md), which this record does not restate:
its sections are the decisions, in the order they were made, and why.

This is the design record of the `quintgo` spike, which started in
casselc/observability (`quintgo/`, against the otel-chdb models) and moved
here as quint-connect-go, now gotogether. Sections 1–8 are the spike's research and
decisions, lightly edited for the new layout (the edgePublish model is
vendored in `examples/edgepublish/model`). Section 9 covers what was added
in the move: quint-connect parity for `connect`, the Hegel integration,
and span conversion.

The spike's verification ran on Go 1.25, Quint 0.32 (`--backend
typescript`) and Orchestrion v1.13.1. The migrated code is tested with
Go 1.26, Quint 0.33 (both backends) and hegel-go v0.9.27. Anything marked
**speculative** or **unverified** was not run.

The ask had two parts:

1. **MBT for Go**, like quint-llm-kit's quint-connect flow for Rust: drive Go
   code from Quint traces and compare states; possibly via comment
   annotations, code generation or Orchestrion.
2. **Runtime conformance à la PObserve**: the running Go program emits the
   model-relevant facts as OpenTelemetry span events, a reconstructor turns
   live telemetry into a model trace, and that trace is checked against the
   Quint model: that each transition is valid, and the invariants.

Two later requirements: the toolkit must be generic (edgePublish is the first
binding, not the only target), and usable as a Go library from tests, e.g.
property-based state-machine tests. A third came after review: running apps
must be able to emit a checkable trace directly, without reconstructing it
from OTel spans, for tests, simulations, deterministic replays and CI. §6b
covers it.

## 1. Prior art

### quint-connect and the kit (Rust)

- **Trace generation.** Quint produces traces: `quint run --mbt` (random
  simulation) or a deterministic `run` test. `--mbt` adds two fields to every
  ITF state: `mbt::actionTaken`, the innermost action that fired, and
  `mbt::nondetPicks`, a record of `Option`s holding each `nondet` value
  picked on the way. (Checked on edgePublish: `writeTable` shows up with
  picks `x`, `o`, and also the step-level picks `w` and `p` that it did not use.)
- **Driver.** `quint-connect` is a Rust crate. `#[quint_test(spec, test, main)]`
  replays a named `run`; `#[quint_run(spec)]` replays random simulations. Both
  call the Quint CLI to get ITF. The user implements a `Driver`:
  `step(&mut self, &Step)` dispatches on the action with the `switch!` macro,
  as in `switch!(step { init => self.init(), MyAction(a, b?) => self.my_action(a, b) })`,
  and a state check compares the spec state (`step.get_in(path)`, deserialised
  with `itf`/serde) with the implementation's, converted with `From`.
  `QUINT_SEED` and `QUINT_VERBOSE` control reproduction.
- **The kit.** `agentic/scripts/quint_connect/project_scaffold.py` runs
  `quint compile --flatten false`, derives Rust types from the spec's type
  declarations, and fills templates (`driver.rs`, `state.rs`,
  `transition.rs`, `types.rs`). The templates assume a Choreo-style spec,
  where the state is `s.system` per process and each transition has a
  `transition.label`. The `mbt-validator` agent then adds one transition per
  TDD cycle: run the test, see "Unimplemented action", add the label, the
  `switch!` arm and a handler that asserts every effect the spec emits, and
  stop. The rule throughout: the spec is ground truth; fix the code, not the
  test. The template pins a private quint-connect branch named
  `connect-and-observe`, a hint that Informal is working on the observation
  direction as well (**speculative**).
- **Quint's docs** list "trace validation" as the inverse of MBT: production
  data, model expectations. They have not written that page yet.

### PObserve (P language, AWS)

A log parser turns each log line into `PObserveEvent{key, timestamp, event,
logLine, customPayload}`. A **sequencer** sorts events by timestamp into one
global stream. A **demultiplexer** routes them by `key` to instances of P
spec monitors, which assert safety (and bounded liveness) over the events
they observe. PObserve runs post hoc, on logs, in test and in production. Its
monitors are hand-written P `spec` machines that watch events; they do not
replay the system model.

What gotogether keeps from it:
- partition keys: `quint.actor`;
- post-hoc checking of real telemetry.

What it changes:
- It replays the model's own transitions, so it checks membership in the
  model and the model's invariants. No separate monitor has to be written.
- Ordering does not trust wall clocks, which is PObserve's weak point:
  see §4.

## 2. Options for Go

| | Annotations + codegen (`go generate`) | **Orchestrion aspects** (`//quint:action`) | Library only (`qobs.Record`) |
|---|---|---|---|
| Code change | comment, plus generated wrappers the code must call, or a rewritten copy of the source | a comment on a function | one call per step |
| Build | extra generate step; generated files to keep in sync | `orchestrion go build` / `go test`; a plain build ignores it | none |
| Off by default | only with build tags | **yes**: without Orchestrion the comment is inert and the binary is unchanged | needs a runtime switch (`qobs.SetGlobal(nil)` makes it about 22 ns a call) |
| Granularity | function | function (statement level is possible, **unverified**, see below) | any statement |
| Timing of the step | wrapper returns | function returns (deferred advice) | wherever the call is |
| Tooling cost | a Go AST rewriter to write and maintain | none (the aspect is 70 lines of YAML/template, and already written) | none |
| Build time | small | cold ~70 s (it weaves the standard library too), warm ~2 s | none |

**Recommendation: Orchestrion for the annotations, with qobs as the library
under it.** The directive is the whole integration. It costs nothing unless
you build with Orchestrion, and it is generic: the aspect ships with qobs, not
with the project. The library path stays open for steps that are not
function-shaped, and for tests that record explicitly. A `go generate`
rewriter would be a less capable Orchestrion that we would have to maintain
ourselves.

What makes it "generated from the model" is not advice generation. The
aspect needs nothing model-specific. The model drives:
- `gotogether scaffold`: a binding skeleton, with every action's signature taken
  from `quint typecheck`;
- `gotogether lint`: the binding against the model (action names, parameter
  names, constants, invariants), and the Go annotations against the binding
  (every field a template uses must be recorded).

Binding scaffolding now separates runtime property roles from model simulation
claims. Authors select safety vals with `-invariants` and completion vals with
`-eventually`; they must not overlap. Model guarantee/allowed-violation
classification is optional and explicit through `-holds`/`-allows`, restricted
to selected safety vals; when either is provided, the two lists must cover all
selected invariants exactly once. Without them no model-guarantee block is
enabled. Unselected Boolean vals are listed, not asserted.
A model with Boolean candidates requires a selection or the explicit
`-no-properties` acknowledgment. Validation happens before output creation.
This corrects the HTTP/SQL rehearsal's observed misclassification of `completed`
as an invariant; names never determine semantics.

The library starter extends the same model-first workflow to ordinary
synchronous kernel calls. Its mutable store returns immutable snapshots;
recording occurs in an observing test driver, and production code imports only
the standard library. The finite model retains captured values independently of
later writes. A single source overlay aliases the mutable map, so a real old
snapshot read disagrees with the frozen binding. Snapshot IDs are driver-chosen
labels for actual objects, not invented return values. Generated evidence and
manifest replay use fresh executions. This is a concrete template, with explicit
adaptation work for another API, rather than automatic model derivation or
independent-adopter evidence.

The starter's first bounded adaptation uses the published SDK and Goo dependency
for strict definition reading. A fresh agent authored 16 fixed YAML inputs and an
observation projection from the actual returned definition/error, then froze the
model, binding, driver, cases and module pins before receiving a source contrast.
The model graph is complete for those case IDs (17 states, 272 transitions), not
for arbitrary YAML or every validator branch. Structural model invariants and
binding observation checks establish different facts; the injected regression is
caught by an observed-state mismatch after unknown fields become accepted.
See [the recorded exercise](../internal/adoptiontest/goo-read/README.md).

Two workflow lessons are retained. An expected API rejection belongs in the
contract's observed data; fixture, recorder, compiler and tool failures must not
be relabeled as successful rejections. Also, Go 1.27 refuses overlays beneath its
module cache. Dependency contrasts use an exact temporary production-package
copy and a separate modfile plus source overlay. The baseline project remains on
its published pins, and source hashes identify the injected change. Neither the
initial rejected overlay nor the later planted contrast is a production finding.
This exercise supplies no evidence for a new runtime facade.

The adaptation exposed a report-boundary gap even though the runtime adapter was
sufficient: agents could obtain graph completeness/counts only by interpreting
English summary text. The canonical `validate/model.Report.Doc()` projection now
constructs `validate.Document` directly from native results. `graph` identifies
`model_state_graph` scope and retains states, starts, transitions, depth,
completeness, cut/stopped reasons, configured state/depth bounds, deadlock and
terminal/quiescent counts, and fairness/stopping selections. These describe a
model graph, not recorded executions or all workloads.

Each model check carries a typed `bound`; its `kind` and `proves` describe
coverage, independently of success. A sampled check is never a proof; exhaustive
coverage within a depth remains bounded by that depth. A counterexample can fail
a check even when exploration is incomplete. Reachability `possible` data retains
found/impossible separately from the evaluator's search completeness, cut, node
count, depth and witness path. An unperformed model check retains `skipReason`
and no-verdict status, including unsupported modes that are skipped. A partial
exploration reports its native cut/stopped scope instead; actual errors remain
errors. CLI rendering uses the same
projection, so changing display text cannot change machine facts. Notes and
details provide context rather than a second machine interface.

These optional fields extend `gotogether.report/1` without an alternate legacy
representation or prose-parsing fallback. The TUI already consumes native graph
results, so this change needs no navigation format migration. The adaptation's
portable checker now consumes native scope and proof data; faithful and contrast
bundles replay after relocation with the original project unavailable, and a
missing observation remains error 2 rather than mutant credit.

The core lint report is now assembled before either text or JSON rendering.
`qlint` retains the lightweight syntax rules used by analyzers; the `lint`
package composes bindings, conversion rules and schemas above it.
`lint.Inspect` records selected targets and their dependency phases, then runs
independent targets even if a binding fails its schema. Each requested phase is
`pass`, `fail` or `blocked`; blocked model/source checks retain their reason.
The current machine contract is `gotogether.lint/1`, with rule-site codes,
severity, message, fix, analysis basis and native source positions where known.
No renderer extracts identity or position from prose. Source inventories remain
structured evidence rather than proof that dynamic recording sites were found.

Completeness and the command's exit status are derived from that report. Exit 0
means clean performed checks, 1 means authored findings (including invalid schema
that blocks dependent phases), and 2 means operational input/tool failure or an
invalid request. A completed failing check is complete; a blocked check is not.
The CLI scans Go syntax, while qanalysis can use Go type information. Model
metadata/template inspection is not trace conformance. Binding's string lint
helpers render the same typed findings for existing Go callers; they do not
introduce a second machine report format. Grist preflight remains its separate
maintenance contract; LSP/SARIF integration is not part of this increment. The
[executable walkthrough](lint-onboarding.md) exercises the public CLI contract.

Rules the annotation route imposes, learned in this spike:
- **Function granularity.** A model step must be a function whose return is
  the step's linearization point. In the stand-in, the three S3 writes, the
  batch start and the rotation are small functions. In the real
  `publish.go`, `writeParquet` and `seal` already are. The table insert and
  the manifest write are inline in a closure, and the rotation is inside
  `acquire`. Orchestrion's `directive` join point also matches expressions
  inside an annotated statement, so a statement-level `//quint:step` that
  wraps a call expression looks feasible. **Unverified**: not built.
- **What an expression may use.** Advice runs deferred at return, so an
  annotation may use parameters, the receiver and named results, but not
  locals.
- **Record before spawning dependent work.** Recording at return is correct
  only if nothing that depends on the step was started inside the function.
  The stand-in's `rotate` records before `acquire` starts the background
  seal. Otherwise the seal's step could be recorded before the rotation's,
  and the model has no seal of an unrotated generation.

## 3. Telemetry schema (generic)

One span event per model step, named `quint.step`, on a short span
`quint <action>` that is a child of the span in the context. Attributes are
on the event. For the non-argument keys, readers fall back to the span's and
then the resource's attributes, so a deployment can hoist e.g.
`quint.process` into the resource.

| Attribute | Type | Meaning |
|---|---|---|
| `quint.action` | string | model action name (required) |
| `quint.seq` | int | per-recorder sequence number, **dense** (1, 2, 3, …): a hole means a lost step |
| `quint.recorder` | string | id of the recorder, the scope of `quint.seq` |
| `quint.actor` | string | partition key: which model instance the step belongs to (e.g. `edge-1/traces`) |
| `quint.process` | string | incarnation of the actor (e.g. the producer epoch); a change means the process restarted |
| `quint.thread` | string | program-order key: steps with the same thread are totally ordered by seq (default: the qobs thread in ctx, else the parent span id) |
| `quint.outcome` | string | `ok`, `error`, or an error's own classification (`QuintOutcome() string`, e.g. `ambiguous`) |
| `quint.error` | string | error text |
| `quint.arg.<name>` | string, int, bool, double | an observed parameter, in implementation terms (ids, not model values) |
| `quint.obs.<name>` | same | an observed projection of the post-state (e.g. `obs.batches`: what `_sealed.json` said) |
| `quint.order.<domain>` | int | a domain logical clock: steps of one (actor, process) that share the domain are ordered by its value |

Observations stay in implementation terms. The binding holds the
abstraction function (interning, templates), so the same telemetry can be
checked against another model or design without redeploying.

**Sampling.** Step spans must not be sampled out. qobs takes its own
TracerProvider, and the demo uses `AlwaysSample`. With parent-based sampling,
a step under an unsampled request would be lost, so give qobs a provider of
its own. A dropped export, or a BatchSpanProcessor dropping on a full queue,
shows up as a seq gap. The validator reports it as an `error` issue: the
verdict is unsound. **Not built:** carrying steps as OTel log records
instead, which pipelines usually do not sample.

## 4. Reconstruction (`qtrace.Reconstruct`)

Input: an unordered bag of steps from any number of recorders, processes and
actors (OTLP/JSON files, or in-memory SDK spans).

1. **Dedupe** on (recorder, seq): exporters retry and collectors fan out.
2. **Gap detection.** seq is dense per recorder. Any hole is an `error`
   issue for every actor that recorder reported.
3. **Partition by `quint.actor`.** Each actor is replayed as its own model
   instance, like PObserve's key. Actors that share one model instance, such
   as edgePublish's writer and consumer, need the cross-process edges below.
4. **Order** each actor's steps by a topological sort of a happens-before
   graph:
   - hard edges, **program order**: steps with the same `quint.thread`, by seq;
   - hard edges, **domain clocks**: steps with the same
     `quint.order.<d>` in one (actor, process), by value. Example: batch ids
     come from an atomic counter, so their order is the order pushes
     started, even when the `pushStart` steps were recorded in the other
     order. The seq alone cannot be a hard edge: the id allocation and the
     recording are not one atomic moment. Treating seq as a hard edge next
     to the domain clock produces cycles (there is a unit test);
   - **preference** among steps with no hard edge between them: recording
     order, i.e. seq within a recorder and time across recorders, with time
     made monotone per recorder. Such steps were concurrent. Choosing either
     order is sound when their model actions commute, and the model's
     interleaving semantics already requires that of steps on disjoint state
     (e.g. two pushes' writes). If a recording A happens-before a recording
     B in the Go memory model, then seq(A) < seq(B), so causality inside a
     process that passes through a recorder is preserved;
   - a **cycle** (contradictory keys) is reported and broken at the
     earliest step.
5. **Incarnations.** Processes are numbered in order of first appearance.
   A process change becomes the binding's `restart` action (`crash` for
   edgePublish), the step a dead process cannot report. If the steps of two
   incarnations interleave, that is an `overlap` error: two writers alive at
   once, which the model does not allow (it was a warning, and the old
   incarnation's step replayed as a third boot). The round trip in §6 caught a driver
   bug this way.
6. **Missing information.**
   - A missing step: a gap, reported.
   - A missing field: the template fails, naming the step.
   - An unobserved variable: the model computes it. Replay is a deterministic
     `run`, so the model fills in every hidden variable. Here that is the
     queue, the in-flight pushes, and the consumer, which stays at init.

Across processes (**not built**): the writer–consumer link would be a hard
edge from `writeManifest` to the `claim` that read that manifest. Carry the
writer's `traceparent` in the manifest JSON and emit a span link. This is
the one ordering fact wall clocks cannot supply.

## 5. Validation (`binding` + `validate`)

The binding generates a Quint module per actor:

```quint
module observed {
  import edgePublish(PAYLOADS = Set(1, 2, 3, 4, 5), MAX_BATCH = 5, MAX_GEN = 2, ..., CRASHES = true).* from "../../model/edgePublish"
  pure def px(...) = ...                       // binding defs
  run conformsTest =
    init
    // [9] seq 10 epoch-A pushStart(batch=4, gen=g20260924T100000, payload=req-1)
    .then(pushStart(2).expect(writer.pushes.contains(px(2, 1, 4, 1, Encoded))))
    // [15] seq 16 epoch-A rotateGen(gen=g20260924T100000)
    .then(rotateGen.expect(writer.rotated.contains({ epoch: 1, gen: 1 })))
    ...
  run sealMatchesManifestsInvTest = init.expect(sealMatchesManifests).then(pushStart(1).expect(sealMatchesManifests))...
}
```

`quint test --max-samples 1 --out-itf` runs it (since §56, only for a run
the Go evaluator does not pass: it decides the passes). Each step is on its own line,
so quint's error position maps back to the observed step (the test-time
`LineStep` table). Nested `A.expect(P)` inside `.then(...)` pinpoints the step
precisely. A top-level `.expect` would only point at the whole chain. Verdicts:

- **disabled** (QNT513, or QNT508 "Cannot continue to expect"): the model
  cannot take the observed step from the state it reached. The
  implementation did something the design does not allow.
- **state** (QNT508 in `conformsTest`): the step was possible, but the
  observed projection (`expect`) does not hold. The implementation's choice
  (a batch id, a generation, a seal's count) differs from the model's.
- **invariant**: the trace conforms, but a model invariant fails on it. This
  is a behaviour the design allows and the design's own properties reject.
  It is the model's known bug, happening in the implementation.
- **issues** from reconstruction (gaps, cycles, overlaps) make a pass
  unsound.

A conforming run's ITF has the module prefixes stripped, and
`mbt::actionTaken` and `mbt::observedSeq` added, so the ITF viewer and
trace tools read it.

**Binding conventions**: what a model needs to be checkable this way.
- Actions parameterised by values that can be observed or derived. A value
  the model picks internally, such as the batch id in `pushStart(p)`, is
  checked with `expect` instead.
- Opaque ids need an abstraction function. `intern` renames them 1, 2, … by
  first appearance, per actor or per process, which covers ids that name
  things in creation order.
- One parameterless restart action, if processes can restart.
- A non-`ok` outcome must mean something: `outcomes` maps it to a model value
  used as `{{.outcome}}`, or to `skip` or `call` per action. If a failed step
  hits an action whose templates ignore `.outcome`, generation stops, so a
  failure is never replayed as a success. This rule came from the round
  trip: a failed seal was being replayed as a seal.
- Constants sized from the trace (`set`, `max`, `procs`), because model bounds
  like `MAX_BATCH` are small.

**Limits of the approach**:
- **Hidden nondeterminism.** The writer cannot tell `Fail` from `Ambiguous`,
  and only a test double knows. With a plain error mapped to `Fail`,
  `ambiguous-seal` would replay as a failed manifest write, and the seal
  undercount would be invisible. It is real in S3, missing in the replay.
  - A deterministic `run` cannot branch. The sound treatment is to replay the
    error as `any { writeManifest(x, Fail), writeManifest(x, Ambiguous) }`
    and ask an exhaustive checker whether some resolution matches every
    later observation, and whether all resolutions satisfy the invariants.
  - That is Apalache: a step-indexed encoding with an `obsIdx` variable, the
    TLA+ trace-validation technique of Cirstea, Kuppe, Merz et al.
    **Not built.**
  - The cheaper route is to observe more. An S3 listing or bucket
    notifications become a second recorder whose steps pin the outcome.
- **Soundness is relative to the binding.** The abstraction function is
  trusted. A wrong template can hide a bug or invent one.
- **Checks are per actor, bounded by the trace, post hoc.** Liveness is not
  checked. Long traces need chunking: validate in windows, starting each
  from the ITF state reached by the previous window (**not built**). The
  typescript backend took 4–8 s per trace of ~30 steps, most of it Node
  start-up.

## 6. Capability 1: driving Go from Quint traces (`connect`)

`connect.Generate` runs `quint run --mbt`. `itf` decodes the result into a
navigable `Value` tree, and `connect.Driver{Init, Actions, Skip, Check}`
replays it. This is the Go counterpart of quint-connect's `Driver` and
`switch!`, with a map standing in for the macro.

The per-project part is the handlers and the state check. For edgePublish
(`examples/edgepublish/mbt`):
- **Scheduling through the effect seam.** Every store write blocks in a gate
  until the handler for that model step releases it with the model's
  outcome (`Ok`/`Fail`/`Ambiguous`). That reproduces the model's
  interleaving of concurrent pushes, S3 faults, rotations and background
  seals exactly, in real goroutines. `crash` releases everything in flight
  as `Fail` and starts a new incarnation.
- **State check.** After every step, the store's table rows, Parquet
  objects, manifests and seals (with batch counts) are compared with the
  model's `tableRows`, `parquet`, `manifests` and `seals`, via learned maps
  from implementation generation ids to model generations.
- **Skipped actions.** The consumer's actions have no implementation here.

Verified: 5 random traces of 30 steps replay against the faithful publisher,
and the manifest-first mutant fails on all 5. Random traces from the full
`step` spend most of their steps in `otherInsert` and consumer actions. A
writer-focused step in the model, or `--step` with a projection, would give
better coverage per trace (roadmap).

**Round trip.** `TestRoundTrip`, under `orchestrion go test`, closes the
loop: model trace → Go → recorded steps → `Checker.Check` → the same model.
5/5 validate. On its first run it found two real problems:
- a driver race: the dead incarnation's seal was recorded after the restart,
  which the reconstructor flagged as an overlap;
- the failed-step-replayed-as-success hole in the binding semantics.

## 6b. Emitting checkable traces directly

The OTel path suits production, where steps ride the telemetry pipeline next
to the app's own traces. Tests, simulations, CI and deterministic replays
need neither the SDK nor a collector. There are two additional paths, and
both are verified with the same verdicts as the OTel path.

**Native step log** (`qobs.JSONLSink` / `FileSink`, `qtrace.ReadJSONL`).
- One JSON object per step, whose keys are exactly the OTel attribute keys of
  §3, plus `quint.time_unix_nano`. Decoding reuses `qtrace.FromAttributes`,
  so the two formats cannot drift apart. It is the same data without the
  span wrapper:
  ```json
  {"quint.action":"writeManifest","quint.actor":"edge-1/traces","quint.arg.batch":1,"quint.arg.gen":"g20260924T100000",
   "quint.arg.payload":"req-1","quint.outcome":"ok","quint.process":"epoch-A","quint.recorder":"20147ffb9e1d",
   "quint.seq":2,"quint.thread":"t1","quint.time_unix_nano":1790292680572863711}
  ```
- It keeps implementation terms, so reconstruction (ordering, gaps,
  restarts) and the binding apply exactly as for spans. A step log can be
  re-checked against another binding.
- `load.Sniff` tells OTLP/JSON (`resourceSpans`), a step log
  (`quint.action`) and ITF (`states`) apart by content, so every command
  takes any of them.

**Model-level ITF** (`binding.Plan` → `StepsToITF` / `ObservedITF`; `validate.ValidateITF`).
- The binding is applied in-process: interning, restarts, outcome mapping and
  instance constants. The trace is written in the shape `quint run --mbt`
  writes, per state:
  - `mbt::actionTaken`;
  - `mbt::nondetPicks`: each parameter as `Some(value)`;
  - `gotogether::expect`: the binding's rendered post-state check;
  - `obs::<name>`: the observed projections.
  `#meta.gotogether` names the spec, module, constants, defs and invariants, so
  the file validates with no binding at hand.
- Turning a template into a pick needs a *value*, not an expression. Without
  a Quint evaluator in Go, argument templates must render to Quint literals.
  `itf.ParseQuint` handles records, `Set`/`List`/`Map`, tuples, constructors,
  ints, strings and bools, and rejects operator calls with a clear message.
  The edgePublish binding moved from `px(...)` to literal records for this.
  Its expects still use `px`.
- Validating an ITF replays `.then(action(picks…))`, matching picks to
  parameters by name. After each step it checks `gotogether::expect` and
  equality for **every model variable the state carries**. So the same code
  validates three kinds of trace:
  - a gotogether-emitted ITF (no model variables; expects only);
  - the enriched ITF of an earlier validation (full model state at every
    step);
  - raw `quint run --mbt` output (`-instance currentDesign`: its constants
    are read from the instance module's source, because Quint does not
    re-export an instance's import).
  Tampering with one variable in one state of a `quint run --mbt` trace is
  reported as a state mismatch at that step. (Verified.)
- Picks are matched by name, which works when the model names its `nondet`
  bindings after the action parameters they feed. edgePublish does. That is
  a convention for the `--mbt` direction, not a guarantee of Quint's.

**Symmetry with `connect`.** A validated run's ITF now carries
`mbt::nondetPicks` next to the model's states, so it has the same shape as
`quint run --mbt` output. `connect.Driver` consumes both.
`scenarios.TestScenarios` replays each observed conforming trace
(`happy`, `rotation-race`, `ambiguous-seal`) through the edgePublish MBT
driver, with store state compared after every step. A trace observed in a
run can be replayed deterministically on a new build. Model traces drive
Go; Go traces go back to the model; either can drive Go again.

**Record then check** (`qtest`).
- `Record(t, binding, scenario)` makes the scenario's goroutine the run's
  (a recorder to memory plus a step log; the global one too with
  `qtest.Global()`), runs the scenario, writes the model-level ITF and
  validates; `Validate` takes steps recorded elsewhere. Either states its
  verdict with `Run.Expect` (`Conforms()`, or a mutant's `Mismatch`...).
- Checkers are cached per binding, so the model is typechecked once per
  test binary.

Costs, measured: the step log costs ~7 µs per step (reflection-based JSON
of a map, synchronous; `FileSink` is buffered), about the same as the OTel
batch path. It avoids the dependency, not the cost. A hand-written encoder
would be the fix if it mattered.

## 7. edgePublish mapping

| Model action | stand-in (`publisher/publish.go`) | real `chdbexporter/publish.go` | observed args | notes |
|---|---|---|---|---|
| `pushStart(p)` | `begin` (acquire + batch id) | `push`: after `acquire` and `nextBatch` | batch, gen, epoch; payload **not available** | order domain `batch` |
| `writeTable(x, o)` | `insertTable` | `s.Insert(g.ts.insert, …)`, inline in `withConn` | outcome ok/error | ambiguity not visible to the writer |
| `writeParquet(x, o)` | `writeParquet` | `p.writeParquet(s, signal, gen, batch, rb)`, already a function | same | annotatable as is |
| `writeManifest(x, o)` | `commit` | `p.writeObject(s, manifestPath…)`, inline; `writeObject` also writes seals | same | needs an extraction, or `thread:`/statement-level |
| `rotateGen` | `rotate` | `acquire`, `st.cur = g` under `mu.Lock` when `old != nil` | gen | record before the seal goroutine starts |
| `sealGen(gk)` | `seal` | `seal(signal, g)`, already a function | gen, `obs.batches` = `g.batches` | `close()` seals without rotating: emit `rotateGen` first |
| `crash` | inferred | inferred from a `producer_epoch` change | none | |
| consumer actions | none | not in the exporter | none | a separate recorder in the consumer (roadmap) |

**Observable vs abstracted**
- Observed: batch ids, generation ids, epochs, write outcomes (as the writer
  sees them), and the seal's batch count.
- Abstracted: generation ids and payload names (interned); epochs (numbered
  by appearance).
- Not observable by the writer:
  - whether a failed write landed;
  - payload identity across retries. The collector's queue does not expose an
    item id to the exporter. The options are a content hash of the pdata
    (CPU cost), an upstream change to exporterhelper, or dropping payload
    identity, which gives up the retry-duplication properties;
  - the queue itself (the model computes it);
  - the consumer.

**Model/implementation gaps the spike exposed**
- The model rotates on its own. `publish.go` rotates only when a push
  arrives, so an idle generation stays open until `Close`. The model is
  more permissive, which is fine for replay. MBT needed a `Tick` on the
  stand-in.
- `close()` seals the current generation without a rotation. The model can
  only seal rotated generations, so `Close` must be recorded as `rotateGen`
  followed by `sealGen`, which needs `MAX_GEN = observed + 1`.
- Configurations without `store_tables` or Parquet skip writes that the
  model always performs. They need a model flag, or a binding that emits the
  skipped writes as no-ops (neither built).
- The model's `nextBatch` starts at 1 per epoch, as do `publish.go`'s
  per-namespace counters. That coincidence lets batch ids pass through
  uninterned.

**Applying it to the real exporter** (not done, per the brief: no changes to
`chdbexporter`), in about five small edits:
1. Extract `insertTable`, `commitManifest` and `rotate` as functions.
2. Annotate those three and the existing `writeParquet` and `seal`,
   with `thread:env.batch` (no ctx is threaded through `push`).
3. Decide on payload identity.
4. Add `orchestrion.tool.go`.
5. Build the collector with `orchestrion go build`.

The stand-in's binding should then apply unchanged.

## 8. Roadmap

The spike's roadmap is done. What came after is in docs/HISTORY.md; what
is open, in docs/ROADMAP.md.

## 9. After the move: quint-connect parity, Hegel, conversion

### 9.1 `connect` and quint-connect

`connect` follows the Rust crate's model:
- Quint generates traces with `quint run --mbt` (`Run`) or `quint test`
  (`Test`).
- A `Driver` dispatches on the action taken (`Switch`, standing in for the
  `switch!` macro).
- Nondet picks decode into Go types (`Pick[T]`, `OptPick[T]`, through
  `itf.Decode`, the analogue of serde with the `itf` crate).
- A `StateDriver`'s state is compared with the model's after every step,
  `init` included.

Deliberate differences, each removing a quint-connect gotcha:

| quint-connect | connect |
|---|---|
| one driver instance for every trace; `init` must reset it | `newDriver()` per trace |
| `QUINT_SEED`/`QUINT_VERBOSE` read with `option_env!` (compile time) | read at run time, plus `QUINT_MAX_SAMPLES`, `QUINT_MAX_STEPS`, `QUINT_BACKEND`, `QUINT_BIN` |
| traces read in `read_dir` order | sorted by sequence number |
| state diff only at `QUINT_VERBOSE>=1` | the failing field paths, always (`.Balances[bob]: model 16, implementation 17`), plus the step |
| `quint test` traces fail with "Missing mbt::actionTaken" | the same limit, with an error that says why and what to do (`NondetPath`, or `connect.Run`) |
| `--n-traces` = `--max-samples` | same |

`Step.State` is `Config.StatePath` applied to the variables. Module
qualifiers are stripped from variable names (`m::choreo::s` is `s`), and
a first path element that still carries its qualifier is matched too.
`NondetPath` reads the action and its picks from a sum-type variable, as
quint-connect's `Config.nondet` does.

### 9.2 Hegel (`qhegel`)

hegel-go (v0.9.x) runs its engine in-process (libhegel, through purego),
with no Python and no server. Its stateful API discovers `Rule*` and
`Invariant*` methods by reflection. Two directions are useful with a
Quint model.

**Hegel drives, the model judges.**
- Hegel's rules call the implementation with drawn inputs, including
  invalid ones. The implementation, or the rule, records model steps.
- At the end of each case the run is validated through the binding.
- In `Each` mode a rejected run fails the test case, so Hegel's shrinker
  minimises the run against the model. This is the only direction that
  finds code accepting what the model forbids: model-driven tests never
  take a disabled step. `examples/bank`'s SelfTransfer mutant passes
  every model trace and is caught here.

Cost: each validation is a `quint test` process (~0.5–1 s, mostly
start-up), and shrinking runs hundreds of candidates. `CaseChecker`
caches verdicts:
- identical runs, keyed by the instance constants and the steps with
  process and thread ids numbered by first appearance;
- prefixes. If the model rejected run R at its k-th step, any run with
  the same constants and the same first k steps fails the same way. A
  conformance failure at a step, and an invariant violation after it,
  depend only on the steps up to there.

- skipped steps. Steps the binding skips (refused calls, with
  `outcomes: { error: skip }`) become no model call. In a single-process
  run they are left out of the keys, so candidates that differ only in
  refused calls share a verdict.

Prefix reuse is restricted to one actor, with no reconstruction issues
and no domain clocks, where the reconstructed order is the recording
order. On `examples/bank`'s SelfTransfer test, the three together cut
quint runs from ~450 to 53 (about 5 min to 40 s), with the same shrunk
result. Constants are part of the key
because bindings may size them from the whole run (`max "amount"`): a
longer run can have larger bounds, which can enable steps.

`After` mode keeps the distinct runs and validates them in parallel at
the end (`validate.Checker.CheckRuns`, one quint process per run, up to
GOMAXPROCS). We tried batching several runs into one `quint test`, but a
Quint module can import a parameterised module with only one set of
constants unqualified, and bindings size constants per run, so batching
would mean rewriting every template's expressions with instance
qualifiers. Parallel processes are simpler and nearly as fast.

**The model drives, Hegel varies.** `Replay` draws a trace index and
optionally a prefix length, then replays the trace on a driver built
from the Hegel test case. Handlers can draw what the model abstracts:
payloads, timings, interleaved invalid requests that must change
nothing. A failure shrinks toward trace 0, a shorter prefix and simpler
draws.

Not built:
- driving the model live from Hegel rules (checking each rule's
  precondition in Quint). It would need a Quint evaluator in-process, or
  a long-lived quint REPL; the post-hoc check gives the same verdicts at
  the end of each case.
- seeding Hegel from Quint traces (Hegel has no explicit-example API in
  Go yet).

### 9.3 Converting existing telemetry (`convert`)

Not every system can be instrumented with model steps. `convert` maps
an application's ordinary spans to steps:
- span name globs, with `where:` attribute filters, or span events;
- each argument, observation, order key and identity is an expression:
  `attr:K` (looked up on the event, then the span, then the resource),
  `span:`, `resource:`, `event:`, `name`, `trace_id`, `status`, a
  `=literal`, or `|int` to convert.

Converted steps are numbered densely per recorder in time order. Native
`quint.step` events in the same input pass through untouched. What
conversion cannot give:
- **gap detection.** A missing span is invisible: there is no dense
  sequence number from the source;
- **program order beyond time order**, unless a `thread:` expression
  (e.g. `trace_id` for strictly sequential traces) supplies it. Concurrent
  operations within a span's resolution are ordered by end time, which is
  sound only where those operations commute in the model.

The binding is unchanged: the same binding checks recorded and converted
steps (`examples/bank`).

### 9.4 Long replays and quint's error positions

A replay is one `init.then(c1).then(c2)...` chain, and the Rust evaluator
rejects chains nested much past 50 steps ("recursion limit exceeded").
`internal/replay`'s RenderModule writes longer chains as named segments of 20 calls
(`action conformsTest_seg0 = c1.then(c2)...`), and the run chains the
segments. Each call stays on its own line, so a failure is still located
by line, with two cases quint reports elsewhere:
- the last call of the whole run evaluating to false is QNT511 ("Test …
  returned false") at the run's own line;
- the last call of a segment evaluating to false is QNT513 at the
  top-level `.then(segK)` line.

Both are mapped to the call. A failed test with no location at all is
quint failing, not the model rejecting the run, and is an error. A
400-step concurrent kvstore run validates in about 7 s.

### 9.5 Bulk checking and the Batched mode

Process start-up and model loading cost a quint process ~0.5 s, which
dominates the replay of a short run. `validate.Checker.CheckBatch` works
like this:
1. It reconstructs and plans every run in Go.
2. It groups the actors' traces by their rendered instance constants.
   Traces of one instance can share a module. Constants may depend on
   the run, so grouping, not a union of constants, keeps every verdict
   the one a lone check would give.
3. It writes one module per group and chunk (tests `r<i>_conformsTest`,
   `r<i>_<inv>InvTest`), runs the chunks in parallel, and maps quint's
   output back to each run.
4. A chunk quint cannot evaluate is re-checked one trace at a time.

24 runs: CheckBatch 1.6 s, CheckRuns (a process per run, in parallel)
6.6 s, one at a time ~14 s.

Hegel needs a verdict for each test case before it moves on, and its
shrinker is sequential, so a failing case cannot wait for a batch.
`qhegel`'s `Batched` mode splits the work instead:
1. **Generate.** `hegel.WithPhases(PhaseGenerate)` and a fixed seed;
   cases are recorded, not checked.
2. **Check in bulk** (`CaseChecker.Prefetch`, which fills the verdict
   cache). If every run passes, start another round with the next seed
   (`Rounds`, `Budget`).
3. **Replay** the failing round with the same seed in `Each` mode. The
   cache decides every case up to the failing one without quint, and
   Hegel shrinks the failure against the model.

If the replay does not meet the failing case (the body was not
deterministic in Hegel's draws), the bulk failure is reported unshrunk.
On a 40-case machine:
- all runs passing: Each 22 s, After 3.3 s, Batched 3.4 s;
- with a bug: Each 32 s (shrunk), After 3.4 s (not shrunk), Batched
  23 s (shrunk, same counterexample).

Batching amortizes start-up over runs that are all new (generation); the
cache reuses verdicts across runs that share prefixes (shrinking).

### 9.6 Distributed systems (`examples/glomers`)

Three things were needed beyond the single-process examples:
- **Cross-process order.** Each node records its steps with itself as
  the thread and a Lamport clock as the domain clock (`order.lamport`);
  the clock rides in message bodies.
  - Reconstruction then orders a delivery after its send, even across
    per-node step logs written by separate processes under real
    Maelstrom.
  - Getting this right needed a fix: steps with equal clock values are
    concurrent, and every one of them must precede every step with a
    larger value. Linking sorted neighbours only left `a(3)` unordered
    with `c(4)` in `a(3) b(3) c(4)`.
- **Linearization points at services.** A KV operation takes effect at
  the service, and the node only hears of it later, after other
  operations may have happened. The simulator's lin-kv records its
  operations itself.
- **Collections as observations.** Gossip carries sets and vectors, and
  reads return them. qobs records slices and maps as JSON text, and the
  binding's `toSet`, `toList` and `toMap` turn them back into Quint
  values.

Long, real runs also needed the replay segments (§9.4) to nest. At 2841
steps, 142 segments chained at the top level were again too deep for
the Rust evaluator, so segments are now grouped into segments
recursively, and no chain is longer than 20.

### 9.7 What a check costs

Measuring a 2841-step Maelstrom log of unique ids (152 s at first) showed
where a replay's time goes:
- **quint's start-up and parse**: ~8 s for a 660 KB generated module;
- **the evaluation itself**: ~5 s;
- **writing the ITF**, the model's full state at every step: 105 MB when
  the state holds every id issued, and as long again to annotate in Go;
- **one more full replay per invariant**: each invariant had its own run
  (for precise attribution), and each wrote its own ITF, which nothing
  read.

Changes:
- **The fused run.** A replay module first holds one run that checks
  each step's expect and every invariant together (`allTest`,
  `binding.FastTests`). It decides a passing run in one replay. Only the
  runs that fail it are replayed again, from a second module
  (`<base>_detail.qnt`, `binding.DetailTests`), whose conformance run and
  per-invariant runs say which check failed and where. Failing runs pay
  a second quint process; passing ones, the common case, save k replays.
- **Optional ITF.** `Options.NoITF` (CLI `-noitf`) skips it, and Hegel's
  checks always skip it, since they only need verdicts.
- **Segment names** no longer contain "Test", so `quint test --match`
  does not take them for tests.

The model matters as much:
- **A history property should not be an invariant that re-scans the
  history.** `issued.keys().forall(i => issued.get(i) == 1)` is
  quadratic over a run. A ghost flag set when a new element repeats an
  old one is constant per step.
- **A model of the mechanism can avoid the history.** Per-incarnation
  counters are state bounded by restarts, not by ids. They are a proof
  of the specification only with a lemma about the encoding, which a
  property test checks (examples/glomers README).

After both: 64 s (19 s without ITF) for the original model, 21 s (11 s)
for the ghost-flag model, 7 s for the scheme model.

Batches of many short runs (explorations, sweeps, Hegel, Linearize's
candidates) cost something else. With `--backend rust`, `quint test`
starts one evaluator per test, one after another, each sent the whole
compiled module as JSON (3.3 MB per test for a module of 40 short runs):
40 tests took 14.6 s, against 4.6 s with the TypeScript backend, which
evaluates them in its own process. CheckBatch therefore replays a chunk
of several traces with TypeScript, unless a backend was chosen
(checkout's, glomers' and streams' short suites: 20-55% faster), and a
single long replay stays on Rust.

Two ways around quint's front end were measured and dropped:
- **A long-lived `quint repl` session per instance**, a replay sent as a
  line per step. Each input costs 10-40 ms: the TypeScript front end
  parses and typechecks it against the whole module, so a 90-step run
  took 0.7 s, slower than in a batch. The verdicts matched.
- **The Rust evaluator directly** (`quint_evaluator test-from-stdin`,
  0.12 s per run): its input is the compiled syntax tree and lookup
  table, and `quint compile --target json` costs as much as the front
  end it would replace (6.6 s for the same 40 runs). Only an IR built in
  Go from the binding's Quint text (a parser and name resolution of our
  own) would avoid it; the evaluator's stdin protocols are internal and
  pinned per quint release.

## 10. Simulated environments and fault injection (`qenv`)

Maelstrom found the bugs no in-process check found in examples/glomers.
Each time, the in-process environment had been more forgiving than its
contract: a "sequentially consistent" store that was in fact
linearizable. `qenv` makes the environment's contract explicit, and
adversarial within it.

**A fault is a choice the contract allows.** A lost response, a
redelivered message, a read from an older snapshot and a crash between
two statements are all behaviours that the network's, the broker's or the
store's contract permits. A double makes exactly those choices and no
others, so a failure is never "the harness did something impossible". The
remaining question, whether a contract is the one production has, is
answered by running against more than one profile (§10.2).

### 10.1 One decision source

Every choice goes through a `Chooser`: which runnable task goes next, whether
a fault happens here, which pending message is delivered, how long a
status lags. The conventions:
- **Option 0 is nominal**: no fault, the oldest message, the first task.
  A shrinker that lowers values toward 0 removes faults and canonicalises
  the schedule.
- **Kinds.** Kinds starting with `fault.` are faults, with per-kind rates
  (`Rates`, matched by prefix). Any other kind is a schedule choice,
  uniform under a seed.
- **Backends.**
  - `qhegel.Choices(tc)` draws entropy in fixed-size byte chunks: a run makes
    hundreds of choices, and a draw per choice made a shrinking test 20x
    slower.
  - `NewSeeded(seed)` gives random runs, Maelstrom- or Jepsen-style.
  - `Replay(choices)` repeats a recorded run, and panics at the first
    divergence.
  - `Nominal` makes no faults.
  - `Swarm` turns whole fault kinds off per run.

### 10.2 Doubles with contract profiles

| Double | Contract | Faults (choices) |
|---|---|---|
| `Store` | transactions at `Serializable` (optimistic validation of reads, scans and writes), `SnapshotIsolation` (first committer wins, write skew allowed) or `ReadCommitted` (latest committed reads, last writer wins); `GetForUpdate` row locks; a crashed process's transactions abort | the interleaving of statements; a commit applied but reported unknown (`store.<name>.commit`) |
| `Queue` | at least once; FIFO only if `Ordered` | lost acknowledgements, redelivery of a crashed consumer's messages, delivery order |
| `Service` (RPC) | at most one response, maybe never | lost or duplicated requests, lost responses (the ambiguous failure) |
| `Process` | tasks die together; no deferred code runs; a restart rebuilds from durable doubles only | crashes at any step (`nemesis`, per step) |
| kubesim (fleetrollout) | generation bumps on spec change; status lags; resourceVersion conflicts | failed requests, applied updates with lost responses, status lag |

Profiles turn "harness quirk or real bug?" into a table. In
examples/checkout, missing row locks are a bug only under read committed,
and a lock without a write only under snapshot isolation. Which row
applies depends on what production runs.

### 10.3 Scheduling

Tasks are goroutines, but only one runs at a time. A task runs until it
reaches a seam (`Yield`, `Wait`, `Sleep`, an operation of a double), where
the scheduler picks the next one. Code between seams is atomic, so a run
is a function of its choices. Virtual time jumps to the next deadline when
nothing can run. `Settle` turns faults off, heals what persists, and runs
until quiet, drawing nothing: it takes runnable tasks in turn, a fair
schedule (always the first, choice 0, let a task spinning until another
acted starve it). Properties conditioned on quiescence apply after it.
A chooser may pick the task to run by id instead of by index
(`ChooseTask`): PCT does, from random priorities with depth-1 change
points, for bugs that need one task far ahead of another. Its picks are
recorded as the index choices they stand for, so its runs replay without
it, and a settle never asks it. `SettleCore` is VOPR's liveness mode: faults stop for a core of processes
and go on, drawn as in a run, for the rest; a system that needs a quorum
must finish with that core.

Code under test reaches the environment only through seams: `qenv.DB` and
`Txn`, its own interfaces for brokers and services, `qenv.Go`, `qenv.Now`
and `qenv.Sleep`. Outside a simulation these fall through to real
goroutines and time. Production puts real clients behind the interfaces.

Workers distinguish empty-feed waits from work in hand. `Signal.WaitIdle(ctx, d)`
uses the same notification/deadline semantics as `Wait`, but exempts only that
confirmed no-work wait from stuck/settlement checks. A decided notification/deadline clears the exemption before the task runs again,
including when a process pause holds it. Predicate waits count as active while
ready, without consuming readiness or permanently changing the idle flag.
Predicate-based doubles use `WaitIdle` similarly. Handler execution, delivery
callbacks, failed readiness reads, retry backoffs and operator-paused obligations
stay active. `Daemon` remains a lifetime exemption for passive infrastructure;
it is not a worker-loop default. An exhausted settle bound yields `Unsettled`
and writes no completion marker. A genuinely quiet history can still violate a
model's business completion obligation; runtime idleness does not prove it.

### 10.4 Recording and checking

Steps are recorded where effects happen:
- `Txn.OnCommit` runs at the commit itself, even when the caller is told
  the outcome is unknown. In production, the step goes in a table in the
  same transaction.
- A remote service records its own operations (the payment provider).
- A simulated API server records patches as it applies them.

`qtest.Explore` runs a scenario over many seeds and checks every run in
one `CheckBatch`. `Exploration.Expect` states either that all conform, or
that some failing run shows the expected verdict.
`qhegel.FindViolation` with `qhegel.Choices` shrinks a failure to the
interleaving and faults it needs. In examples/checkout that took 11 quint
checks; the verdict cache answered the other 599 candidates.

### 10.5 The same model in the real environment

examples/fleetrollout runs one controller two ways:
- in kubesim, under `qenv`;
- on three kwok clusters, with real etcd, kube-apiserver and
  kube-controller-manager, and `kill -9` as the fault.

Both write the same steps, judged by one model. The model works on
evidence (reads ordered by resourceVersion), so it is sound wherever the
controller reads fresh from each API server. Both mutants fail in both
environments.

What the real run needs that the simulation does not: a durable step log
(`FileSink`, durable because a killed process loses buffered steps and the
loss at the end of a log is undetectable), and recording at the response
rather than at the server. The API server's audit log would be the
faithful recording point.

### 10.6 Running someone else's controller logic (`examples/crossdb`)

crossdb simulates crossplane-runtime's managed reconciler itself, not a
re-implementation of it. Two things made that work:
- **A manager stub.** The reconciler only needs a manager's client and
  scheme, and the client is the intercepted fake.
- **Clock translation at the seam.** The reconciler stamps annotations
  with `time.Now()` and checks them with `time.Since`. Those calls are
  out of qenv's reach. The kube interceptor stores the timestamps in
  virtual time and returns them in real time (`Sim.ToVirtual`,
  `Sim.ToReal`), so a 30 s grace period lasts 30 s of virtual time.

Without the translation the simulation was wrong in a way that looked
like a finding: deletions never finished. With it, the exploration found
real ones:
- a leak with no crash (an ambiguous create, then a delete);
- a leak when the grace period is disabled (deletion during the cloud's
  read-after-create lag).

The same provider, model and audit then ran on kind with full Crossplane,
with steps from two processes merged by a wall-clock domain clock.

### 10.7 Not yet

- **Contract conformance of the doubles, in both directions.** Every
  `Store` checks each commit's dependency cycles against its level and
  every `Queue` its contract under `GOTOGETHER_CHECK` (§42): what a double
  does is allowed. That everything allowed it can do is still argued by
  the anomaly tests (`TestIsolationAnomalies`), not checked.
- **Machines.** Clocks, disks and partitions are per process (§42); a
  machine grouping processes that share a clock, a disk and a network
  link is not modelled.
- **Steering by coverage.** Fault coverage (`Coverage`, `Exercised`) says
  which choices runs made; exploration does not yet steer toward the
  ones they did not.
- **Go's own scheduler.** Goroutines that bypass `qenv.Go` run
  uncontrolled, and a run that depends on them is not reproducible.
  Replay detects this but cannot prevent it.

## 11. Metastable failures (`qload`, `qmeta`)

A metastable failure leaves no wrong answer to check. The system enters
a degraded state and stays in it after its trigger is gone: a spike
fills a queue, requests time out, and their retries keep the queue full.
No Quint invariant over individual steps says "the system came back".
That is a property of rates over time. Two papers set out how to analyse
it, and this is their pipeline, with each tier checking the next:
- Isaacs et al., "Analyzing Metastable Failures", HotOS '25;
- Alvaro et al., "Formal Analysis of Metastable Failures in Software
  Systems", 2025.

| Tier | Package | Cost | Answers |
|---|---|---|---|
| CTMC over (queue, orbit) | `qmeta.New`, `HittingTimes`, `Field`/`SVG` | ~0.1–1 s per configuration | where the tipping point is; how policies rank |
| calibration | `qmeta.Calibrate` (simulated trajectories) | seconds | the chain's rates, so its numbers mean something |
| simulation | `qload.Simulate`, `qmeta.Recovers` | ~0.1–0.5 s per run of 20 simulated minutes | does this policy, this code, recover from this trigger |
| trigger search | `qhegel.FindTrigger` (Hegel) | ~1 min | the smallest trigger that breaks it, shrunk |
| the real system | (load tests) | hours | confirms the one trigger the search found |

### 11.1 Recovery as the property

`qmeta.Recovers` takes the papers' definition. Within W after the
stressor ends, goodput is back to at least 90% of what it was before. A
trigger is extra load, a slowdown (a capacity dip in a dependency) and a
duration. All three shrink toward none, so Hegel's result is the smallest
stressor that tips the system. A run's escape from the metastable region
is chance, so each candidate trigger runs several seeds and a majority
decides (`Mostly`). Per-request randomness comes from a seeded
generator, not the Chooser: a run has tens of thousands of requests, and
what the search should vary is the trigger.

The majority is meaningful only if every requested seed was judged. `Runs`
returns all observations in seed order and an error wrapping the lowest-seeded
failure; `Count` and `Mostly` refuse aggregate conclusions on that error, even
when the remaining seeds already form a majority. A missing healthy baseline
or simulation failure is neither recovery nor evidence of degradation. Empty
batches and nil targets are configuration errors. Trigger search checks its
error before either claim; sizing stops without a recommendation when a
requested simulation cannot be judged. Explicit chain-only sizing remains a
separate mode. This preserves the distinction between a system that stayed
degraded and an experiment that could not measure it.

Hegel's `Run` reports ordinary property failures and engine failures through
the same error channel. Trigger search therefore uses a private panic marker
that Hegel captures during search but propagates from its final deterministic
replay. Only that reproduced marker establishes a found trigger. An earlier
candidate, a failed replay, or an ordinary engine error cannot establish one;
tests exercise this contract through the public pinned Hegel API.

Generated calibration trajectories obey the same evidence rule.
`SimulateTrajectory` returns an error on invalid inputs, simulation errors, or
missing exact samples; it never substitutes zeros or returns a partial mean.
Sample times are nonnegative whole seconds, matching its one-second sampler;
their order and duplicates are retained. Time zero is the supplied initial
state. Queue states that the simulator would silently clip are refused.
The client timeout must be positive, and the simulation extends past the last
requested sample by that timeout plus one sampling tick: orbit membership is
retrospective, so an attempt active at the sample may only be classified when
it later times out. Duration overflow is rejected; this is not a runtime or
memory budget. Grist propagates trajectory errors before fitting its timeout.

### 11.2 The code under test on virtual time

qload runs on qenv. That is what lets the retry library itself sit in the
loop (`ClientConfig.Retrier`), rather than a model of it. The library
receives the virtual time as an argument, and limiters such as
`x/time/rate` already accept one. Clients are open-loop (Poisson
arrivals) or closed-loop (`Population`, `Think`). The difference
decides results. A controller's objects are a closed loop: the workqueue
merges a failing object's poll into its retry. The open-loop version of
the Crossplane provider found storms under crossplane-runtime's defaults
that cannot happen. The closed-loop version found none.

### 11.3 What the tiers taught each other

- **Rejection semantics.** In examples/retrystorm the simulation showed
  that backoff alone recovers worse than immediate retries. An immediate
  retry rejected by a full queue spends its retries at once and gives up,
  which sheds load. A retry after backoff comes back. The first chain
  dropped rejected transitions and predicted the opposite. It now has
  both rules.
- **Truncation.** With the orbit bounded at 60, the chain predicted quick
  recovery for backoff. At 120 it predicted a storm. A finite chain must
  be big enough to hold the storm it is asked about.
- **Absolute times.** The chain's recovery times were orders of magnitude
  above the simulation's (immediate retries at 9.5 req/s from (100, 30):
  minutes simulated against days predicted). It ranks policies and places
  the tipping point; for numbers it must be calibrated.
- **A simulator bug.** A `qenv.Signal` wait that timed out stayed in the
  signal's waiter list, so a later `Notify` cut short the task's next
  wait, a backoff sleep. Requests in the orbit outnumbered the controller's
  objects, which is impossible, and that exposed it. The fix tags each
  wait with its generation (`TestSignalTimeoutIsForgotten`). Checking a
  simulation's own invariants (population ≥ orbit) is cheap. Do it.

### 11.4 Correctness stays with Quint

A mitigation is code. The retry budget's accounting is recorded and
checked against `model/retry.qnt`, whose invariant is the amplification
bound. Its `Uncapped` mutant passed the load tier: after 6000 s of quiet
the overfilled bucket still let 4 of 5 runs recover. The model rejected
its first wrong step. The tiers are complementary: Quint for what each
step does, qmeta and qload for what the rates do.

### 11.5 Not yet

- **Multi-server chains.** The chain has one server. A dependency chain,
  where retries amplify at every hop, needs a product of chains or
  simulation only.
- **Calibration of mitigations.** `Calibrate` fits λ and τ. A budget's
  or backoff's effective parameters could be fitted too.
- **Real-system confirmation.** Run the trigger Hegel found against the
  real provider on kind, with a throttling dbaas server (`dbaas-server
  -throttle-rps`).

### 11.6 The application itself under load (`examples/checkout`)

The tiers above abstract a system into queues and rates. The checkout
saga ran under load as itself: its outbox, consumers, retries and
database, with only the provider given a capacity (`qenv.Capacity`:
workers, a queue, service times from a seeded generator, a slowdown, and
optional shedding of calls whose caller gave up). Goodput was counted
from the shop's own model steps, and the same run was checked against the
model. What that showed, which no abstraction had:
- **The trigger can be noise.** With more consumers than the provider's
  capacity times the timeout, the collapsed state is reachable from
  ordinary variance. Search finds the empty trigger. This is a design
  bound, best stated as one: consumers ≤ capacity × timeout.
- **Retry loops compose.** A retry budget in the shop's provider client
  did nothing, because the broker's redelivery is a second, unbudgeted
  retry loop around it. A budget has to cover every loop, or the
  outermost one has to be bounded.
- **Wasted work is the mechanism.** Shedding calls whose caller gave up
  restored recovery without changing anything else.
- **Load is a fault injector.** With no injected fault at all, the storm's
  timeouts charged orders twice when the idempotency key was missing. With
  crashes, lost responses and redeliveries added, the faithful shop
  conformed through the storm. Correctness and recovery are separate
  checks on the same run.

## 12. What the examples changed in the core

After checkout, fleetrollout, crossdb, retrystorm and checkout-under-load,
the same workarounds kept appearing in examples. They are now in the core.
- **A recorder per run, carried by the context.** `qobs.WithRecorder`
  puts a recorder in a context. `qenv.WithContext` makes it the root of
  every task's context in a simulation (`Sim.Context` outside tasks).
  `qtest.Explore` gives each seed its own recorder and runs seeds in
  parallel, and `qmeta.Runs`, `Count` and `Mostly` run a trigger's seeds
  in parallel. A Hegel test records the same way
  (`qhegel.Case.Context`). The global recorder remains for real
  processes and woven code.

  At first, a step recorded through `context.Background()` inside a
  simulation went to the global recorder and was missing from its run.
  Four call sites did this (`settle` in three examples, a test scenario),
  and the model caught each one at once. That is now impossible rather
  than merely caught. qenv registers which simulation each goroutine
  belongs to: every task's goroutine, and the goroutine that created the
  simulation. qobs consults that registry when a step's context carries
  no recorder (`qobs.SetFallback`). Go has no goroutine-local storage, so
  the goroutine id comes from the stack header, read once per task. It
  cost nothing measurable in the checkout load tests. What remains, a
  step from a goroutine started with `go`, is made loud: Explore and
  Experiment runs are strict (`internal/hooks.Strict`), and such a step panics with
  the fix. It is also caught before running, by `gotogether lint -sim`,
  which flags `go` statements, wall-clock calls and background-context
  steps in files that import qenv, and runs in CI. A compile-time
  guarantee (a context type only qenv can make) was rejected: code under
  test, woven advice and production code all pass a plain
  `context.Context`.
- **Load experiments on the application itself** (`qload.Experiment`).
  Before this, checkout's load harness had its own sampler, step counter,
  recovery windows and arrival process. Now an experiment:
  - deploys the app;
  - drives it open-loop or closed-loop;
  - counts its model steps per tick, so goodput is the rate of a model
    action;
  - samples probes;
  - gives each source of randomness its own seeded stream (`Env.Rand`).

  `qmeta.Assess` and `qmeta.Of` turn an experiment into a recovery
  verdict and into a `System` for `Search`.
- **A recovery verdict that tolerates noise and needs a healthy
  baseline.** Comparing two 100-second windows at a 90% threshold failed
  healthy runs about one time in ten, and `Judge` now allows two standard
  errors. Comparing with the goodput before the trigger hid a real bug:
  with 8 consumers, the checkout shop had collapsed before any trigger,
  and "recovered" to its collapsed rate. An experiment states its healthy
  goodput (`Healthy`), and the verdict uses it.
- **Crashes per virtual second** (`Sim.NemesisEvery`). Load multiplies
  scheduling steps per second, so a per-step crash rate turns into a
  crash storm. Experiments with faults use one crash choice per second.
- **The scheduler checks itself.** `Sim.Check` (or `GOTOGETHER_CHECK=1`)
  verifies, at every step, that:
  - the run queue agrees with the queued flags;
  - no task is both queued and waiting;
  - every timed wait has a live timer;
  - no deadline has passed unserved;
  - time does not go backwards.

  `TestSchedulerSemantics` states the scheduler's semantics as a Hegel
  property over random programs. It fails within a second on the `Signal`
  bug of §11.3, reintroduced. CI runs qenv's tests with the checks on.
- **The store checks its own histories.** Under the same switch, every
  `Store` keeps its committed history (each transaction's versions read,
  scans with their snapshot, and writes) and checks each commit against
  its level in Adya's terms (`qenv/internal/history`): the commit's ww,
  wr and rw dependencies, and the cycle through it with the fewest rw
  edges. No rw edge (G1c) or a dirty read (G1a) breaks every level, one
  (G-single: a lost update, read skew) breaks snapshot isolation, any
  cycle breaks serializability; write skew (two rw edges) is snapshot
  isolation's. TestIsolationAnomalies shows each level's anomalies can
  happen; this shows those it forbids never do. A store with its
  validation turned off is caught at the commit that closes the cycle
  (`TestHistoryCatchesBrokenLevel`), and checkout's, twophase's,
  grpcledger's and streams' runs pass it. A `Queue` checks its contract
  after each operation: every message published and not acknowledged is
  pending or held, none acknowledged is, and an Ordered queue's first
  deliveries wait in publication order. A queue that forgets a crashed
  consumer's messages is caught (`TestQueueContract`), through crashes,
  lost acknowledgements, nacks and partitions.
- **Constants per check** (`qtest.Constants`, in qhegel's `Config.Opts`,
  `validate.Checker.WithConstants`). One binding serves runs of any size.
  Checkout's load runs had needed a copy of the binding with a different
  stock.

Not done, and why:
- **A clock seam for libraries.** Code we write already takes `Sim.Now`.
  The libraries that read the wall clock (crossplane-runtime,
  `x/time/rate` inside client-go's limiters) can't be reached by a seam,
  only by rewriting their calls at build time. That's the same problem as
  scheduling goroutines that bypass `qenv.Go`, and edgepublish (real
  `sync.RWMutex`, a background goroutine) is the place to work on it.
- **A closed-loop chain in qmeta, splitting long runs per key, and one
  vocabulary for performance expectations.** These are useful, but no
  example is blocked on them.

## 13. Code that uses plain Go concurrency (`qenv/weave`)

qenv controls only code that blocks and spawns through it (`qenv.Go`,
`qenv.Sleep`, the doubles). Real code takes `sync` locks and starts
goroutines with `go`. The choices for bringing such code in:
1. **Seams in the source.** The approach so far (checkout, crossdb). It
   is small for code we write, but it changes code that should stay as
   it is.
2. **Rewriting at build time.** This is weaving, and it is what
   `qenv/weave` does.
3. **A patched runtime.** It would cover everything, including the
   standard library, but it ties the toolkit to one Go release.

For a stand-in that must track real code, or for the real code itself,
weaving is the right trade.

**What is woven.** Orchestrion's method-call join point matches calls of
`sync.Mutex`, `sync.RWMutex` and `sync.WaitGroup` methods in the root
module's packages (`package-filter: {root: true}`; never the standard
library or dependencies). Each call becomes a function in package `woven`
(the woven builds' ABI, §60; `qenv/weave` holds the aspects). That
function asks qenv whether the calling goroutine is a simulation task
(`internal/wovenrt.Current`, installed by qenv from the goroutine
registry of §12):
- inside a task, it operates on a lock or counter kept by the simulation
  and keyed by the real object's address (`wovenrt.Lock` and the others).
  Taking the lock is a scheduling point, and waiting for it is a simulated
  wait, with `sync`'s semantics (a waiting writer blocks new readers);
- outside, the real method runs.

**Goroutines.** Orchestrion has no join point for `go` statements. A
directive does reach the statement's function value, so `//qenv:go`
before `go func(){…}()` wraps the literal as `woven.Go(func(){…})`.
`woven.Go` runs in the goroutine executing the go statement, before the
new one starts. That ordering is the trick: in a task it starts a
simulation task running the function, and returns a no-op for the real
goroutine. Outside a simulation it returns the function unchanged.

**What escapes.**
- A method value (`return st.mu.RUnlock`) is not a call.
- An unmarked `go` is not rewritten.
- A call promoted through an embedded lock (`t.Lock()` on
  `struct{ sync.Mutex }`) is not matched, because Orchestrion's
  method-call join point compares the receiver expression's type, not
  the method's. A probe confirmed this, through a nested struct too.

`gotogether lint -weave` reports all three by name, without type information
(it flags the embedding, not each call). The edgepublish stand-in needed
the first two fixes; the lint flags both on the original code.

**Interrupted work.** A crashed task stays frozen while the run goes on
and unwinds at `Close`. A step its deferred code records then
(`//quint:action` records on return) would claim a completion that did
not happen. It goes to `Recorder.Interrupted` instead of the run:
reported with a failing exploration, never checked. Recording on return
describes what the caller saw. An effect the model must see whether or
not its caller survives belongs at the effect's site, recorded by the
double or service, as checkout's store commits and crossdb's cloud
already are. A task that crashes its own process stops in `Crash`; it
used to run on to its next scheduling point.

**What the publisher in a simulation found.**
- The stand-in rotated backwards: a `Tick` waited for the exclusive lock
  past a boundary another push had rotated across. The real exporter had
  fixed this, and the stand-in predated the fix. Hand-written scenarios
  never found it: it needs a read of the clock, a wait for the lock
  through slow writes, and a rotation by someone else in between.
- With the publisher unchanged and in the simulation, the timing
  questions became testable: load turned S3 queueing delay into ambiguous
  writes and duplicate commits.
- qenv had two gaps, found by this example:
  - a background goroutine's `context.Background()` slept in real time;
    qenv now finds the task through the goroutine;
  - a killed task's deferred `//quint:action` recorded a write that never
    completed; such steps are now discarded.

**Not done.**
- `sync.Cond`, `sync.Once` (harmless: it never blocks across simulated
  time unless its function does), channels and `select`. Channels are
  operators, not calls, so Orchestrion cannot match them; they would need
  a rewriter of our own.
- The real exporter, which also needs its chDB sessions behind a seam.

## 14. A whole program in a simulation (gosim, `qgosim`, `examples/gosim`)

§13's third option, a patched runtime, exists:
[gosim](https://github.com/jellevandenhooff/gosim) translates a program and
everything it imports, standard library included, into a module that runs
on its own deterministic runtime. Goroutines, channels, `select`, timers,
maps, `sync`, the network (TCP) and the file system are all simulated, on
several machines that can crash and restart. It is MIT-licensed and
experimental, by one author; the last commit is from December 2024. The
spike asked whether gosim can be the runtime under qenv's choices and
Quint's checks, for code that qenv's seams and weaving do not reach.

**Go 1.26.** gosim works by replacing, at translation time, every
standard-library function that talks to the Go runtime (a body-less
function, an assembly routine, a `linkname`) with a hook. The hook tables
were written for Go 1.23, so 1.24 and 1.26 both fail on the first missing
hook. A translator change reports every missing hook in one run, not one
per run. That list for 1.26 was about 40 entries:
- `sync.Mutex`, moved to `internal/sync`;
- `sync.Map`, now a hash trie that takes the runtime's hasher from a map
  type descriptor (that map stays a real map);
- weak pointers (kept strong) and synctest (unsupported in a simulation);
- `vgetrandom`, which falls back to the simulated system call;
- three compiler intrinsics that panic when they are not intrinsified,
  once translated to another package path.

Other translator fixes:
- FIPS 140 crypto: its assembly reads package variables that translation
  moves into per-machine globals, so translation builds with `purego`;
- hooks matched methods of the same name;
- `~map[K]V` constraints did not admit named map types;
- reflect gained `TypeAssert` and `Seq`;
- the file system had to accept `O_DIRECTORY`.

gosim's behaviour suite passes on 1.26.1. Its race mode does not build
yet (`internal/race` changed), and is not needed here.

**The Chooser.** All of gosim's nondeterminism comes from one seeded
generator in its scheduler. That makes it one seam:
- every bounded pick goes through `gosimruntime.Choose(kind, n)`: the next
  goroutine ("sched"), a ready `select` case ("select"), where a map
  iteration starts ("map"), and whatever the program asks for
  (`fault.partition` in the example);
- unbounded randomness (math/rand, crypto/rand, hash seeds) comes from a
  generator seeded by one choice.

Option 0 is nominal: the first runnable goroutine, the first ready case,
no fault. That is qenv's convention, and it makes Hegel shrink toward the
canonical schedule. A qenv `Chooser` is a gosim `Chooser`. With qenv's
`Recorder` and `Replay`, a run of 5500 choices replays to the identical
step sequence, and `Replay`'s divergence check holds.

**Hegel stays outside.** Translation covers every import, and Hegel calls
a native library, so Hegel cannot run in the simulation. The test is in
two halves:
- the simulated half is an ordinary gosim test (`//go:build sim`);
- a normal `go test` in the host drives it over gosim's metatest protocol
  (the test binary on a pipe). The fork extends that protocol: during a
  run, the binary asks the host for each choice and sends it each line
  the program `Emit`s, here the qobs step log. On the host,
  `metatesting.MetaT.RunWith` serves a qenv `Chooser`.

A Hegel abort is a panic from inside `Choose`. It stops the binary, and
the next run starts a fresh one. `qhegel.Case.Add` takes the
emitted steps, so `FindViolation` judges and shrinks gosim runs as it
does qenv's.

**What it ran.** The Glomers broadcast node, unchanged, on three machines
over real TCP, with a client and a nemesis that isolates nodes on chosen
steps. The runs conform to the broadcast model. The ReadOwnOnly mutant
fails at a read. Hegel found NoRetry's lost value and shrank it to two
gossips lost to an isolated node, with the rest of the schedule nominal.
Code in the run, none of it written for the simulation:
- the node and its Maelstrom runtime, `net`, `bufio`, `encoding/json`,
  tickers and mutexes;
- qobs and the OTel API it imports.

One harness change: the runtime got `Node.Start` (a transport other
than stdio) and `Barrier` (the Lamport barrier behind `Sim.Step`). Its
Hegel network driver is excluded from `sim` builds.

**Costs.**
- A choice round trip to the host is about 50 µs. A cluster run takes
  350 ms with qenv choosing and 60 ms on gosim's own seed. Eight in ten
  choices are the scheduler's. Handing the simulation entropy in chunks,
  as `qhegel.Choices` already draws it, would remove most of the round
  trips. Fault choices would still go to the host one by one, for their
  rates and notes.
- The first build translates the standard library and every dependency,
  about a minute. It is cached after that.
- The fork is about 600 lines. The upstream project is quiet, so
  carrying the patch is the realistic plan; most of it is the version
  port, which upstream would need anyway.

**Compared with qenv and weaving.** gosim covers what qenv reaches only
through seams, and what weaving cannot rewrite:
- channels and `select`;
- the standard library's own goroutines and timers;
- dependencies;
- the network and the disk.

In exchange:
- the code under test runs in a translated copy;
- the Go version is pinned to the hooks;
- doubles with contract profiles (isolation levels, lossy RPC) and
  qload's open- and closed-loop clients still belong to qenv.

The two fit together. The simulated program asks `Choose` for its faults
under qenv's names, and the same Chooser, Hegel included, drives either
runtime.

**Not done.**
- Machine crashes (`Machine.Crash`, restart). The broadcast model has
  no restart.
- Chunked entropy.
- Race mode on 1.26.

A machine that crashes stops without running deferred code, so §13's
interrupted steps cannot arise. A step is sent to the host when it is
recorded.

## 15. Where gosim and a weaver of our own take the toolkit

§14 showed that gosim can run a whole program with every choice made by
qenv's Chooser. This section is the plan that follows from it:
- rebase qenv onto runtimes, so that the same deployment runs on qenv's
  scheduler or on gosim;
- turn gosim's translator into a weaver of our own;
- use the weaver's annotations to tie simulation, load, the CTMC and
  production telemetry together.

### 15.1 One deployment, several runtimes

qenv is two things in one package:
- a scheduler: tasks, virtual time, signals, processes, choices;
- doubles written against it: `Store` with its isolation profiles,
  `Queue`, lossy `Service` calls, kubesim.

The doubles reach the scheduler through a narrow seam:
- the context-level functions (`qenv.Go`, `Now`, `Sleep`, `Wait`,
  `WaitFor`, `Signal`);
- `Process`, for crashes;
- `Choose` and `Fault`.

Put that seam behind an interface, a runtime, with three
implementations:

| runtime | tasks, time, waits | processes | choices | for |
|---|---|---|---|---|
| qenv's scheduler (today) | simulated tasks, virtual time | frozen tasks, `OnCrash` | the Chooser | exploration, Hegel, load: a run in milliseconds |
| native, in gosim | `go`, `time`, channels: simulated by gosim | gosim machines (`Machine.Crash`, `Restart`) | `gosimruntime.Choose`, so the same Chooser on the host | fidelity: the standard library, dependencies, the network |
| native, on the wall clock | the same code, unsimulated | OS processes, or none | a seeded or random chooser | soak tests and demos against real time |

The native runtime is plain Go, so it belongs in core. gosim contributes
only two things, the choice function and machines, through two hooks
that the gosim adapter installs from a `sim` build. Core never imports
gosim.

A deployment written once (checkout's `Deploy`, crossdb's cloud,
fleetrollout's API servers) then runs on all three runtimes, with the
same model and binding:
- on gosim, crossdb's clock translation (`ToVirtual`, `ToReal`) is
  unnecessary, because crossplane-runtime's own `time.Now` is virtual;
- fleetrollout's interceptors can become real HTTP between machines.

The predicate `Wait` is the one operation without a native counterpart.
The doubles that use it (the queue, call results, keyed locks) should move
to `Signal`, which maps onto a channel. `Wait` stays, polling on the
native runtimes.

What does not move:
- qenv's scheduler: Hegel and qload need its speed;
- the doubles' contract profiles, because a real database rarely shows
  write skew on demand;
- trace validation of real environments.

### 15.2 A weaver of our own

gosim's translator already does the hard part of a weaver. It loads the
whole program with types (`go/packages`), rewrites it with `dst` and
keeps the comments. It rewrites more than Orchestrion can:
- channels and `select`;
- `go` statements;
- the standard library and dependencies.

Factor it into a pass framework with two front ends:
- **translation**, for gosim: the passes run before gosim's own;
- **`-toolexec`**, for ordinary builds, as Orchestrion does: each
  package is rewritten on its way to the compiler, so the same aspects
  reach production binaries.

Orchestrion parity is the floor:
- **join points:** function declarations (by name, signature,
  receiver, directive), calls, struct literals, imports and directives;
- **advice:** wrap an expression, prepend or append statements, inject
  declarations, add a struct field, replace a body;
- **aspect files:** read Orchestrion's YAML format, so its existing
  integrations (net/http, database/sql, gRPC) are available.

Types make our weaver better than parity where Orchestrion's matching
is syntactic, which caused the escapes §13 lists:
- method values (`return mu.RUnlock`) are woven: bind the receiver
  when the value is made;
- promoted calls through an embedded `sync.Mutex` are matched by the
  method's own type;
- calls through an interface get a dynamic check;
- channel sends, receives and `select` become join points, so
  `qenv/weave` covers channels on qenv's fast runtime, the case §13
  left undone.

Then `gotogether lint -weave` has nothing left to report.

The weaver lives in its own module, with gosim's translator, because it
depends on `dst` and `x/tools`; core stays light. The fork needs a home
of its own first (§15.5).

### 15.3 Annotations that serve the model, the load model and production

Today one annotation, `//quint:action`, makes a function a model step.
With a weaver in both front ends, every annotation means one thing in
production and one in a simulation, from the same source.

**Actions (exists, extended).**
- `//quint:action` records on return, as now. It can also record a
  `begin` at entry, which makes an interrupted action explicit. A crash
  between the two leaves a started, never completed action. The model
  can take that as a possible effect, instead of the step being
  discarded (§13).
- Every action is also a metric: a counter by action and outcome, and a
  latency histogram, under the OTel names that qload's `Series` already
  uses (`writeManifest/ok`). A goodput assertion in a load test and a
  dashboard in production then read the same numbers.

**Load roles.** Two annotations give the CTMC its parameters:
- `//quint:serve name:checkout` on a handler, or on a worker's loop, is
  the server side of `qload`'s model;
- `//quint:call service:payments` on a call site is the client side.

The weaver instruments them to measure the quantities `qmeta.Params`
needs, live:
- the arrival rate and the service rate;
- the concurrency (the CTMC's `u`);
- the timeouts;
- retries in flight, the orbit, the CTMC's `v`. No dashboard has `v`
  today, and it is what tells a slow system from a metastable one.

`qmeta.Calibrate` fits the chain to the trajectories from simulated runs.
The same fit works from production's.

**Alarms from the chain.** `qmeta` computes, for every state `(u, v)`,
the expected time to return to the healthy set (`HittingTimes`).
- The states whose time exceeds a bound form the region a system does
  not come back from by itself. Its boundary in `(u, v)` is an alert
  threshold with a derivation, not a guess.
- The smallest trigger that crosses it (`FindTrigger`) is the headroom
  to report: how big a spike, how long a dip.
- The healthy baseline (`Experiment.Healthy`) and `Recovery.Judge`'s
  tolerance give the normal band.

All of these can be generated as alerting rules over the woven metrics.
They should come with the policy they assume, so a change to the retry
budget in code shows up as a changed alert.

**Faults.**
- `//quint:fault kind:rpc.loss` on a call site is a fault point.
- In a simulation, the weaver wraps it in
  `Choose("fault.rpc.loss", n)`, injecting an error of the call's own
  result type. Option 0 is no fault, so shrinking removes the fault.
- In production it compiles to nothing, or to an opt-in chaos hook.
- Either way it is a list: a run can report which fault points it
  exercised and which it never reached. That is coverage of faults, not
  lines.

Fault points also give `connect` a way to drive a whole program. A Quint
trace's crash and partition actions answer the fault choices, through a
Chooser built from the trace, while the schedule stays nominal.

**Context and ordering.**
- The weaver can propagate model identity across RPCs as OTel baggage
  (`//quint:actor` on a type): actor, process, payload ids.
- It can propagate Lamport clocks, stamping on send and merging on
  receive, as the Maelstrom runtime does by hand.
- Distributed step logs then merge without per-protocol plumbing.
- Deadline propagation, the mitigation checkout's load tests showed to
  recover, can be inserted and its remaining budget recorded on every
  call.

**From the model to the code.**
- The weaver knows every annotated site, so it can list the model's
  actions that have none, and annotated sites that name actions the
  model lacks. That is the binding lint, from the code side.
- The model's actions with weights become `qload`'s workload, the
  model's types its argument generators.

### 15.4 The loop this closes

1. The Quint model fixes what may happen. Annotations put its actions,
   load roles and fault points on the code.
2. qenv's runtime explores the code fast under faults, Hegel shrinks
   the failures, and the CTMC ranks the policies.
3. gosim runs the same deployment, the standard library and network
   included, to confirm what qenv found. A shrunk trigger replays there.
4. Production runs the same annotations as telemetry. Its steps are
   checked against the model, its metrics fit the chain, and its alerts
   come from the chain's recovery region.
5. A production trigger the chain did not predict, or a step the model
   rejects, is a new test, back at step 2.

### 15.5 Phases

| phase | work | depends on |
|---|---|---|
| 0 | the fork in a repository of its own, with CI; choices in chunks from the host (§14's cost) (done, §15.7: a branch for now) | a repository |
| 1 | the runtime interface, the native runtime and the gosim adapter; checkout's tests on qenv and on gosim (done, §15.6) | — |
| 2 | the pass framework in the translator; `//quint:action` as a pass, so the edgepublish publisher runs in gosim without Orchestrion (done, §15.8) | 0 |
| 3 | the `-toolexec` front end; Orchestrion aspect files; method values, promoted calls and channels woven (done as an overlay front end, §15.9; channels left to gosim) | 2 |
| 4 | load roles and action metrics; calibration from production; alerting rules from the chain | 3 |
| 5 | fault points and fault coverage; a Quint trace as a Chooser | 2 |

### 15.6 Phase 1 as built

The runtime interface became a second scheduler inside `*Sim`, not a new
type. `qenv.NewNative(c, platform)` returns the same `Sim`, and every
double, process and example runs on it unchanged:
- **One task at a time.** The running task holds a lock and gives it up
  at seams: Yield, Sleep and waits. A condition variable is broadcast
  when it does, and that serves both predicate waits and Signals. The
  predicate `Wait` therefore stays as it is; the move to `Signal` that
  §15.1 expected was not needed.
- **Processes.** With a Platform, each process runs on a gosim machine.
  The tasks a process is handed (a service's handler, called from
  another process) start there, from a spawn queue that the machine's
  main goroutine serves. A crash stops all of them at once.
- **Crash order.** The crash is requested before the crash hooks run,
  because a hook may restart the process. A process that crashes itself
  has its machine stopped by the platform's goroutine on another
  machine, in order with the restart; gosim cannot stop the machine its
  caller runs on.
- **Settle.** It turns fault choices nominal. It cannot make gosim's
  schedule canonical.
- **Hegel stays out of the simulated packages.** The Hegel adapters
  moved out of the packages a simulation runs, into qhegel:
  `qhegel.Choices` (once `qenv.Hegel`), `FindTrigger` and
  `SearchTrigger` (once in qmeta). The Glomers network driver moved to
  `maelstrom/hegelnet`. Build tags had hidden the dependency instead,
  and the first gosim translation found each one late.
  `internal/simdeps` now fails, in ordinary CI, when a simulated package
  depends on Hegel, purego or cgo, and it prints the import chain.

`qgosim` is the adapter, in a module of its own:
- on the simulated side, `New` (the native runtime with gosim's choices,
  machines and a sink to the host);
- on the host, `Run` and `Scenario`, which give `qtest.Explore` a pool of
  test binaries so that seeds run in parallel.

checkout's tests on gosim reuse the qenv tests' model, binding and
expectations. The faithful shop conforms at every isolation level
through machine crashes, and the write skew is caught. NoIdempotencyKey
shows a second symptom that gosim's schedules reach first:
- the cancellation's void looks the charge up by the order's key;
- the mutant stored the charge under a per-attempt key, so the void
  finds nothing;
- the order stays paid and cancelled, and `settledCorrectly` fails.

On qenv's scheduler the double charge shows first. Both are this bug's,
and the test now names both.

A checkout run makes about 27,000 choices, nine in ten of them the
scheduler's. At 2.7 s a run on gosim against milliseconds on qenv's
scheduler, phase 0's chunked choices are the next thing to do.

### 15.7 Phase 0 as built

**Where the fork lives.** The fork is its own repository and module,
github.com/chucklehead-dev/gosim (it was first a `gosim-fork` branch of
this one, fetched by a script at a pinned commit). It is upstream's
history plus our commits, and its FORK.md says what they change. Keeping
upstream's history makes our changes ordinary commits, which rebase onto
a newer gosim; the module path is one scripted commit on top. qgosim
requires it like any module, at the commit it was built against.

**Entropy.** A chooser that implements `qenv.Entropy` hands out bytes.
`qenv.EntropyOf` finds that capability through `WithContext`,
`Recorder` and `Replay`. The test binary then makes every choice but
faults from 512-byte chunks, by the same rule as `qhegel.Choices`: a
byte, two above 256 options, modulo n.
- Faults still go to the host one by one, for their rates and their
  notes on the test case.
- A Recorder stores each chunk as a choice of kind `entropy`, so a run
  replays exactly.

The effect on broadcast's TCP cluster:

| how choices are made | time per run |
|---|---|
| one round trip per choice | 438 ms |
| gosim's own seed | 60 ms |
| chunks of entropy | 48 ms |

For Hegel, the chunks are the same draws its chooser makes on qenv's
scheduler, so shrinking a gosim run behaves as shrinking a qenv run does.

### 15.8 Phase 2 as built

**The translator reads Orchestrion's own aspect files.** It applies the
aspects of the packages the module's tool file names (`gosim.tool.go`,
else `orchestrion.tool.go`). `//quint:action` is therefore woven from the
same `qobs/orchestrion.yml` that `orchestrion go build` uses: there is no
second definition to drift from it.
- **Where weaving happens.** The fork's `weave` package works at the
  source level, before gosim's own rewrites. Woven files go to every
  `go/packages` load as an overlay, so types, imports and the
  dependency graph see the woven code. `//line` directives keep positions
  on the original lines, and the aspects are part of the translation
  cache's key.
- **What is supported.** Function-body join points with a directive, and
  prepend-statements advice, with `.Function.ArgumentOfType`,
  `.ResultOfType` and `.DirectiveArgs`. Unnamed results the advice needs
  are named, as Orchestrion names them.
- **Imports.** An import the advice needs that the file already has
  under another name reuses that name. gosim's translator gives all
  imports of a path one name, so a second import of qobs, as
  `quintqobs`, failed to compile.

**The publisher on gosim.** The edgepublish publisher runs unchanged
and with no qenv/weave:
- its `sync.RWMutex`, its `WaitGroup` and its background seal goroutine
  are gosim's;
- the seal records through `context.Background()`, so qgosim installs
  the run's recorder as qobs's global on every machine.

It passes the same checks as the woven qenv runs: explored runs conform,
and ManifestBeforeTable and NoRotationLock are caught. NoRotationLock is
caught in 29 of 30 runs. gosim's schedules interleave its unlocked
rotation with pushes far more often than qenv's woven locks do.

**The native runtime became cooperative.** With a Platform, it takes no
lock across the code under test. Three failures led there:
- **A lost mutex.** A `sync.Mutex` in starvation mode is handed to its
  next waiter. When that waiter's machine crashed, the mutex stayed
  locked, and Settle never returned.
- **A forgotten waiter.** A broadcast looping over channel sends
  switches goroutines at every send. A waiter that woke and re-registered
  in between was forgotten, and two paid orders never shipped. Waits are
  now a `sync.Cond` with no lock, whose `Broadcast` is one step of
  gosim's runtime.
- **Mismatched clocks.** A `time.Time`'s monotonic reading is relative
  to a per-machine global under gosim, so deadlines passed between
  machines compared wrongly. The native clock now drops the monotonic
  reading.

### 15.9 Phase 3 as built

**An overlay, not `-toolexec`.** The go command already takes
`-overlay`: a map from source files to the files that replace them. It
computes the build, imports included, from the replacements. So woven
code that imports qobs or qenv/weave, which the original did not, builds
and links with nothing else to do. Orchestrion gets this from
`-toolexec`, by editing each compile's import configuration and building
what is missing. `qweave go test ./...` weaves the module's package
graph, writes the woven files and the overlay, and runs the go command
with it. gosim takes the same overlay through `GOSIM_OVERLAY`. The fork's
own weaver was removed, so qweave is the only weaving engine, and the
fork's change is generic: any tool's overlay.

**qweave** lives in a module of its own, because it needs `x/tools`.
- It reads Orchestrion's aspect files, from the packages a tool file
  imports.
- It supports what our aspects use:
  - join points: `function-body`, `method-call`, `function-call`,
    `function`, `directive`, `package-filter: root`, and the
    combinators;
  - advice: `prepend-statements` and `wrap-expression`, with
    Orchestrion's template data.
- It weaves on typed packages and edits the original text. Nested
  matches compose: each template is rendered from the woven text of the
  spans inside it.

**What types add.** Orchestrion matches syntax. qweave matches types,
so it weaves what Orchestrion missed in §13:
- **method values.** `return s.mu.RUnlock` becomes a literal that binds
  the receiver when the value is made, then makes the advised call;
- **promoted calls.** A call through an embedded `sync.Mutex` is advised
  on the field, `(c).Mutex`.

A fixture uses all three escapes Orchestrion misses: an embedded lock, a
method-value unlock and a `//qenv:go` goroutine. Woven, it runs in a
qenv simulation in 16 ms. Unwoven, it hangs the scheduler.
edgepublish's woven tests (sim, mbt and scenarios) pass under qweave as
under Orchestrion. CI now runs the simulation with qweave, and mbt and
scenarios with Orchestrion, to check compatibility.

**Channels were not woven.** gosim simulates channels and `select`
natively. On qenv's scheduler, a simulated channel would need `select`
over simulated and real channels, and the runtime's own channels such as
`ctx.Done()` and timers. Code with channels belongs on gosim, which
phase 1 made a runtime for the same deployment.

**Found on the way.** A woven goroutine that captured another task's
context slept and waited as that task, and lost increments. Code in a
woven goroutine must record and sleep through `context.Background()`,
which qenv resolves to the goroutine's own task. The skill says so.


### 15.10 Phase 4 as built

**Load roles (`qrole`).** Three annotations, woven by qrole's aspect
file (`//quint:serve name:N`, `//quint:operation service:S`,
`//quint:call service:S`), or the calls they weave (`Serve`, `Operation`,
`Call`), each returning a function that takes the outcome:
- u is a server's requests from arrival to response, waiting included
  (`Roles.Queue`);
- v, the orbit, is the operations in progress with no attempt in flight:
  operations minus calls (`Roles.Orbit`). It needs no knowledge of the
  retry policy, which is why the client side is two annotations;
- `Roles.Instrument` exports them as OTel gauges, with counters of
  requests and attempts by outcome. A simulated run gets a registry of
  its own through its context (`WithRoles`), as with recorders.

**Action metrics.** `qrole.MetricsSink` counts steps by action and outcome
(`quint.steps`) and passes them on. Every `//quint:action` becomes a rate
and an error ratio, counted as the model counts steps. Latency was left
out: a step has one timestamp, at its effect.

**Calibration from observations.** `qmeta.Trajectories` turns a series of
sampled (u, v) into Calibrate's trajectories: from each sample, the
states some seconds later, averaged per cell of the state grid. Any
series works, a qload run's probes or a production scrape.
- A series taken mostly in a storm cannot tell a higher arrival rate
  from a shorter timeout. The fit took λ to μ and above, where nothing
  recovers. `CalibrateTimeout` holds λ, which a running system measures
  (its rate of new operations), and fits the timeout.
- Loss evaluated each trajectory with its own transient analysis, 12,000
  states each time, and a calibration from one run's cells had not
  finished after several minutes. Expectation is linear, so one backward pass
  (`MeanAll`) gives the expectation from every state at once. The same
  calibration takes 5 seconds.

**Alerts from the chain.** `Chain.Region(healthy, bound)` is the set of
states the system is more likely than not still unhealthy from after
`bound` seconds (`RecoveryWithin`: one more backward pass, with healthy
states absorbing).
- The first version used the expected hitting time, and alerted at
  (13, 0) at λ = 9, μ = 10: a state that drains in about a second. Near
  a storm, the mean is dominated by the rare run that tips into it and
  stays for hours. So the probability defines the region, and the mean is
  still reported (`Region.Recovery`).
- `Region.Rules` writes a Prometheus rule group over qrole's metric
  names: one term per band of u, `u in [lo, hi) and v >= vmin`, coarsened
  to bands of `Step` (10) that each take their largest vmin, so the rule
  fires only inside the region. The chain's parameters, the healthy set
  and the bound go into the annotations, so a policy change in code is a
  diff in the rule.

**The example (`examples/retrystorm/alerts.go`).** A server and a
retrying client, instrumented with qrole, run under a spike in a qload
experiment that probes u and v. The chain is calibrated on the run, its
region taken at two minutes, and the alert checked against the run.
- With backoff alone it holds for the whole last minute of the storm
  (u = 100, v = 143).
- With a retry budget the chain has no region: the rule cannot fire, and
  the run has recovered.
- Without a spike it never fires.

### 15.11 Phase 5 as built

**Fault points.** `qenv.FaultPoint(ctx, kind, crash, errs...)` is a fault
choice `fault.<kind>` at a point in the code under test.
- Its options are nominal, then each error in order, then, with crash
  and inside a process, the crash of the calling task's process. The
  process is restarted by a task outside it, as the nemesis restarts
  one. Outside a simulation it returns nil.
- `//quint:fault kind:K error:E... crash:true` weaves it at a function's
  entry (`qenv/fault`'s aspect). An injected error returns through the
  function's named results, which weaving names if they are not. So the
  error is the package's own, typed as the code expects, not a generic
  one. A function without an error result can only crash.
- The same aspect declares the point at init, with a second advice on
  the same join point: Orchestrion's `inject-declarations`, which qweave
  now supports, together with several advice per aspect and
  `.Function.Name`. Written to Orchestrion's schema, checked with qweave
  only.

**Coverage from choices.** A run's recorded choices already hold every
fault choice it reached and the option taken. So coverage needs nothing
from the simulation:
- `qenv.CoverageOf(runs)` and `qtest.Exploration.Coverage()` count per
  kind, declared points and doubles' kinds alike, the runs that reached
  it, the times reached and each option taken;
- `Unreached` lists the declared points no run reached, and `Unfaulted`
  those reached but never faulted. A point declared outside any run
  (woven code's, at init) is the process's, every run's; one declared
  in a run is that run's (`hooks.FaultNames`' sites, `Sim.FaultSites`,
  `qenv.CoverageWith`): declared for the process, one test's points were
  every later test's unreached ones, and a test's verdict depended on the
  order tests ran in.

**A Quint trace as a Chooser.** `qenv.Script` makes exactly the faults it
is given, in order, and no others. Each waits for the steps before it in
the trace (`Progress`, the run's recorded steps). The schedule is
nominal, or a base chooser's.
- `qtest.FaultScript(trace, FaultMap)` reads one from a trace: each
  mapped action is a fault kind and option, or a crash
  (`nemesis.crash.<process>`).
- The trace then drives a whole program through its environment, not
  through a driver: the program makes every other step itself, and its
  recorded run is checked against the model.
- A nemesis is chosen by index among the enabled ones, which a trace
  cannot name. The scheduler now asks a chooser that implements
  `ChooseNemesis(names)` by name, through the package's wrappers, and a
  Recorder still records the choice.

`connect/testdata/journal.qnt` is a writer whose writes fail and which
restarts once:
- under each of 8 traces, the program, which knows nothing of traces,
  takes the trace's actions, failed writes and restart included, and
  conforms;
- the mutant that keeps its count in memory follows the same traces, and
  the model rejects its restart (`Mismatch("restart")`).

## 16. Covering arrays (`qsweep`, `qtest.System.Sweep`)

Explorations sample the schedule and the faults. Configurations were a
product written by hand: checkout's isolation levels × mutants, three
sizes × three levels, and so on. Most failures need only a few factors to
interact, so rows in which every pair of factor values appears find what
the product would, at a fraction of its runs (Kuhn et al.; Microsoft's
PICT).

**Generation.** `qsweep.Cover` enumerates the allowed rows of the space
and picks, greedily, the row covering most uncovered t-way tuples, until
none is left.
- A constraint is a Go function of a whole row, and tuples that only
  ruled-out rows contain are not required. That is the hard part of
  PICT's constraint language, for free.
- It finds 9 rows for four 3-valued factors pairwise (the optimum) and 33
  for three-way (27 is optimal).
- The space is bounded (`MaxSpace`, 2M rows), far above what a test
  sweeps.

**Sweeps.** `System.Sweep` explores each row as a subtest, with
per-row options (the model's constants for the row's sizes) and per-row
expectations.
- Fault kinds are factors (`qsweep.Bool`), turned off with `qenv.Without`.
  Swarm testing chooses them at random. A covering array guarantees that
  every pair of kinds runs together, and that each runs without the
  others masking it.
- `Without` and `Swarm` had to keep the run's context outermost: `New`
  finds it only on the chooser it is given.

**Pair coverage.** `qenv.Coverage` now keeps each run's faulted kinds in
the order of their first fault:
- `Pairs` counts, for each two kinds, the runs that faulted both and in
  each order;
- `Untried` lists the pairs never together;
- `OneWay` lists the pairs seen in one order only.
Checkout's random exploration (60 seeds) had every pair together and six
in one order only.

**Orders.** A `qenv.Script` per ordered pair imposes both orders, on a
seeded schedule. All 30 orders of checkout's six kinds happen, and every
run conforms.
- A sequence covering array (`qsweep.Sequences`: every order of t events
  as a subsequence of few sequences) did not fit checkout. A Script
  imposes its faults strictly in turn, and the API's faults happen only
  while orders are placed, so a sequence of all six never completes.
- Sequences suit events that recur through a run, such as crashes and
  partitions.

**What the checkout sweep found.**
- The faithful shop conforms on all 13 pairwise rows of the 432
  combinations of isolation × orders × stock × crashes × three fault
  families (260 runs).
- `NoIdempotencyKey` captures with a fresh key on every attempt, and it
  is two bugs:
  - a cancellation's void cannot find the charge, in every row, with or
    without faults;
  - a double charge, which needs lost provider responses: some run of
    every row with them charges twice, and no run of a row without them
    does.

  The mutants table expected only the second. `Expect(Violates(x))`
  passes on the first run violating x, so the first bug never showed
  there. The README did name both.
- Rows with other stocks than the binding's failed every run until the
  sweep passed the model the row's `STOCK`: sizes are factors of the
  model too.

## 17. Code coverage by model step (`qcover`, `qweave -cover`, `qrun`)

The fault coverage of §15.11 says which faults runs tried. This says which
code they ran, step by step, so coverage can be read against the model
(docs/HISTORY.md, A1).

**Why weave, not Go's `-cover`.**
- Go's counters are global to the process: runs in parallel (Explore's
  seeds) would mix.
- Reading them mid-run means parsing an internal file format.
- qweave already rewrites the code under test, so it inserts the
  counters itself. `qweave -cover` puts a call of `woven.Hit(id)` at the
  start of every straight-line run of statements, splitting after
  compound or branching statements as cmd/cover does, on the line it
  starts so positions do not move. Each file registers its blocks in a
  package variable.
- Only the main module is instrumented, and never the toolkit's own
  packages, its examples aside: qcover would count itself, and qenv's
  scheduler is not code under test. `-coverpkg` narrows it further.

**Attribution.** A block finds its run's `Counts` and its task through the
goroutine.
- The blocks a task runs between two of its steps are its next step's,
  because steps are recorded where they take effect, at the end of the
  code that made them.
- Blocks are attributed per task, not per stretch of time, so an
  interleaved task's code never lands in another task's step. Two tasks
  interleaving at every step, in four parallel runs, pass under `-race`.
- Code that causes a step in another task is "run, but by no step of the
  task that ran it". That covers a client whose step the service records,
  a relay, setup, and code after a task's last step: the report lists it
  by function.

**The report** (`Evidence().Code`, a `qcover.Summary`):
- statements covered;
- blocks never run, by function;
- code run by no step;
- the blocks each action and outcome ran;
- outcomes of one action that ran the same code: the model tells them
  apart, and these runs' code does not.

On checkout under exploration, 30 seeds cover 76.8% of the shop's
statements. The fixture (qweave/testdata/cover) shows each category: a
fault branch is put/error's alone, a get records both outcomes from the
same code, Compact never runs, and setup and Close run under no step.

**One lookup of a run's state (`qrun`).** qcover needed what qobs and qrole
already had, in different shapes: a value carried by the run's context,
found from the goroutine when the context carries none.
- qrole had no goroutine fallback. Its woven calls in a function with no
  context passed `context.TODO()` and counted into `qrole.Default`,
  shared by runs in parallel.
- `qrun.Key[T]`, `qrun.Get` (the context, then the goroutine's run: a
  task's context, or the simulation's for the goroutine that created it)
  and one resolver installed by qenv now serve all three.
- Every task's context names its task (`internal/runkeys.Task`), for state kept per
  task.
- `qobs.SetFallback` is gone.

**Found on the way.**
- A switch's or select's body is a block of clauses, not statements. A
  coverage call before `case` does not compile; the clauses are
  instrumented, not the body.
- An insertion at the offset where an aspect replaces an expression
  (`mu.Lock()` woven by qenv/weave at the start of a block) must go
  before the replacement, not inside it. qweave orders an insertion
  first among the edits that start at its offset; the sync fixture runs
  woven with `-cover` too.
- A test binary's generated main is in the build cache, outside the
  module: it is not instrumented.

## 18. Claims over runs (`qtest.Some`, `All`, `None`)

`Exploration.Expect` meant two things:
- with `Conforms()`, that every run conforms, recognised by comparing its
  description string;
- with anything else, that one failing run meets all the expectations.

So `Expect(Violates("a"), Violates("b"))` wanted a single run showing
both. Nothing could say "no run of this row double-charges". The
checkout sweep walked reports by hand for it, and the mutants table's
first-match semantics hid NoIdempotencyKey's second bug (§16).

A `Claim` judges a set of runs:
- `Some(es...)`: one run meets them all;
- `All(es...)`: every run meets them all;
- `None(es...)`: no run meets any of them.

An expectation alone is a claim about some run, and `Conforms` is about
every run (a flag on the expectation, not its description). Several
claims are judged one by one, and each failing claim is reported with the
run that shows why. A `Some` claim that no run meets, while every run
conforms, names the fault kinds no run faulted: the usual reason a bug
did not show. No call site passed several expectations meaning "one run
with all", so the change broke no test.

`qhegel.Violation` now carries the shrunk run's full report: the
shrunk run is checked once more, since cached verdicts keep only their
summary. With `Violation.Expect`, the four tests that matched strings in
summaries state their verdicts like the rest (`Rejects("rotateGen")`,
`Violates("convergedWhenSettled")`).

## 19. Choices aligned to steps (U3)

A run had two records that did not meet: the steps it recorded and the
choices it made.
- A Script waiting for the steps before a fault needed a `Progress`
  closure wired to the run's sink by hand.
- A shrunk Hegel failure kept its steps but not its choices, so it could
  not be replayed outside Hegel, run on gosim, or kept as a seed.

**`Choice.Step`.** `qenv.ChoiceLog` stamps every choice with the run's step
count (`qobs.Recorder.Count`). The run's recorder is found once, through
qrun, on the first choice. A choice's step says which step a fault came
after, what fault-to-action coverage will build on. A Script with no
`Progress` waits on the same count, so `FaultScript`'s callers set
nothing.

**`qhegel.Case.Env(c)`** is the chooser a test case's simulation
runs on: `c`'s choices recorded into the case's recorder, the recorder in
the chooser's context. It replaces
`qenv.WithContext(qhegel.Choices(tc), rec.Context(ctx))`. The shrunk
`Violation` carries its `Choices`. Checkout's write-skew test replays
them with `qenv.Replay`: the same steps, rejected the same way, with no
Hegel in the run.

Writing that test hit P3 of the review: `qtest.Check` fails a test on a
violation before `.Expect` can say it was wanted. The test uses
`Validate`.

## 20. One system, every workflow (`qtest.System`, U4)

Each workflow wired the same pieces by hand: a scenario that runs the
app, its recorder, the chooser's context, and the binding passed again at
every call.
- connect's trace-following test was 40 lines of `GenerateRun`,
  `FaultScript`, a sink, a recorder and the `Progress` closure.
- Checkout's tests called `t.Fatal` from seeds' goroutines.

`qtest.System{Binding, Constants, Opts, Run}` binds an application to its
model once. `Run(c) error` deploys and runs it on a chooser. An error
means it could not run, which fails the test and names the seed; it is
not a verdict.
- `sys.Explore(t, cfg)` explores seeds. `ExploreConfig.Chooser` gives
  each seed its own chooser instead of a seeded one.
- `sys.Sweep(t, sw)` sweeps covering rows, the System's own Run reading
  the row from its chooser (`qtest.RowOf`, V4; W4 removed the per-sweep
  run function, and `qsweep.FaultKind` factors turn kinds off).
- `sys.FollowTraces(t, trace, faults)` generates the model's traces,
  scripts each one's faults, runs the system on each script, and fails a
  run whose script did not complete (`Remaining`), the P6 message.
- `qhegel.Shrink(t, sys)` lives in qhegel, which imports qtest:
  Hegel stays out of qtest. It is FindViolation over `sys.Run` on
  `rec.Env(Choices(tc))`.

connect's test is now a System and two calls. It runs in 5 s instead of
9 s, because Explore checks the traces' runs in one batch. Checkout's
replay of its shrunk write skew is an exploration of one seed whose
chooser is `qenv.Replay(v.Choices)`.

Left for later: one `Run` type for exploration runs, Violations and
qgosim's, and `gotogether scaffold` printing a System skeleton.

## 21. Production workloads from SimKube traces (`sktrace`, D1–D2)

SimKube captures what happens to a real cluster's objects (sk-tracer)
and replays it on KWOK. It has no determinism, no faults and no oracle:
for us it is a source of workloads.

**`sktrace`** reads its trace format (v3: msgpack of `{version, config,
events, index}`) with a decoder of the subset it uses. The core module
stays free of a msgpack dependency.
- Events are the objects applied and deleted each second.
- The index gives pods' lifetimes, keyed by the owner's version.
- Derived views:
  - `Rates` (events per second of a tick);
  - `Gauge` (a value over the objects alive at each tick: desired
    replicas with `Replicas`);
  - `Starts` (pod starts per second).

SimKube's three example traces are its test data. The CronJob trace
shows why the index is keyed by version: the CronJob's second version
runs 45 s jobs where the first ran 30 s.

**D2, measured load shapes.** `qload.Piecewise(tick, scale, values...)`
makes a series a load shape. The scaling trace's desired replicas (27, up
to 52, back to 42) at 0.2 requests/s each pass the server's capacity for
two minutes and settle at 8.4 requests/s. Backoff alone stays in the
storm in 2 of 3 seeds; immediate retries and the budgets recover in all
3. It is the same ranking as the synthetic spike's, on a cluster's own
scale-up.

**D1, a replayed workload.** `kubesim.Fleet.Replay` plays the trace's
Deployment scalings onto the fleet's members, 60 times faster, as
another writer, while the rollout runs, through the same faults.
- The first replay showed the model had no other writers: a
  non-rollout update had no step.
- `scale` is the environment's spec change, recorded at the API server
  for writes marked with `kubesim.AsWriter`. The generation moves on, and
  the cluster is complete again only when its status catches up.
- With it, the faithful controller conforms over 30 seeds (540 scale
  steps), and `IgnoreObservedGeneration` is still caught.

## 22. Profiles and measured service (B1–B2)

**B1, labels by role.** `qrole.Labels(true)` makes `Serve`, `Operation` and
`Call`, and so the woven `//quint:serve`, `//quint:operation` and
`//quint:call`, set goroutine labels while the role lasts:
`quint.serve=<server>`, `quint.operation=<service>`,
`quint.call=<service>`. A CPU profile then splits by server and service.
It is off by default, since a label set allocates. It needed no hook
refactor (review S5): the roles' runtime functions set the labels, and
the aspects are unchanged.

**B2, measured service.** `qmeta.Measure(samples)` summarizes a handler's
latencies, run alone:
- the mean, and `Mu` for the chain;
- the coefficient of variation `CV`, 1 for the chain's exponential
  service;
- `Exponential()`, whether that approximation is fair;
- `Draw`, the measured distribution for `qload.ServerConfig.Service` or a
  `qenv.Capacity`.

It changed a verdict. At the same mean (100 ms), constant service
recovers from the spike in 0 of 6 seeds where exponential service
recovers in 6 of 6.
- Constant service keeps a healthy queue half as long, but the spike fills
  it to 100 requests: 10 s of work against a 9 s timeout. Every request in
  it then times out, with certainty, and the server works only on
  requests nobody waits for.
- Under exponential service some still finish in time, and the queue
  drains.

The chain, being exponential, predicts the recovery. The measurement's CV
of 0 is the warning. I had expected the opposite: that regular service
would time out less.

## 23. Fuzzing over the Chooser (A3)

**One byte rule (`qenv.Bytes`).** A schedule choice is a byte modulo n.
A fault is nominal when its byte is below 256 × (1 − rate), and otherwise
1 plus a byte modulo n − 1. Past the end of the stream every choice is
nominal. The rule lived in qhegel's chooser and, in another form, in
the gosim fork. It is now qenv's: Hegel's chooser is the rule over Hegel's
draws, and `FromBytes` is the rule over a fixed input. `EncodeChoices`
inverts it: a recorded run, or a shrunk Hegel failure's choices, becomes
the bytes that make it again. A test turns checkout's shrunk write skew
into an 834-byte corpus entry that replays to the same steps and the same
rejection.

**`System.Fuzz(f, seeds...)`** (at the System's Rates since Z4). Each fuzz input is one run on
`FromBytes(input)`, judged by the model. `go test` runs the corpus and the
seeds; `go test -fuzz` explores, guided by coverage of the code under
test. Seeds and Hegel are random and shrinking respectively; neither is
guided by coverage.

**Verdicts by steps.** With quint judging every input, the fuzzer made 16
runs in 90 s. Most mutations change bytes that the run's model-level
steps do not show (a schedule choice between equivalent tasks, a byte
past the faults that matter). `qtest.VerdictCache` keys verdicts by the
steps, with recorder, process and thread ids renumbered by first
appearance and times left out. With it, the same 90 s made 14,633 runs
and found 30 inputs reaching new code. The cache also deduplicates
identical runs within one exploration. CI fuzzes the faithful shop for 60
s.

**Found on the way.** The scheduler's self-check (`GOTOGETHER_CHECK=1`) failed
on a test with frequent crashes: "task is past its deadline and not
runnable". A task killed while it waits keeps its deadline and rightly
never runs again. The check excluded killed tasks in its timer clause but
not in the deadline clause. The fix is to the check, not the scheduler;
`TestSelfCheckUnderCrashes` keeps it.

## 24. Chooser wrappers see through (U5, S2)

`EntropyOf` and `nemesisOf` switched on the package's own wrapper types,
and `New` and `ContextOf` looked only at the outermost chooser. So a
user's wrapper, or a `Script` over a `Base` carrying the run's context,
hid the context, the entropy (gosim runs about 9× slower without it) and
nemeses by name. `Swarm` and `Without` had to re-hoist the context by
hand.

A wrapper now implements `Unwrap() Chooser` (`qenv.Wrapper`), as
`WithContext`, `Recorder`, the swarm, `Script` (over its `Base`) and
qgosim's entropy wrapper do. `As[T](c)` finds the first `T` in the chain,
and the lookups walk it. A Recorder still intercepts what it passes on,
to record it. The hand re-hoisting is gone, and so is the gotcha that a
wrapper of a `WithContext` chooser must keep the context outermost.

## 25. Fault options by name (U5, S3)

A fault choice was an index, and the same kind took different option
counts at different calls: `kube.request` has 2 options on a Get (fail)
and 3 on an Update (fail, applied with the response lost). Coverage's
`Injected[2]` then meant nothing without knowing the call, and a
`Scripted{Option: 2}` panicked if a Get came up first.

`qenv.DeclareFaults(name, options...)` declares a kind's options, nominal
unnamed, and `Sim.FaultOf(kind)` returns "" or the option's name.
`kind.Only(...)` restricts a call to a prefix of the options, and only to
a prefix, so an option's number means the same option at every call. A
non-prefix panics. With that invariant:
- coverage names options from the declaration ("lostResponse 13");
- `Scripted.Name` maps to the option's number;
- a choice that does not offer the scripted option leaves the fault
  waiting, where it panicked before. The reads pass, and the first update
  loses its response.

The doubles (RPC, queue, store), kubesim and crossdb declare their kinds;
nothing in the byte rule, the recordings or the rates changed. The
`fault.` prefix in rates, `Without` and `Swarm` is still a second
spelling; changing it would break every rate table, so it stays until a
breaking release.

## 26. Mutant schemata (A2)

Coverage says which code ran; it does not say whether the model cares
what the code did. A mutant answers that: change the code, explore, and
see whether any run is rejected. A survivor in covered code is code the
model does not constrain.

Building one binary per mutant costs a link per mutant, and a simulation's
explorations are short. `qweave -mutate` instead weaves every mutant of
the main module (or `-coverpkg`) into one build, each behind a switch
(mutant schemata):
- an `if` condition `c` becomes `quintwoven.Cond(id, c)`, negated when on;
- a pure comparison (operands without calls) becomes
  `quintwoven.Pick(id, a < b, a <= b)`: its boundary moved; an `if` whose
  condition is one gets both, nested;
- a call statement (not `panic` or `print`) becomes
  `if !quintwoven.MutantOn(id) { call }`: dropped.

Each file registers its mutants at init (`woven.RegisterMutants`, §60), with the
function, the operator and a one-line snippet. A run turns one on with
`qmut.WithMutant(ctx, id)`, found through qrun like a recorder, so seeds of
one mutant, or of different mutants, run in parallel in one process. A
`qmut.Reach` notes the mutants whose code a run reached, on or off.

`qtest.System.Mutate` explores the baseline (which must conform, and
gives the reach), then each reached mutant with the same seeds. A mutant
is killed by a rejected run, a `Run` error or a panic. The explorations
share a `VerdictCache`: most mutants' runs repeat steps already checked.

On checkout (SnapshotIsolation, 20 crashy seeds, about 8 minutes):
67 mutants reached, 12 unreached. The first run killed 48. A survivor was
`Cancel` dropping `tx.Put("stock/"+sku, stock+qty)`: the model restocks on
cancel, but no step carried the stock and the binding compared nothing.
Place and cancel now record `obs.stock`, and the binding expects
`stock == {{.obs.stock}}`; the restock mutant is killed (49, 73%). The
other survivors:
- equivalent under the environment: dropped acks (the queue redelivers,
  the consumers are idempotent), dropped rollbacks (an uncommitted
  transaction is discarded), the retry loop's boundary and its sleep, a
  fourth payment worker;
- the relay's `Delete` of a sent outbox row: resending is idempotent;
- mutation switches (`NoLocks`, `DualWrite`) negated: equivalent under
  snapshot isolation, or a race the seeds did not reach;
- `PlaceOrder`'s error branches (`err != nil`, `ok` negated): a shop that
  places nothing records nothing, and a safety model accepts a run with no
  steps;
- the refusal's own recording (two mutants dropping it) and the
  `ErrSoldOut` return negated: a missing refusal is a step the model
  cannot require (a client may give up before any), and the client's
  answer is no step.

Refusals are steps (V17). A refused order recorded nothing, so the stock
check was unconstrained both ways. The model has `refuse(order, qty)`,
enabled only when `stock < qty`, and changes nothing. The shop records it
from its read-only transaction's commit: returning ErrSoldOut from the
transaction would roll back and drop the hook, so it returns nil and the
error after. The binding compares the stock. Both stock-check mutants
(negated, `<=` for `<`) are now killed: 70 reached, 51 killed (73%), 19
survive, 12 unreached. The three new mutants, in the refusal code, survive:
they are among the bullets above.

The report is a list to read, not a score to assert: equivalent mutants
survive by right. `examples/checkout/mutate_test.go` logs it.

## 27. Messages, constants and one recovery API (U6: P6, P5, P4)

**Messages for the traps (P6).** The review's traps each cost a debugging
session; each now has its message where the trap shows:
- an exploration whose every run fails at step 1 names the binding's
  constants: the instance more likely disagrees with the scenario's than
  every run has one bug (`Exploration.Expect`);
- runs whose Script never made all its faults list them, in the run's
  report and in "all N runs conform; want ..." (`Explored.Unscripted`);
- every failing run's report ends with how to reproduce it:
  `GOTOGETHER_RUN='TestX#1:seed=17' go test -run '^TestX$'` (Y2; it was
  `GOTOGETHER_SEED`, which every exploration of the test read), and that
  exploration runs the seed alone;
- a recovery judged against goodput far below the offered load says the
  baseline was unhealthy (`Recovery.Warning`, in its `String`);
- a chain whose arrival rate is at or above capacity says so, and that a
  calibration that got there was fed storm data (`Params.Warning`).

**Run-declared constants (P5).** A sweep's size went to the system and to
the model separately, and rows failed until both agreed. A factor now
names its model constant (`qsweep.Ints("stock", 1, 2, 3).Constant("STOCK")`)
and each row is checked with it (`Row.Constants`). The run-attribute form
(a run declaring its instance in its recording) would let one batch mix
instances; the checker validates a batch against one instance, so it
waits until a sweep's rows are checked in one batch.

**One recovery API (P4).** `qmeta.System` is the one interface: a qload
simulation (`OfSimulation`) or an application (`Of`), run with `Runs`,
`Count` and `Mostly`. `RecoversMostly` and `SearchTrigger` are gone, and
`qhegel.FindTrigger(t, sys, space)` takes any System and returns a
`TriggerSearch` judged by `Expect(t, found)`, as FindViolation's result
is. `MaxExtraRate` no longer defaults to the base rate (the System does
not know it): set it.

## 28. Small unifications (U7: S5, S6, S7)

**S7, the orbit.** The chain's v is the papers' orbit: an arrival that
will time out enters u and v together, and leaves v after the timeout
and the backoff. That is `qload.Sample.Orbit`, known in hindsight. qrole's
v (operations in progress with no attempt in flight) is what a running
system can see: the waiting part, now also `Sample.Waiting`, so one run
has both. In the paper's storm (9.5 req/s, a spike to 20) the orbit is 79
and the waiting part 0 under immediate retries; with a 5 s backoff, 215
and 134 (`TestTwoOrbits`). So:
- a chain calibrated on qrole's gauges (`CalibrateTimeout`) is a chain of
  the waiting part, and its fitted timeout absorbs the attempts in flight;
- under immediate retries v is invisible to a running system, and an
  alert on it sees only u. retrystorm's alerts are on backoff, where
  the waiting part is most of the orbit.

**S6, duplicates.** The simulation lint moved from qenv to
`internal/lint` (then `qlint`, §31): gosim translates everything a simulated package imports,
and qenv pulled go/ast, go/parser and go/token into every translation.
`Run.Faults` (then `Explored.Faults`) reuses `Recorder.Faults`. Kept: qload's step counter,
which samples per tick inside the simulation (merging it with
`qrole.MetricsSink` would make qload depend on OTel metrics), and the
simdeps list, which a derivation could only replace with a deny-list of
host packages or a gosim build of examples/gosim.

**S5, one hook entry.** Superseded: B1 put the pprof labels in qrole's
one `track`, which Serve, Operation and Call share; a hook package would
add an indirection for three call sites.

## 29. One evidence report (U8, P8)

A green exploration said "every run conforms" and nothing about what the
runs did. Its other reports (fault coverage, code coverage, a sweep's
rows, mutants) were separate calls with separate formats. `Evidence`
puts them in one: verdicts by reason ("violates chargedAtMostOnce",
"rejects ship", "mismatch after place"), fault kinds faulted and pairs
together, rows, code and mutants, each part present when its data is.
`Exploration.Evidence` and `SweepResult.Evidence` make it; a mutation report is
added by the caller (`Mutants`), since `System.Mutate` runs explorations
of its own.

The review's other parts of P8 wait: coverage gaps as expectations
(`Reaches`, `NoSurvivors`) need a decision on what a test may demand of
chance (a pair of kinds is reached in most seeds, not all), and a JSON
form next to `GOTOGETHER_KEEP` needs the reports' types to marshal.

## 30. Choreo models (examples/twophase)

[Choreo](https://github.com/informalsystems/choreo) is Informal's
framework for distributed protocols in Quint: nodes with local state, a
message soup (messages are delivered by being in a node's inbox, never
lost or reordered away), and listeners returning the transitions a node
can take. Its `step` picks a node and a transition itself:
`nondet v = oneOf(processes)` in the framework and `nondet transition =
oneOf(transitions)` in `process_transitions`. Two consequences for us:
- a recorded step says "p2 prepared"; nothing in a `step`-only model can
  be called with that;
- `quint run --mbt` reports `mbt::actionTaken = step` and a transition
  record, which a driver could only match by its post-state.

Choreo already has the deterministic form: `choreo::step_with(node,
listener, effects)` runs one listener for one node (its own tests use it),
and `with_cue(...).perform(...)` one transition with parameters. A model
that names each transition as an action of its node over `step_with`, and
chooses among those in `step`, is a model like any other: bindings call
`prepare("p2")`, `--mbt` names the action and the node, and connect
compares a driver's state with `choreo::s.system`. So "supporting Choreo"
needed no code: connect already strips the `m::choreo::` qualifiers, and
the replay module imports the model, whose relative imports find the
vendored framework.

What the example taught:
- Choreo's message soup is a good fit for an implementation over a lossy
  network: a lost message is a transition not yet taken, a duplicate is a
  message already in the set. Retries need no model of their own.
- The implementation's protocol state is the model's local state, so the
  steps are recorded where the stage changes, in the node's store
  (OnCommit): a crash after the commit keeps the step.
- `choreo::s` is module-private; a binding's expectations need a def
  (`stage(n)`) in the model.
- Choreo's listeners leave role guards to the local state; the named
  action should check the node is of the right kind, since the node now
  comes from the code.
- Of three mutants, the model's traces (connect) catch one (committing on
  the first vote); the two that need a late decision or a crash show
  only in explored runs: the same split as bank's, now on a protocol.

## 31. Analyzers and the static action map (C1, C5)

**Analyzers (C1).** `gotogether lint -sim` and `-weave` were directory
walks; the analyzers wrap the same per-file checks (`qlint.SimFile`,
`WovenFile`), so the CLI and quintvet agree finding for finding, and
`gotogether lint` stays std-only. x/tools went into a new module,
qanalysis, not qweave: qweave is a build tool with no dependency on the
core, the analyzers need the core's binding and lint packages, and a
golangci-lint plugin is a dependency of its own that a weaver should not
carry. quintaction adds what the CLI could not check without types: an
annotation's expressions may use only parameters, the receiver and named
results, since the advice records on return, at the top of the body,
where locals are out of scope. With `-binding` it runs
`binding.LintAnnotations` per annotation (loading the model once per
process: it runs quint). Run on edgepublish with its binding it reports
the same as the CLI: `probe`'s `woven` action is not in the model (the
weaving probe; left as is).

It also checks explicit steps: the literal action of every `qobs.Record`
and `(*qobs.Recorder).Record` call must be one of the model's
(`binding.LintRecords`, with the nearest action and the list). The scan
is the binding package's (`binding.RecordsOf`, syntax only, so the CLI
shares it); the analyzer confirms each call is qobs's by type, which the
CLI approximates by skipping calls on other imported packages. quintsim
checks test files, like `gotogether lint -sim`: a System's `Run` closure is
test code, and a `go` statement there escapes the run as surely as one
in the library.

gopls cannot load third-party analyzers; editors get them through
golangci-lint (the module plugin `gotogether`) or `go vet -vettool`.

**Static action map (C5).** A1 says which code each step ran; the map
says which code each action can run. Sites are `//quint:action`
functions and functions calling `qobs.Record` with a constant action
(checkout records in `OnCommit` closures, so a site's closures count as
the site). Reachability is a VTA call graph from the sites, stopping at
functions outside the loaded packages. Without the stop, every closure
handed to qenv's commit hooks was reachable from every other, and cancel
reached PlaceOrder. Names follow qcover (`pkg.(T).M`, closures under their
declaration), so the two maps compare function by function.

On checkout (30 crashy seeds at SI):
- `Deploy` is run by the first step of every task: setup, attributed to
  the task's next step (A1's rule), and reachable from no site;
- markPaid's steps run `RunPayments` and `inbox.Receive`, which `pay`
  does not reach: code that leads to a step lives in the site's callers,
  which downward reachability cannot see;
- `settle`'s site is the run driver (`RunOn`), so it reaches everything,
  and all of it runs under other actions: a driver-recorded step says
  nothing about code;
- nothing reachable from a shop site went unrun.

Deviation: "blocks" reachable was planned; the map is by function. qcover
blocks carry their function, and an SSA block is not a qcover block (the
weaver splits at statements, SSA at control flow), so blocks would need a
position join for little gain over A1's never-run list.

## 32. Skills restructured (U8, P7)

Eight skills had grown to 1,500 lines, the fault-injection one to 411:
an agent loading it for a first exploration read weaving, gosim, sweeps,
fuzzing, coverage and mutants too. Now:
- `gotogether-start` is the entry: a question-to-tool table naming the skill
  to load next (and the same table heads the README);
- gotogether-faults keeps the workflow (seams, recording, deploy,
  explore and shrink, the real environment) in 202 lines, and its
  mechanics are references loaded when the task needs them
  (`references/fuzzing.md`, `fault-points.md`, `coverage-and-mutants.md`,
  `sweeps.md`, `weaving.md`, `gosim.md`, `workloads.md`);
- verdicts, expectations and claims over runs are one reference
  (`gotogether-workflow/references/verdicts.md`) the other skills
  link to, instead of each restating them.

`gotogether skills install` installs the references with the skills, and a
test checks every relative link between them resolves among the
installed files.

## 33. A flight recorder on alert, and a virtual-time profile (B3, B4)

**B3.** The alert rule (§16, `Region.Rules`) says a system is in a state
it is unlikely to leave in time; it cannot say what the code was doing.
Go's flight recorder (`runtime/trace.FlightRecorder`, Go 1.25) keeps the
last seconds of execution trace in memory. `qmeta.Watch` samples qrole's
(u, v) on the real clock, and once the state has been in the region for
`For` samples it writes the recorder's window out (at most once per
`Gap`): the storm's first seconds, with every goroutine's blocking and
scheduling. It is a production tool; the lint does not flag its ticker
because its file does not import qenv. One flight recorder may run per
process.

**B4.** A CPU profile of a simulation shows the scheduler: virtual time
costs nothing. `qenv.TimeProfile` records, at every wait of a task (Sleep,
Wait, a Signal: calls, locks, queues all wait through them), the stack
and the virtual time until the task ran again, and writes pprof's format.
The roadmap put it in a module of its own for pprof's protobuf; the five
message types it needs are hand-encoded instead (`pbuf`), so the core
takes no dependency. Two details: a return address is looked up as
itself (`CallersFrames` steps back to the call, inlined frames included;
handing it the call's address attributed an inlined call to its
neighbour), and a wait a crash cuts off is not counted (the killed task
never returns from park). The native runtime does not record it.

## 34. A real environment's coverage, and synctest (C3, C2)

**C3.** Go's `-cover` counters are global to a process and written to
`GOCOVERDIR` at exit: a real environment's binaries (Maelstrom nodes, a
kwok controller, an HTTP server) can report what they ran, but not under
which step. What a real environment can say is the comparison with the
simulation: `gocover.Read` (qcover/gocover, host-side) merges the runs' data with `go tool
covdata textfmt`, and `Compare` matches a simulation's blocks (by file
and line; Go's files are import paths, qcover's module-relative) over the
files the binaries contain. Code that ran for real and in no simulated
run is where the simulation's verdicts are silent: the doubles never take
that path. `gotogether cover -covdir ... -sim summary.json` prints both lists.

**C2.** The review's advice held: qenv's native runtime with no platform
(`NewNative(c, nil)`) waits with `time.Sleep`, `time.AfterFunc` and a
`sync.Cond`, all of which a `testing/synctest` bubble fakes and counts as
blocking. Checkout runs in a bubble unchanged (`TestNativeInSynctest`):
80 s of its timeouts, retries and redeliveries pass on the bubble's clock
in a few seconds, with real goroutines and Go's scheduler. The schedule
is Go's, so a run is not reproducible from its seed (its faults still
are the chooser's); it is for timing behaviour, next to qenv's own
scheduler for interleavings. No runtime interface was needed.

## 35. Structured logs as a step source (C4)

Many services already log what they did, with `log/slog`'s attributes.
`qobs.SlogHandler` wraps the application's handler: a record with the
attribute `quint.action` is also a step, recorded into the run of the
record's context, its other attributes the step's fields as `Record`
takes them (`obs.*`, `order.*`, arguments; groups join with "."), and an
`error` attribute its error. Every record still reaches the wrapped
handler, at that handler's level; a step is recorded at any level, since
the model needs every step and the log only what it keeps. Unlike
`convert` for spans, there are no rules: logs are written by the
application, so it can name the action, which a span's library cannot.

## 36. Selecting and sanitizing step logs (D4)

A production log is checked by people who should not see customers'
identifiers, and is mostly steps a check does not need. `qtrace.Filter`
selects (actors and actions by pattern, a time window: an actor is a
trace, so selecting by actor keeps whole runs) and sanitizes:
- pseudonyms for chosen fields: a salted SHA-256 prefix, the same for the
  same value, so the binding's interning and the model's comparisons
  mean what they did. Unsalted pseudonyms of guessable values (account
  names) are reversible; the CLI warns without `GOTOGETHER_SALT`;
- minimizing: with a binding, every argument and observation the binding
  does not read is dropped, and error texts with them
  (`Binding.UsedFields`, the same field analysis the annotation lint
  uses).

A model constant listing concrete identifiers must list their pseudonyms
(`Filter.Pseudonym`), as examples/bank's `TestSanitizedRunConforms`
does; interning the field avoids the question. `gotogether steps -out`
applies a filter and writes a step log for `gotogether validate`.

## 37. Scenario lint, measured pod times, dead code and untried error paths (D5, D3, A1)

**D5.** A replayed trace assumes a cluster: its first event applies pods
into a namespace it never created (all three of SimKube's example traces
do), owners may arrive after their pods, and a trace that starts mid-run
deletes objects it never applied. `sktrace.Trace.Lint(env)` checks each
event against a target that starts as `env` (namespaces, served kinds)
and says what to create or skip; a replay that ignored these fails at the
API server, or replays less than the trace recorded.

**D3.** kubesim's pods became ready after 100 to 300 ms by a schedule
choice. A trace's pod lifetimes (`Lifetimes`, from its index) give a
measured distribution; `Quantiles(ds, n)` makes n options of it, shortest
first, so option 0 stays nominal and Hegel's shrinking still moves toward
fast pods. fleetrollout's `Config.PodTimes` uses the CronJob trace's 30 s
and 45 s pods, 60 times faster: the faithful controller conforms and the
stale-status mutant is still caught.

The kwok tier is timed by the same numbers (R3). `fleetrollout.KwokStages`
turns `sktrace.Quantiles(tr.Lifetimes(gvk), n)` into kwok pod Stages, one
weighted pod-ready stage per option: the discrete, uniform choice kubesim
makes with `sched.kube.pod`. A real cluster's pods then become ready as
slowly as the trace's did. The generator lives in the example, not in
sktrace: sktrace stays a dependency-free decoder, and the stages'
templates are kwok's (v0.7.0's kustomize/stage/pod/fast), copied
unchanged. A test loads them with kwok's own config loader and matches
pods with its lifecycle package.

**A1, the rest.** Two classifications of code no run ran, both in
qanalysis/actionmap, which has the call graph:
- `Map.Dead`: own functions no root reaches (main, init, the exported API
  of library packages): dead, as `deadcode` reports, under qcover's names;
- `UntriedErrorPaths`: for a fault point the runs reached and never
  faulted (`Coverage.Unfaulted`), the blocks no run ran in its callers,
  where the fault's error is handled. A fault point is at its function's
  entry, so its error path is in the callers: qcover alone, without a
  call graph, could not place it. Read these as untried before reading
  them as dead: raise the kind's rate or script it.

## 38. gRPC services, simulated and for real (examples/grpcledger)

A gRPC service is checked the way checkout is, with one new seam:
`simgrpc` (first examples/grpcledger/simgrpc, ~150 lines; since H7 the
module qenv/simgrpc), which runs any
generated service inside qenv.
- `Server` implements `grpc.ServiceRegistrar`, so the generated
  `RegisterXServer` registers the real handlers. `Conn` implements
  `grpc.ClientConnInterface`, so `NewXClient` works unchanged.
- A call is protobuf-marshalled and crosses a `qenv.Service`, whose faults
  (dropped or duplicated request, lost response) and process crashes are
  qenv choices. simgrpc adds one kind of its own: a connection refused
  before sending.
- Transport outcomes become the status codes a real client sees:
  - no response in the connection's virtual Timeout: `DeadlineExceeded`,
    outcome unknown;
  - a full queue: `ResourceExhausted`;
  - a refused connection: `Unavailable`.
- Handler errors pass through as their status, and outgoing metadata
  arrives as incoming metadata.
- Streams, peers and the transport's own retries are not simulated. The
  client's retry policy lives in the client (`ledger.Client`), so both
  tiers run it.

The real tier serves the same handlers on a `grpc.Server` with otelgrpc.
At the commit, the server stamps its own span with the step
(`ledger.action`, fields, `ledger.seq` for commit order). The convert
rules (`ledger.convert.yaml`) then make the spans the steps, which is how a
production gRPC service would be checked from traces it already exports.
`gotogether validate -convert` checks them from the command line
(`run-demo.sh`).

What the example found:
- **The example's own first draft deadlocked.** Lock order was src, then
  dest; the faithful exploration's seed 22 recorded no steps. The
  simulation had found a real bug, but Explore said only "does not show
  the run conforms" with an empty report.
- **No crash window between commit and response.** A sweep row with
  crashes on and the network faults off conformed under `NoDedupe`.
- **Refusals are steps.** The model allows a refusal only when funds are
  short, so a ledger that wrongly refuses is caught too. (Checkout's open
  question, refused orders that record no step, is the same design point.)

Deviations from the brief:
- Unary RPCs only; NewStream returns `Unimplemented` in the simulation.
- The client's retry is in the client, not a gRPC service-config
  retryPolicy: the transport's retries cannot run over simgrpc, and a
  policy in code runs in both tiers.
- `SplitTxn` is checked in the simulation only. With MemDB's one lock per
  transaction, the real interleaving is a race the load rarely hits.
- No protoc on the machine: buf v1.50.0 compiles the proto, with
  protoc-gen-go v1.36.12 and protoc-gen-go-grpc v1.6.0. The generated
  code is checked in and the regeneration command reproduces it byte for
  byte.

## 39. Streams on Kafka and Kinesis (examples/streams)

A publisher relays a transactional outbox to a partitioned log;
consumers apply records to a projection, deduplicated by a per-key
version, and commit offsets after a batch. One model, one binding and
three logs: qenv's simulated log, Kafka through franz-go, and Kinesis
through the AWS SDK.

**The model learns the log from reads.** A broker's appends are not
observable in every tier. A real cluster has no hook, and recording at
the producer's acknowledgement misses records whose acknowledgement was
lost. So the model takes the log from the consumers' `read(partition,
offset, key, n)`: the first read of an offset learns its record, later
reads must agree, and an offset past what was read is not a step. The
properties are over the projection (`apply`), the commits (`commit`) and
what the publisher was told (`publish`). The binding has no templates.

**A simulated partitioned log, not qenv.Queue.** qenv's Queue is
per-message acknowledgement with redelivery, and has no offsets,
partitions or groups. simlog (first in the example module; since H7 the core's qenv/simlog,
its Kafka and Kinesis adapters modules beside it) is Kafka's contract:
- an idempotent producer client: sequence numbers per partition, the log
  rejecting out-of-order and dropping duplicates, 5 requests in flight;
- commits with lost requests and acknowledgements;
- rewinds;
- a consumer group with generations, eager rebalances and fencing;
- evictions as nemeses at any step (mid-batch: the zombie keeps
  applying while the new owner re-reads; the serializable projection and
  the version keep it idempotent).

It belonged in core only if a second user needed it: grist's streams
(GR5) are that user, so H7 moved it there. Kinesis's contract
is a subset (no idempotence, no groups), so it could serve that too.

**Findings while building it.**
- An argument named `seq` collides with the step's own field in binding
  templates (proposed core fix below).
- A publisher that republished everything on restart livelocked runs at
  per-step crash rates; the outbox fixed it, as it would in production.
- On a nominal run a plain test of the projection sees only `off-by-one`.
  The other three bugs need a fault, and the model names the property
  each breaks. `commit-first` needs no crash for the model:
  `committedApplied` fails at the commit itself.

**Deviations.**
- The real tiers inject no faults: kfake and the fake Kinesis run
  nominally. Their value is that the real clients, the adapters and the
  model agree.
- Kinesis runs one consumer owning every shard. Leases are the KCL's,
  with DynamoDB, and out of scope.
- Kinesis needed a fake endpoint (four calls of the JSON 1.1 protocol),
  not a Docker image (none needed: in process, like kfake).

## 40. What the examples asked of the core

The gRPC and streams examples found four things the core got wrong or
could not say; each is fixed where it belongs:
- **A step argument named like a step field** (`seq`) was shadowed in
  templates by the recorder's sequence number: `apply(seq=1)` replayed as
  `apply(4)`, and the error said only "not a transition". Arguments now
  win, and the step's own are `.step.seq` and `.step.action`.
- **Deadlocks were silent.** Two transactions taking row locks in
  opposite orders waited forever, the run recorded nothing, and Explore
  failed it with an empty report. `qenv.Store` now walks the lock-wait
  chain at each `GetForUpdate` and aborts the transaction that would close
  a cycle (`ErrDeadlock`, an `ErrConflict`), as PostgreSQL does; and a run
  with no steps says so in its report.
- **No crash between commit and response.** `qenv.Service` delivered the
  response in the handler's scheduling step. A scheduling point there
  lets a crash fall after the commit; opt-in at first (`CrashWindow`),
  since it changes the choices of runs recorded before it, and the
  default since the third review (`NoCrashWindow` closes it).
  grpcledger's `TestCrashWindow` shows crashes alone exposing a
  missing dedupe.
- **A domain clock written `attr:x|int` was dropped.** convert appended
  `|int` to every `order:` expression, so `attr:ledger.seq|int` became
  `attr:ledger.seq|int|int`, the lookup failed, and the order was
  silently empty: grpcledger's real-tier steps were ordered by span time,
  which concurrent requests contradict, and CI failed one run in a few.
  convert now adds `|int` only when missing, and grpcledger gives each
  request its own thread (`thread: span_id`), so a shared thread does
  not assert span time order as program order.
- **Long failing runs seemed slow to validate** (17 to 30 s for 160 to
  500 steps, measured while four agents shared the machine). Measured
  alone: a failing run costs one more quint run, the detail module (each
  step's expectation, ITF for the failing checks), about 3 s of quint's
  startup whatever its length (bank, 300 steps: 3.1 s conforming, 6.3 s
  failing; each quint invocation 3.3 to 3.5 s with or without ITF);
  streams' no-dedupe mutant, 20 seeds, 8 failing, validates in 13 s. All
  of a batch's failing runs share one detail run, so the cost is per
  batch, not per run. Nothing to fix; a machine under load multiplies it.

## 41. After the second review (V1–V17)

What changed in the core, and why, item by item (HISTORY's "Next" has the
plan; the commits have the detail).

**Boundaries (V10).** qmeta is the chain and its alerts, and imports
nothing of the module: the recovery property and the trigger search's
System (`Recovers`, `Assess`, `Of`, `OfSimulation`, `System`,
`TriggerSpace`, `Runs`, `Count`, `Mostly`, `Window`) run simulations, so
they are qload's, and `qmeta.Simulate` is `qload.SimulateTrajectory`.
qenv's choosers and their byte rule are `qenv/choice`, which needs no
simulation (a fuzz corpus, gosim's host side); the virtual-time profile
is `qenv/profile`. qenv re-exports both, so no caller changed. (The third review made both internal,
`qenv/internal/choice` and `qenv/internal/profile`: nothing but qenv
imported them, and two import paths for one API was one too many.) OTelSink
stayed in qobs: qobs reads a step's thread from the span in its context,
so it needs otel/trace whatever the sink's package.

**Faults (V11).** One registry (`choice.Declare`, `DeclareSite`) holds
each kind's option names and sites, so a recorded choice names its
option (`fault.rpc.counter.request=duplicate(2/3)`), a nemesis is
recorded by name (its options are the nemeses enabled at that moment,
which no declaration can name), and `FaultPoint` declares its kind from
its errors and `crash`. Declaring it made the numbering a rule: the
crash option exists whether or not the caller is in a process (outside
one it is nominal), and one kind has one option list. Scripts were a
strict order: a fault whose phase had passed blocked all later ones.
Each kind's faults are now in order and kinds independent, with
`Follows` for an order across kinds.

**Replay (V12).** A replay that diverged panicked in whichever task asked
for the choice, so an exploration with a stale recording died instead of
saying why. A Replayer now stops at the first divergence (the choice's
index, the steps before it, what was recorded and what was asked) and
makes every later choice nominal; the exploration reports the
divergence as the run's error. An encoded run (a fuzz corpus entry)
starts with the byte rule's version: a raw fuzz input has no header and
is choices as it is, and an entry of another version is an error with
its fix, not another run.

**Evidence (V13).** A passing exploration says nothing of the faults it
never took: `Reaches(kind).Faulted().InAtLeast(p)` is a claim about the
runs' choices, not their verdicts, so a test can state that the fault it
is about happened. The report names the pairs of kinds never in one run
and the ones only ever in one order. A mutation run's report keeps its
baseline, and `NoSurvivors(marks...)` makes survivors a failure unless
marked equivalent by position; a mark that matches no survivor is stale
and fails too. On the metastability side, `Assess` without a healthy
goodput is an error, not a verdict, and `qmeta.Healthy` is one value for
the region's predicate and the rules' description and PromQL, where a
func and a hand-written string could drift.

## 42. Exhaustive schedules, real time, contracts, clocks, disks, partitions (X)

**Exhaust (X3).** A seeded exploration samples; `System.Exhaust(t,
Bound{Delays, Faults})` enumerates every run within a bound: every way of
deviating from the canonical schedule at most `Delays` times and from
nominal at most `Faults` times, fewest deviations first (delay-bounded
scheduling, as in CHESS and Coyote, over qenv's choices). A run is named
by its deviations (`Run.Schedule`, "17:1,40:2"), so `GOTOGETHER_RUN`
reruns exactly it. A search `MaxRuns` cuts short fails unless
`Bound.Partial` says a sample is what the test wants (Y3): a forgotten
claim once turned a sample into what read as a proof. `Evidence.Search`
records the bound and what was left.

**Real time (X4).** A client records an operation when it returns, with
when it was invoked (`qobs.Record`'s `invoked`, a span's start), so a
step has an interval, not a point. `Reconstruct` orders steps by their
keys and reports an order the keys force that real time contradicts;
`qtrace.Linearize` searches the orders the intervals allow for one the
model accepts (guided: from the failing step, every single move
constraints allow, keeping the one that gets furthest). `qtest.Linearize`
checks a history so; finding none is strong evidence of a bug, a budget
running out is no verdict at all (Y6: n/a, which Rejects does not match).
Since Z8 a complete search follows a guided one that finds none (every
order real time allows, level by level, rejected prefixes pruned, no
beam but the budget): it finds an order the guided search missed, or
proves there is none; cut short, it is no verdict (the guided search's
stop is no proof: T5's brute force showed one wrong). Candidates are
memoized by prefix (T2b) and checked for verdicts only (T2a), and since
T2c the complete search keeps one of the prefixes that placed the same
steps and reached the same model state (Lowe's memo for Wing and Gong's
search, `qtrace.Linearize`'s check, the state read from a conforming
candidate's ITF): concurrent operations that commute cost a prefix a
set of them, not an order (five concurrent deposits: 146 orders, 456
without). States are known only for orders quint checked whole, not for
prefixes the memo accepted from a longer order, which are kept apart. Since T1a a call recorded with `qobs.Invoke` and `Call.Done` has
its interval from the recorder, and one recorder's steps are ordered by
seq, exactly: in a simulation, where calls take no virtual time unless
something sleeps, times alone made every call concurrent. The searches
are validate's (`Checker.Judge`, T3, S3), so the CLI runs them too.

**Contracts (X5).** A double is only as good as its contract. Under
`GOTOGETHER_CHECK` every `Store` keeps the history of its transactions
and checks, at each commit, the dependency cycles its isolation level
forbids (G1a, G0, G1c, G-single, G-nonadjacent, G2: Adya's phenomena,
as Elle checks them), classified by the one classifier qhistory uses
(`internal/depgraph`, checked against an enumeration of every simple
cycle): each commit classifies its transaction's component. A search
for the cycle with the fewest rw edges took G-nonadjacent, which
snapshot isolation forbids, for write skew, which it allows. A change to the store's validation that lets one commit panics there,
naming the cycle. Every `Queue` checks after each operation that no
message is lost, none is delivered after its acknowledgement, and an
Ordered queue's first deliveries are in order.

**Unobserved list appends (I45).** The clients' list-append checker must retain
known committed writes even when no external read observed their values. For
each key, committed writers absent from the maximal observed prefix are unseen
tail writers. Each whole-list read precedes each such writer in every serial
explanation, so it contributes an rw edge (excluding its own transaction).
Their mutual ww order is unknown and is not invented. This detects write skew
without requiring a final observer transaction, and strict stale reads when a
known committed writer completed before the reader began. An unobserved Unknown
transaction remains excluded; externally demonstrated commitment admits it,
while its timeout still supplies no completion edge. No read of a key cannot
prove a write was lost. Strict checking needs supplied common-clock timestamps;
the generic recorder does not synthesize them. A bounded independent oracle
checks 2–3 transaction histories by executing every serial order and comparing
actual list reads, rather than rebuilding the dependency graph.

**Clocks (X6, Y4).** A process has two clocks, as a Go program does: the
wall clock (`qenv.Now`: skewed per process, jumping by nemesis, drifting)
and the monotonic one (`qenv.Mono`, `Since`, `WaitFor`: drift only),
which `time.Since` of two `time.Now` readings uses. The first version had
one clock that jumps, so "a lease timed on the holder's own clock breaks
on one jump" was the model's claim, not Go's; examples/lease now shows
each fault breaking the variant that trusts it (a skew, absolute
expiries; a jump, intervals on the wall clock; a drift past the margin,
intervals on the monotonic one) and fencing surviving all. Jumps are
nemeses, not choices at every read, so reading the clock makes no choice.

**Disks and partitions (X7, Y7).** `qenv.Disk` is a machine's disk:
writes reach the OS at once and the disk on `File.Sync`; a crash of its
process is a fault point (`disk.<name>.crash`: nominal keeps every write,
`powerLoss` the synced ones, `tornWrite` part of the last). It is what
tells "written" from "durable", which a sink's `qobs.PerStep` (one write
per step) and `qobs.Synced` (an fsync per step) differ in. The network
(`Sim.Net()`) cuts and heals links between processes, by hand or by a
nemesis (`PartitionNemesis`), which a `Service` call and a `Queue` whose
broker runs in a process (`NewQueue(s, name, p)`) see; a nemesis's cut
heals by itself after `CutLength`, since a network every task waits on
would otherwise never heal (a nemesis acts only at a scheduling step).

**Vacuity (X2).** An invariant `A implies B` holds in runs where `A`
never does, for nothing. `binding.Model.Antecedents` finds `A`, a binding
may name it, and `NotVacuous` fails when no run that passed witnessed
it, when one cannot be evaluated, or when no invariant has one (Y6).

**Corpus (X8, Y5).** A failing run's choices are saved and replayed
before the seeds, so a regression stays found whatever the seeds are. An
entry records its format and the exploration that saved it, which alone
replays it; one whose replay diverges is stale, with the seed that finds
its run again, and one no exploration replayed fails the test.

**Verify (X2).** `gotogether simulate -verify N` runs Apalache to depth
N on each invariant: "no trace of up to N steps violates it" where
sampling said only "none of the sampled traces". A binding caps N for a
model whose depth Apalache cannot reach in time (`simulate.verifySteps`)
or skips it (`verifySkip`: Choreo's recursion).

## 43. After the fifth review (Y1–Y12)

The fifth review read the X track as a user would, and found claims that
said more than they proved. The fixes share one rule: what a test does
not check, it says, and a name that matches nothing fails.
- A run's panic is a failing run, named and kept (Y1); a run reproduces
  alone, as its report prints it (`GOTOGETHER_RUN`, Y2); a truncated
  search fails (Y3); corpus entries say what they are (Y5).
- Claims are judged once (`Exploration.verdict`, for Expect and Mutate),
  `Exercised` counts each option and nemesis by name, nemesis families
  each have a rate, and `NotVacuous` and `Linearize` say when they cannot
  judge (Y6).
- Doubles take their process at construction and name their fault kinds
  after themselves; a Rates key, a sweep's FaultKind or an exception
  naming nothing declared fails (`KnownFault`, Y7). An option a workflow
  ignores fails (Y9).
- The API record lists fields, methods and values for every published
  module (Y8); evidence is a format with a role, so a suite's gaps count
  the system as it is, not its mutants (Y10); the core's Hegel
  properties test the current core (Y11).

## 44. The exploration engine (T6)

Explore and Exhaust each had their own setup, their own GOTOGETHER_RUN
path and their own fan-out, and Exhaust had no determinism check: the
modes had grown into flags (on ExploreConfig, on Exploration, on Run's
seven identity fields) that every message decoded. Since T6 one engine
(`qtest/engine.go`) runs every exploration: sources make its runs in
batches, then stages put them through what every exploration shares.

- **Sources.** Seeds, corpus entries, a saved run's choices
  (`choices=<file>`), steered runs and an exhaustive search's schedules
  (a breadth-first level per batch). A source's `next` sees every run so
  far, so steering and schedules grow from the runs before them; the
  engine asks each in turn, runs a batch side by side and writes runs by
  index, so their order is the same every time. Each planned run carries
  its origin (what GOTOGETHER_RUN names and messages print) and, if it
  can be made again, how: the first run is the determinism check's, if
  it can be. Seeds come first, so the check is still the first seed's;
  Exhaust's nominal run (or the one schedule run alone) now has it too, and a nondeterministic system
  fails Exhaust as it fails Explore, rather than finding schedules that
  would not replay.
- **Stages.** The determinism check, the names users gave checked
  against what the runs declared (only when every run ran: one that did
  not declared nothing, and finish says why), then finish: escapes, run errors,
  emptiness, the searches, KEEP logs, judgment and evidence.
- **Specs.** What only a System method sets (its options and Rates, a
  fuzz input, a mutant's baseline) is an internal `exploreSpec` around
  the user's ExploreConfig, not hidden fields of an exported struct.

The engine is unexported: a public source would be frozen by the tags it
precedes, and nothing outside qtest needs one yet. qhegel's runs do not
go through it, since they must not go through runOne's panic recovery
(Hegel aborts a test case by panicking).

**Fault names per run.** A Rates key, a Bound.Without kind, a sweep's
FaultKind or an exception that covers nothing declared fails, since it
would match nothing. The check asked the process's registry, so a name
another test's double had declared passed, and whether a misspelling
failed depended on what else the binary ran. Now each run carries its
own names (`hooks.FaultNames` in runOne's context): a simulation adds
the nemeses it registers, `DeclareFaults` adds to the calling
goroutine's run (qrun's resolver finds it for the simulation's creator
and its tasks; a fault point adds to its task's), and runOne adds every
fault kind and nemesis its choice log saw, which is all the host sees
of a simulation under gosim. A kind declared only on a path some runs
never reach is known only to the runs that reach it: declare it at
package level. What is declared outside any run
(a woven fault point's site at init, a double built before its
simulation) is static, known to every run, as before. A run of another
workflow without names (Hegel's, a load experiment's) adds to neither.

## 45. A Quint evaluator in Go (T2d)

A linearization's search checks candidate orders by the hundred; each
check was a quint process (half a second to start, the model loaded
again), so a contended history's proof cost minutes. Since T2d the
candidates are judged in Go (`internal/qeval`), and quint confirms.

- **Over quint's own IR.** quint compiles the binding's evaluation
  module (`binding.EvalModule`: the model imported with the constants the
  binding computes from the steps passed through, a definition for each
  action's expect whose parameters are its template's fields, a wrapper
  of each action, the invariants and eventual vals as vals) to JSON once
  per binding and model; the evaluator reads it. Names are resolved by
  the IR's table, so no parsing or name resolution of Quint is ours. Its
  stdout is written to a file: `--out` writes the modules unflattened,
  and a pipe loses what quint has not flushed when it exits.
- **The fragment.** About seventy builtins, actions (assign, all, any,
  ite, lets and nondet, actions called from actions) and values with a
  total order (sets and maps sorted, records by field). What it cannot
  evaluate (an unknown operator, a choice outside an action, an integer
  past 64 bits, a fold over a set that some order of the set changes, a
  value of a kind it did not expect) is `Unsupported`; the search sends that candidate and the rest to
  quint. The model's own runtime errors (a missing key, a division by
  zero) are `EvalError`s, and also go to quint.
- **Set folds.** Quint folds a set in an order of its own, and neither
  backend's is documented or the other's: TypeScript's is a hash order
  that also depends on the order elements were added (`Set("b", "a")`
  and `Set("a", "b")` fold differently), Rust's another (`Set(1, 33, 2)`
  folds as `[1, 33, 2]` in TypeScript, otherwise in Rust). So the
  evaluator gives a fold's result only where every order gives it: an
  operator whose shape commutes whatever the elements (a sum `acc + E`,
  conditionals of sums, a min or max `if (E < acc) E else acc`, a union,
  E never reading acc), or a set of at most 10 elements tried in every
  order (by subsets: `f(acc(T \ {x}), x)` the same for each x of each
  subset T, n·2^(n-1) applications); anything else is `Unsupported`, and
  so is a fold that fails in some order (the failure may be in an order
  quint does not take). It once folded sorted and reversed and took
  agreement as order-independence: a round robin over three nodes
  agrees both ways and not in quint's order, and `simulate -graph`
  "proved" an invariant quint violates (review 11, F1;
  `TestSetFoldsAgreeWithQuintOrAreRefused`, rr in `TestAgreesWithQuint`).
- **Judged as quint's replay reports.** A candidate is planned within
  its history (`binding.Planner`), replayed from init, each call's expect the
  definition applied to the call's `ExpectArgs`; its failure index is
  failedAt's and violatedAt's reading of a report (an eventual val false
  at the settle: the last step), and a conforming one's state is the
  model's state as a key (with the last process when the binding infers
  restarts), for the merge of prefixes by state.
- **Confirmed by quint.** An order the search finds is checked by quint
  (a disagreement searches again with quint alone, and the issue says
  so). A search that finds none is confirmed from its proof
  (`qtrace.Linearization.Proof`), in one quint batch: the complete search
  drops a candidate only when it is rejected (the prefix through the
  failing step, which every order it begins shares) or when another
  prefix of the same steps reached its state, so quint rejecting every
  dropped prefix (at any step: each order it begins fails) and giving
  each merged pair one state (its ITF's, written for the invariants'
  search too) is what "no order" rests on. A candidate the evaluator
  wrongly accepted needs nothing: its extensions were judged in turn. A
  proof quint contradicts searches again with quint alone, and says so;
  a search cut short is no verdict, and stays the evaluator's: quint's
  search of the same budget, from the same verdicts, would be cut where it
  was. From other verdicts it may not be: a prefix the evaluator wrongly
  accepted spends budget quint would have pruned, so quint might finish
  with no order (a rejection); one wrongly rejected, quint might extend to
  an order. Either is an evaluator bug, which the nightly shadow job
  (every batch judged by both, every example) exists to catch; the cost
  of one here is a verdict missed (no verdict, which a `None` claim fails
  on and a `Some` does not count), never a verdict wrong. It was quint's, searched
  again, which cost minutes for nothing (raftkv's faithful explorations,
  82 s for two cut runs of ten; 5 s with the cut kept), and the shadow
  mode checks the verdicts it rests on. The same holds for the
  invariants' second search. A merge assumes that equal states have
  equal futures: the key holds, beside the model's state, the order the
  candidate's processes first appear in (which numbers them for
  `{{.process}}`) and, with inferred restarts, its last process (a review
  found two prefixes of one state numbering their processes otherwise,
  merged). The caller's check of the order is quint's, as before (since
  §56, the evaluator's when it passes the order, quint's otherwise).
- **Checked against quint.** Every binding's model, in its binding's
  instance, against `quint run`'s traces (each state one of the
  evaluator's successors of the one before), and a table of builtins at
  their edges against both backends' values; a search with and without it
  gives the same verdict, and one made wrong is caught by the
  confirmation. `GOTOGETHER_EVAL=shadow` checks every batch the
  evaluator judges with quint too, failing the search on any
  disagreement of a failure index, and each actor of every run a
  Checker checks judged by both too (its plan as the check plans it,
  §56): every binding a run is checked against, not only the
  searches'. The nightly runs every example so, where the histories are
  the ones users' runs make.

- **Confirmed by the run's check.** An order found was confirmed by a
  quint process of its own, and then replayed by quint again in the
  run's check, one process after the other (2–4 s a run). With
  `Checker.Judge` (qtest's searches and the CLI's: S3 made it the one
  way, after three callers each wrote out search, check, confirm, check
  again and mark) the check is the confirmation: the search's confirm
  takes its report, and for each actor it does not pass (its
  conformance, an eventual val or an invariant failed, or the search left
  it undecided, confirmed whatever its verdicts) whose order only
  the evaluator judged, checks that order with quint; one quint rejects
  makes the run searched again with quint alone, and checked again, with
  a warning. An actor that passes confirmed its order in the check. A
  failing run pays the process it saved, no more. ResolvePending's
  placements are judged the same way: the first the evaluator accepts is
  the resolution, confirmed with the placements it rejected before it
  (the run's invariants are judged on the one taken, so it must be
  quint's: a review found a pending deposit's no-effect placement
  wrongly rejected, and the limit violated on the next), by the check or,
  outside Judge (a search's own confirmation), in one quint batch; when it accepts none,
  quint's batch decides as before, a verdict of none needing every
  placement rejected. A review also found a failed eventual val left
  unconfirmed (it is no conformance failure in the report, but rejects
  the order in a search).
  The placement taken must not make the run look violating either, as
  an order of Linearize's must not: when the first that conforms
  violates an invariant (the evaluator's word that it holds suffices,
  else quint's), the later placements are searched, with quint and the
  invariants, for one that conforms with every invariant holding, and
  that one is taken; none, and the run violates; none within a budget
  that cut the placements, and the run has no verdict. Judged on the
  first alone, a pending inc's "no effect" violated an invariant its
  effect keeps, and ResolvePending said Violates where Linearize found
  the placement (review 11, F3; `TestResolvePendingInvariantsAgainstBruteForce`).

quorumkv's contended-key mutants took 51 s with the evaluator's
unconfirmed negative verdicts, 214 s with quint searching them again,
62 s with their proofs confirmed, and 43 s with found orders confirmed
by the runs' checks (337 s with quint alone); its faithful exploration
20 s with quint alone, 13 s, then 4 s. wallet's pending transfers 44 s
to 12 s. `GOTOGETHER_EVAL=quint` turns it off. Every candidate is still
replayed from init: extending a prefix's held state is the next step
(ROADMAP T2d).

## 46. Pending calls inside the linearization (T7a)

A client that times out or crashes leaves its call pending: it may have
taken effect, once, or not at all. ResolvePending searched where in the
recorded order; Linearize searched orders of the completed steps; a
client history with timeouts needs both, and they were refused together.

`qtrace.Linearize`'s Pending gives the complete search optional nodes, one
per pending call, bounded as ResolvePending bounds them: after every step
recorded before its invocation, before every step recorded after a
settle that followed it (the system was quiet there). A pending call is
placed while those bounds allow, and a prefix that passed its upper
bound leaves it out ("no effect"). An order is complete when every
recorded step is in it; check tells a complete order by its steps that
are not assumed (`Ambiguous`), and judges it whole (a settle's eventual
vals). Two rules keep the memo and the proof sound:
- a complete order is no prefix's verdict: accepted, it is not marked
  for its extensions (a prefix is not judged at a settle), and the search
  judges a complete order before its extensions, so the guard costs
  nothing;
- a complete order failing at its last step (which an eventual val
  does) prunes nothing: a pending call placed after it may make it hold,
  and its extensions are judged; its rejection is recorded whole in the
  proof.
The guided search orders the recorded steps alone (no pending call took
effect), the cheapest explanation; the complete search places them.
Real time across recorders is their clocks' word: a step of one
precedes another's invocation when it completed more than the skew
earlier (`LinearizeOptions.Skew`, `SearchOptions.Skew`,
`qtest.ClockSkew`, `validate -skew`; default `RealTimeSlack`, 1 ms),
unless a call record pairs them on one recorder or domain clocks order
them. A verdict of no order resting on such orders is true only under
that assumption, which quint's re-check of the Proof cannot touch (it is
given the same orders), so the verdict names it: `Linearization.Clocks`
(`qtrace.ClockOrders`) counts those orders, their recorders and the
closest gap, and the issue says "it assumes the clocks within 1ms ...
with clocks 2ms apart or more, the nearest of those orders may not
hold". The one order real time allows (`ForcedOrderWithin`) says so too,
and a conforming order says the assumption and that it stands under any
skew (a larger one only allows more orders). The check of the order
found keeps its skew (a step's `slack`), or its real-time issue would
judge it by 1 ms again. Production hosts under NTP differ by more than
1 ms; a skew per pair of recorders is ROADMAP G8's (review 11, F5;
`TestSoundClockSkewNamed`).

A review found two pending calls on either side of a quiet settle
unordered (the first, if it took effect, did so before the settle, and
so before the second): a forbidden order passed a ghost history; and an
order's completeness counted assumed steps out, so a saved resolution
checked again was never settled (its eventual vals unjudged). Both fixed,
the brute force given the rule independently.
Validated against brute force on random register histories (every
subset of the pending writes, every order the bounds allow, with and
without a settle and a final value some runs require), through the
complete search alone and under small budgets, each rule ablated.

A failing history is drawn (`qtrace.HistorySVG`): a lane per recorder,
each call a bar from invocation to completion numbered by the order
judged, pending calls dashed to the history's end, the step the check
failed at red. Kept failing runs of a search get one beside their
judged order; the CLI writes one with `-history`.

## 47. Rolling upgrades (T7b)

A rolling upgrade is a run in which processes restart, one at a time,
running other code: for a while two versions talk to each other, which
neither version's own tests see. `Sim.Upgrades(version, start, procs...)`
makes it a nemesis per process, `nemesis.upgrade.<name>` (its own
family, at the nemesis rate or `nemesis.upgrade`'s), enabled while the
process is up and runs another version: doing it crashes the process and
restarts it as the version, `Process.RestartAs`, which sets `start` and
`Version`. Being a nemesis, it is a fault to everything that handles
faults: Scripts name it, `Exhaust` bounds it (one fault: one upgrade, at
every point), Swarm and Without turn it off, and a run's choices replay
it. A process need not be crashable to be upgraded: crashability is the
crash nemesis's. Faults stay off after a settle, so a rollout a test
needs finished calls `Process.Upgrade` itself. The same model judges
every version; code that differs reads `ProcessOf(ctx).Version`. A
review found an upgrade doing nothing under a supervisor that restarts
from an OnCrash hook (it rebooted the old code first): the new code is
in place before the crash now. A process upgrading itself, and a second
rollout of one process (two nemeses of one name undoing each other),
panic with the fix.

## 48. Bounded response (T7d)

An eventual val passes code that recovers, however slowly; a deadline is
a property of its own. A binding's `within` (after a val, another within
a bound) is judged after a run's check, on the states quint's replay
reached (its ITF, written for such a binding whatever the options), each
timed by the step that reached it. Each time After becomes true (a
rising edge: a second trigger opens a second obligation), Holds must hold
in a state no later than the bound: later, or never in a run that
settled or went on past the bound, fails, with the trigger's step and
what came of it; never in a run that ended before its bound, unsettled,
is not judged, as an eventual val of an unsettled run is not (an info
issue says so). The vals are the Go evaluator's to compute on quint's
states: the states are quint's, and a val the evaluator refuses is no
verdict, said as a warning. A verdict depends on the steps' times then,
which a cache keyed by the steps alone ignored: qtest's gave a slow run
the fast one's verdict, so caches key by `Checker.Key`, with the times
for such a binding, and qhegel does no prefix reuse for one.

A review found: a run that stalls in silence, unsettled, never judged
(the obligation open at its last step), passing every claim; a refused
val failing a conforming run; a run ending exactly at its bound failing,
though a step of the same instant could still respond. Now an
exploration claiming conformance fails when no run judged an obligation
(settle the run: a settled run that never responded fails); what is not
judged (a refused val, no ITF, steps whose times go back along the
order checked, as a linearized or Lamport-ordered trace's may) is no
verdict, not a failure, with a warning; the bound is inclusive both ways.
An obligation is judged on the order checked, so a search's choice of
order (Linearize's) is not made for it; the initial state's trigger is
timed at the first step (the run's start is unknown).

## 49. Hidden actions (T7e)

A production trace records what its code records; some of what the
system does never shows (a gossip round, a handoff). A binding names
such model actions `hidden` (nullary: their choices are the model's, by
nondet), and a trace is then judged as a search over the states the
model may be in: before each recorded step, the states reached are
closed under at most `hiddenDepth` hidden actions, the step is applied
to each, and those where its expect holds go on. It conforms if some path
takes every step; the invariants hold if some such path keeps them all;
an eventual val holds if some final state has it. Quint cannot judge this: a
replay names every step's arguments, and a hidden action's choices are
unknown, so the verdict is the Go evaluator's alone (checked against
quint's own traces of every model in CI, `TestAgreesWithQuint`), and the
report says so. More states than the cap at a step is no verdict.
Checked against an independent brute force (the model in Go, every
gossip sequence up to the depth), the depth's bound shown by a line of
nodes where a value two hops away takes two rounds. A review found shadow
mode comparing the evaluator with itself (a binding with hidden actions
has nothing for quint to compare, and shadow skips it now), an invariant
the initial state violates reported after step 1, and the invariants
judged each on a path of its own: they are now kept on one path, as a
replay keeps them. Eventual vals are judged over the states hidden
actions may reach after the last step, since they may run before the
system is quiet: a probe recorded after the settle observes what they
did. A failure within the depth while more hidden actions reach new
states (the depth, not the model, ended a closure) is no verdict, "raise
hiddenDepth": a probe found a step five hidden actions away rejected at
the default depth of four, and the brute force, bounded alike, could not
see it.


## 50. Partial logs (S11)

A recorder's log can lose its head (rotation, a collector that started
late) or its tail (a killed process, a torn last line). Both were judged
as if nothing was lost: a log starting at seq 40 was replayed from
`init`, and a lost tail was read as a run that stopped.

A lost tail is a prefix of the run, which every check already allows,
except where other recorders went on. Their later steps may depend on
what was lost (a read of a lost write), so a failure at or after the cut
(by time, with `RealTimeSlack`; a bounded response by the end of its
window) is no verdict, not a FAIL; a failure before it stands. A lost
tail leaves the whole run unsettled, since the lost steps may follow any
settle, so no eventual val is judged; a pending call invoked before a
cut may have completed in what was lost, so `Completes()` and `None`
claims count the run as unjudged. A torn line does not name its
recorder, so a torn log marks every recorder of it: that loses FAILs
after the tear, never makes a PASS.

A lost head needs a first state. Two ways, both named in the binding:
- `snapshot:` an action with parameters assigning every variable; a step
  of it first in a log is that log's start. One state, so every check
  works as from `init` (quint's replays, the evaluator, Linearize with
  the snapshot first, partitions, pending calls). A step overlapping the
  snapshot in real time may already be inside it, which no order can
  say: an order that conforms stands, a failure is no verdict, and the
  fix is to record the snapshot when nothing is in flight (kvstore
  rotates its log under the store's lock).
- `resume:` a nullary action whose nondet picks admit the states a log
  may start in. The evaluator searches them (§49's search from a set of
  states, filtered by the binding's invariants), quint confirms a pass
  from its witness start and a failure from every start up to 200 (the
  starts are the evaluator's list of the resume's states: quint has no
  way to enumerate an action's successors, and the evaluator's agreement
  with it is tested generally, `TestAgreesWithQuint`). Each state the
  search reaches carries the start it came from at that step, and
  conformance, invariants and eventual vals are judged on one path, as a
  replay judges them (hidden actions alone once judged eventual vals on
  another path than the invariants', a false PASS). A
  pass means some admitted state explains the rest: sound only if the
  admitted states are reachable, which is the user's claim, as a
  template is. Bounded reachability from `init` was rejected: its PASS
  is sound but its FAIL only once the closure stops growing, which real
  models do not reach, so every such log would be no verdict.

Later snapshots are observations of the current whole state, not transitions
that restore it. Planning retains them as checkpoints. Native replay requires
the snapshot action's result to equal the prior state; Quint replay eagerly
captures that prior state through a singleton choice and checks it after the
original snapshot action. A lazy local val is insufficient: it may read the
post-state and turn the comparison into a tautology. A matching checkpoint
stutters; a differing one fails at its recorded location. It cannot repair
missing history, clear an earlier failure, or declare settlement. The model's
snapshot action still describes the full state through its parameters, without
reading the preceding state. History-order and pending-effect searches refuse
later checkpoints for now: reordering must not promote an observation to a free
initial state, including the final replay of a searched history. Partitioned
checking likewise refuses a whole-state checkpoint rather than projecting it
into separate entities. These refusals leave the existing head-snapshot search
contract intact. Finite model-choice witnesses retain their existing snapshot
exclusion; hidden and resume searches constrain snapshot successors before
merging paths. Checkpoint capture must also include imported state. If state
reachable through imports is not visible in the selected model's exports, the
planner and ITF replay refuse the checkpoint; comparing only local variables
would silently miss a change in an imported module. Visibility is checked using
variable declaration identities, so equal names do not establish equal scope.
Stateful qualified imports and instances are refused conservatively because
pre-flattening declaration IDs do not identify each instantiated state cell.

Verdict caches key a lost log by its steps' times and its start
(`Checker.Key`), since the cut and the start decide the verdict.

**Online validation (`-follow`): not built.** Judging a growing log
looked like resuming quint from the last state, which is unsound for a
nondeterministic model (one resolution kept). The rest of the toolkit
decides on whole runs too: Reconstruct orders a trace at once (a late
step of another recorder reorders steps already judged), the plan sizes
constants from the whole trace, and Linearize, ResolvePending, eventual
vals and bounded responses judge a run at its end. Online validation is
instead a window of the log judged as a log that lost its head, from a
snapshot step or a resume, by the ordinary `gotogether validate`, run as
often as wanted.

Two reviews: the first found `gotogether itf` dropping the snapshot (a
false PASS through `ValidateITF`), a bounded response timed by its
trigger, `None` claims holding over invariants left unjudged, a cache
key without times, a step concurrent with the snapshot rejected, and a
disabled snapshot reported as quint's error; all fixed, each with a test
that fails without its fix. The second found a start tracked by state
across steps (a witness the steps do not pass from), eventual vals and
invariants judged on different paths (with hidden actions alone too: a
false PASS), and claims of no verdict read as met outside `All` and
`None` (`Holds`, `Some`, `AnyOf`); claims are now three-valued wherever
they are read.

## 51. Unmodified net/http and database/sql services (S12)

A team's service is written against `net/http` and `database/sql`, not
qenv's seams. Two adapters run such a service in a simulation unchanged:
`qenv/qsql`, a `database/sql` driver over a Store, and `qenv/qhttp`, an
`http.RoundTripper` and an `http.Handler` server over a Service.
`examples/payd` is the demonstration: a payments API that imports only
the standard library, explored at three isolation levels with crashes,
commit faults, duplicated and late requests, and run for real on SQLite.

**What of the standard library runs.** qenv runs one task at a time and
switches only at its seams, so a task blocked on a real channel or lock
stalls the run. Read against Go 1.26's source: `database/sql`'s
connection opener only connects when the pool is limited and full, its
`awaitDone` goroutines touch the driver only after a cancel, and a query
holds a read lock released on its return path, not by a defer.
`http.Client.Do` calls `RoundTrip` on the caller's goroutine; its
`Timeout` becomes a request deadline. So: no `SetMaxOpenConns` below the
tasks' concurrency, rows read whole when a query runs, a cancelled
transaction rolled back by a reaper task (never the Store touched from a
goroutine that is not a task), the request body's probe decided on the
task, a handler served on its Service's task without `http.Server`.
Pool limits and handlers that start goroutines are qgosim's (§14).

**Ended transactions do not keep waiting for locks.** A lock or commit wait
ends when the waiting transaction is rolled back or its connection is reset,
regardless of whether a different transaction still owns the key. Resetting a
connection and then giving the same key to a new transaction must not keep the
old waiter asleep. Store callers retain their existing ended/reset errors.
For explicit SQL transactions, the cancellation reaper can roll a waiter back;
the statement rechecks transaction usability after yielding, preserving
`sql.ErrTxDone` instead of translating the ended transaction to 40001.
An interrupted commit rechecks only a non-applied error: a successful or
unknown-outcome commit cannot be reclassified as rolled back because its context
was cancelled after application. This does not add new cancellation tracking for
autocommit operations or distinct per-statement contexts.

**When a task is unwound inside a statement** (a crash, or Close), qenv's
unwind panic passed through `database/sql`'s query and leaked its lock,
and the service's `defer tx.Rollback()` deadlocked: the driver recovers
the unwind (`qenv.Unwinding`) and returns 08006, so `database/sql`
releases on its error path, and the task unwinds again at its next seam.

**A task blocked outside the simulation** made Run wait forever, naming
nothing: `Sim.Hang` (30 s of wall time, zero off) panics with the task
and its stack. It costs nothing per step: the scheduler receives plainly
and a watchdog abandons a task it sees running in one wake for `Hang`.

**The SQL subset** is one table per statement, equality conditions,
`ON CONFLICT DO NOTHING`, `FOR UPDATE`, `c = c ± ?`; anything else is
42601 naming the subset. Rows and unique indexes are Store keys; a
locking statement re-checks its condition after a lock wait, as
PostgreSQL's read committed does. Errors are PostgreSQL's SQLSTATEs,
read by retry loops through `SQLState()`: 40001, 40P01, 23505 (a
duplicate key at every level, 40001 only where PostgreSQL gives it),
08006 for an unknown commit outcome (never `ErrBadConn`, which
`database/sql` would retry), 25P02 after a failed statement.

**HTTP outcomes** are what a client sees on a wire: refused only when
nothing was sent, a reset when a sent request found no server, a
timeout as a `net.Error`, overload as 503. Requests and responses go
through their wire forms. A context deadline is wall time: the adapter
converts it to virtual time once, rounded to two significant digits so
jitter does not change a run, and remembers it, so retries under one
deadline use it up. A service's own `ctx.Err()` stays on the wall timer.

**payd.** Steps are recorded at the adapters and in test code only: the
client's transport records requests and responses (what was asked, and
the charge a 2xx names), qsql's commit hook the charges, edits of charges
and debits, the processor double the captures, and a probe after the
settle the balances. The model asks for no double charge or capture,
responses matching charges by id, account and amount, no refusal of a
key that was charged, no money leaving an account but by its charges,
and, after the settle, every answered or captured key charged and every
valid key answered. Mutants are build overlays of the service's files
(`go test -overlay`), since qmut's operators cannot write a
check-then-insert: the key checked outside the claim, the key not
forwarded to the processor, a pending key answered "retry later", the
debit ahead of the compare-and-set, the race's loser refused, the
response's amount in cents. Each is caught; the real tier, where each
key's senders meet and send together, is claimed to catch all but the
last under concurrency alone. The review of payd found the model blind to money
leaving accounts and to refusals: two of those mutants passed every
test before the debit, balance and refusal properties.

Three reviews: of the plan (database/sql's pool), of the adapters (a
refusal for a request already handled, deadlines never used up,
rounding that flipped, a race at Close, the watchdog's cost, 40001 for
a duplicate key, a unique lookup between two states), and of payd.

The review of the keys of several columns, ranges and upserts found
qsql more lenient than the database it stands for: `ON CONFLICT ... DO
UPDATE SET n = n + 1` and `... WHERE n = 1`, which PostgreSQL 16 refuses
(42702: `n` is the existing row's or the proposed row's), ran (as in
SQLite, which takes the existing row's). A simulation must not pass SQL
the real database refuses, so a bare column on the value side or in the
WHERE of a DO UPDATE is 42702 naming `t.n` and `excluded.n`, and payd's
differential test runs each upsert's bare form against qsql alone.
`LIMIT NULL` is no limit, as in PostgreSQL; ORDER BY may name a column
fixed by equality anywhere (it orders nothing); a table name with `/`
or `!` (its keys' separators) or the `_qsql` prefix is refused. The
range read was a free function, `qenv.ScanRange(tx, r)`, whose fallback
for a Txn without range reads silently scanned the whole prefix: it is
`Txn.ScanRange`, every Txn's, and an adapter without range reads says
so by calling `qenv.ScanPrefix`. A KeyRange's From and To are whole
keys, not suffixes after its prefix (a choice documented rather than
changed: qsql builds them from whole encoded keys).

## 52. A real deployment's nemesis (S13)

A simulation proves what qenv's doubles allow; a team's question is also
whether the binary they ship, on real TCP and a real disk, survives a
crash, a pause and a partition. `qnemesis` (standard library only) is a
nemesis for processes on one host: kill and restart (a process group,
reaped), SIGSTOP and SIGCONT (waiting until every thread of the group
has stopped: SIGSTOP takes effect after kill returns), and through
toxiproxy a cut, latency or loss per directed link. Schedules keep
qenv's names and partial order (`Random`, `FromScript`); clock skew is
out of reach (Go reads the clock through the vDSO, which libfaketime
cannot intercept) and stays the simulation's.

**Faults are steps.** The nemesis records on its own recorder, on the
side that keeps the check fair: a crash after the process is reaped, so
every step of the killed incarnation precedes it; a cut after it takes
effect; a heal before the call, so a bound covers the API's latency.
Every process is on one host, so wall times compare within
`RealTimeSlack`. A kill between a program's effect and its record would
leave a clean log end, and another recorder's step depending on that
effect would be rejected: a program that records on return names its
recorders (`Process.Recorders`), and the crash declares their tails
lost, bounded by the reap (`Step.LostBefore`), so such a failure is no
verdict and a later settle still stands. raftkv records each step
before its effect is visible and does not name them: declaring a loss
it cannot have would make every cluster failure after a crash no
verdict.

**A journal is a script.** Every fault's start and end goes to a
journal; `Script` re-makes them in a simulation in their real order,
each at its first chance (a real run's step counts are not a
simulation's; `StepScript` keeps them for a simulated program's own),
and `Unmade` names what a simulation could not make and the setting it
needs. A real run is not replayable (real elections, real timing); its
faults are.

**raftkv's real tier** runs the same node code as three processes over
HTTP with a bbolt write-ahead log. Its shared state was safe only
because qenv runs one task at a time: a lock, never held across a seam,
left 41 simulated runs' logs byte-identical, and without it the race
detector reported thousands of races. Under random crashes, pauses and
isolates, client-response loss (one connection per call) and three
whole-cluster cuts, both histories conform and settle; NoRequestID is
caught at a per-seed rate stated in the test, and SlowElection by a
bounded election (500 ms in `cluster.real.binding.yaml`, against the
simulation's 300 ms: a faithful election once took 320 ms under the race
detector, said openly). NoPersistVote and NoSnapshotReport need
interleavings the real schedules did not reach; the simulation catches
both.
A node's port is picked below the kernel's ephemeral range: one picked
by listening on ":0" and released was once a toxiproxy proxy's (made
with ":0") by the time n3 bound it, and the proxy, forwarding to another
node, answered for n3, so a run passed with a node never up until the
harness's last pause found it gone. An agreement now needs each node
answering at its own address as itself, a node down by itself fails the
run (the journal's Stop restart of no fault), and a pause or resume of a
process not running says whether it was killed or exited by itself, and
how: the fault is not made, and nothing of it journaled.

Three reviews: of the plan, of qnemesis and the real tier (a kill
between effect and record giving a false FAIL, the nemesis's steps
counted as the system's, a pause recorded before the group stopped,
orphans, a journal stalling after a long cut, claims over real runs
skipping checks), and of the harness (node bugs left without a verdict
by declared losses, replays stranded by step counts, loss drawn per
connection, weak fixed seeds).

## 53. Absence diagnostics (E7)

A step a run was expected to show and does not was read one way: a
bounded response never seen in a trace that went on past its window
was a FAIL, one the trace ended before was nothing, a call never
completed was pending. A production trace says neither which: its
exporter may have dropped the response, its collector may not have
ingested it yet, or the system never answered. Only the last is the
application's bug.

**Three causes.** Each obligation not met or not judged (a within, an
eventual val, a pending call) gets one, in `Result.Absent`: dropped (a
gap, a lost tail or a loss record of a recorder that would hold the
step, while it was due), uncollected (its window ends past those
recorders' watermark, or the run recorded no settle and ended first),
or a defect (they are complete past the window and lost nothing). Only
a defect is a FAIL; the others are no verdict, their fix named. Which
recorders would hold a step: those of its actor's steps, the rule a
gap and a loss already follow (an actor is one instance of the model,
any of its recorders may record the step that meets an obligation).

**A watermark** is a record, not a step (no seq, so a recorder's
sequence stays dense): recorder r's steps through T are in the input,
`*` for every recorder. Two choices made it compose with what was
there:
- A log without one is taken whole, as before: the trace's own end
  still covers a window, so a within the trace went past is judged as
  it was (a simulation's recorder is in memory, a closed file whole).
  Requiring a watermark everywhere would have turned every such within
  into no verdict. What does change without a watermark is the gaps'
  reading (below): a failure a gap may explain is no verdict now.
- A recorder's dense seqs raise its watermark: its step at time t with
  no seq missing below it says every step it recorded by t arrived
  (`qtrace.CollectedThrough`). A periodic watermark always lags, and
  without this a stated one cost certain FAILs: one recorder's log
  going on past a window, its seqs dense, was no verdict for a watermark
  short of it. Steps without seqs (converted spans) have the watermark
  alone.
- A search re-sequences an actor onto a recorder of its own, whose
  seqs prove nothing of the recorded ones: it carries the actor's
  collection as it is (`qtrace.CarryWatermark`: the watermark, exempt
  from its order's seqs, and whether a recorder of the actor was whole,
  whose log the trace's own end bounds, from invocation times as the
  check of the recorded steps reads them). The first carry took the
  actor's last record's completion time as that bound and every
  recorder as stated, and a slow call completed after the window made a
  FAIL through a search of a run the plain check left uncollected; a
  test now judges every row of the absence matrices through
  ResolvePending and Linearize too, which must agree.
- The recorders that would hold a step are those of the actor's steps
  in the input. A recorder that has recorded nothing of it yet (a
  restarted process's new recorder) has stated no watermark and is in
  no trace: a per-recorder watermark cannot cover it, `*` (every
  recorder) does. State `*` where recorders can appear later. Requiring
  it for a watermark's FAIL after a restart was considered and left to
  that advice: a restart does not always bring a new recorder, and the
  rule could not tell one that will record from one that never will.

So a within never met is judged where the watermark reaches its
window's end (give or take the clocks' slack): a production trace, which never settles, is judged
absent, the capability the item was for. One the trace went past but
the watermark does not reach is no verdict (`no verdict (uncollected
steps)`), not the FAIL the trace's end made of it; so is a response
held late in what was collected, since an earlier one may not have
been (with dense seqs on its recorder the watermark reaches it, and it
stays a FAIL). A settle a stated watermark does not reach, by more
than the slack (as a step after a settle is given it), settles nothing: a step after it may not be
collected yet, and a settle that a later step voids was the rule
already. A pending call has no window: with a watermark and no settle
it is uncollected; in a log taken whole it stays pending (Completes
misses it), and the report says to state a watermark if the log is
still growing.

Gaps had been an error issue only: a within's failure, or an eventual
val false at the settle, that a gap may explain (its missing steps
recorded while the response was due, by the times of the steps either
side, `Gap.After` and `Before`) is now no verdict, as one after a lost
tail's cut was. So, since a review found the log of deposits seq 1, 2
and 4 judged "mismatch after deposit", is a conformance failure or an
invariant's at or after a gap of the actor's recorders (`no verdict
(dropped steps)`, `judgeGaps`): after it in the order checked, a later
seq on the gap's recorder, a later time (by the slack) on another. A
failure before every gap stands. Recorded steps that pass beside a gap
are no verdict too, not a PASS, and `Finished` reads a gap as no
verdict, not as an issue. A resolution keeps the recorded order, and a
failure of its order is placed by its original. Two orders do not keep
the dropped step where its seq was, and there no failure but the
initial state's stands beside a gap (a FAIL lost, never a PASS made):
- A linearization: the dropped step's invocation and thread are
  unknown. Its thread may be one no recorded step has, so the recorded
  steps' being one thread says nothing (the first version let a
  failure stand there, and a review's log whose dropped step was
  another thread's conformed whole and FAILed gapped); its invocation
  may be as early as any, so it may be placed before any recorded step.
  Invocation records would bound it (a dropped call's record is in the
  gap too, after the step before it), but only if the recorder writes
  one for every call, which no log shows of the step it lost.
- A domain clock (`quint.order.*`) on the actor's steps: the dropped
  step's value is unknown, so the clock may order it before any step of
  its process, whatever its seq. Placing it by position in the order
  checked (a failure after any step of a later seq than the gap's) is
  not enough: a dropped step whose value is below every recorded one
  comes first, and explains a failure at the first step. The rule is
  the actor's, not the gap's recorder's: domain edges join a process's
  recorders.
A lost tail's cut (§50) is read the same way in those two orders: a
lost step's invocation, or its clock value, is as unknown.

A failure anywhere wins over no verdict: an invariant violated before
every gap is a FAIL though another check's failure after it is none
(`Reason`, and with it the manifest and the CLI's result line, once
said no verdict there while `Finished` said FAIL), for a lost tail too;
each check carries its own cause, so a within or an eventual val a gap
explains says `dropped steps`, as `Finished` does, not `lost steps`.
qtest reads it so: a run's first failure is known where every check
with no verdict failed after it (`SameFailure`, `ShrinkActions`), and a
FAIL of any actor kills a mutant, whatever another actor has no
verdict of.

The gap op of `qtrace.Mutations` drops a step without renumbering, so
the absence path is mutation-tested: its mutants are undecided, never
killed (nor malformed: a gap is no longer among the issues that make
one). It is made only when named (`MutateOps("gap")`, `-mutate-ops
gap`): undecided by design, it said nothing of a run and filled every
report.

A pending call's completion can be only in a gap of its
own recorder after its seq: a completion follows its invocation in its
recorder's sequence, and gaps read by time made a call dropped by a
hole before it at the same instant (a simulation records many steps at
one) or by another recorder's.

A review of the first version found the carry above, pending calls
dropped by gaps that could not hold their completion, a lagging
watermark losing certain FAILs, a late response judged under a short
watermark, and the settle check without the slack; each is fixed, with
a test that fails without its fix.

`-complete-through r=T` states an external collector's watermark (a
ClickHouse poll that knows what it ingested) and is in the manifest, so
a replay judges as the run was. `Filter` keeps a watermark no later
than its `Until`: steps after it are taken out, and a watermark past it
would read their absence as the system's.

## 54. A run gotogether did not drive (E)

Every check before E judged a run gotogether made: a simulation it
scheduled, a test it ran, a log its recorder wrote while it watched. An
agent using it in another project (an observability app explored by
Bombadil, Antithesis's browser explorer) had runs of another kind: an
external tool drove the system, or a deployment ran it, and gotogether
only read the log afterwards. Such a run cannot be rerun with another
seed, its end is not known, its failures must travel to whoever reads
them, and what its adapter wrote may be wrong. Eight items, planned
together by a design review and each reviewed when built, answer one
question each: did the run finish, did the checks run, where is the
evidence, what was judged, does a real driver fit, does the check read
the log, is a missing step the system's, and which of the driver's
actions matter.

**E1. Completion stated.** A conforming prefix is not a finished
journey: the pilot passed a three-step prefix of a workflow with
`Conforms` alone, since every check allows a prefix (§50). `qtest.Finishes()`
claims the end: the run conforms, settled, every eventual val was judged
and holds, and every invoked call completed. It is three-valued: a lost
tail may hold the settle, the end or a completion, so it is no verdict,
while a part certainly missing decides. A binding with no eventual val
finishes no run, because nothing in it says where the journey ends; a
settle alone would pass any quiet prefix. `validate -require-finish` is
the same judgment for the CLI, and a binding's `complete: true` makes it
the CLI's default for that binding (it changes no test's claim: a test
states `Finishes`; lint wants an eventual val beside it). The first
version computed the judgment twice; the review found the two
disagreeing (a call `-resolve-pending` placed passed the CLI though its
caller never got the answer, an actor with only a pending call reported
as unsettled, a settled run with no eventual val judged read as
undecided, an ITF input passing though it records no settle), and
`Report.Finished` is now the one judgment both read: `incomplete (no
settle)`, `(pending calls)`, `(eventual val false)`, `no verdict (lost
tail)`, `no verdict (ITF trace)`, exit 1 for each, and
`validate.Finishable` refuses a binding that cannot finish.

**E2. Strict mode.** A test that skips passes: a CI job that lost quint
from PATH, or its weaving step, went green by skipping. `GOTOGETHER_STRICT=1`
turns every toolkit skip (no quint, no mutants woven, Record's, Explore's
and `qhegel.Shrink`'s empty runs, the scaffold's unwritten mutant test)
into a failure with the skip's message; `qtest.Optional(t, reason)` is
the same for a user's own gated skip (a driver not on PATH). Skips are
a default, not removed, since a laptop without quint must still run the
rest. The review made `internal/skip` the one skip the core's own tests
go through, and the variable fail on any value but 1, true, 0 or
false. `evidence -expect checks.txt` catches the other way a check
vanishes, renamed or deleted: a listed test,
subtest or exploration that wrote no evidence fails the command by name,
packages matched by import path (the review: a package of another module,
an example's root package). A test that skips clears its earlier
evidence. Session-scoped aggregation additionally excludes a renamed or deleted
test's retained evidence: a fresh `GOTOGETHER_SESSION` labels exploration
JSON and manifests, and `evidence -session ID` filters before aggregation.
Completeness checks require a session or explicit `-history`; disable Go
test caching with `-count=1`. IDs must be fresh and shared across processes;
concurrent invocations still need separate evidence directories. CI runs strict
but for the examples' unwoven mutation and coverage tests, skipped by
name; the nightly, whose fresh-seed runs include them, is not.

**E3. Step references.** A step from a driver had no way back to what
the driver saw (`Source` says where gotogether read a step and is never
written). `quint.ref` (`Step.Ref`) is free text, a trace line and a
screenshot, kept by every reader: step logs, OTel events (the event's or
its span's, never a resource's, which would link every step), convert
rules (`ref:`), and from an invocation to a completion that names none.
It is printed at the failing step, in `-history` and `steps`. No verdict
and no cache key reads it, so a ref cannot change a verdict; the cost
is that a verdict taken from the cache, reached on another run, would
show that run's step: qtest finds the run's own failing step and shows
its ref (the review: found without a seq too, with the run's own
recorder and process). `qobs.Ref(s)`
is a typed value in a key's place, so a parameter named ref stays an
argument. `Filter` drops refs when it sanitizes, since a path often
carries a user's name no pseudonym reaches (`KeepRefs` keeps them).

**E4. Run manifests.** A failure did not travel: nothing said which
binding, model, logs, tools and driver it was judged with, or whether
they had changed. `gotogether.run/3` (`validate.Manifest`) records
gotogether's and quint's versions, the binding, spec and convert rules
with their sha256, the instance (constants, invariants, backend), how it
was judged (searches, the finish check, declared losses, watermarks),
each input's path, hash, step count and losses, the verdict by actor
with the failing step and its ref, and what gotogether did not see: the
driver (name, version, seed, origin, args) and its artifacts, hashed.
`validate -manifest` writes one (driver and artifacts from a
`-run-info` file the driver writes), qtest's Validate and Record write
one per run under `GOTOGETHER_EVIDENCE`, and `validate -replay` judges
it again, refusing a changed input, binding or spec, taking no flag the
manifest decides, and exiting 1 on another verdict; a changed artifact
only warns, since a screenshot judges nothing. Paths under the
manifest's directory are relative, so the directory moves whole. The
review found the replay checking a model other than the one it judged:
the spec hashed was the binding's old absolute path, while a replay
resolves the spec as the binding names it now and quint reads its
imports. Verify now hashes the spec the binding resolves and its
imports, transitively, as quint resolves them; an ITF input records its
own instance; the check recorded is the check judged (a complete
binding's finish check in the CLI, none in qtest, where the test's claim
is the check); `Manifest.Bundle` copies every file beside it, so a CI
job's uploaded evidence replays on another machine; a reworded reason
is the same verdict (`Same` compares kinds: a mismatch, a rejection,
the invariant, incomplete's and no verdict's cause, and the action each
failing actor fails at; G1 found the cause and every actor after the
first ignored), and `evidence` skips a
later format's manifest by name. Every validate call typechecked its
model again, about a second each, and the replays multiplied the calls
(cmd/gotogether's tests went from about 60 s before E1 to over 300 s on
CI): models are cached by content (quint, the module, the hashes of the
spec and every import), never by path, so the same files give the same
model and a changed one another (162 s to 63 s here).

**E5. A Bombadil example.** examples/webjobs is the whole case: a
standard-library server and one HTML page (submit, 202 accepted, a
background job, poll until ready, cancel), Bombadil exploring it, the
server recording its effects where it makes them (created, finished at
the commit, cancelled, drained, a settle), and `bombadiltrace` turning
Bombadil's trace into steps. A click is an invocation record, never a
step: a recorded click standing for an observed success would pass a
page that never answered. The first mapping completed a call with the
next change of the page, and the review showed it passing a cancel
answered 500 and ignored, the job shown ready by a poll: a poll's
answer, a second click's and a cancel's can all change one status, and
the page's display does not say which click caused what. So the page
logs each request it sends, numbered as sent, with its own answer, and
only that answer completes the call, whose arguments are what the
request sent (not the state shown before the click, which a late answer
may change between the snapshot and the click). The adapter writes
Bombadil's own times, call ids and order as steps and never records
them again through a Recorder, which would stamp validation's time and
put the whole page after the server's settle (`TestTimesKept`: the
readyEarly mutant conforms that way). The model states the page's end,
not only the server's (`pageShowsEnds`, and a 500ms `within` from a
commit to the page showing it), since a page that never polls again
otherwise passes, and the live journey stops submitting before
Bombadil's time limit (`?until=`) so that its end is fair to it.
Bombadil's own violation exit fails a faithful run. Per push, recorded
runs of the real Bombadil are checked offline and a Go client drives
the real server, faithful and per overlay mutant; nightly, live 45s
runs, strict. The two recorders are ordered by one host's wall clock,
which only one host gives.

**E6. Observation mutants.** A code mutant asks whether the check
catches a wrong program; nothing asked whether it reads the log.
`qtrace.Mutations` plants one mistake per mutant in a conforming log
(drop, dup at the next seq, swap within one recorder and incarnation,
corrupt an argument or observation, retime, move to another actor),
each keeping the recorder's contract (dense seqs, calls and losses
renumbered), so the model judges it and not Reconstruct's gap and
duplicate checks. `qtest.MutateSteps` judges each through the verdict
cache: killed (fails where the original conformed, or unfinished where
the original finished), survived, or undecided; a corruption of a field the binding does not read is marked
`Unread`. The review found kills that were not the model's: a reactor
into an actor whose Lamport keys contradicted its thread was refused by
Reconstruct's cycle check (such candidates are left out, and one refused
anyway is `FateMalformed`, in no score, never killed), and a retime past
its actor's next step sent a within's clock back (a retime now stays
before it, a settle and a watermark, and a survivor whose bounds went
unjudged is undecided). G1 found a third: a mutant the binding could
not plan (a field it reads removed) counted as killed, though a real
log like it is a tool error (exit 2), no verdict; it is malformed too,
refused before a verdict. The brute force it is checked against was rewritten as a
naive enumeration sharing no code with Mutations, over six shapes. On
webjobs' journey it found two gaps in the model, a job created twice
and a submit answered 199, both now claims.

**E7. Absence diagnostics.** §53: a missing step is dropped,
uncollected or a defect, and only a defect is a FAIL; a watermark says
how far a collector delivered.

**E8. External shrinking.** A driver's failing journey is long (45s of
clicks); its failure needs a few actions. gotogether cannot reschedule
someone else's run, so it reruns it: `qtest.ShrinkActions` takes a
failing list of actions, a `Rerun` that does a list on the system and
returns its steps, and reruns subsequences, keeping the smallest that
still fails the same way. The search is ddmin (`internal/ddmin`) over
index sets with a three-valued test: a set fails, passes or is unknown
(every rerun diverged, the budget ran out), an unknown is never taken as
a pass, and the result is 1-minimal (every one-removal passed), not the
smallest by size: an exhaustive search costs subsets, ddmin a few reruns
per action. It is checked against brute force on up to 8 letters (a
monotone failure reaches one of the inclusion-minimal sets) and with
quint on letters.qnt.

Component-aware action shrinking uses the same identity in baseline selection,
calibration and candidate matching. Existing primary actor fingerprints remain
first failures in replay order. Registered local/protocol models add a multiset
of `(role, target, binding, obligation kind, obligation name)` for every failed
named obligation. Receipts do not carry temporal failure indices, so this does
not invent a first component property. Actor IDs, record IDs, counterexample
ordering and explanatory prose are excluded. Mixed primary/component failures
must keep both scopes. Missing checks, exhausted searches and inconsistent or
unjudged nested receipts are insufficient; peer-agreement and component
conformance failures lack supported identities and are explicitly refused.
A checked protocol counterexample is a decided failure without exhaustive order
search; an exhausted search without a counterexample is not a reproduction.

`ActionShrink.System` supplies the registered binding, options and assessment;
`Binding` must be empty, and additional `Opts` follow the system's options.
`Rerun` still creates and drives the fresh environment. Each intermediate rerun
and the final `Validate` recompute the composition assessment outside the primary
verdict cache. The generic shrinker checks the assessment's SHA256 against the
captured native steps and detects mutation around `Judge`. Selected component
identities survive expectation wrappers in `ComponentFailures` and shrink.json;
text names the criterion, and the final composition receipt remains available
to JSON and navigation/TUI consumers. The acceptance case is a plain qenv
producer/consumer counter with a no-deduplication contrast checked by Quint,
independent of grist and inventory.

Four decisions make a rerun's answer mean something:
- **The failure kept.** Any failure would let a shrink drift to another
  bug its smaller sequences show. `SameFailure` (Keep's default) is each
  failing actor's first failure, by the replay's order, by kind and name
  (a state mismatch with its observed fields that differ from the
  model's), whatever ids and arguments. It is unknown, never no, when
  the rerun has no verdict for a part the first failure is judged by: a
  lost tail, an undecided actor, a conforming actor's invariant
  unjudged, which may have failed first. A Keep a passing run meets
  (`Conforms`, `Finishes`) is refused.
- **A rerun that did not run.** An action the system's state does not
  offer (a cancel with no job shown) returns `ErrDiverged`: counted,
  never a pass nor a failure. A shrunk run that did not reproduce is a
  sequence the failure was not seen in, never a pass.
- **Calibration.** Real reruns fail at a rate. The original is rerun 10
  times; each candidate up to n = ⌈ln α / ln(1−p)⌉ times, stopping at
  its first reproduction, p the rate's Wilson lower bound at 1−α. With p
  the rate measured, a half-rate failure reached its 1-minimal sequence
  in 933 of 1000 shrinks, under 1−α; with the bound, in 987. So
  `Minimal` holds with chance at least 1−α for each removal, not
  certainly, and says so. A shrunk sequence whose rate after the shrink
  is 0, or whose upper bound is below the original's lower bound, is not
  minimal: what it showed came from state earlier reruns left, or from
  chance.
- **The reset contract.** Reruns on one system must start alike. The
  first version required a Reset then a rerun of no actions to record
  nothing, and webjobs had to filter its shutdown out to pass: a fresh
  server records its drain and a settle every time it stops. Two such
  reruns are compared instead (up to recorders', actors', processes'
  and threads' names, and times): an environment's own steps repeat, a
  job or poller the first left running shows in the second. The empty
  rerun is repeated after the baseline and at the end, where work the
  actions left would show, and must not reproduce the failure.

The review of the first version found a rerun with no verdict for the
failure counted as a miss, so a one-removal decided by such reruns alone
was taken to pass (it is `Undecided` now, apart, and leaves the result
not minimal); leaks of the actions themselves unchecked, the contract
having run before any action; Keep's default taken from the first rerun
that was not OK, which might have had no verdict, where it is now the
first rerun with a decided failure; the check that the original's first
two reruns fail alike reading only a leak, where two ways of failing may
be the system's and ask for a Keep; SameFailure's first failure not
taken in the replay's order (an invariant failing before a
disabled step is first) and comparing a mismatch without its fields;
and two shrinks in one directory overwriting the files an earlier
manifest hashed (each now in `shrink<N>/`). What the checks cannot catch
is said openly: state the actions leave that changes later reruns
without a step of its own (a counter Reset forgets, a cache) passes
every check, so a shrink can end on a sequence that fails only after
the others. Only a fresh environment per rerun (`Reset` nil, `Rerun`
building it, as webjobs' does) is safe.

webjobs makes its reruns meaningful by keying jobs: a job's id and run
are its key's, so removing a submission renames and retimes no other
job. Its client plays a list of the page's actions against a fresh
server per rerun, and `bombadiltrace.Actions` turns a Bombadil trace
into such a list; Bombadil's own `--reproduce` was the planned rerun and
is not used, since it stops where a timing-dependent page offers other
actions. Every mutant shrinks to one to three actions, 1-minimal,
reproducing 10 of 10, each shrunk list finishing on the faithful server
(the webjobs review: a rerun ending before the page saw its job end
"failed" the within on the faithful server, so a rerun ends as an open
page does, polling to the end).

## 55. Driver adapters, published (G7)

E5's adapter lived in examples/webjobs with webjobs' names, so every
adopter would have copied its 330 lines: a trace's parsing, its refs,
the pairing of a request with its own answer. G7 publishes it as a
protocol and a package: a driver writes a **request log**
(`gotogether.requests/1`, JSONL), package `qdriver` turns it into
steps, `gotogether validate` reads it by its header (package input), and
two drivers write it: Bombadil, through `qdriver/bombadil`, and
Playwright, through adapters/playwright, one JavaScript file with no
dependency.

The protocol is §54's rule made a format. A `request` record is an
invocation with what the request sent; a `response` names its request
(`req`) and is the only thing that completes it; a `step` is anything
seen that answers no request, and names none. A response's arguments
are the answer's fields, under the request's: the call's arguments are
what was sent, whatever the answer says (an answer naming another key
does not change the call), and the answer's value of a field the
request sent is `answer_<field>` beside it. Decisions:

- **A header, required, versioned exactly.** It names the recorder the
  records are numbered in, so two logs joined cannot share one by
  default; a later version is refused with the fix (upgrade), never
  read as this one, and sniffing knows the family by its prefix.
- **`code` is required on a response, 0 for none came.** A missing code
  read as 0 would make a driver's omission a network error the model
  judges. The outcome stays `ok` unless the driver says otherwise: the
  call completed with the code it got, and the model says which codes
  are failures (webjobs' `noFailedRequest`).
- **Unknown fields refused.** A misspelt `arg` would otherwise drop a
  request's arguments silently; a new field is a new version.
- **Times in the log's order, never back.** Seqs are assigned in file
  order, so a record timed before the one before it would contradict
  the thread's program order.
- **`corr` goes into refs, not arguments.** It names the server's
  records to look for in a failure; no check reads a ref, so a
  correlation id never changes a verdict. A model that needs it takes
  it as an argument the driver writes.
- **A torn last line is a lost tail**, as a step log's is.

Bombadil's reader goes through the protocol (trace → `qdriver.Log` →
steps), so there is one pairing of requests with answers, and its
options say what was webjobs' (the extract, the custom actions that
send requests, the log entry's field names, the display's fields);
`AnswerFields` fills an answer the page left null, so a 500's
completion has its action's arguments. webjobs keeps its names
(`journey.Bombadil`) and its rerun list (`journey.FromTrace`, over
`bombadil.States`). Every recorded run, its observation mutants and
its shrinks judge as before.

The Playwright adapter's one finding: Playwright's `request` event
reaches the test after the browser sent the request, and in a measured
run 1 ms after the server's own record of it, so an invocation written
then follows its effect, which a real-time check (Linearize) would
read as a call that took effect before it was made. The adapter writes
a call when `page.route` holds it, before the browser sends it on, and
injects its correlation id there. Its answer's body is read later than
its response event, so records are chained in the order the events
happened, a body waited for at most 10 s (a stream that never ends is
answered with no body). The adapter is tested without a browser:
recorded events (a real Chromium journey through webjobs, and a
hand-written one with a failed request, a 500 and a call never
answered) replayed through it give a golden log, which qdriver reads;
webjobs' `TestPlaywright` runs a real page end to end (full mode,
nightly). Its first version of the journey closed the log while the
page still showed the job before: the last submission's call stayed
pending and the run did not finish, the check catching the driver.

Rejected: a reporter in Playwright's sense (a `Reporter` sees tests'
results, not a page's requests); taking requests from Playwright's
trace file after the run (its format is Playwright's own, unversioned
for this, and it is written at the end, so a killed test leaves
nothing); completing a call from `expect(...).toHaveText` (§54: a poll
may have made it true); reading Bombadil's traces in the CLI (its
mapping needs the spec's names, which a flag list would only
restate: `bombadil.Log` writes the protocol for the CLI instead).

**The review of G7.** Two logs naming one recorder (the README's and
webjobs' `ui`, Bombadil's default `bombadil`) were numbered alike, and
Reconstruct's dedupe on (recorder, seq), there for exporters' retries,
kept the first log's records and dropped the second's as copies, saying
so as info: a second run that failed passed on the first's. A record
twice is a copy only if it is the same record (all but `Source`, where
it was read); two that differ are an error issue `duplicate` of each
actor of either, as a cycle is: a defect of the input, which no verdict
of the system reads past (an actor's result is not OK, its reason
`issue`, and an observation mutant that makes one is malformed). It is
not no verdict: more of the log would not resolve it, and a FAIL it
gave would be the input's, which the issue names. The CLI refuses two
request logs naming one recorder before reading further (a tool error,
exit 2, `input.Distinct`), and the Playwright adapter makes each log's
recorder its own (`ui-<pid>-<n>`).

The review found three more of the protocol's mappings losing what the
model needed. A request argument named `code` was replaced by the
answer's status, silently: it is refused, naming `CodeArg`. An answer's
field the request had sent too was dropped (the request's value kept),
so no model could see a server that applied another value than the one
sent; refusing such a log would have made a misbehaving server a tool
error, so both are kept: the request's under its name (the call's
argument is what was sent), the answer's as `answer_<field>`, always
(a binding reads one parameter whatever the values), and a warning when
they differ. And nested arguments reached steps as Go maps and slices,
which `Step.Args` (scalars) does not hold and no template function read:
they are JSON text, as qobs records a collection, decoded by toQuint
(an integer past int64 is a `json.Number`, whole, in a step log too).
The Playwright adapter chained its records in the order of Playwright's
events, so every line after a response waited for that answer's body: a
test killed while one was read lost them all, where the README promised
a torn last line at worst. Each record is now written when its event
happens, a response when its body has been read (its time then: the
driver had the whole answer, about 50 ms after the headers here, so the
call is wider than it was, never narrower). Its mock and cookie
suspicions were checked against Chromium 141 and are in its README's
limits: a mocked answer registered before the adapter's route is
written as the server's, one after it is never seen; cookies reached
the server though the adapter re-sends a call with `request.headers()`,
which lists none.

Strings went to quint with Go's escapes (`%q` in `itf.Value.String` and
the binding's literals), and a Quint string literal has none: its
lexer takes everything to the next double quote, so `"a\tb"` is a
backslash and a t, a tab is written as itself (quint 0.33, checked by
`TestQuintStrings` against its own ITF), and a quote cannot be written
at all. A string is now its characters between quotes; one with a
double quote is refused, naming the field or the path inside a value,
rather than escaped some way Quint does not read. The replay module's
comments, which named each step with its arguments, were the one place a
raw newline then broke: they are one line each.

## 56. The Go evaluator decides passing runs (H1)

After the batch fix (performance.md, item 2) a grist suite's quint CPU
was mostly the processes that decided runs that passed: counters 231 of
428 CPU s, wallet-grist 210 of 319. The evaluator of §45 already judged
whole runs (a search's candidates, a binding with hidden actions, the
shadow's checks) in milliseconds. The maintainer's decision (the tenth
review's H1): it decides the runs that pass; quint keeps judging the
ones that fail.

- **What it decides.** Each actor's plan, as the check plans it
  (`Check`, `CheckBatch`, and so `Judge`, qtest and the CLI), is replayed
  by the evaluator as quint's fused replay checks it: the start (init,
  or the snapshot's action from no state with its expect), each call
  from the state before it with its expect after, every invariant in
  every state, and in a settled trace every eventual val in the last
  one (decide.go, over the walk the searches' `judge` shares). A plan it
  accepts is a pass: the result quint's fast phase would fill (the
  steps, every check passed), marked "judged by the Go evaluator" in its
  summary, with no module, and, unless NoITF, an ITF written from its
  states (the model's variables, annotated as quint's is), so a passing
  run's ITF still drives `connect.Replay`.
- **What stays quint's.** A plan it rejects goes to quint's detail
  replay alone (the fused one would fail too: one process saved), whose
  report it is: the position, quint's message, the model's state at the
  failure. One it refuses (an `Unsupported` or `EvalError`, a model
  outside the fragment, a binding with no evaluation module) goes to
  quint's replays as before, as does every run under
  `GOTOGETHER_EVAL=quint`. A search's candidates and its confirmations
  (a checker with a history) are §45's: quint confirms what the
  evaluator concluded there. Hidden actions and resumes are the
  evaluator's searches, as before.
- **Bounded responses.** A binding with `within` obligations was quint's
  at first, its obligations judged on quint's ITF states (webjobs: 125
  of its 134 CPU s quint). Now a run the evaluator passes has its
  obligations judged on the evaluator's states, by the same code
  (judgeWithin over the ITF the evaluator writes, its vals computed by
  the evaluator as before): every obligation met is the pass; one late,
  never met, or with no verdict sends the run to quint as before, so a
  report that fails or is not OK is judged on quint's states. Under
  shadow each passing run's obligations are judged on both states and
  compared, verdict, failing step, note and absences, beside the
  states themselves.
- **Finite pinned model choices (I4).** The ordinary complete-trace path now
  uses whole-path finite enumeration and original-action witness specialization,
  superseding the sampled-replay limitation below within its supported scope.
  All evaluation policies disclose this Go search plus selected-path Quint
  confirmation. Complete negative conclusions remain Go enumeration results.
  Bounds, joint-property semantics, current refusals and receipt fields are
  specified in [model-choice witnesses](model-choice-witnesses.md). Initialization
  now follows the same rule: distinct finite roots are charged before filtering
  observations, each carries its initial invariant history, and the selected
  original initializer is pinned from no pre-state and confirmed by Quint.
  Equal later states do not merge different property histories. Initialization
  has separate receipt evidence without an invented observed row; cursor model
  exploration still explicitly refuses ambiguous starts. The following
  sampled-replay notes record the historical limitation and other replay paths.
- **A choice quint samples.** quint's replay of `nondet x = oneOf(S)`
  picks one value at random, not one that enables the action
  (`oneOf(1.to(1000))` with `x == 7` fails `quint test` at almost every
  seed), where the evaluator takes the step if some value enables it.
  The spec admits the step, so the evaluator's verdict is the model's,
  but a pass quint would sample otherwise is not one this change may
  give: a run's evaluator (`qeval.Model.AsReplayed`) refuses a step some
  values of a choice disable, and quint judges that run as before. A
  search's evaluator keeps taking it (its conclusions are confirmed).
- **A choice quint samples.** Handing such a run to quint is no
  verdict either when the step's action chooses inside (a nondet, an
  `any {}` of several enabled branches) and its expect reads what it
  chose: quint's replay draws one choice, so a run the model admits
  failed at one step, another or none on three reruns (review 11, F2).
  The evaluator's refusal names the choice (`qeval.Unsupported.Choice`)
  and counts the successors after which the call's expect holds; quint
  judges the run, and a failure it reports at or after that call is no
  verdict (`the model's choice, sampled`), unless it is the call's own
  expect failing after every successor (a Mismatch whatever the draw).
  A pass stands: quint found a path the model takes. The run's issue
  names the action, the choice and the fix (expose the choice as a
  parameter, or record it as an observation), and so does `lint
  -binding` (`Binding.LintChoices`, from `Model.Chooses`), whose "fits"
  line counts them. Pinning the evaluator's witness in the replay, so
  quint confirms a path instead of drawing one, is ROADMAP I4's second
  half.
- **The guard.** `GOTOGETHER_EVAL=shadow` has quint judge every run the
  evaluator judges, plan for plan, and fails the run on a difference: of
  the verdict (pass, conformance and the call it fails at, the first
  state an invariant fails in, an eventual val false), of a passing
  result (its steps, its checks), or of an ITF state. It replaced the
  shadow's own re-planning of each actor on one thread, which compared
  the evaluator with quint on plans no check made. Rejected runs take
  the detail-only path in shadow too, so the shadow runs exactly the
  path that decides outside it (with `within`, the obligations judged
  on each one's states, compared too). The nightly's evaluator-shadow jobs run
  every example so (the grist examples added), and print how many runs
  the two judged and how many the evaluator refused
  (`GOTOGETHER_SHADOW_LOG`); a disagreement fails the job by name.
- **A sample of the passes, judged by quint too.** Outside shadow, a
  share of the evaluator's passes (`GOTOGETHER_EVAL_SAMPLE`, 1/50 unset,
  1/5 in CI's workflow, 0 for none, `rate:seed` for another choice) goes
  to quint as well, compared as the shadow compares (decide.go's
  sampling). The choice is a hash of the actor's plan (its instance, its
  start, its calls) with the seed against the rate, not a draw: a rerun
  of the same code samples the same passes, its verdict and cost the
  same, and a disagreement found is found again. A sampled pass quint
  does not agree with is an evaluator bug: the verdict is quint's (its
  report, or a result quint passed made to fail), with an error issue
  that says the Go evaluator passed the run and names
  `GOTOGETHER_EVAL=shadow` to reproduce it; the shadow log records it
  as DISAGREE; nothing swallows it. A sampled pass's summary says it was
  judged by both (`Result.JudgedBy`), and an exploration's summary
  counts the evaluator's passes and the sampled ones. The evaluator
  never runs without quint: its IR, cached or not (§57), is found by a
  key that asks quint for its version, and the model's typecheck is
  quint's, so there is no run to sample without it. Cost (-short, CI's flags,
  a cold cache): counters 235 CPU s at 0, 239 at 1/50 (one pass
  sampled), 303 at 1/5 (13); webjobs 82, 85 (none sampled), 107 (3).
  A sampled pass joins its batch's quint chunk when one runs anyway, so
  the cost is less than a process a sample.
- **Shown before it decided.** TestAgreesWithQuint over every model of
  the repository, and the shadow over the examples' -short suites and
  the core's validate and qtest (performance.md, D1, for the numbers).
- **Probed where no model goes (I11).** The repository's models use a
  fraction of the fragment, and the shadow and the sampled passes see
  only what they use. internal/qeval's differential test generates
  well-typed random expressions and actions over the builtins the
  evaluator supports (gen_test.go: integers at the corners of 64 bits,
  sets, maps, records, tuples, lists and variants, folds of the shapes
  fold.go takes as commuting and of none, lets, `match`, `nondet` and
  `any`, guards before and after an assignment, set comprehensions) and
  has quint judge each on both backends: a batch is one module, each
  entry a val (or an action), a variable of its type and a run assigning
  it, so one `quint test` a backend gives every entry's value (its ITF)
  or its runtime error, and one compile the evaluator's IR. An action is
  judged twice: quint's sampled successor must be one of the evaluator's
  (and disabled only if the evaluator's are none, or some of a choice's
  values disable it), and the evaluator's successors must be the value
  of the action read as an expression (`nondet x = oneOf(S); v' = E` as
  `S.map(x => Set(E)).flatten()`, a guard as an `if`, `any` as a
  union), itself judged by quint, so a successor the evaluator misses is
  found though quint samples one. A difference where the evaluator
  answered (a value, or the model's runtime error) fails the test,
  shrunk to a smallest expression that still differs the same way (a
  batch of one-step reductions a round: a node replaced by a descendant
  of its type whose names stay bound, by its type's least literal, a
  literal's element dropped, an action by a branch). Generated by
  math/rand, not Hegel, which the core does not import. Two batches of
  100 per push (full mode), 80 of 200 a night at fresh seeds
  (`qeval-differential`).
  Its first runs found three kinds of wrong answers, now fixed and kept
  as `differentialFindings` (checked with both backends on every push):
  a let's value evaluated eagerly (`{ val n = 1 / 0; 0 }` a division by
  zero, where both backends give 0: quint evaluates a let where its name
  is read); `exists`/`forall` stopping at the first witness in the
  evaluator's order (TypeScript evaluates every element and fails on
  any; Rust stops at a witness in its own order); and `setToMap` of two
  pairs of one key keeping the greatest pair's value, where quint keeps
  the one its set order meets (a value Rust contradicts: now refused).
  An action starts from no state or from one its run's first step
  assigns (`(v' = s).then(a)`), reading v. And one of quint's:
  the Rust backend writes an integer past 2^53 into ITF as bignumber.js's
  object (`{ s, e, c }`), in `quint run` and `quint test` alike, which
  the harness reads back.
- **Tests that fail without it.** A passing run starts no `quint test`
  process and a failing one starts exactly the detail replay, whose
  report is quint's (TestEvaluatorDecidesPasses, counting a wrapper's
  processes); the evaluator's ITF is quint's states
  (TestEvaluatorITFIsQuints); an evaluator that passes every run
  (`TamperEval` 6) is caught by the shadow, by Check and CheckBatch, and
  is the verdict without it (TestShadowCatchesAWrongPass); a partial
  choice is refused as replayed (TestAsReplayedRefusesPartialChoices).

## 57. Typechecks and compiles cached across processes (D3)

§56 left each process that judges runs compiling the binding's
evaluation module once (`quint compile`, about a typecheck's cost):
counters' -short suite compiled it nine times where it had compiled it
once, in its packages' test binaries and its overlay mutants'
subprocesses, beside six typechecks of the same model. validate now
keeps quint's output for both under the user's cache directory
(validate/cache.go), and a process that asks again parses the entry.

- **The key** is everything quint's output depends on, conservatively:
  quint's version (`quint --version` of the binary used, as manifests
  record it); the spec's and every imported file's absolute path and
  content hash (the manifest's import scan, transitive), and every
  `.qnt` file in each of their directories as well, so an import the
  scan misses in a sibling file still changes the key; the subcommand
  and every argument but the file (compile's main, init and step), and
  the evaluation module's text, which carries the binding's fixed
  constants and the model's relative import; the cache's own format
  (`cacheFormat`). An import the scan names and cannot read gives no
  key: quint runs, uncached. The evaluator's version needs no place in
  it: an entry is quint's output, which the evaluator reads afresh.
- **Entries** are whole or absent (written under a temporary name and
  renamed); one that does not parse is a miss and is written again. An
  entry no process used for a week is pruned, once a process.
  `GOTOGETHER_CACHE=off` bypasses the cache; `=<dir>` places it.
- **The entry point** it needed: `binding.ParseTypecheck`, the model
  from typecheck JSON (what `LoadModel` parses).
- **Not across CI runs.** CI's runners start empty, so it saves within a
  job (a suite's binaries and subprocesses, the CLI's invocations).
  Carrying it across runs (`actions/cache`) would put a stale entry's
  risk on every push for the binding and qeval packages' typechecks,
  which do not go through validate: not done.
- **Tests that fail without it**: a second ask starts no quint
  process, and an import's change, a sibling's, another quint version
  and `GOTOGETHER_CACHE=off` each start one (TestCacheAcrossProcesses);
  no key without every import (TestCacheNeedsEveryImport).

## 58. Why a step was rejected, and the navigators (H2, P5)

The newcomer's largest time sink (the tenth review) was a rejection
whose report gave the step and the state before it, not which of the
action's conditions was false: a grist mutant's create committed blind
(`conditionHolds` false), an app's decision with an empty read set
(`covers` false), a limiter's `admitted.size() < LIMIT` with 2 < 2. And
pavlov's navigator showed what an interactive walk of a model's states
and of a run's choices gives an agent and a person alike.

- **The explanation is quint's report's, never a verdict.** A check
  whose actor quint rejected (a conformance failure of kind "disabled"
  at a call) asks the Go evaluator, when the binding has an evaluation
  module (§45) and GOTOGETHER_EVAL is not quint: it replays the plan's
  calls before the rejected one (the state compared with quint's, as
  quint reported it), then `qeval.Model.Explain`s the call
  (`Failure.Why`, explain.go). A search's candidates (a checker with a
  history, VerdictsOnly) ask nothing. The evaluator disagreeing (it
  takes the step, reaches another state, rejects an earlier call) is
  said ("not explained: ..."), beside quint's report as before.
- **Explain** descends the action conjunct by conjunct: `all {}` and
  `and` to the first false one, the actions and definitions it applies
  (their arguments bound), lets, `ite` (the branch taken, named in the
  context), `forall` (its first witness), `implies` (the consequent); a
  choice whose every value disables it, by the value closest to enabled
  (the most conjuncts holding before the false one: the limiter's third
  client, not the first's "already admitted"); an `any {}` none of whose
  branches is enabled, each branch's. Each level is a condition: where
  it is, its text, the values of its free names and of its operands that
  are applications (`admitted.size() = 2`), a value shown once.
- **Source text from the IR, not positions.** quint's JSON IR has ids
  but no positions, and `quint parse --source-map` numbers the parsed
  files' nodes, not the flattened module `quint compile` writes (an
  instance's import copies the model's definitions under new ids). So
  qeval prints Quint from the IR (print.go): builtins as methods,
  operators infix, the compiler's parameter renamings (`p_<id>`) and the
  evaluation module's `__gtg_` names undone. It is the expression, in
  the printer's layout, which a reader finds in the file by its text.
- **What the model could do instead**: the transitions of its step in
  that state, each named by the first action it applies below step (a
  helper's, part of its caller), an action enabled many ways once with
  how many (`createCounter (32 ways, e.g. ...)`), six shown.
- **Labelled transitions.** `qeval.Model.Transitions` is Successors
  with what led to each successor (the actions applied, the choices
  made), one per label and state; a successor's labels cost nothing when
  unlabelled (searches and runs). P2 reuses them. TestAgreesWithQuint
  holds them to quint's traces (the same states as Successors, the state
  quint took reached by a transition applying the action its trace says).
- **grist's vocabulary.** grist's generated step actions reject through
  grist.qnt's few definitions, read by their frames (name, parameters,
  arguments): `covers(reads, need)` false is a decision that read
  nothing of need's events, the read models whose `<rm>Reads` cover them
  named; `conditionHolds` false names the event appended after the read
  position that matches a read (a concurrent append); `freshKey` a
  command id answered again; `<call>Admits` with its record's key a
  Keyed dependency applying a key twice. A hint beside the conditions.
- **Info lines that do not apply.** The actors a binding's actors:
  patterns leave out are in Report.Issues (the data) and not in its
  Summary: every two-sided run (a grist store and its clients) had one on
  each report. The Linearize hint is an actor's whose own steps carry
  invocation times without a domain clock (order.*): a store's commits
  and a processor's calls are placed where they took effect, and another
  actor's client history made every grist store-side failure suggest it.

The navigators share the explanation and the transitions.

- **A model's** (`Checker.Nav`, validate/nav.go): a node is a state and
  its path from the start (init, or the state after an actor's first k
  calls of a recorded run, the evaluator's replay; a call the model
  rejects before k is the error, explained); its branches the step's
  transitions (`take`, the call with its arguments as literals, which
  `-follow` takes, `#n` after it when transitions share it), its
  disabled actions the step's own actions (BranchActions) no branch
  takes, each with Explain restricted to its branch; its invariants'
  values; the variables its last transition changed. Checked against
  brute force (TestNavBranchesAgainstBruteForce: every action with every
  argument of its domain enabled exactly when a branch takes it) and its
  JSON by a golden.
- **A run's** (qtest/nav.go): a node is a prefix of an exploration's
  run's choices in Exhaust's schedule syntax, `<schedule>@<k>`, the
  choices before k as the schedule says, nominal otherwise; its branches
  the next choice's options, named by the simulation (`Sim.ChoiceOptions`:
  a fault kind's declared options, the runnable tasks of a scheduling
  choice in option order, the nemeses enabled, read by a chooser qenv.New
  hands the simulation); a child the replay with that option forced and
  nominal after, shown at the next choice: the steps recorded so far,
  Sim.Tasks, each actor's model state after them (the model navigator's
  start), and the verdict of the whole run it makes. It is stateless, as
  Exhaust: each node a replay from the start, so a system must be
  deterministic, and a node of the base run's own choices says where its
  replay diverged from them (entropy, which a schedule does not replay,
  is said too). It runs in the test binary: GOTOGETHER_RUN names the run,
  GOTOGETHER_NAV the node (start, fail: the last choice before the
  failing step was recorded, @k, an id), and the exploration navigates
  instead of exploring; its claims are not judged. `-save` writes the
  node's run to the exploration's corpus.
- **Front ends.** `gotogether nav` (-json for agents: one object a
  call), a failing run's `navigate:` line and validate's (the model
  before the rejected or mismatched step). The TUI (P5's people's front
  end, Charm's libraries) is a module of its own, kept out of the core:
  it reads the same types (validate.Node, navproto.Node) and drives the
  same commands.
- **Sessions** (`GOTOGETHER_NAV=serve`): a node cost a go test process
  (seconds: the build check, the binary's start, quint's checker), where
  its replay is milliseconds. A session is the test binary answering
  requests (`navproto.Request`: a node, options to follow, fold,
  save) a line at a time with `navproto.Response` lines, the base run
  made once and again first (the first-seed-twice check: a base that
  does not replay fails the test and the session's first line says so),
  the checker kept, every node still a replay from the start (a node
  reached twice is the same replay, not a cached answer: a session hides
  no nondeterminism), its verdict cached by its steps (judge's cache). A
  front end runs the compiled binary (`go test -c`): go test buffers a
  passing package's output.
- **Folding**: a model's node folds its single branches until a node of
  several (`Navigator.Fold`: a state seen again or a thousand
  transitions stop it; Changed is against the state it folded from); a
  run's node folds the choices that are no decision, those of one
  option and those made nominally where the base run made them
  nominally too, to the base run's next choice made otherwise (on its
  path: the next decision the run made), or to the run's end off it,
  where every later choice is nominal. One replay either way: the
  chooser moves its snapshot's choice past each folded one. Both say how
  many they folded.
- **The TUI** (qnav, a module of its own: Charm's v2 libraries, bubbletea
  v2.1.0, lipgloss v2.0.6, bubbles v2.2.1, stay out of the core). Its
  state (the nodes visited, a cursor each, the filter, the scroll)
  changes in Update alone and View renders it, so its tests drive
  Update with key messages and compare screens with goldens, no
  terminal (charmbracelet's teatest has no released v2). A model's node
  is fetched from the in-process Navigator; a run's from a session of
  its test binary (`go test -c`, then `GOTOGETHER_NAV=serve`, the
  responses on their own descriptor where the platform has one, so the
  system's prints stay out of them), in a command, so the screen says
  it is working. The branches stay in view (windowed around the cursor)
  above the node's detail, which scrolls; a changed variable is marked
  `*` (a model's Node.Changed; a run's actors' states against the node
  before it), the base run's option `◂`, so that NO_COLOR loses nothing;
  colors are the terminal's sixteen, which its theme shades for light or
  dark. Back is the visited stack (a node is a replay or a path: nothing
  to undo), `g` a run's `fail` or a recorded run's state before the call
  the model rejects, with why (a run the model rejects opens there).
  `gotogether nav` without `-json` execs it on a terminal when it is on
  PATH. Its VHS tapes (qnav/tapes: the limiter, hello's failing run,
  counters' blind mutant read as grist's, folding) are the README's GIFs
  and, as text (their navigator screens, each once, the frames of a node
  being fetched left out), checked against goldens when vhs, ttyd and
  ffmpeg are installed (a CI job of their own).
- **Tests that fail without it**: the limiter's rejection names
  `admitted.size() < LIMIT` with its values, by Check and CheckBatch
  (TestRejectionNamesTheConjunct), none under GOTOGETHER_EVAL=quint; the
  golden shop's empty read set reads as grist's (TestRejectionReadsAsGrist);
  counters' blind mutant's rejection names conditionHolds and a concurrent
  append (its TestMutant); the info lines (TestLinearizeHintOnlyWhereItApplies,
  TestActorsLeftOutNotInSummary); the navigators (TestNavBranchesAgainstBruteForce,
  TestNavNodeJSON, TestNavRun: a node by its id and by following equal,
  fail's node, its nominal child passing; TestNavFromValidate); folding
  (TestNavFold, TestNavFoldFlag) and sessions (TestNavServe: a node by
  its id and by following equal, a fold to the base run's next decision
  and, off its path, to the end; TestNavServeNondeterministic); the TUI
  (qnav's TestLimiter, TestFold, TestRunDiff, TestHelloRun,
  TestGristRejection: without folding, the change marks or back, they
  fail).

## 59. Possibility and scenario claims (P3, P4)

pavlov's `:possible` and its scenarios check what a model must be able
to do, where every check before them said what it must not: a model too
strict to reach a scenario a conforming run would show passes every
invariant and judges a code that never shows the scenario faithful.

- **A possible** (`binding.Simulation.Possible`, `simulate: possible:`)
  is a Quint predicate over the state, a model action's name (taken by
  some trace), or `{name, steps}`: actions some trace takes in turn,
  others between, each a name or a call naming some parameters' values
  (`buy(result = "short")`). A bare string that names an action, or
  calls one, is a step, any other a predicate (`Possible.Resolve`,
  against the model): an action is never a predicate, so the reading is
  unambiguous, and a `when:` naming one fails with the `steps:` to write
  (quint refused it as a val, naming neither). A mapping takes name,
  when and steps alone: a misspelled key fails the load.
- **The Go evaluator searches first** (`model.Analyzer.Possible`,
  `qeval.Model.Reach`): from init by the model's step, distinct states
  by Key, a parent tree for the witness. The predicates and one-step
  sequences share one breadth-first search (a predicate's path is the
  shortest; a step is a property of a transition, checked on every one,
  a seen state's too); a longer sequence has its own, its node a state
  and the steps taken, best first (the most steps taken, then the
  shallowest), so a scenario the model can follow directly costs as many
  expansions as it has steps. Greedy matching of a sequence is exact:
  taking each step at its first chance is the longest prefix any path
  through those transitions takes. Under a depth bound a (state, steps
  taken) reached again by a shorter path is expanded again: best first
  can reach it by a longer one first, and the bound then cut what the
  shorter one reaches (two of vending's 125 three-step sequences, each
  reported "not found" with a path within the bound;
  `TestReachDepthBoundAgainstBruteForce`). Bounded by states (default 2000,
  since §69 the one `simulate.search`, `-states`; default 100000 and no
  depth) and depth (the trace's steps): a
  search a bound cut says "not found within the bound", with its extent;
  only a search no bound cut says impossible, the whole state graph seen.
  The predicates are vals of a module of their own
  (`binding.PossibleModule`), so a predicate quint refuses fails the
  search, never the binding's evaluation module.
- **quint samples what the evaluator did not find**, in the first
  invariant's own run (a held one's, whose samples no violation cuts
  short): the predicates as its `--witnesses` (how many traces each held
  in: the witness), the sequences of names in the first 100 of its
  traces, written with `--mbt` (their `mbt::actionTaken`, a stutter's
  empty name dropped), then in batches of 100 traces of their own until
  each is found or the samples are spent. A binding with no invariant,
  or whose hosting run violated it, samples them in runs of their own.
  They once had their own runs from the first, a predicate found by
  quint another to take its trace (`--invariant not(P) --mbt`): 15-40 s
  more per grist binding in CI's loop (library 23 s against 64 s now,
  counters 16 against 33), for a random trace's action names. A step naming a
  parameter is the evaluator's alone (quint records no action's
  arguments). With `-verify`, predicates go to Apalache (`quint verify
  --invariant not(P)`: no counterexample is a bounded proof of
  impossibility). The evaluator calling impossible what quint found is
  the evaluator's bug, an error, never a verdict. A possible neither
  found fails simulate (exit 1): "the model forbids it" after a complete
  search, else "the model may be too strict", each with nav to find the
  guard and the knobs if it is only rare. The evaluator runs first
  because it is cheap (milliseconds a state, its witness the shortest
  and with arguments); quint confirming what it found would double
  simulate's time for what TestAgreesWithQuint already holds it to.
- **The grist generator writes one per Take** (gristtest.Takes, gristtest.Catalog since H4): each
  command by each outcome (ok, each refusal, its rules' too) as the
  predicate that the model's step can answer so next (an identity left,
  some arguments of the step's draws with that outcome: a refusal leaves
  no trace in the state, so "it happened" is no predicate), each query,
  processor step, declared call and site by name. Possibles are in the
  default and calls bindings, not a variant's. Checking them on the
  three examples found the generator's own limit: four identities, one
  per command, could never reach the library's loan limit (a join, two
  books, three borrows, then the refused one), and with ten drawn at
  random a trace's refusals spent them first (0 of 10000 sampled
  traces). Identities are now thirty, taken in turn (`nextID`: the
  first unanswered), so a trace of simulate's default length never runs
  out and the step never draws a used one; the limit was then reached
  in 58 of 1000.
- **A scenario** (`qtest.Scenario`, `NamedScenario`) is an Expectation:
  a run's steps take its Takes in turn (each Take's action, Outcome and
  `With` arguments, compared as Quint values), so Some, All, None and
  Expect take it as they take any; the actors' calls are merged by their
  steps' times. A claim no run meets lists, per run, the longest prefix
  taken with each step's place, where it stopped, the next step's near
  misses (taken with another outcome or arguments) and its hint, then
  whether the model on its own can follow it (the evaluator's search for
  the same sequence: the system is to be driven there, or the scenario
  names what the model forbids). A Take's outcome reaches the model
  through the parameter the binding passes `{{.outcome}}`, mapped by its
  outcomes; with none (or an outcome it skips) the scenario is not
  searched, saying so, where the search once dropped the outcome and
  called possible a step the outcome made impossible. A `With` naming no
  parameter of its action fails the claim before any verdict, as a
  misspelled action does (`None` of it held for nothing). `qtest.Model(t, binding).Expect(t, qtest.Possible(scenarios...))` is the model-first
  check, no run needed.
- **grist's scenario** is written in the catalog's values:
  `Command.With(args)` is a `grist.Invocation` (the command's action,
  its scalar arguments as the generated model's parameters, renames
  included, and Quint literals), `Refused(err)` expects a refusal the
  command or its rules declare (another panics);
  `gristtest.Scenario(name, invocations...)` is the NamedScenario of
  their Takes. `grist new`'s starter and examples/library check one in
  the model (`qtest.Possible`) and in runs.
- **Tests that fail without it**: `TestReachAgainstBruteForce` (every
  path of the vending model to depth 7: found exactly when some path
  meets the goal, a predicate's path the shortest, a complete search's
  "not found" no path; the progress and the order ablated fail it),
  `TestSimulatePossible` (vending's possibles reached, its strict
  variant's impossible, quint and the evaluator agreeing on each),
  `TestPossibleResolve`, `TestScenarioAgainstBruteForce` (every step
  list of up to five over two actions and two outcomes, every scenario
  of up to three: holds exactly when an increasing choice of steps
  takes it, the prefix the longest; the outcome filter ablated fails
  it), `TestScenarioMiss`, `TestScenarioOverRuns`, grist's
  `TestGeneratedPossibles` (the shop's outcomes reached; its rule
  mutated never to refuse full, `addItem.full` not), gristtest's
  `TestScenario`, library's `TestAtTheLimitPossible` and `TestAtTheLimit`,
  the starter's `TestScenariosPossible` and `TestFull` (TestNewIsGreen).

## 60. One ABI for woven code (H5)

Three weavings rewrite code to call the toolkit: qenv/weave's aspects
(sync methods and `//qenv:go`), `qweave -cover` (a `Hit` per block, the
blocks registered at init) and `qweave -mutate` (`Cond`, `Pick`, the
dropped call's switch, the mutants registered at init). Their targets
were exported from the packages users read (fourteen functions in
`qenv/weave`, `qcover.Hit` and `Register`, `qmut.Register`, `On`, `Cond`
and `Pick`), so a user saw them next to the API meant for them, with
the same compatibility promise implied.

They are package `woven` now, the only surface public by necessity
(woven code is the user's package, so it cannot import an internal
one):
- **No promise to users.** Its doc says so: nothing in it is called by
  hand, and any of it may change in any release; it is stable only
  between code woven by one version's aspects and qweave and that
  version's core. A user names `qenv/weave` in a tool file (it keeps the
  aspects, and its path, so no tool file changes) and reads coverage and
  mutants through `qcover`, `qmut` and qtest, whose types (`Block`,
  `Mutant`) stay theirs: `woven.Block` and `woven.Mutant` are the
  registration's own, converted when read.
- **Its state is internal** (`internal/wovenrt`): the sync hooks qenv
  installs at init (was `qenv/internal/keyed`, now with `Go`, so `woven`
  does not import qenv), the registries, and the run keys qcover's
  `WithCounts` and qmut's `WithMutant` and `WithReach` set, a function
  each rather than the collector's type, so `wovenrt` imports nothing of
  the core but qrun and runkeys.
- **Why so small an import graph.** The core's own mutation testing
  weaves core packages: a woven package imports `woven`, so any package
  `woven` imports, directly or not, would import itself once mutated.
  qweave never mutates `woven`, `internal/wovenrt`, `internal/runkeys`,
  `qrun`, `internal/hooks` (and still not `qmut` or `qcover`), and a
  program woven without qenv sees `wovenrt.Current` false, so the real
  sync methods run.
- One import alias, `quintwoven`, for coverage and mutants alike (was
  `quintqcover` and `quintqmut`).

Twenty identifiers left the user-facing packages' sections of
api/core.txt (qenv/weave's fourteen, qcover's two, qmut's four);
woven's own section records the ABI (those twenty functions and the two
registration types), so a change to it still shows in review.
storetest's twelve per-check functions left api/grist.txt (their names
stay the Checks' names).

grist's half: the generator is grist/internal/gen (Generate, Starter,
the model command's flags), reading an App through an interface of its
`Verify` and `Catalog`, so it imports nothing of grist; grist's model
command and gristtest call it. The catalog's types moved beneath both,
to grist/internal/catalog (with `QuintName`, the scalar-field rule and
the JSON Schema both the generator and the runtime use), and grist
aliases them, so a user's `grist.Catalog` is unchanged. grist embeds
grist.qnt and hands it to the generator at init (an embed cannot reach
a parent directory, and whoever holds an App has imported grist).
`MainArgs`, `UsageError` and `QueryArgs` went unexported (the tests
reach them through export_test.go) and so did nine limits, their values
documented on the options they bound. 18 identifiers, 40 lines with
their fields and methods, left api/grist.txt; gristsim's timeouts stay,
being what a simulated client's retries are reasoned about by.

## 61. The state-graph explorer (P2)

`gotogether simulate` sampled: a thousand traces of thirty steps, so
"holds" meant "no sample violated it". The Go evaluator (§45, §56)
computes a state's transitions in microseconds, so a binding's model
small enough is checked in every reachable state instead, with no
quint process but the cached compile: `gotogether simulate -graph`, and
`model.Analyzer.Graph` beneath it.

- **One search core** (internal/qeval/graph.go). P3's Reach and the
  explorer are one search: nodes by Key (and a sequence goal's progress),
  dense ids in the order found, a parent tree, a frontier ordered by
  progress, depth and id (so breadth first without a sequence), bounds
  on states and depth, and which bound cut it. Hooks make the
  difference: Reach's meet its goals and stop; `Model.Explore`'s keep
  every expanded state's out-edges, so the graph is there for what
  builds on it (P6's cycles), not re-derived. Init's every successor is
  a start (a nondeterministic init has several).
- **Edges as labels.** An edge is a label id and the state it reaches;
  labels are interned. Validate's label is the step's branch: the
  action applied with its arguments and the choices it made itself. The
  step's own choices used by no branch are dropped (bank's step picks a
  transfer's accounts for a deposit too: about 130 labelled transitions a
  state, 37 edges), merging edges that differ only in them. A label is
  interned by its events written in full: `Event.String` cuts a value
  past 200 characters, and interning by it once gave two calls whose
  arguments differ past the cut one label, so the second state's path
  named the first call (`TestExploreLongLabels`).
- **Shortest counterexamples.** Ids are in breadth-first order, so the
  first id where an invariant is false is a nearest violating state and
  its path up the parent tree a shortest trace. Every state found is
  checked, those at the depth bound (not expanded) included.
- **Complete or not.** Complete means no bound cut the search and the
  evaluator refused nothing: every state reachable (within the depth
  bound, if set) was checked, and "holds" is a proof for the instance,
  as far as the evaluator is right.
- **Checked by quint's sampling.** That proviso was the whole proof: no
  quint process looked, and a fold the evaluator folded in the wrong
  order (review 11, F1) made a complete graph of states quint never
  visits, lacking the one it does, and "proved" an invariant quint
  violates. `simulate -graph` now also samples the instance with quint
  (`simulate.samples` traces, at most 50, of `simulate.steps`) and
  requires every state quint reaches to be a node of a complete graph
  (`GraphResult.Lacks`, by `Model.Key`): every state quint reaches is
  reachable, so a missing one is an evaluator bug, a FAIL (exit 1) that
  names the state and voids the graph's verdicts (none says `holds`).
  The old fold is caught at once (the round robin's first sampled
  state); `TestSimulateGraphCheckedByQuint` tampers the graph to lack a
  state (`hooks.TamperEval` 8). A sample is no proof that the graph has
  no extra states (an evaluator that reaches a state quint cannot would
  show a false violation, which its replay in quint refutes) nor that it
  lacks none quint's sample missed: it is the evaluator's run-time audit,
  as a sampled pass is (§56).
  State identity is `Model.Key`, each variable's value written as a
  Quint literal in the order of Vars: strings quoted, sets and maps in
  Compare's order, records by field name, every compound bracketed, so
  equal keys are equal states (operators, which no state holds, aside).
  A state at the depth bound cuts it even if it has no successor
  (unknown, since it is not expanded). The default bound is 100000
  states and no depth bound (`-states`, `-depth`, `simulate.search`
  since §69): grist's log grows without end, so
  its graphs are always cut, and the depth bound makes the cut "every
  state within D steps" rather than a breadth-first prefix.
- **Unsupported is never complete.** A construct outside the evaluator's
  fragment met during the search (an integer past 64 bits, an order-
  dependent fold, a set past 65,536) stops it: the graph found so far
  stays (its paths are real traces, a violation in it is real), cut
  "unsupported", `Stopped` naming the construct; an invariant the
  evaluator cannot evaluate in some states is "unchecked" (never held),
  still checked in the others, where a violation is one. Either exits 2
  when nothing failed, and the report says to sample without -graph.
- **Classified as simulate classifies.** `holds` in a complete graph is
  proved; in a truncated one it is "bounded" ("none of the N states
  explored violates it, which proves nothing past them"), never "holds";
  violated, it fails with the shortest counterexample. `allows`
  passes with the shortest violating path, and fails when the complete
  graph has none (checking the code against it proves nothing).
- **Deadlocks** are expanded states with no edge. quint run says
  nothing of them (a sample just ends); TLC fails on one by default, and
  so does -graph, unless `simulate.terminal` (a Quint predicate) holds
  there: a model's intended end. The smoke run found two in the
  examples, both complete graphs: twophase (99 states, 9 deadlocks:
  every participant decided, the protocol's end, unsaid) and crossdb
  (10377 states, 128: a resource stuck deleting once the model's three
  database ids are spent, the finite pool's end). A state with a
  stutter (an edge back to itself) is no deadlock, so a model whose step
  can always stutter (a read, a grist model's queries) never has one:
  the report counts its quiescent states (every edge back to itself)
  instead, which say where it stops, and fails on none of them.
  `terminal:` is any Quint predicate over the state, evaluated where
  the step has no edge (a non-Boolean is an error). twophase declares
  its end (every node decided) and is -graph's first demo: both
  invariants proved over 99 states, and a model mutant (the coordinator
  deciding on one vote) refuted with a counterexample quint replays.
  The binding's actions step never calls are listed (`outside`): no
  state of the graph comes from them, so an invariant about their
  effect holds vacuously (crossdb's settledCorrectly, whose antecedent
  only settle sets).
- **Actions never enabled, and coverage**: each of the step's actions
  with the edges taking it and the states it is enabled in. One never
  taken in a complete graph is dead in the instance (vending's repair,
  by design); in a truncated one, only not reached. Reported, not
  failed: an instance's constants may rule one out on purpose.
- **Replay in quint.** Each counterexample is a Quint run over the
  simulation module (`-out f.qnt`, then `quint test --main simulate
  --match gtg_ f.qnt`): init (or, where init has several states, the
  start's values assigned), each step its branch's call with literal
  arguments, then `.expect(not(inv))` or, for a deadlock,
  `.then(step.fail())`. Each call is checked by the evaluator before it
  is written: the branch's call where it alone reaches the path's state,
  else step (a step that assigns more than its branch, `all { any {
  inc, dec }, n' = n + 1 }`, once replayed as `inc`, which quint
  refused to typecheck). The replay is exact where each call has one
  successor; where one has others (`Counterexample.Sampled`), quint
  draws among them and may miss the path. A call replayed as the
  branch's action does not re-check a guard step adds around it (the
  evaluator found the transition through step).
- **Possibles under -graph**: the evaluator's search at the graph's
  bounds, then quint's sampling for what it did not find, as without
  -graph (a grist app's deep scenarios, which a 20000-state breadth-
  first prefix four or five steps deep never reaches). The report says
  when it samples ("sampling N possibles the Go evaluator did not find
  with quint"), and each such possible names quint's trace: it is the
  one quint process of -graph, and no verdict of the graph's rests on
  it.
- **Cost.** A state's expansion is its step's whole choice product.
  The smoke run (every binding of the repository with invariants, 20000
  states each, one core): 0.1-0.7 ms a state for most (checkout 6.2 s,
  counters 7.6, crossdb's whole graph 6.7, edgepublish 6.3, grpcledger
  12.8, jobqueue 2.9, lease 2.3, library 12.6, raftkv 4.6, streams
  14.3: 10^5 states in 10-65 s), 1.2-9 ms where the product is large
  (wallet-grist 26 s, account's amounts 29, webjobs 56, broadcast 71,
  bank's 135 picks 90, payd 93, kvstore's `0.to(revision)` 188). No
  model met a construct the evaluator refuses. The P2 review's profile
  (kvstore) found a step's choice product re-evaluating each branch for
  every value of the choices it does not use, and every labelled
  transition building its label's text and its state's Key: a call's
  successors are now computed once per state (`callAction`: an action's
  body binds only its parameters; a call with an operator argument,
  Choreo's, is not remembered, since no key writes a closure), labels
  are lists a choice prepends to in one allocation, and a state's Key is
  computed once per assignment. At 20000 states, on one machine:
  kvstore 137 s to 78, payd 72 to 20, bank 62 to 30 (CPU 2-3.7 times
  less at 6000), the same graphs.
- **Tests that fail without it**: `TestExploreAgainstBruteForce` (every
  path of graph.qnt and vending to each depth bound from every initial
  state: the same states at their least depths, each path a trace of
  that length, an expanded state's edges exactly its transitions, the
  deadlocks, complete exactly when no state lies at the bound, a state
  bound's prefix; a deepest-first frontier, or edges to seen states
  dropped, fails it), `TestExploreGraph`, `TestExploreUnsupported`,
  `TestCheckerGraph` and `TestCheckerGraphUnsupported`,
  `TestSimulateGraph`, and `TestGraphAgreesWithQuint`: on eight small
  models, each invariant quint's sampling violates within the steps the
  explorer reports violated (graph's small, account's underLimit,
  lease's noStaleWrite), and every counterexample it reports replays in
  quint test.

## 62. Liveness as cycles (P6)

An eventual val (a binding's `eventually:`) is judged in runs at a
settle: the last state of a run whose system went quiet. The model said
nothing of it on its own: `simulate` samples finite traces, and a
finite trace never shows "forever". P2's graph does: a reachable cycle
of states where the val is false, fair under the assumption below, is a
behaviour that from some point on never makes it true. What is checked
is that the val is true infinitely often (□◇P; for a run that settles,
P at quiescence), not that it is true once (◇P): a lasso's prefix may
pass through states where the val holds (review 11, O2). That is the
right semantics for a settle, and the report says it so.
`simulate -graph` judges each eventual val so, and a run that did
not settle has the cycle its model states went round reported with it.

- **The search** (internal/qeval/lasso.go, `Graph.HotComponents`).
  The graph restricted to the hot states (the val false; expanded, and
  not the one state the state bound cut while expanding it, whose edges
  are not all known) and to the edges of families that do not stop,
  split into strongly connected components (Tarjan's, iterative: a
  graph's paths are up to 10^5 long). Every component is reported; one
  of a single state has a stutter for its only cycle, fair where no
  weakly fair family is enabled there (a deadlock, a terminal end, a
  state only stutters, unfair or stopping families leave), else ruled
  out with what is enabled as its missed families (it was once dropped,
  so the note naming what fairness rules out never counted the states
  of a model whose state only grows: P6's review). The lasso is the shortest path to the
  component's nearest state (the least id: ids are breadth first) and a
  cycle through it inside the component.
- **Fairness: weak, per action, with stuttering.** A family is the
  branch's action of a label (the step's `any {}` branch: an action
  name, whatever its arguments; `step` for a transition no action
  names). Per action rather than per call: a grist model's processor is
  an action whose trigger argument changes with every event, so a call
  is never continuously enabled and per-call fairness would oblige
  nothing; and per call would oblige every client's every input. As in
  TLA's WF, a step that leaves the state unchanged is no step of its
  family: an edge back to its own state neither enables nor takes it.
  That is the subtle part the ROADMAP named: reads (a grist model's
  queries, quorumkv's read) loop in every state, and counted as steps
  they would make every state a fair cycle (a reader may read forever)
  or satisfy a processor's fairness with a no-op. A weakly fair family
  enabled (by an edge to another state) in every state of a cycle must
  be taken by one of the cycle's edges. Weak fairness needs no
  refinement of a component (strong fairness does): a cycle through a
  subset of its states has every family enabled throughout the
  component enabled throughout it and takes no more families, so a
  component that misses a family has no fair cycle in it, and one that
  misses none has a fair cycle through all of it. A fair component's
  cycle is built from its nearest state through waypoints, one per
  family the walk so far leaves enabled throughout and untaken (an edge
  of it inside the component, or a state of the component where it is
  not enabled), shortest paths between them; each waypoint settles its
  family for good, so there are at most as many as families, and the
  result is checked fair by the same definition.
- **What is fair by default: every action.** Two opt-outs, per binding
  or on the command line: `simulate.unfair` (`-unfair`), an action
  never obliged and allowed to run forever (a client's input, which may
  come or not), and `simulate.stops` (`-stops`), an action in no cycle
  (a fault, which a settle turns off; an input the driver stops sending
  before it settles). The default is the most fairness of its kind
  (weak, per action), so a reported lasso is fair under any weaker
  assumption of that kind: no lasso is a false alarm for want of an
  `unfair` or `stops` declaration. It is not the most fairness there is:
  per-call weak fairness and strong fairness oblige more, and a lasso is
  a false alarm for a system whose real fairness is of those (the
  hasLeader lasso that per-call fairness rules out, below); `live` is so
  only for a system at least this fair (review 11, O2). It errs the
  other way too: an input left fair
  is obliged, and a processor stuck while inputs remain enabled is
  hidden behind them ("fairness rules out N hot cycles and M hot states
  stuttering (the nearest
  leaves X enabled throughout, never taken)" says so, naming X: declare
  it if it is an input or a fault). The alternatives considered:
  - no fairness: every hot state stutters forever, every eventual val
    is hot (TestCheckerGraphEventually's ablation: hot_stops' queue
    "stays hot" one submit from init);
  - fairness for the binding's declared processors only (`fair:`): a
    grist model knows its processors, but the core does not, and a
    hand-written model would declare everything to get any answer;
  - the run's assumption, inputs and faults stopping (a settle's): the
    right one for comparing with runs, but not knowable from a model
    whose actions do not say which are inputs. `stops:` states it where
    the binding knows.
  It comes out right for grist's generated models without a
  declaration: their state grows (the log, effects, calls and served
  records are lists that only grow), so their only cycles are stutters,
  and a hot state is a lasso only where no fair action can change the
  state; P7's obligations (`<processor>EventuallyHandled`,
  `<call>EventuallyServed`) have their enabling actions (the
  processor's, the dependency's served record) always enabled until they
  hold, weakly fair, so no lasso. The generator does not emit its
  commands as `unfair:` (P6's review, grist.md): tried on counters,
  library and wallet-grist, it changes no verdict, and it would not show
  a stuck processor either. With counters' `average` disabled outright
  (a processor that never runs) and its commands unfair, 250 000 states
  to depth 9 hold no lasso: the processors' effect tries, the sites'
  records and the dependencies' served and replayed records are
  any-number actions, enabled in every state where work is pending,
  which grow the state and satisfy fairness per action without progress.
  Declaring those unfair too makes false lassos (an at-least-once
  processor's handled needs an effect try, a call's served the
  dependency's record). A stuck processor is the runs' to show: a settle
  with its obligation false.
- **Classified as simulate classifies.** An eventual val may be in
  `simulate.holds` (the model drives it: a lasso fails, exit 1) or
  `simulate.allows` (the code's to reach: the lasso is reported, the
  model allows it; a model of observations, quorumkv's probe, always
  has one). Unclassified, a lasso is reported `hot`, not failed: unlike
  an invariant's violation, which a sampled trace shows and the model
  may forbid, a model of recorded observations (most of the
  repository's run models) cannot drive the vals its runs are judged
  by, and failing them all would make -graph's verdicts noise.
- **Truncated graphs.** A lasso found in a truncated graph is a real
  one (its states and edges are the model's, the cycle's states
  expanded); "no fair cycle" is `live`, a proof for the instance under
  per-action weak fairness, only
  in a complete graph, else `bounded`. A state the evaluator cannot
  evaluate the val in is in no cycle: a lasso found is real, none found
  is `unchecked` (exit 2, as for an invariant).
- **Replay.** The prefix replays as P2's counterexamples do; the cycle
  is printed with each step's state and the repeated state named, and
  `-out`'s module has a run of the prefix and one turn of the cycle,
  `.expect(not(val))` after each of its steps (P6's review: the val
  was once checked at the turn's end alone) and ending in
  `.expect(and { not(val), <the state's variables, as at the
  cycle's start> })`: quint checks that the cycle is a path of the model
  that returns to its state with the val false, not that it repeats
  forever or is fair, which no quint run can (said with the replay).
- **In runs** (validate/hot.go, `Checker.MarkHot`): a trace that
  recorded no settle, replayed by the Go evaluator, whose latest
  entered state (not stuttered in: a run of reads is no cycle) repeats
  an earlier one with an eventual val false at every state between, is
  reported on its result (`Result.Hot`, the report's `hot cycle
  (evidence, no verdict: the run did not settle)` line): the vals, the
  steps of the cycle, and the run's visits to the state with them false.
  Evidence only: a run cut short has work pending by right, and one that
  went on might have left the cycle; no verdict changes. The cycle is
  the model's states', not the system's: progress the model abstracts
  away (a counter that wraps, a retry count it does not keep) repeats a
  state too, and nothing checks the cycle's fairness (an action enabled
  throughout that a longer run would have taken), so the line says what
  repeated, not that it would forever; examples/requeue's storm is one
  that would, the lasso `-graph` finds in its model. qtest marks a
  run whose Settle failed (`Run.Unsettled`), on a copy of its report
  (verdicts are shared by runs of the same steps); `gotogether
  validate` every trace without a settle. A binding with hidden actions
  or a resume, and a Linearize search's orders, get none (their states
  are not one replay's).
- **Soundness, against brute force and ablated.**
  `TestHotComponentsAgainstBruteForce`: 10 000 random graphs of up to five
  states, under random fairness, all weakly fair and none: every set of
  edges between hot states that is strongly connected is the support of
  a closed walk, fair by the definition above; the states on a fair walk
  are exactly those of the fair components, each component is a
  component, its Missed exactly what its walks cannot take, each lasso
  a closed walk inside it from its nearest state, fair. Ablated, it
  fails: fairness ignored (an unfair lasso), the SCCs computed over the
  whole graph and kept only when all hot (cycles missed), a stutter
  counted as enabling its family (a fair walk missed), and (P6's review)
  a stutter counted as taking it, which survived the 3000 graphs it had:
  one stutter edge in four now, and ten thousand graphs (the first
  failure is the 3964th), with `TestHotComponentsStutters` by hand. Each
  lasso's Obliged, the families enabled throughout the cycle itself (it
  was once the component's, fewer when the cycle leaves out a state), is
  checked against the walk.
  `TestHotComponentsFairness` and `TestCheckerGraphEventually` (hot.qnt:
  a client that submits forever, a worker, a read, a retry; with STORM a
  retry blocks processing) by hand: all fair, the queue stays busy under
  submissions; submissions stopping, it drains, the retry loops unfair
  (process enabled throughout), and the complete graph proves it; the
  storm a fair cycle of retries (process not enabled throughout it);
  retries stopping too, a stutter where only they were enabled; a depth
  bound short of the graph still finds the lasso. `TestSimulateGraphEventually`
  replays the storm's lasso in quint; `TestMarkHot` and
  `TestUnsettledRunsHotCycle` the runs' side.
- **Ends with the val false.** A state the step cannot leave (a
  deadlock, or an end `simulate.terminal` declares) with an eventual val
  false is a lasso, a stutter there (`HotLasso.Ends`, "the step cannot
  leave that state"): a run that ends there settles with the val false,
  which its check fails, so the model's graph fails it too when the val
  is in `simulate.holds`. A terminal end means the model stops, not that
  its obligations are met; a model whose end leaves one unmet either
  allows it (the val in `allows`) or has a bug (validate/testdata's
  hot_ends). Quiescent states (P2: only stutters leave them) are the
  same where no fair action is enabled.
- **The smoke run** (the repository's 23 bindings with eventual vals,
  `-states 20000`; grist's two generator fixtures do not typecheck
  alone): quorumkv's converged is hot, the only lasso under the
  default: a probe may observe any value forever, a cycle of writes and
  observations. raftkv's converged is the same model's, hot once its
  clients' writes, cas and transfers stop (`-stops`), and is bounded
  under the default only because transfer, enabled throughout, is
  obliged. Both are the code's to reach, and their bindings now say
  `simulate.allows: [converged]`. Everything else is bounded (graphs
  truncated, states that grow): the grist examples (counters' four
  bindings, library, wallet-grist's two) with no hot cycle at all;
  payd's three vals, webjobs' two and raftkv cluster's hasLeader with
  hot cycles that fairness rules out by an action left fair, enabled
  throughout and untaken (payd's request, an input; webjobs' created;
  the cluster's isolate, a fault); with the inputs and faults declared
  stopping, by the next progress action (payd's response, the
  cluster's campaign), or none at all (webjobs). The testdata bindings
  are bounded, or live in a complete graph (qtest's journey and probed).
  No finding was a model's liveness bug, and none asked for a
  different default. wallet-grist's `ledgerAcksUnposted` mutant is a
  run-only bug: its runs settle, the ledger having answered posts it
  never made, and the model cannot stay hot on
  ledgerPostEventuallyServed, since ledgerPostServed is enabled, weakly
  fair, for every answered call without a served record until it has
  one. With every action unfair (the ablation, on a real model) the
  search reports postEventuallyHandled hot, a stutter one deposit from
  init.
  hasLeader shows the cost of fairness per action: `lead` of an old term
  satisfies lead's fairness, so the model may lead old terms forever
  (past this bound) with no leader of the newest; fairness per call
  would rule that out, and oblige every client's every input with it.
  hasLeader is the code's to reach (raft's election), judged `within`
  its bound in runs.
- **Cost** (P6's review, measured at `-states 20000`): the liveness
  search, the vals evaluated in every expanded state and
  HotComponents, is 14 to 18 ms of a graph's 2 to 18 s (counters' six
  vals 18 ms of 8.1 s, wallet-grist's three 16 ms of 17.6 s, quorumkv's
  lasso 14 ms of 2.3 s): Explore is all of it.
- **examples/requeue** is the storm as an application: a controller
  that applies a cloud resource's spec and requeues until the cloud,
  which provisions in two steps and starts over on a changed spec,
  reports it ready. Its model's `RESEND` (an apply after the first
  replaces the resource) has the lasso, an advance to warming and a
  replacement back to pending, advance taken and its step to ready
  never enabled throughout; the faithful model is live, the requeue a
  stutter. The `stampSpec` mutant (a timestamp in the spec) is rejected
  by the faithful model and, against RESEND's, conforms, never settles,
  and has that cycle as its `hot cycle` line.

## 63. Formats and their versions (I13, I14)

Five things cross between pieces installed apart, each at its own
version: files a run writes and a later gotogether reads (step logs,
request logs, corpus entries, manifests, evidence), files a person
writes and every later gotogether reads (bindings, conversion rules),
the navigation protocol between a test binary and a navigator, the
woven ABI between qweave's output and the core it is compiled against,
and the core itself, pinned by every published module. The eleventh
review found the step log, the binding, the nav protocol and the ABI
unversioned, and seven core pins across the published modules.

**The policy** (decided by the maintainer on 2026-10-28: "this is a
prerelease library; once our own examples are migrated we can drop
support for earlier data formats"; one table, `internal/formats`, so
adjusting it is a field per format):
- Every format is named `gotogether.<thing>/<major>`. A breaking change
  (a field renamed, a meaning changed) bumps the major; an added
  optional field does not.
- **Pre-release, a reader reads the current major only.** A later one is
  refused with the upgrade (`Format.Check`: "gotogether.steps/2 is a
  later version than this gotogether reads (gotogether.steps/1): upgrade
  gotogether ..."), an earlier one with how to write it anew (each
  format's `Older`: record the run again, rewrite the binding, save the
  corpus entry again), another format's name with what it wanted. A
  major's change migrates the repository's own files in the same change
  (raftkv's corpus entries, every example's binding).
- **After v0.1, the maintainer decides** whether data nobody can write
  again (step logs, request logs, corpus entries, bindings and rule
  files) is read from an older major on (ROADMAP's "For the
  maintainer").
- The step log's attributes keep the `quint.` namespace: they name model
  steps. Its format key is `quint.format` too.

| format | where | reads |
|---|---|---|
| `gotogether.steps/1` | step logs (a format record), OTel steps (span or resource attribute) | the current only; none named: the current |
| `gotogether.requests/1` | qdriver's request log header | the current only |
| `gotogether.binding/2` | a binding's optional `format:` | the current only; unnamed protocols are refused |
| `gotogether.convert/1` | a rule file's optional `format:` | the current only; unnamed protocols are refused |
| `gotogether.corpus/1` | qtest's corpus entries | the current only |
| `gotogether.choices/1` | qenv.EncodeChoices' header (`BytesVersion`) | the current only |
| `gotogether.run/3` | validate's run manifest | the current only |
| `gotogether.evidence/2` | qtest's evidence | the current only |
| `gotogether.model/1` | a compiled model (gotogether compile, §64) | the current only |
| `gotogether.report/1` | validate's and simulate's `-json` (validate.Document, §68) | the current only |
| `gotogether.nav/2` | navproto's Session and Node's `protocol` | the current only; unnamed protocols are refused |

**None named is the current.** A binding with no format key, a log with
no format record, a session naming no protocol is read as this version's
major: the files a person writes need no key to be current, and none of
the repository's carries one but init's template. A file of an earlier
major that names none fails on what changed: a key that major had is
unknown here, with the hint that it may be another version's.

**The step log's record.** A line holding `quint.format` alone, which
WriteJSONL and JSONLSink write first, and any reader skips (several, for
logs concatenated with `cat`, each checked). JSONLSink writes it with the
first step, in one write: a log is never a record alone, an empty log
stays empty, and a disk sees one write per step, so qenv.Disk's
schedules (a write is a fault point) are those of before. A per-line
key would cost 35 bytes a step for nothing a record does not say. OTel
steps carry the attribute on the step's span (OTelSink), where
FromAttributes looks as it does for the other non-argument keys, so a
deployment may set it on the resource instead. A log whose format
record is refused stops the read there, never mistaken for a torn tail.
An old gotogether reading a new log fails on its first line ("missing
quint.action"): nothing older promised to read later logs.

**Bindings and rule files** are decoded strictly (`KnownFields`), so a
key another version has is "unknown" here. The format key is read
first, loosely, and another major refused with its fix before the
strict decode names its keys; an unknown key in a file naming no other
major gets the hint that it may be another version's.

**The corpus entry** is `gotogether.corpus/1`, a name of its own (it was
the manifest's, `gotogether.run/1`, until I13; raftkv's two entries were
rewritten when the alias went).

**The navigation protocol.** A test binary is built against the user's
core, `gotogether nav` and `gotogether-nav` against theirs. The session's
first line and a one-shot node name `gotogether.nav/2`
(`navproto.Protocol`), and both navigators refuse another with
`navproto.CheckProtocol`'s fix: install the navigator at the module's
version, or rebuild the test binary at the navigator's.

**The woven ABI.** qweave is a module of its own (no core requirement),
so the code it writes (`Hit`, `RegisterBlocks`, `Cond`, `Pick`,
`MutantOn`, `RegisterMutants`) could be compiled against a core whose
`woven` means something else. `woven.ABI1` is a constant whose name is
the version: every file qweave instruments references it (`var _ =
quintwoven.ABI1`), so a skew fails to compile naming it, and before
weaving with -cover or -mutate qweave loads the module's `woven` and
refuses one without `ABI1`, naming the module's core and the fix. Any
change to woven's functions renames it (ABI2). Orchestrion's aspects
(qenv/weave's orchestrion.yml) ship with the core they call, so they
cannot skew. `qweave -version` prints the ABI its code calls.

**One core version (I14).** Every published module requires the core at
one commit, and a published module it requires (qgosim's qweave) at the
same one: a user's `go get` resolves the newest of their pins, and a
combination of pins no job built is what a user got. `ci/bump-core.sh
<pushed commit>` moves them all (GOWORK=off go get, then tidy), and
internal/ciconfig's `TestCorePinsAgree` fails while they differ, naming
each pin; its list of modules is the script's, checked against every
go.mod without a replace. No module may lag: a module the newer core
breaks is fixed with the bump, since its users get that core anyway. CI's
`consumer` job builds ci/consumer, an order-sync service on simgrpc,
simlog, kafkalog and qhegel, copied outside the repository, with no
replace and no workspace: first with the satellites at the pushed commit
and the core at their pins' maximum, then with the core at the commit.

**`gotogether doctor`** reports what is installed: the CLI's version and
its core's woven ABI, the module's core and published modules (`go list
-m all`), qweave's version and the ABI its code calls (`qweave
-version`), gotogether-nav's and any named binary's core (their build
info), quint, the evaluator its version expects (read from its package)
and whether it is installed, Node, the formats this version reads, and
the `GOTOGETHER_*` and `QUINT_*` variables in effect. Each skew is a line
with its fix; `-strict` exits 1 on one. The rest of I21's doctor (the D3
cache, bindings' formats in the module) is not built.

## 64. Packaging, and a model compiled for the evaluator alone (I21)

An adopter's CI copied `ci/install-quint.sh`, pinned quint by hand and
had no word on what to run when (review 11's adopter, AD3). I21 packages
what this repository's CI does:
- **The Action** (`.github/actions/gotogether`, composite): Go, Node,
  quint and its Rust evaluator at pinned versions (install-quint.sh, the
  evaluator from its release asset, not GitHub's rate-limited API), the
  CLI at a version (a tag or commit, or `local`: built from the checkout
  at hand, as this repository's own job does), quint's install and the
  cache of typechecks and compiles (`GOTOGETHER_CACHE`, §57) kept by
  actions/cache, `gotogether doctor`, then the command given, its output
  and result line in the job summary. Composite, not a reusable
  workflow: it runs in the adopter's job, after their checkout and their
  build of the binary under test. With `compiled:` it installs neither
  Node nor quint.
- **The image** (`ci/image/Dockerfile`): Go, Node, quint and the
  evaluator pinned by build arguments, the CLI built from the context;
  CI builds it and runs `gotogether doctor -strict` inside. No registry:
  where it is published is the maintainer's call.
- **The tiering** (docs/ci.md): per push, per night, per release, with
  this repository's costs (ci/test-times.txt) and what each tier catches.

**The compiled model.** The Go evaluator decides the runs that pass
(§56); quint judged the rest, so every validating pipeline needed Node
and quint, though most runs pass. A validate-only pipeline (a canary, a
CronJob, an incident laptop) needs neither if what quint contributes is
done once, ahead: `gotogether compile -binding b.yaml -o model.json`
writes quint's typecheck of the model (what the binding's plan and lint
read) and quint's JSON IR of the binding's evaluation module (what the
evaluator reads), and `validate -compiled model.json` judges with the
evaluator alone. What that may claim:
- **The evaluator judges, and says so.** A pass is its pass and a
  failure its failure, written in quint's terms (the call rejected, a
  step disabled or its expect false, each invariant's first failing
  state, an eventual val false at the settle, a bounded response judged
  on its states), the model's state the evaluator's. Each result is
  `JudgedBy` "evaluator" and says "judged by the Go evaluator alone";
  the report has an info issue naming the artifact and its quint. No
  pass is sampled by quint (`GOTOGETHER_EVAL_SAMPLE` has nothing to ask),
  which is the trust the §56 sample buys elsewhere: the evaluator's
  agreement with quint is `TestAgreesWithQuint`'s and the nightly
  shadow's, on every model of the repository, not this run's.
- **What it cannot decide is no verdict, "needs quint"**, never a
  guess: every `qeval.Unsupported` (a fold whose result depends on
  quint's order of a set, review 11's F1; a fold over more than ten
  elements of an operator not known to commute; a choice inside a bound
  action, F2; an argument that is no literal), a binding with hidden
  actions or a resume (searched with quint confirming).
  Searches (`-linearize`, `-resolve-pending`), ITF inputs and
  `-manifest` (which `-replay` judges again with quint) are refused up
  front. The checker's quint is a binary that does not exist, so a path
  that would still ask quint is a tool error, not a verdict; so is an
  evaluator that fails a run while every check holds on its states.
- **A stale artifact is refused**, naming what changed and
  `gotogether compile` as the fix: another binding than the one it was
  compiled from (its sha256), a source of the model present here whose
  sha256 is not the artifact's (the main file and its transitive
  imports, relative to the binding), an evaluation module this
  gotogether writes otherwise for the binding (an older toolkit's vals
  or expects: the module is written again and compared, text for text),
  a later format (`gotogether.model/1`, the current only: a breaking
  change makes it another check), a quint older than 0.32. A source
  that is absent is not checked: a canary's image carries the binding
  and the artifact, not the model; the report warns that it took the
  artifact's hash for it.

Why not ship the IR alone: the plan reads the model's types (the
typecheck), and an artifact without the binding's hash and the
evaluation module's text could be judged against a binding it was not
compiled for, which is a wrong answer that looks like one. Why not
gzip: an artifact is 100-200 KB of JSON for the repository's models,
and readable JSON is diffable in review.


**Portable compiled judgments.** The single current `gotogether.run/3`
manifest records `model.compiled` as a hashed executable dependency. Its presence
selects evaluator-only replay; absence retains source/Quint replay. Runtime
Quint/backend fields stay empty for compiled judgments, while compilation
provenance remains inside the artifact. Creation, verification, export and replay
perform no Quint or Node version probes or fallback. Unsupported operations keep
their native error/no-verdict behavior.

The actual loaded artifact digest and source-presence inventory travel privately
with compiled results. Manifest creation rejects a different artifact or changed
coverage instead of attaching an old verdict to freshly loaded data. Source hashes
come from the artifact; the manifest records the subset present when judged.
High-level input readers also retain fingerprints of the exact bytes parsed,
including converted and schema-typed input paths. Judged.Manifest refuses changed
or uncaptured inputs; it never attaches new bytes to an old verdict. Low-level
RunRecord callers may supply expected RunInput.SHA256 fingerprints, or explicitly
own provenance capture when they leave that field empty.
This does not establish an atomic snapshot of all source-mode binding/spec or
conversion-rule files; anchoring their actual load identities remains a separate
follow-up. Artifact/input guards must not be described as compiler authenticity
or a proof of a globally atomic capture.
Previously present sources must remain present and identical; absent sources must
remain absent. Their provenance stays disclosed as unchecked, not silently
upgraded. Portable export preserves artifact bytes and all relative paths,
including virtual absent-source paths when choosing the common copy root. It
rejects inconsistent artifact-to-binding links, absolute references and file
imports in binding defs that are not in the recorded graph. Existing exclusive
staging/publication and input/artifact integrity checks remain in force.

A compiled literal constant is baked into IR: changing only Binding.Constants
cannot change its meaning. WithConstants therefore refuses such changes with
recompilation guidance, while runtime parameter slots remain supported.
WithInvariants can select compiled invariant wrappers; an unavailable wrapper is
refused. A compiled ambiguous ITF input is refused before type inference, which
would otherwise probe Quint. These bounds preserve native receipt/report facts
rather than expanding the evaluator or claiming a finished/reconciled run.

## 65. Verdicts state their bounds (I9)

Review 11's formal-methods reviewer found claims that read as proofs
and were bounded searches, or that held of nothing: `All(...)` over zero
runs passed (`(kind == "some") == (n > 0)` is true for "all" at 0), an
exhaustive search printed "search of 2 delays" where its evidence said
"complete", the summary's "all conform (quint confirmed 2)" left unsaid
that the Go evaluator alone passed the other 28, and "none of 1000
sampled traces violates it" said nothing of what 1000 traces bound. The
rule since: a claim has three values (it holds, it fails, it has no
verdict, which fails the test too), and every pass says what it covers
and what bound limits it, in its text and in its machine-readable form.

- **No runs, no verdict.** `quantified.judge` holds of no claim over no
  runs, and `Exploration.verdict` says so for every claim (runs claims
  included: `InAtLeast(p)` of zero runs needed zero, and held). An
  exploration of no runs comes from `Seeds` below 1 or a filter that
  left none; `qtest.Runs()` with none was refused already. `TakesEach()`
  of no takes panics, as a catalog's `Exercised` excepting every block
  did: it would hold of any runs.
- **Conforms and Settles are three-valued.** `Conforms()` of a run whose
  report has no verdict (`Report.Verdict` NotEvaluated: a search's
  budget, a lost tail, dropped steps) is unknown, not "does not
  conform": `All(Conforms())` fails of it as unjudged, with the fix
  (raise the budget, keep the tail). `Settles()` of a run that recorded
  no settle and lost its tail (a recorder's lost suffix, or watermarks
  that reach no settle) is unknown: the settle may be out of sight, as
  `Report.Finished` already said. Settles is judged whatever the run's
  conformance (`undecided`), so `None(Settles())` over a run with no
  conformance verdict but no settle holds.
- **Who judged the passes.** A failing verdict is always quint's (§56);
  a pass the evaluator's unless quint sampled it. The summary says
  "quint confirmed 2, the Go evaluator alone the other 28" (by run: a
  run is the evaluator's if one of its actors is), or "the Go
  evaluator's, none confirmed by quint"; `Evidence.Judges` and a
  manifest actor's `judgedBy` carry it. The sample is at least 1 in 5
  (`raisedSample`, unless sampling is off) for a plan whose actions reach
  a set fold of no commuting shape, or an operator in
  `qeval.Unshadowed` (new to the evaluator since the last shadow-clean
  nightly): `qeval.Model.Unconfirmed`, a static walk of the action's
  definitions, memoized per model. Static, not the evaluation's own
  record of what it touched: the evaluator is on I19's hot path, and a
  plan's actions are known before it runs. After I2 the fold is sound
  (every order up to 10 elements, else Unsupported); the raise is for
  the class of plans an evaluator bug would be likeliest in, as the
  review asked, not a fix of one. CI's 1/5 is unchanged by it.
- **A sample's bound.** A seeded exploration's universal claim (All,
  None, Conforms) that passed is a sample: none of N independent runs
  showed the failure, which bounds its rate below 1 - 0.05^(1/N) at 95%
  confidence (about 3/N, the rule of three). The summary says "a
  sample, no proof: a failure 1 run in 10 shows is missed at most 5% of
  the time, a rarer one more often, one the runs cannot reach always";
  `Evidence.Sample` has the rate. Only after a universal claim: a claim
  some run shows has its witness. Not stated of a search, a run alone or
  runs given one by one (not a sample of anything). What it does not
  bound is said in words, not numbers: a fault never on, a workload that
  never asks, a run too short. `simulate`'s sampled holds says the same
  of its traces; a possible quint did not reach, of states.
- **A share's margin.** A share claim (`Reaches(...).InAtLeast(p)`,
  `Takes(...).InAtLeast(p)`) that held logs, when above one time in a
  thousand, how likely as many seeds are to fall short of it at the rate
  measured (the binomial tail, `confidence.Short`): the nightly's
  per-claim confidence, beside Some's `(1-p̂)^n` (power, I31).
- **Exhaust's bound.** "complete within 2 delays and 1 fault: every run
  within that bound, none beyond it", or "truncated within ...: at least
  7 runs within it left untried"; Evidence's String and the evidence
  command's line say the bound too.
- **PCT's guarantee, on its conditions.** The paper's bound
  1/(n·k^(d-1)) holds for k at most Steps (change points are drawn in
  1..Steps, so a longer run's tail has none) and n counting every task
  the run ever created (qenv creates them as it goes). `PCT.Seen`
  reports both from the run; the summary prints the bound with the most
  tasks a run ranked, or that it is void when a run took more than Steps
  scheduling steps, with the Steps to set. qenv's doc said "about 1/n",
  which is depth 1's.
- **Unchanged, checked.** `-graph`'s `bounded` already said "which
  proves nothing past them", and a complete graph's `holds` is a proof
  for the instance, cross-checked by quint's sampling (I3); a truncated
  graph still exits 0, as a sampled `holds` does: both state their
  bound, and a run-demo relies on the exit. Linearize's budget was
  already no verdict (I5's second search too); a "not linearizable"
  verdict is a complete search with a Proof.

## 66. Validation at volume: a serial log by entity (I19)

**The cost was the model's states, not the evaluator.** A synthetic day
of payments (examples/migrate-payments, each charge authorized, captured
and refunded) took 0.6 s at 102 steps, 63 s at 1,002 and over ten
minutes at 10,002, compile cached. The profile at 1,002 steps: 98% of
the time in the walk's check of the invariants after each step
(validate/eval.go), none in the replay of the steps. Each state's
invariants are judged over every charge so far, so a log judged whole
costs the sum of its states' sizes, quadratic in the log for an
invariant that judges each charge, and cubic for payments'
`oneChargePerKey`, which compared every pair of charges. No evaluator
can judge "every pair of charges, after every step" of a day in linear
time; what is linear is judging each charge's history alone, when the
model says the charges do not interact. The evaluator's own per-step
costs were linear in the state too (each successor's key, the whole
state written as a string to deduplicate a successor that is alone:
gone), and so was splitting a log by key (a scan of the parts for a
name's collision: a map).

**`partition:` in the binding** names the argument that says whose each
step is. A trace is split by its value (the split LinearizeByKey's
search uses, `splitTrace`), each part an actor of its own (`payments
id=ch_3`), planned in the whole log's instance, judged by the batch
(`CheckBatch`: the evaluator's passes in parallel, the failures' quint
replays in shared processes), and reported each: the passes folded into
one line, every failing entity in full with its module and its `nav`
line (Nav splits the run the same way). That is the continuation after
a failure: a day's report lists every entity that disagrees, where a
log judged whole stops at its first failure, its later states not the
run's. Within one entity a failure still ends that entity's check.
Under `validate -compiled` (§64) the parts are judged by the compiled
model's evaluator alone, the factoring checked by the same footprint.

**The factoring is checked, never assumed.** The partition's verdicts
are the whole log's only if each entity's steps neither read nor change
another's state. LinearizeByKey assumes it, in its doc comment; a
binding's partition is judged on every production log, so a model
that does not factor would give a pass the whole log would not. Each
part is replayed first by the Go evaluator with a footprint
(qeval.Footprint, `Model.Tracking`): every map variable's value is
tainted with its name as it is read, and the builtins that reach an
entry record it (get, contains of the map's keys, put, set, setBy, the
maps they make tainted in turn); a tainted map passed whole to any other
builtin (keys, size, eq, filter, fold), a scalar variable read, or a
map variable assigned something not made from it entry by entry, is a
whole access. Then the run is refused, an error naming the step, the
entity and the access, when

- a step, an expect or a judged val made a whole access;
- two entities' steps wrote one entry;
- a step accessed an entry another entity's step wrote before it in the
  log's order (it would have read that entity's value);
- a judged val read an entry another entity's step writes at all (the
  whole log judges its vals after every step, that one's too).

Otherwise, by induction over the log's steps, every step reads the same
entries with the same values in the whole log as in its entity's trace,
so it takes the same branch and writes the same entries; the whole
log's state is the union of the entities' at every step. A judged val
(an invariant, an eventual val) may take a forall over a state map's
keys at a conjunctive position (the val, an `and` of such, a val or def
it names) as each entry's judgment: such a val holds of the union
exactly when it holds of each entity's state. The same forall anywhere
else (under a not, an or, nested in another's body, cached and reused
outside a conjunction) is a whole access. The footprint walk judges
every invariant in every state and every eventual val past the first
false one, as quint's detail replay does, so it sees what quint will
evaluate. `TestPartitionAgreesWithWhole` checks the claim against the
whole log on random logs small enough to judge whole: where the
partition is accepted, it passes exactly when the whole passes, and the
whole's first failure is a failing entity's; transfers, which join two
accounts' histories, are refused when they do (with the conflict check
ablated, a transfer's run passes whole and fails partitioned).

The check is of the run, not of the model: a path no step of this log
took is not checked, and needs not be, since the verdict is this log's.
Its limits are conservative, never unsound: a map the model's init (or
a snapshot) fills is read by every part's forall, so a log whose
entities write such entries is refused; and a bug that crosses entities
(one idempotency key making two charges) is a refusal, not a verdict,
which judging that log whole then gives. A partition does not combine
with hidden actions, a resume, a restart (each a step of no entity's, or
of every one's) or a search (a client history is partitioned by
`-linearize-key`).

**Payments restated to factor.** R2's lookup of a key's charge was a
scan of every charge (`charges.keys().filter(c => charges.get(c).key ==
key)`), a whole access. The model now keeps the index the code keeps
(charge.go's `byKey: str -> str`), and `oneChargePerKey` says each
keyed charge is its key's and each key's charge has it: two foralls of
one entry each, in place of a pair over every charge. The example's
suite, its mutants and its README's verdicts are unchanged.

**The instance.** The parts share the whole log's instance (a constant
an action reads, LIMIT from the log's largest amount, keeps the whole
log's value), but a constant no evaluation of any part read (a day's
IDS, every charge) takes the first part's value: every failing part's
quint replay typechecked a set of thousands (two failing parts of a
10,000-step log: 7 minutes; now 4 s). Its report says which constants
were read by none. Quint's audit of a sample of the evaluator's passes
(GOTOGETHER_EVAL_SAMPLE, 1 in 50) takes at most one quint process's
worth of a partitioned trace's passes (64): 1 in 50 of 33,000 charges
took quint 37 s.

**What it costs** (examples/migrate-payments' synthetic day, the CLI,
compile cached, a 4-core machine shared with other agents):

| steps | before (whole) | whole, the model restated | partitioned | of it the Go side |
|---|---|---|---|---|
| 10² | 0.6 s | 0.5 s | 0.4 s | |
| 10³ | 63 s | 4.4 s | 2.9 s | |
| 10⁴ | over 10 min (killed) | over 20 min (killed) | 4.9 s | 1.1 s |
| 10⁵ | (cubic: days) | (quadratic: hours) | 13.5 s | 10.4 s |

The "of it" column is with quint's sample off (GOTOGETHER_EVAL_SAMPLE=0);
the rest is quint's one audit process. validate's `TestPartitionAtVolume`
judges 10⁴ steps under a 30 s budget on every push (about 2 s) and 10⁵
in the nightly (GOTOGETHER_SCALE_STEPS=100000, about 5 s).

**Interacting histories.** Transfers cannot be partitioned into independent
account histories. The test-only [interacting baseline](validation-volume.md)
measures transitions, tracking, full invariants and warm whole validation
separately. Its written-entry cache is compared with full evaluation on bounded
prefixes; destination-only and cross-entry counterexamples rule out using an
action's source or write set alone as an invalidation proof. Existing footprint
judgments do not provide such a proof. Production semantics remain unchanged;
the profiles guide allocation changes before a new incremental API or dependency
analysis.

The first allocation follow-up shrinks immutable lexical binding frames by
storing deferred errors in separate cells created only on failure. It preserves
closure ownership and lazy-error semantics, with full checks still performed.
The measured 1,000-account invariant uses 21% fewer bytes and whole validation
about 6% fewer; failed lazy definitions gain one error-cell allocation. The
success/failure benchmark checks that successful lets do not pay that extra
allocation. See the [storage measurements](validation-volume.md#binding-frame-storage).

The wider ring follow-up shares immutable map keys on existing-key updates while
copying values; insertions copy key and value arrays before changing them. Independent
copying-reference and retained-prefix controls protect snapshot ownership,
including spare capacity. The ring fixture checks bounds and global conservation;
the whole-validator benchmark retains its existing nonNegative predicate.
Measured transfer bytes fall about 49% and whole-validator bytes about 12% in the
documented cases, with no throughput claim. State-size scaling remains, so the
next experiment broadens product coverage through I20's incident workflow before
any incremental-invariant API. See [the map storage follow-up](validation-volume.md#wider-histories-and-map-storage).

## 67. Explicit quantifiers, one place for claims (I16)

§18 made an expectation alone a claim about some run, with `Conforms`
about every run by a flag; `Settles`, `Completes`, `Satisfies` (and so
gristtest's `NoClientErrors`) took the flag later, so a bare claim's
quantifier was its constructor's: `.Expect(t, Scenario(...))` meant some
run, `.Expect(t, NoClientErrors())` every run. A reader had to know each.
Review 11 asked for it decided before tags; it is, as I16 put it:

- **Over runs, a claim says its quantifier.** `Conforms`, `Settles`,
  `Completes` and `Finishes` alone are `All` of themselves: their names
  say every run (an `alone` flag, apart from `every`, which still marks
  what a passing run meets for ShrinkActions' Keep). Any other
  expectation alone panics, naming `Some(...)`, `All(...)` and
  `None(...)` and the quantifier it meant alone, so a migration is
  mechanical. A panic, not a compile error, because the four are
  Expectations too, and one run's Expect (Validate's, Record's, a
  Violation's, a shrink's) takes any expectation bare. Every claim goes
  through one function (`quantify`) at `Exploration.Expect` and at
  `PerMutant`, before anything runs or is judged, so the verdict code
  sees quantified claims only and every failure prints its quantifier.
- **Every report's claims are its Expect's.** `Mutate` judged claims
  given to it, the one report that did. `Mutate(t, cfg).Expect(t,
  PerMutant(claims...), NoSurvivors(...))` keeps each survivor's
  exploration so Expect can kill the ones that miss a claim (judged by
  `verdict`, as the baseline is); Mutate itself kills only what any
  exploration would (a broken run, no steps, a rejection). The six
  Expects and their claim types are listed in one place, qtest/claims.go,
  where the types live: Claim (explorations), RunClaim (a run, a
  Violation), MutationClaim, StepClaim, ShrinkClaim. qhegel's and
  gristtest's claims are qtest's types; qload's recovery claims are of
  a chain, not runs, and stay there.
- **One negation.** `qtest.Not(e)`: met where e is missed, unknown where
  e is. grist §0.16 had refused a general negation, since most
  expectations' have no one meaning; that case, `Not(Conforms())`,
  panics naming the three failures. gristtest's two by-hand negations,
  `ClientErrors` and `WritersFirst`, are `Not(NoClientErrors())` and
  `Not(ReadersFirst())`, with the same verdicts on every report (a
  report with no client actor has none).

## 68. Reports for on-call (I20)

**The report served the model's author.** The adopter's flipped step
(eleventh review, B5) read "step 16 is not a transition", then "(seq 7,
steps/x.jsonl:19)": three indices for one record, none with the time,
the pod or the trace on-call looks up next; "enabled instead" listed
arguments drawn from the instance's constants (`createCounter(counter =
"a", name = "a", id = "i1", ...)`) that read as records; the model's
state was dumped and cut at 240 characters, mid-value, past the field
that differed; every invariant after the failure had a line saying "not
checked"; and nothing was machine-readable.

**One index: where the record was read.** A report names a failing
record by `validate.RecordID`: its `Source` (a step log's `file:line`
as package input writes it, `line N`, a span's id), else
`<recorder>#<seq>`, which every reader keeps. The step's place in the
replay and its recorder's seq stay in the data (`DocRecord.Step`,
`Seq`) and in `-v` (`VerboseSummary`): they are what `nav -at` and the
manifest's failing step use, and a person reading the report greps the
line. A step with no record (an inferred restart) is still "step N".
The failure's `record:` line adds the step's wall time (UTC), its
process, its recorder when that says more (grist's
`<app>/<node>/<incarnation>`, read as the node and its start), and the
trace of a span's step: otelio and convert write `Source` as `span <id>
trace <id>` (`qtrace.SpanSource`). A store-side grist step is the
relay's (`grist/<store>/<tenant>`): the store does not record which
node committed, so it has no pod to show (G8's location would carry it).

**A diff, not a dump.** For a mismatch or a violation the Go evaluator
replays the plan to the step and diffs the state before it against the
successor equal to quint's state after it (`Failure.Changes`): by path
in the model's names, into records, maps' keys, sets' elements, lists of
one length and variants of one label (`charges["ch_3"].refunded: 700 ->
1200`). A successor quint's state is not, or an evaluator that cannot
replay, gives no diff, and the dump stays (cut at an element's end
now). For a rejected step the evaluator looks among the model's moves
for the same request: the rejected action's transitions whose
parameters the binding does not fill from the outcome, observations or
error all equal the step's. That is the model's answer to what the code
answered otherwise (`the model expects: void(id = "ch_2", result =
"ok"), which changes: charges["ch_2"].status: "authorized" ->
"voided"`), `Explanation.Expected` and `Changes`. With a diff the state
is `-v`'s.

**The model's moves are labelled.** "enabled instead (the model's moves
from this state, not records)"; an action enabled many ways is shown by
its count, and `-v` adds the example whose arguments agree most with
the rejected step's (the request's parameters weighing twice the
response's), so it reads the record's values where the model allows
them. The invariants a non-conforming run leaves unchecked are one line.

**A document for machines.** `validate -json` and `simulate -json`
print `validate.Document`, `gotogether.report/1` in internal/formats'
table: the verdict, exit status, reason and result line; inputs;
validate's actors (each failure's record, `brief` line, `why`,
`changes` and whole `state`, the text as printed), the passing
partitions folded as the text folds them (§66); simulate's checks.
`-report junit=FILE` and `-report sarif=FILE` render it for CI's test
and code-scanning views (SARIF's results at a record's file and line);
`GOTOGETHER_REPORT_DIR` makes every validate and simulate write its JSON
there, which the Action's job summary and annotations read instead of
grepping the RESULT line. simulate's checks are read from the lines it
prints (each "  <verdict> <name>: <detail>"), so its text and its
document cannot disagree; a new kind of line must keep that shape. Its
JSON Schema is generated from validate.Document as the binding's is from
binding.Binding (schemas/gotogether.report-1.json, `schemas.Report`,
I36's generator reading json tags), and a filled document is checked
against it.


**Incident rehearsal and captured rejection evidence.** The
[jobqueue runbook](../examples/jobqueue/INCIDENT.md) forces the existing
stale-ack implementation mutant through actual service calls. Faithful and
repaired controls use the same model and binding; the repaired execution also
completes a fresh lease. Native conformance identity survives JSON, exact-record
inspection and relocated replay. A header without observations has no verdict;
changed or missing replay data is an error. These cannot earn incident credit
from exit status alone.

The exercise found that the TUI exposed annotated obligation records but only
explained ordinary model rejections. Its failing-history adapter now confirms
the selected actor and disabled-call boundary with the full checker and retains
the captured `Failure.Step` reference. Inferred failures and missing identities
do not acquire records; checking and lookup errors stay explicit. Evidence is
labelled as the original collected history, separate from cursor diagnostics,
and remains fixed across hypothetical model moves. Opening and leaving the
record inspector does not move a cursor or change the history's verdict. The
incident's four captured records become five model calls because restart is
inferred, so record sequence and model position are deliberately distinct.
Real terminal tapes and native-JSON acceptance cover that distinction.

**Checked portable handoff.** `gotogether bundle -out DIR MANIFEST` exports
existing native evidence without executing or rejudging it. The library's
`Manifest.Bundle` first validates the recorded hashes and full model reference
graph, including imports that ordinary verification leaves for Quint to diagnose.
Unsupported absolute model references and missing/unhashed/changed files,
including artifacts, are refused rather than rewritten. Relative parent imports
remain supported when the copied model layout keeps the whole graph internal.
Source symlinks to regular files are materialized as ordinary copied bytes.

Both the library file operation and the CLI's complete manifest directory use
private sibling staging, verification of staged copies, and atomic exclusive
publication. Concurrent exporters cannot replace each other's evidence; failure
leaves the receiver and final destination unchanged. Linux, macOS and Windows
use their native no-replace rename operation; unsupported systems/filesystems
return an error. Atomic visibility does not claim crash durability. The CLI emits
the native manifest under `-json`; exit zero confirms export integrity even for a
recorded model failure or no verdict. Composition receipts remain portable
non-executable evidence, and packaging does not expand replay support.

The jobqueue rehearsal now validates in its original layout, exports all three
cases, removes source captures and replays after relocation. It no longer needs
manual model/binding copies. This closes the observed handoff gap without a new
report format or treating packaging as production capture completeness.

## 69. The model alone: one engine, claims, one bound (I17)

Review 11's architect (F4) found three engines answering "can the model
...?" with two semantics: `gotogether simulate` asked the Go evaluator
and sampled with quint what it did not find; `qtest.Possible` asked the
evaluator only and failed "not searched" where the CLI would sample; a
test of `-graph`'s verdicts walked `GraphResult` by hand (twophase's,
crossdb's, requeue's), the walk of a report the conventions forbid for
runs. And the binding's simulate section had three state bounds.

- **One engine.** `model.Simulate(binding, SimulateOptions)` (and
  `model.Analyzer.Simulate`, reusing a checker's compile) is what the command
  ran, moved whole from cmd/gotogether: quint's sampling, `Verify`
  (Apalache), `Graph` (the evaluator's state graph, its cross-check
  against quint's samples, deadlocks, actions never enabled, eventual
  vals as cycles), and the possibles (the evaluator's search, then
  quint's sampling of what it did not find, hosted in an invariant's
  run). Its `SimulateReport` carries every verdict as data beside the
  text the CLI prints unchanged (and I20's `-json` reads from it):
  `ModelVerdict` (held, violated, or neither: an error, a construct the
  evaluator refused) with a `ModelBound`, `PossibleVerdict` (found,
  impossible, or neither), the graph, `Code`.
- **Bounds stated (§65).** A `ModelBound` is one of: complete (every
  reachable state of the instance), depth (every state within D steps,
  the state bound never reached: every trace of up to D steps, a
  bounded proof), states (a breadth-first prefix the state bound cut,
  which proves nothing past it), stopped (the evaluator could not go
  on), verified (quint verify's), sampled (quint run's, with the rule
  of three). `Proves()` is true of the first, second and fifth.
- **Claims, three-valued.** `qtest.Model(t, binding).Expect(t, ...)`
  takes `ModelClaim`s (qtest/modelclaims.go, the claims table in
  qtest/claims.go), simulating once for all of them, graph only if one
  needs it. `Proves(inv)` holds where the invariant held within a
  proving bound; a violation fails it with the shortest counterexample;
  a truncated graph is no verdict, failing the test with the bound to
  raise. `Refutes(name)` wants a witness: an invariant's violation, or
  an eventual val's lasso, each replayed by `quint test` (Replay); a path
  quint fails is the evaluator's error, no verdict; a proving bound with
  none fails it (the model guarantees what the binding would check the
  code against). `.InAtMost(n)` bounds the witness's length. `Live(val)`
  holds only of a complete graph: a cycle may close past any bound, so a
  depth bound proves no liveness. `NoDeadlocks()` holds within a proving
  bound. `Possible(scenarios...)` is a scenario as a possible (its
  steps' actions, arguments, and outcomes the binding passes to a
  parameter), found by the evaluator or quint, impossible only by a
  complete search. A name that is no invariant or eventual val of the
  binding fails before anything runs, as a misspelled run claim does.
- **One bound, binding/2.** `simulate.search: {states, depth}` bounds
  every search of the model alone, the graph's and the possibles' (it
  was three: one for the possibles, 2000 states as deep as `steps`, two
  for the graph). Default 100000 states and no depth: a possible's
  search now goes as far as the graph would, and found witnesses are
  real however deep. Removing keys is a breaking change of the binding
  format (§63), so it is `gotogether.binding/2`, read alone, as every
  format is pre-release.

## 70. validate split: the model alone and the nav protocol apart (I18)

Review 11's architect (F6): validate was 86 exported identifiers of five
concerns (runs judged, the model alone, the model navigator, the run
navigator's wire protocol, manifests and step mutants), all to freeze as
one v1 surface, and qnav imported all of it for the protocol's types.

- **The import graph.** `validate/navproto` is a leaf (internal/formats
  alone); `validate` judges runs; `validate/model` sits above it. qtest
  imports all three (its claims over runs, `qtest.Model`'s over the
  model, its run navigator's lines), the CLI the same, qnav navproto and
  validate (for the model navigator). Nothing in validate imports model:
  the trial move of graph.go, possible.go, live.go and simulate*.go
  compiled validate untouched, so the analyses never were the runs'.
- **One bridge, internal.** The analyses need the evaluator validate
  compiles and caches per model (its quint compile, the D3 cache, the
  compiled-artifact refusal), which validate's run judging uses through
  methods taking its Checker. Rather than export that, or move it to a
  package both import and turn every method into a function,
  `validate/internal/evalshare` holds what the analyses read (`Eval`:
  module, IR, error, a literal reader) and the two transition helpers
  both use; validate sets `evalshare.EvaluatorOf` in its init, which
  every importer of model has run (model imports validate).
- **The API.** `model.Simulate(path, opts)` as before; for a checker in
  hand, `model.For(c)` (or `model.New(path, opts)`, which owns its
  checker) is an `Analyzer` with `Graph`, `Possible` and `Simulate`:
  methods of the package's own type, since another package's type takes
  none. Names lose the package they now have: `Report`, `Verdict`,
  `Bound`; navproto's `Node`, `Request`, `Response`, `Session`,
  `Protocol`, `CheckProtocol`. navproto has its own `NamedValue` (a
  wire type), validate keeps its explanation's.
- **What stayed.** The model navigator (`Checker.Nav`, `Navigator`,
  `Node`, `NavStart`, `SplitPath`) starts from a recorded run's state
  (its plan, a partition's whole trace, a resumed snapshot) and explains
  disabled actions with the runs' explanations: half of validate's run
  internals. It stays in validate until those have a package of their
  own. Manifests and step mutants stay too: they are of runs.

## 71. A run's records apart, and where a new knob goes (I18's rest)

**qtrace.Step's records.** A run's stream holds model steps and records
about the run: an invocation (qobs.Invoke), a settle (qobs.Settled), a
loss (MarkLost, a torn tail), a watermark (CompleteThrough). They were
six flags and values among the step's fields, so a reader of a Trace's
steps could not tell from the type which fields were ever set there.
Three shapes were weighed:
- **A type of their own in the stream** ([]Entry of steps or records):
  the honest one, and every reader, sink and search would take a sum
  type for a distinction Reconstruct already makes once. Rejected for
  v1: the cost is every signature of the recording side, for no
  verdict.
- **Fields said to be Reconstruct's** in a comment: no cost, and nothing
  a reader sees in the type.
- **Chosen: `qtrace.Record`, embedded in `Step`.** The record fields
  (Invocation, Settled, Lost, LostAfter, LostBefore, CompleteThrough)
  are one struct, `Step.Record`, documented once: what a record line
  says, zero on a model step. Reads and writes go through promotion
  (`s.Settled`), so only composite literals changed (`Step{Action:
  "settled", Record: qtrace.Record{Settled: true}}`; a one-off AST
  rewrite migrated 43 files). Reconstruct's guarantee is stated on the
  type and tested (`TestTraceStepsHaveNoRecord`): every step of a Trace
  has its Record zero, each kind read into the Trace's own field
  (Pending, Settled, Lost, Collected). `Call` and `Invoked` stay on the
  step: they are a completion's, and a completion is a model step.

**Where a new knob goes.** One rule, so the API keeps one shape:
- **What a run is** (its seeds, bounds, rates, constants, budgets, the
  model's instance) is a field of the configuration struct the call
  takes (`ExploreConfig`, `Bound`, `SweepConfig`, `model.SimulateOptions`,
  `validate.Options`, `qload.Experiment`, a binding's key), its zero
  value the documented default. It may change a verdict, so the run's
  evidence or manifest records it.
- **How a test carries a check out** (a recorder installed globally,
  files kept, what it prints) is an `Option` of the package
  (`qtest.Option`, `qobs.Option`): variadic, composable, and it changes
  how a verdict is reached or reported, never which.
- **An environment variable** (`GOTOGETHER_*`) is for a person around a
  run of the suite (keep, run one seed, verbose, update goldens), each
  readable from the code that a test file can see. Never a semantic only
  the environment carries.
- **Never a context value** for configuration: a context carries a run
  (qrun) and its recorder, not settings (grist's `SnapshotEvery` became
  an App option, I24, for that reason).
- A knob that would fit two of these goes where its effect on the
  verdict puts it: if it can change one, it is the run's.

**The model navigator** stays in validate for v1 (§70): it starts from
a recorded run's state and explains disabled actions with the runs'
explanations.


## 72. Stored identity instead of node-local rewind epochs (I25)

A deterministic two-node fixture exposed a mismatch: after restore/resume,
a surviving node keyed a replacement trigger with its local rewind epoch while
a restarted node used its position. Repeating the restore widened the mismatch.
Persisting those counters would need a shared retained boundary, despite
nodes observing different windows and step-log pruning deleting old evidence.

Every store tier now mints and persists an immutable `Stored.ID` for each event;
`Meta.ID` carries it to callbacks. Effect keys include that ID. Retained events
keep their keys, replacement events get new keys even with identical body,
cause, timestamp and position, and all nodes derive the same answer without
rewind-memory reconstruction. Native effect and call records carry `triggerId`;
`CallInfo.TriggerID`, the one required HTTP cause header and the full key must
agree. Missing/invalid identities stop the batch before callbacks or checkpoints.
SQL schema 2 refuses older data rather than inventing identities for events
that may already have caused external effects.

`qenv.NewID` supplies random 128-bit production identities and a deterministic
per-simulation allocator shared by setup, processes and stores. Simulated
restores do not rewind that allocator, and it consumes no scheduling/fault
choices. Separate simulated universes may reuse values. Stores reject duplicate
explicit retained identities and skip allocator collisions with imported fixture
identities. IDs are opaque; users must not derive them from positions or clocks.

Historical copies use explicit `Batch.RetainedIDs`, validated and committed
atomically with the events. `gristtest.Capture` assigns its private batch IDs
before append, avoiding a read-after-commit race with restore/replacement. A
known commit stays committed even if evidence collection fails; unknown outcomes
or overlapping captures make the capture unusable rather than exporting an
incomplete or falsely ordered history. Raw fixture batches without recorded
identities are new materializations, not evidence of original event identity.

Shared store tests cover duplicate IDs, read stability, unknown commit retries,
rollback and repeated restores. The public effect fixture scripts crash/restart
and retry; caller/site checks validate typed identity. Existing generated models
keep keys opaque and check retry consistency; they do not prove ID allocation.
This change does not detect every unobserved restore or atomically close a
read-to-effect restore window. Fail-closed detection and operator reconciliation
remain separate guarantees.


The additional identity-index seams exposed two integration assumptions. A
rollout test depended on a small seed sample happening to select an old reader;
a deterministic eager/held writer contrast now proves the refusal prerequisite,
while retaining broad rollouts. A relay could read an old step row and then find
its events gone after a restore, classifying it as permanent malformed data.
On decoding failure only, it now checks the exact native row again: changed or
missing rows restart prefix verification, while unchanged malformed data retains
its error. Reread failures remain errors. Neither successful rows nor model
judgments bypass their existing validation.


A forced counter incident then exposed a context-composition bug: installing
`gristsim.Config.Setup` dependencies discarded the relay clock for served
commands/queries. Processors still had it, so post-restore query records sorted
before the commits they read. Setup now preserves that mandatory clock layer;
the model is unchanged. The incident fixture targets the paid effect and lost
checkpoint directly, rather than assuming a global nth commit has that role.


The forced fail-open counter contrast retains a bounded claim: specified loss
occurs after a real bank application, a retained trigger's checkpoint is lost,
and only the ablation makes the same full key/ID call again after rewind without
resume. The faithful side makes one call and never rejects a new bank call; it
does not establish full conformance for an outcome already in flight when the
checkpoint disappears. That completion boundary needs its own small contract
fixture before expanding incident-conformance claims. The model is not loosened
to hide that distinction.


## 73. Source judgment identity

A judgment must not be attributed to files reread after it completed. Binding
and conversion parsers retain the SHA256 of their exact input bytes. Source
checkers capture the conservative source inventory before loading and verify it
before and after judgment; model/evaluator caches use the same inventory and
verify it before publishing external tool output. Results retain a private copy
of the effective model instance and source identities. Manifest creation refuses
changed files or effective instances. ITF-only validation captures its model at
validation time, rather than reconstructing provenance during recording.

These guards detect changes observed at the boundaries. External Quint still
reads live source files, so an edit restored during tool execution can evade
pre/post checks. This is not an atomic cross-file snapshot. Stronger guarantees
would require an immutable source tree shared by typechecking, compilation and
ITF execution. Compiled artifacts remain the existing evaluator-only workflow.
Low-level manually constructed records/results remain caller-provided evidence;
optional expected fingerprints allow callers to enforce their own capture.
File imports in binding or ITF definition strings are refused with guidance to
move them into model files. Typed ITF inference retains a `SourceGuard` through
decoding and validation, preventing schema and judgment from using different
observed source inventories. Initial and checker binding loads must agree.
No manifest format change or compatibility path is needed.


Vacuity witness probes generate temporary predicates, so they use an explicit
internal diagnostic checker rather than modifying a loaded source checker.
Only witness booleans escape; the original source inventory is checked before
and after the entire probe, including early returns. Derived reports are not
attributed to the original binding or recorded. Compiled-only checks cannot
compile these extra predicates and return `ErrNeedsQuint` without a tool probe.


## 74. Numeric scope before source-contrast credit

The independent Goo counted-values trial first proves exact values through
native JSON, Quint/TypeScript, native ITF reading and bundled replay. Its
unbounded mathematical sum then detects a planted wrapping-uint64 implementation
contrast. Rust and Go-evaluator range refusals remain tool errors; they cannot
stand in for source-defect evidence. Typed collection templates reuse the
existing binding helpers. Missing TypeScript failure state stays unavailable,
while native captured-record inspection identifies the offending observations.
The missing-state hint now qualifies Rust guidance by model support instead of
promising unavailable state. No compatibility format, core numeric extension or
runtime adapter was needed.
The bounded trial and original dependency/model/driver checksums are in
internal/adoptiontest/goo-counted-values.


## 75. Exact values in collection argument defaults

The large-count trial's explicit typed mappings preserved integers. Removing
those redundant argument mappings exposed a different path: flat collection
defaults call toList/toSet/toMap, which decoded through float64 and converted to
int64. This changed actual recorded operations, not merely display values.

Those helpers now decode numbers without floating conversion and render plain
integer literals with arbitrary precision. Fractional and exponent forms fail
with the element path, consistent with typed conversion. Strict EOF checking
retains rejection of trailing JSON. Maps stay deterministically ordered, arrays
inside collections stay tuples, and existing empty/null handling is unchanged.
This cannot recover values already rounded by a producer, and does not extend
any evaluator's numeric range. The frozen trial remains unchanged; a separate
disposable binding omits argument mappings to exercise defaults through native
validation, ITF and relocated replay.


## 76. Diagnose collection argument templates before replay

A plain recorded argument or observation prints as a scalar literal, even when
its text contains JSON for a collection. The model's resolved collection shape
and a single field-expression template suffice to diagnose this authored error
without a trace. Identity mappings should be omitted so the model-aware default
applies; renamed/observed JSON fields need conversion. Interned arguments render
scalar IDs and need different guidance.

The rule deliberately excludes mapped `.outcome` (already arbitrary Quint
text), other reserved metadata, compound templates and unknown parameter
shapes. It does not infer `expect` types. Existing lint findings carry the rule
identity, model confidence and fix through Go, text and JSON. Metadata supplies
no YAML coordinates, so those remain absent. No format or public API changed.


## 77. Navigation requires coherent captured evidence

A reconstructed trace can retain one payload while reporting conflicting records
or ordering evidence. Replaying that retained subset must not manufacture an
observed model state. Navigation refuses non-loss error issues in the selected
actor before planning, constant derivation and state construction, even at cursor
zero. Partition traces consult their whole actor because splitting omits issues.

Warnings, informational exact retry copies and unrelated clean actors remain
usable. Gap and lost-prefix errors retain their existing snapshot/start policy;
a valid model-failure prefix remains navigable. Refusal is an input error, not a
model verdict or counterexample, and neither JSON nor TUI fallback can expose a
node from that capture. No public API or output format changes are needed.
