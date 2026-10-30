# Model-authored diagnostics: annotation prototype

Status: executable prototype for review, not a frozen API or a complete
obligation-reporting feature. It changes gotogether's model loading and optional
report/navigation details. Grist's application runtime is unchanged.

Usability, design and architecture reviews preferred model-owned metadata over a
second binding association, a naming convention or Go-derived business semantics.
Quint preserves triple-slash documentation in its typed output; ordinary line
comments do not survive. Discovery therefore needs neither a source scanner nor
another YAML section for discovery. Optional evidence links are declared in the
binding because they describe how captured records map to the model.

## Author once, discover automatically

The independent inventory protocol now defines its unanswered intents once:

```quint
val outstandingIntents = /* model-authored Set of diagnostic records */
/// gotogether: diagnostic outstandingIntents
val allIntentsAnswered = outstandingIntents.size() == 0
```

The first line above is illustrative. The runnable definition is in
[protocol.qnt](../examples/inventory-grist/model/protocol.qnt). Its Boolean is
still selected by the existing binding's `eventually` list. Annotation discovery
never adds a correctness check. An association for an unchecked property is
reported as inactive by lint.

The provisional record shape is exactly
`Set[{kind: str, subject: str, intent: str, expected: str}]`. For example:

```quint
{kind: "cancellation", subject: "a", intent: "t1-c1-1",
 expected: "modeled inventory cancellation answer"}
```

`intent` is business identity, not a recorder/sequence address. A response may
be a legitimate refusal: an unanswered intent does not alone prove a leaked
reservation or identify the implementation's root cause. A state-reading
projection is a normal Quint `val`, not the technical `pure val` qualifier.

Model loading validates the annotation, the property's own Boolean type and the
projection's named read-only, zero-argument record-set type using typed IR.
Malformed, duplicate, unknown and wrong-shape declarations give stable error-code
prefixes and model/symbol names. Source ranges, dynamic editor completion and
machine-readable lint findings remain future work; no LSP is added.

## Reproduce

From the repository root with Go and Quint available:

```sh
go run ./cmd/gotogether lint -binding examples/inventory-grist/protocol.binding.yaml
go run ./cmd/gotogether validate -json -binding examples/inventory-grist/protocol.binding.yaml examples/inventory-grist/testdata/diagnostics/missingRelease.jsonl
# Expected exit 1: eventually allIntentsAnswered fails, with one cancellation item.
go run ./cmd/gotogether nav -json -binding examples/inventory-grist/protocol.binding.yaml -run examples/inventory-grist/testdata/diagnostics/missingRelease.jsonl -actor protocol/t1
go run -C qnav ./cmd/gotogether-nav -binding ../examples/inventory-grist/protocol.binding.yaml -run ../examples/inventory-grist/testdata/diagnostics/missingRelease.jsonl -actor protocol/t1
```

The retained input is the real `missingRelease` simulation mutant's projected
history. Recreate it with:

```sh
TRIAL_EVIDENCE=/tmp/intent-diagnostics go -C examples/inventory-grist test -short -run 'TestMutants/missingRelease$' -count=1 -v
```

In the TUI press **g** to open the annotated completion failure even though all
transitions conform. Terminal details remain labeled as fixed checked-history
results while exploring another model branch. Current-node details separately
say **selected model state; no verdict**. Run-navigation JSON carries whole-run
and cursor details separately. Text and TUI render the same typed payload that
JSON exposes; they do not recompute unanswered work.

The report identifies actor `protocol/t1`, subject `a`, intent `t1-c1-1`, and the
expected modeled inventory cancellation answer. The original verdict/exit code is preserved. The binding now resolves the
cancellation to an exact captured record and preserves its native origin.
Press **e** for the evidence list, then **Enter** to inspect a record. **Back**
returns to the list and original cursor. Projected and native records are separate
choices; a saved projected history may not contain the native capture.

## Truth and availability boundaries

Final report enrichment runs after existing absence/loss/search classification.
It only evaluates a conforming settled eventual defect using the exact state
recovered from Quint's failing replay and that actor's planned constants. It does
not independently replay raw input to guess a state. Search/hidden/partitioned/
resumed histories and missing exact states produce explicit unavailable details.
The final Judge hook replaces pre-search details on copies of cached results.

A prefix or lost evidence keeps the existing `uncollected`/`dropped` classification,
with unavailable report details and no settled-state label. Model navigation may
inspect outstanding items at the exact selected state, but makes no failure claim.
Unsupported explanation evaluation cannot turn an existing failure into a pass or
change an absence cause. An empty explanation at a failed property is unavailable,
not an assertion that all work completed.

Items are deterministically ordered, capped at 32 with total/omitted counts.
Returned string fields over 512 bytes refuse the explanation rather than truncate
identity. These are provisional output limits, not a general model-evaluation
resource budget. The TypeScript backend does not emit failing-test ITF, so report details are
explicitly unavailable there; the failure verdict remains intact and exact-state
navigation remains available. Rust-backed reports carry the item details.
Compiled models retain discovered associations and the projection
bridge for navigation; report enrichment still requires an exact failure state.

## Validation and remaining work

Focused tests cover typed annotation discovery, overrides, invalid declarations,
Go/Quint report parity, Check/CheckBatch/Judge, real cached-search finalization,
shared business IDs across actors, multiple intents, incomplete evidence,
compiled metadata, output limits, TUI boundary labels and terminal entry. The real
inventory mutant asserts the exact unanswered cancellation item. No generated
binding changes were required; grist's existing marks coexist with the annotation.

Further work should follow adopter needs: richer subject identities, broader checked-history
support, editor/structured-lint integration and API names. Failed invariants can
have discoverable/navigable projections, but final-report enrichment currently
handles eventual absences only. There are no automatic business-code repairs,
business-metadata binding overrides or inferred explanations. Evidence-origin
templates only read captured metadata and do not evaluate model predicates.


## Exact evidence links

A diagnostic item identifies business work. To link it to an observation, declare
an equality selector against a model action's string parameters:

```yaml
diagnosticEvidence:
  outstandingIntents:
    cancellation:
      action: checkoutCancel
      match: {id: intent, order: subject}
      origin:
        actor: '{{index .obs "protocol.actor"}}'
        recorder: '{{index .obs "protocol.recorder"}}'
        seq: '{{index .obs "protocol.seq"}}'
```

Rules are keyed by diagnostic projection and item kind. `match` maps model
parameter names to `intent` or `subject`; at least one must select `intent`.
Matching uses the binding's actual planned calls for the diagnostic's actor,
not arbitrary raw fields, global ID searches, display text or assumed pending
completions. Lint checks projection/action/parameter names and string types.
The JSON Schema exposes the binding fields to YAML editors.

`origin` is optional. Without it, the captured record is also the origin. For a
projection, all three origin templates are required and read only raw `actor`,
`recorder`, `seq`, `args` and `obs`. Grist's protocol projection preserves native
actor/recorder/sequence as observations, including through JSONL round trips.
The supplied mapping asserts provenance, not a causal explanation; it does not
prove that the native capture is present or that the event caused a defect.

Each item's `evidence` reports `resolved`, `missing`, `ambiguous`, `unconfigured`
or `unavailable`. Matches retain the captured identity, native identity and the
position in the actor's replay plan. Multiple matches remain candidates, with
no first-match selection. Source paths remain display provenance. The report's
model judgment and absence classification remain unchanged.

Cursor diagnostics resolve against the observed prefix only. Exploring a
hypothetical model branch makes capture resolution unavailable. Fixed terminal
diagnostics continue to refer to the checked history independently of that cursor.

The record inspector shows captured values and identity, not a model state.
`validate.Navigator.Record` inspects file-backed captures; run-navigation sessions
accept a request such as:

```json
{"node":"@0","record":{"actor":"checkout/t1","recorder":"grist/checkout/t1","seq":3}}
```

The response's `record` contains exact captured arguments and observations. The
request cannot combine inspection with `follow`, `take`, `fold` or `save`.
Missing captures and conflicting records return explicit errors without ending
the session or changing its cursor. Record inspection never opens a supplied
source path, downloads an artifact or reruns a model action.


For the retained inventory projection, this inspects the actual cancellation
record without loading or replaying the model:

```sh
go run ./cmd/gotogether nav -run examples/inventory-grist/testdata/diagnostics/missingRelease.jsonl -record '{"actor":"protocol/t1","recorder":"grist/protocol/t1","seq":2}' -json
```

The projected record links to native `checkout/t1`, recorder
`grist/checkout/t1`, sequence `3`. That native record needs its native capture;
the projection alone supplies its provenance identity, not the native payload.
