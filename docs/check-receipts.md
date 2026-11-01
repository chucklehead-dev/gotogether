# Check receipts

[Effective run profiles](run-profiles.md) describe the configuration that
produced driven histories. Receipts describe what was checked afterward;
the TUI and JSON carry both without treating configuration as a verdict.

A validation result now carries a structured receipt describing its actual
checks. Text, report JSON and run-navigation JSON/TUI render the same data.
All current run-navigation producers emit a required receipt, and consumers use
it directly instead of interpreting a text verdict. Registered grist
composition checks now add local-model, peer-link and system-protocol results
to the same receipt.

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
inconclusive receipt with that reason. The receipt does not infer new facts
from missing collection data.

## Presentation

Text prints a compact “what was checked” section, with at most three actors,
check counts grouped by kind and status, and three assumed effects per actor.
Passing partitions without search assumptions stay folded; omitted counts point
to the full structured receipt. JSON retains every actor, check and assumption.
The TUI uses a scrollable section and distinguishes whole-run judgment from
cursor-state diagnostics. Navigation uses only `gotogether.nav/2`; the marker
rejects mismatched independently built test binaries and navigators. There is no
old-payload fallback or dual-format reader.

The DTO package `validate/receipt` imports the standard library and the leaf
`validate/diagnostic` DTO package. Validation
constructs receipts after judgment and search marking; `navproto` transports
them without depending on validation. No new checking algorithm, hidden model
property selection, universal execution claim or core dependency on grist is
introduced. Grist supplies the composition assessment through the system's
registered assessor; the core transports and evaluates the resulting data.

## Composition checks

`gristtest.Composition.System` registers the checks once on a `qtest.System`.
Its explicit App-to-Checker table selects the service models; its optional
protocol registration selects the system model and causal-order limit. The
caller owns the checkers and keeps them open for the system's lifetime. No
binding filename is guessed, and the library does not infer model independence
from generated or handwritten source. The
[composition guide](grist-composition.md#register-checks-once) shows the author
workflow.

`validate.Report.Composition` is the canonical result. It participates
automatically in ordinary `Report.OK`, `qtest.Run.OK` and `qtest.Conforms()`;
there is no extra assertion to remember. Model-authored diagnostics remain in
local and protocol `judgment.actors[].diagnostics`, including explicit
unavailable states when exact judged state could not be recovered. The report's JSON, whole-run navigation
receipt and run manifest carry that same assessment. Its history digest binds
all components to one native history; matching seeds from separate simulator
runs are not combined. Navigation reruns the registered system's assessor,
while cursor-state browsing retains the fixed whole-run judgment.

A component's `judgment` is an acyclic `receipt.History` snapshot of its model
check, not another overall `receipt.Summary`. It retains actor checks and
diagnostics without a nested composition. Read the component's status for the
registered obligation: its ordinary model-history judgment can pass while the
component is inconclusive because a configured completion property was not
judged.

The composition lists every registered local service and declared peer edge.
A row is inactive `not_checked` only when the corresponding recorded native work or peer
call/commit is absent. A local binding that selects no actor despite existing
native work, selects another App, or selects a client-side actor is an error;
incomplete relevant evidence is inconclusive. An unconfigured protocol remains
explicitly `not_checked` and provides no protocol guarantee.

Applicable configured checks must establish their named properties. A
completion property that was not judged because no settle was recorded makes
the composition inconclusive, even if ordinary conformance passed. The same
rule applies to other configured named checks with unknown results. Errors and
inconclusive judgments are not counterexamples.

The protocol result uses `quantifier: all_causal_orders`, a definitive candidate
count, its explicit limit and `exhaustive` flag. Exhaustive success covers all
causal orderings allowed by the collected evidence. Finding one acceptable
ordering in an existential history search (`goal: one_conforming_history`)
provides a different guarantee. Neither result covers other executions.

Explore, Exhaust, Sweep, Mutate, Fuzz, FollowTraces, `System.Judge` and navigation
run the registered assessment. `System.CheckCases` rejects the registration
because that driver does not yet support it. Manifest replay also refuses a
manifest containing a composition receipt and directs the user to rerun the
originating registered system or test: a receipt records evidence, not an
executable plan. Neither path silently runs just a subset of the checks.

## Partition scope and replay instances

`counts` separates known unique source actor identities, total judgments, and
partition judgments. If `unknownSourceJudgments` is nonzero, the actor count is
a lower bound: partition display labels alone do not establish source identity. A repeated source actor in two input histories is still one
identity and two judgments. `actors[].sourceActor` is the input actor identity (recorded history or model
trace), distinct from the actor field's partition display label. `partitionInfo` carries the key,
typed recorded value and receipt-local `instance` reference. Numeric values stay
exact through decoding; a number and a string containing its digits differ.

`partitionInstances` stores original whole-history constant literals once per
partitioned instance. Its replay substitutions name only the changed constants,
with exact effective Quint literals and `unread_by_evaluated_prefixes` basis.
Applying those substitutions to the original map yields the effective replay
instance. Factoring is `checked_on_evaluated_prefixes`: evidence about this run's
checked prefixes, not a universal factorization proof or other executions.

Passing-partition folding keeps these tables and references. Merged reports
remap incoming local IDs; separate composition histories keep separate ID spaces.
The TUI renders the same provenance independently of model-cursor states.
Standalone `Result.Receipt()` supplies typed identity without a table reference;
use `Report.Receipt()` for aggregate context or `Result.PartitionInstance()` for
an independent instance snapshot. Partition constants are not repeated in each
JSON actor. Human literal display is bounded; structured values remain complete.
