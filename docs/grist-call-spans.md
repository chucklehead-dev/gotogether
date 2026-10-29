# GR3 slice 6: declared calls through OTel

Status: first increment implemented and validated, 2026-10-29, in
[examples/payouts](../examples/payouts/README.md#declared-calls-through-otel-gr3-slice-6-first-increment).
Existing runtime and generator APIs sufficed; the implementation is the
real-HTTP wiring, native export/decode path and executable acceptance checks. This refines and supersedes slice 6 in
[grist-instrumentation.md](grist-instrumentation.md#4-slices).

## Why this experiment now

GR4 has acyclic composition, causal protocol projection, bounded ordering checks
and the inventory rehearsal. The [Goo trial](https://github.com/casselc/goo/pull/1)
adds evidence for using the core independently of grist: its lifecycle model calls
real APIs, preserves immutable generations and kills three source mutants. It
found no production defect in its bounds and no blocker requiring a new core
mechanism. It is an agent-assisted trial in an independently maintained library,
not an unaided newcomer study or evidence of grist adoption.

The next question is whether a traced real peer call retains enough evidence for
our existing local and causal checks. Start with payouts -> bank, not a new app.
The older proposal inferred reduced caller actions from route/status and omitted
request parameters. That cannot replace the evidence GR4's checks require.

## First increment: preserve native evidence

Use the existing `qobs.NewOTelSink` and `otelio.ReadSteps` path. The sink emits a
short child span carrying a `quint.step` event for each native record; it does
not add the event directly to an arbitrary current span. Start with wiring and a
real-HTTP acceptance test. Change runtime or generation only where that test
shows a gap; do not invent a second recording implementation in advance.

| Evidence | Required meaning |
|---|---|
| Caller and tenant | Native actor `<app>/<tenant>` and process/incarnation identity survive export. |
| Callee and command | The declared action plus the catalog identifies the dependency, peer and command. A route or span name alone is insufficient. |
| Business cause | Preserve processor, trigger, effect key, order coordinates and the peer's command id. Do not derive a trigger from span ancestry. |
| Attempt | Preserve each invocation/completion pairing, recorder identity and dense sequence. Repeated effect keys identify retries, not duplicate telemetry. |
| Outcome | Preserve success, named declared refusal, and unknown. Only the call contract establishes a business refusal. |
| Payload | Preserve the model's declared scalar request/response fields, including types. Absent data is never filled with success-shaped defaults. |
| Diagnostics | Keep available trace/span ids and source locations so a failed record can be found in the exported trace. |

A peer's own committed record remains separate from what the caller observed.
A lost reply may leave a committed payment beside an unknown caller completion.
An interrupted caller with an exported invocation and no completion stays pending;
a timeout completion is unknown, not pending and not a refusal. A retry has the
same logical effect key and a new invocation identity. The transport does not
introduce retries or fresh command ids.

Ordinary HTTP client/server spans provide transport context. Inject/extract their
trace context explicitly in the real-tier fixture and verify the relationship.
An asynchronously polled effect need not be a child of the original command's
span; its recorded trigger establishes the business cause. Trace ancestry and
wall-clock ordering never replace the native causal coordinates.

## Acceptance: payouts -> bank over real HTTP

1. Run the existing pair with an in-memory always-sample OTel provider and an
   explicit flush. Export native step records through OTel and decode them from
   the exported OTLP/JSON. Perform judgments on those decoded records, not a
   parallel memory-sink copy. A parallel native recording is a parity oracle only.
2. Faithful success, the bank's declared `unknown-account` refusal, and a committed
   payment whose reply is lost preserve distinct caller/peer outcomes. Retry the
   lost reply under the same key: one bank payment, multiple caller attempts.
   Exercise two tenants sharing logical request ids.
3. Compare decoded/native semantic records: actions, actors, process, invocation
   pairing, recorder/sequence, arguments, outcomes and order coordinates agree.
   Explicitly allow only transport diagnostics to differ. Export reordering does
   not change reconstruction, local judgments or causal projection.
4. Both existing service models conform and `gristtest.Linked` accepts faithful
   histories. With both committed logs and caller records available,
   `gristtest.ProtocolSteps` produces the same semantic causal projection as the
   native input. Do not add a generated requestless binding to make it pass.
5. The existing `freshKey` mutant remains detectable after OTel round-trip, even
   when the caller's local model conforms. A wrong-command-id contrast must also
   fail the link check. Retain a decoded failing artifact for ordinary validation.
6. Remove or alter tenant, key, trigger, command identity and outcome separately.
   Each relevant check must reject the corruption or refuse insufficient evidence;
   report which, and never count malformed input as a semantic mutant kill.
   A missing required scalar must not become its zero value silently.
7. An exported invocation without completion is preserved as pending. Dropping an
   interior native record exposes a sequence gap. Dropping an entire tail or all
   telemetry is not necessarily detectable: give no completion/coverage claim
   without an independently established boundary. Flushing is delivery hygiene,
   not a proof that all application events were observed. Do not synthesize settle
   from HTTP success, span end, exporter flush or a quiet interval.
8. Example documentation states which check catches each contrast and reproduces
   the export/decode/judgment path. Run focused recorder/decoder and peer-example
   checks, generated-file checks if touched, and relevant race checks. Required
   tools must be present for the acceptance run. State actual results and bounds.

## Delivery order and stopping point

First establish a faithful lossless round-trip with existing APIs. Then exercise
refusal, lost reply/retry and pending invocation, followed by identity corruption
and the source mutant. Review missing evidence or API friction before adding any
new public interface. Record the result in HISTORY/STATUS when implemented.

The old counters `payTwice` span-only example remains a possible second increment
for a third-party at-most-once dependency. Generic conversion of uninstrumented
HTTP spans, generated conversion rules, weaker requestless bindings, collector
watermarks, new transports, cycles and stream adapters are outside this increment.
If a later conversion path has less evidence, give it explicit weaker claims;
never silently treat it as equivalent to native recording.


## Result of the first increment

All four faithful cases pass both local models after OTLP decoding. Native
schema parity and causal projection survive reversed export order. Link checks
reject tenant/key/command/outcome corruption; protocol projection refuses a
missing trigger; the binding refuses a missing amount before verdict. Dense
interior loss exposes an actor-level gap. The generated freshKey mutant remains
a bank invariant violation and link failure while the caller conforms. The
retained decoded witness is examples/payouts/testdata/spans/freshKey.jsonl.
Focused race/repeat checks and the full example suite pass. No public API or
conversion format was added. No blocking tracing defect was found.

These are finite prefixes and assert no eventual-completion verdict. The pending
case interrupts a recorder, not a real process; stores are in-memory and no
collector is deployed. Wire comparison allows nil/empty map allocation and
integer JSON normalization, in addition to diagnostic source locations.
Broader conversion and completeness mechanisms remain the follow-ons above.
