# Check receipts

A validation result now carries a structured receipt describing its actual
checks. Text, report JSON and run-navigation JSON/TUI render the same data.
All current run-navigation producers emit a required receipt, and consumers use
it directly instead of interpreting a text verdict. This is a prerelease
first increment; grist composition aggregation remains follow-up work.

The `receipt` on a validation document includes the whole history,
including passing partitions folded out of the document's `actors` list.
Each `DocActor` also includes its receipt. New run manifests retain the
receipt of their recorded actor judgments. A run-navigation node's receipt is
for the whole nominal run, not the model state at the current cursor.

## Interpretation

- `status`: `pass`, `fail`, `inconclusive`, `not_checked` or `error`.
  Inconclusive means attempted without a conclusion (for example a search's
  budget); not checked means no check was performed. Explicit evaluator errors
  are errors, not counterexamples.
- `scope`: `collected_history` for recorded histories, `model_trace` for ITF
  model traces, or `mixed_inputs` for a report manually combining both.
- Each actor lists conformance, invariant, eventual and bounded-response checks
  actually represented in its result. An eventual property omitted from judgment
  because there was no settle appears as not checked when the report records
  that absence. A pass does not mean every completion property was checked.
- `ordering`: reconstructed history, search-selected candidate, or model trace.
  The search-selected category includes cases where constraints force one order;
  it does not claim multiple orders were enumerated.
- `search.goal: one_conforming_history` is existential. A witness does not prove
  every possible ordering conforms. `budgetExhausted` comes directly from search
  termination metadata, not from parsing an issue's wording. Order budgets are
  per search phase, not total counts; combined linearization and resolution uses
  the order budget, not the standalone resolution budget.
- `assumedEffects` describes calls marked ambiguous in the checked candidate:
  action, arguments, candidate record identity and position.
  These pending-call effects are assumptions, not observed completions. Failure
  and pending-record JSON also expose `ambiguous` when applicable.
- `otherExecutions: not_checked` prevents reading history validation as proof of
  behavior outside that history. Local service checking also does not establish
  a separately authored combined business protocol.

An otherwise passing report whose caller says the system did not settle has an
inconclusive receipt with that reason. The checking algorithms and claims remain unchanged. The receipt does not infer new facts from missing collection data.

## Presentation

Text prints a compact “what was checked” section, with at most three actors,
check counts grouped by kind and status, and three assumed effects per actor.
Passing partitions without search assumptions stay folded; omitted counts point
to the full structured receipt. JSON retains every actor, check and assumption.
The TUI uses a scrollable section and distinguishes whole-run judgment from
cursor-state diagnostics. Navigation uses only `gotogether.nav/2`; the marker
rejects mismatched independently built test binaries and navigators. There is no
old-payload fallback or dual-format reader.

The DTO package `validate/receipt` imports only the standard library. Validation
constructs receipts after judgment and search marking; `navproto` transports
them without depending on validation. No new checking algorithm, hidden model
property selection, universal execution claim or core dependency on grist is
introduced. Grist's local-model, peer-link and protocol-order summaries will need
explicit aggregation of their actual results in a later increment.
