# migrate-payments: a legacy service brought under a model

A walk-through of the gotogether-migration skill on a small legacy
system: a payment handler ([payments.go](payments.go),
[charge.go](charge.go), [refund.go](refund.go), [audit.go](audit.go),
about 300 lines) with the things a team arrives with: its tests
([payments_test.go](payments_test.go)), a production audit log
([testdata/logs](testdata/logs)) and OTel spans from staging
([testdata/spans](testdata/spans)), but no model. The handler imports the
standard library and the OTel API alone (`TestLegacyImportsNoToolkit`):
the migration reads its runs and never changes how it records them.

The legacy code had a bug: it compared each refund with the captured
amount and forgot earlier refunds, so two partial refunds could return
more than was captured. Every legacy test passed. **The production log
caught it first**, before any code changed (stage 4); the code is now
fixed ([refund.go](refund.go)), and the legacy version is the mutant
`forgetsPriorRefunds`.

The example lives in one tree, so its history is told here as stages,
each the skill's step, its files and the command that checks it.

| file | what it is |
|---|---|
| [payments.go](payments.go), [charge.go](charge.go), [refund.go](refund.go), [audit.go](audit.go) | the legacy handler: `POST /charges` (an optional `Idempotency-Key`), `.../capture`, `.../refund`, `.../void`, `GET /charges/{id}`; one lock; an audit record per request with a per-process `audit_seq`; a server span per request |
| [payments_test.go](payments_test.go) | the legacy tests, unchanged: each request's answer asserted |
| [model/payments.qnt](model/payments.qnt) | the context map, the requirements R1-R9 each with its provenance, the model |
| [payments.binding.yaml](payments.binding.yaml) | scaffolded, then each result mapped (`result: '{{.outcome}}'`), the lookup's answer compared; the scenarios under `simulate:` |
| [payments.convert.yaml](payments.convert.yaml) | the handler's own spans as steps (the rule format: `go doc github.com/chucklehead-dev/gotogether/convert`) |
| [auditsteps/](auditsteps/auditsteps.go) | the migration's one adapter: an audit record as a step (a `qobs.Record` per operation), live (`Handler`, the service's logger in a test) or from a log file (`ReadLog`, keeping `audit_seq`); [cmd/auditsteps](cmd/auditsteps/main.go) writes a step log |
| [testdata/draft](testdata/draft) | the stage-3 draft: model, binding and rules, kept to show what the first checks found in it |
| [harness_test.go](harness_test.go), [migration_test.go](migration_test.go), [mutants_test.go](mutants_test.go) | the legacy tests recorded, the logs and spans judged, each stage's verdict stated; the mutants |
| [testdata/mutants](testdata/mutants) | each mutant's file, replacing the package's of the same name (a `go test -overlay`, run by `qtest.Mutants`) |
| [cmd/payments](cmd/payments/main.go) | the handler as production runs it: its audit log on stdout |

## The stages

**1. A context map.** One context: the charges, which commit in one
place (the handler's lock). Its map is the model's header: its state,
its five operations, its environment (the store, serial), and what is
outside (the merchant's notifications, fire and forget).

**2. Requirements, with provenance.** Read from the code's guards, the
tests' assertions, the log and the spans, each beside the `val` or the
action that states it, with its sources: `// req R5: ... code: refund.go;
test: TestRefundTooMuch; log: payments-2026-10-02.jsonl audit_seq
10-11`. R8 (a voided charge is never captured) rests on the code alone
and is marked `assumed:` for its owner. `TestRequirementsHaveProvenance`
holds every requirement to two kinds of source or `assumed`.

**3. The model on its own.** `go tool gotogether scaffold` wrote the
binding; its `simulate.possible` states each requirement's lifecycle.
The first draft ([testdata/draft](testdata/draft)) was written from the
comments and the tests: its capture took the whole authorization (as
`capture`'s stale doc comment says). `go tool gotogether simulate
-binding testdata/draft/payments.draft.yaml` printed `FAIL possible R3
part of an authorization captured`. The model now reaches every
scenario, and `TestScenariosPossible` claims three of them in Go
(`qtest.NamedScenario`, `qtest.Possible`).

**4. The first verdicts, before any code change.**
- The legacy tests, each run again inside `qtest.Record` with the
  service's logger replaced by `auditsteps.Handler` (`recorded`): all 12
  conform. Under the draft, `TestPartialCapture`'s run was rejected.
- The production log of 2026-10-02, read by `auditsteps.ReadLog`:
  `invariant refundsWithinCapture: FAIL, violated after record
  day.jsonl:12`, the second refund of `ch_3` (700, then 500, of 1000),
  as on-call reads it:
  ```
  violated after record day.jsonl:12
    record:   day.jsonl:12, 2026-10-02T09:00:31.37Z, process payments-1
    observed: refund(amount=500, id=ch_3)
    quint:    refund("ch_3", 500, "ok")
    the step changed, in the model's state:
      charges["ch_3"].refunded: 700 -> 1200
  ```
  (`-v` adds the replay's step 11 and the recorder's seq; `-json` the
  same as `gotogether.report/2`.) Under the draft its
  second record, a client's retry answered with its charge (`replayed`),
  was rejected: nothing in the draft knew idempotency keys.
- An older log, rotated, starts at `audit_seq` 4113: `issue: error
  lost-prefix`. The newer logs are whole; a snapshot record at each
  log's head would let the old ones be judged.
- The staging spans, converted by [payments.convert.yaml](payments.convert.yaml),
  conform. The draft's rule file left `GET /charges/{id}` and
  `notify merchant` unmatched (`gotogether convert` names them).
- `go tool gotogether lint -binding testdata/draft/payments.draft.yaml
  -src ./auditsteps`: `lookup is not an action of payments`.

**5. Each disagreement decided** with the owner, and recorded on its
requirement in the model (`decided: ...`): below.

**6. Then the code changes**, under the model: [refund.go](refund.go)
checks the remaining amount. The production day, replayed on the fixed
code, conforms (`TestProductionDayReplayedConforms`); replayed on the
legacy code (the mutant `forgetsPriorRefunds`) it is the log again,
record for record, and violates R5.

## Which source found which disagreement

| disagreement | found by | the check's line | decided |
|---|---|---|---|
| refunds that add up past the capture (R5) | the production log | `invariant refundsWithinCapture: FAIL` (`Violates`) | the code's bug: fixed in stage 6, kept as mutant `forgetsPriorRefunds`; the legacy tests miss it (no test refunds twice), so do the staging spans |
| partial captures (R3) | `gotogether simulate` on the draft; the legacy test `TestPartialCapture`, recorded | `FAIL possible R3 ...`; `Rejects(capture)` | the model's mistake: the draft followed a stale comment |
| idempotent retries (R2) | the production log, under the draft | `Rejects(authorize)` at `audit_seq` 2 | an undocumented requirement: no test states it; the code and the log do. Mutant `replayCreatesCharge` |
| lookups (R7) | `lint -src` on the draft; the draft's convert rules | `lookup is not an action of payments`; `spans that no rule matches: "GET /charges/{id}"` | modelled: a read is what a client acts on. Mutant `staleLookup` |
| merchant notifications | the draft's convert rules | `spans that no rule matches: "notify merchant"` | outside the context: a `drop: true` rule |
| the September log | validating it | `issue: error lost-prefix` | judge the whole logs; a snapshot step for rotated ones |
| a voided charge captured (R8) | the code alone | one source | `assumed`, for the owner to confirm |

## The skill's Results, each shown

| Result (gotogether-migration's table) | where |
|---|---|
| `possible X: FAIL`, the model forbids it | `simulate` on the draft (`run-demo.sh`); `TestDraftForbidsPartialCapture` |
| a legacy test's run `Rejects` | `TestDraftRejectsPartialCaptureTest` |
| a production log `Rejects` or `Mismatch` | `TestDraftRejectsTheLogsReplay` (`Rejects`); the production day replayed on `staleLookup` (`Mismatch`) |
| `Violates` on a legacy run | `TestProductionLogViolatesR5` |
| `lint -src`: `X is not an action of M` | `run-demo.sh`; `TestDraftLacksRecordedLookup` |
| spans that no `convert` rule matches | `run-demo.sh`; `TestStagingSpans` |
| `issue: error gap`, `lost-prefix` on old logs | `TestRotatedLogLostItsPrefix` |
| a requirement with one source | R8, `assumed`; `TestRequirementsHaveProvenance` |

## What each test catches

| test | mutant | check | verdict |
|---|---|---|---|
| `TestRequirementsHaveProvenance` | none | each `// req` of the model | two kinds of source each, or `assumed` (R8) |
| `TestScenariosPossible` | none: the model alone | three lifecycles (`qtest.NamedScenario`), searched in the model (`qtest.Possible`) | possible, each with its witness |
| `TestDraftForbidsPartialCapture` | none: the draft alone | its `simulate.possible` (`model.Analyzer.Possible`) | not found: the draft forbids a partial capture |
| `TestLegacyTestsConform` | none | the 12 legacy tests, recorded (`qtest.Record`) | `All(Conforms())` |
| `TestDraftRejectsPartialCaptureTest` | none: the draft | `TestPartialCapture`'s run | `Rejects("capture")` |
| `TestProductionLogViolatesR5` | the legacy code, as production ran it | the production log | `Violates("refundsWithinCapture").At("refund")` |
| `TestDraftRejectsTheLogsReplay` | none: the draft | the production log, its reads aside | `Rejects("authorize")` |
| `TestRotatedLogLostItsPrefix` | none | the rotated log | `HasIssue("lost-prefix")` |
| `TestStagingSpans` | none | the staging spans, converted | `Conforms()`, no span unmatched; the draft's rules leave two |
| `TestDraftLacksRecordedLookup` | none | the actions `auditsteps` records against each model | the draft lacks `lookup`; the model lacks none |
| `TestProductionDayReplayedConforms` | none: the fixed code | the production day's requests, replayed and recorded | `Conforms()` |
| `TestProductionDayAtVolume` | none | a synthetic day of 10⁴ steps (3,334 charges), the binding partitioned by charge (`partition: id`) | `Rejects("refund")` and `Violates("refundsWithinCapture")`, two charges' disagreements both reported, within a budget (judged whole: over 20 minutes, and the first alone) |
| `TestMutant` (`forgetsPriorRefunds`), run by `TestMutants` | each refund checked against the capture alone | the legacy tests recorded; the production day replayed; the staging spans | the legacy tests pass and conform; the replay is the log, record for record, and `Violates("refundsWithinCapture")`; the spans conform: staging never refunded twice |
| `TestMutant` (`replayCreatesCharge`) | a retried key makes a second charge | the same | the legacy tests pass and conform; the replay and the spans `Rejects("authorize")` |
| `TestMutant` (`staleLookup`) | a lookup answers from a copy taken at the capture | the same | the legacy tests pass and conform; the replay and the spans `Mismatch("lookup")` |

In every mutant the legacy suite passes: what catches them is the model
judging the runs the system already makes.

## A day at volume

A day's log judged whole costs the sum of the model's states it passes
through: every invariant over every charge so far, after every step
(10⁴ steps: over 20 minutes). The charges are independent, so the log
is judged charge by charge: `partition: id` in a binding (DESIGN §66),
each charge's steps alone, in parallel, every failing charge reported
(10⁵ steps: about 15 s). validate checks on each run that the model
factors by the charge: R2's lookup of a key's charge goes through an
index (`byKey`, as charge.go keeps it), not a scan of every charge, and
`oneChargePerKey` judges one charge, or one key, at a time. A run where
a step touches another charge's entries (the mutant
`replayCreatesCharge`: one key, two charges) is refused, naming the
step and the entry: judge that log whole, as this README's stages do.

## Run it

```sh
go test .                                    # needs quint on PATH
./run-demo.sh                                # each stage's check, as go tool gotogether runs it
MUTANT=forgetsPriorRefunds ./run-demo.sh     # the legacy code: its tests pass, the production day does not
UPDATE_STAGING_SPANS=1 go test -run TestWriteStagingSpans .   # rewrite testdata/spans from the staging workload
```

The CLI is a tool of this module (`go get -tool
github.com/chucklehead-dev/gotogether/cmd/gotogether`), so `go tool
gotogether` runs the version the module requires. Next: the
gotogether-workflow skill's loop from "code" on, every change judged by
the model; [examples/payd](../payd) checks an unmodified service in a
simulation as well.
