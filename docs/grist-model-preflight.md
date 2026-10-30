# grist model preflight

A catalog edit can require several kinds of work at once: regenerated bindings,
a lock update, a new handwritten rule, and review of a fold whose inputs changed.
The model preflight reports those together, before a command writes files. The
app's model command and `gristtest.Generated` use the same findings and generated
change plan.

From an app directory, run the binary's model command. For the inventory example:

```sh
go run ./cmd/checkout model -check
go run ./cmd/checkout model -json
```

Both commands are read-only. JSON implies the check mode; `model -check -json`
is equivalent. JSON output is one report on stdout even when the check fails,
so an agent can inspect every finding instead of parsing a first-error message.
Do not combine these read-only modes with `-init`.

The report uses `gotogether.model-preflight/1`. Its key fields separate
maintenance readiness from permission to update generated artifacts:

| Field | Meaning |
|---|---|
| `ok` | No error findings in the completed maintenance inspection; no claim of model validity. |
| `canGenerate` | The current private plan can safely apply mechanical changes, even if handwritten work remains. The JSON itself is not an executable plan. |
| `complete`, `checks` | Which maintenance checks ran, failed or were blocked; Quint typechecking remains `not_checked`. |
| `changes` | Proposed generated-file creates, updates and removals. |
| `findings` | All known problems and review observations, with codes, locations, confidence and fixes. |

## Read the findings, then apply the mechanical changes

A report distinguishes generated changes from decisions belonging to the app:

- Generated artifacts: missing, stale or obsolete generated files, including
  bindings for variants that no longer exist. The proposed operation names
  each file to create, update or remove.
- Stored schema: lock changes and incompatible event-version declarations.
  A stale lock is visible alongside the other changes; an invalid version
  transition blocks generation rather than being repaired by rewriting the lock.
- Handwritten model: missing files, missing definitions called by the skeleton,
  required module imports, and structural layout or import-cycle problems.
  Findings identify the file or symbol and the required edit.
- Model review: changed event or read-model inputs and discovery warnings about
  unread events, unused rule mutants or commands absent from acceptance scenarios.
  These retain the scope of the source search that produced them.

Each finding has a stable code, severity, message, location when known, fix and
confidence. Exact filesystem or catalog comparisons are distinguished from
source scans. A source scan finding says what was searched; it does not prove
that no indirect or dynamically assembled test exists. Warnings remain warnings.

The report also records which checks completed and which could not run because
an earlier dependency was invalid. An empty list of findings from a blocked
stage is not a successful check of that stage. Preflight does not run Quint;
its check status makes that omission explicit. Normal model validation and
simulation assertions still judge the model and implementation.

After reviewing the plan, apply its generated-file changes:

```sh
go run ./cmd/checkout model
```

The command applies only a safe generated plan and checks the resulting files
again. It can fix stale artifacts while still failing on missing handwritten
rules or imports. It never replaces the app's rules or invariants with generated
answers. If structural or catalog errors prevent a safe plan, resolve those
findings first. Review warnings from the original inspection survive the update:
writing a new baseline does not mean the app's model has been reviewed.

`model -init` is the explicit operation for creating absent handwritten starter
files. Existing handwritten files remain the app's. Starter definitions provide
a place to write the model; their presence alone is not semantic validation.

## The witness test uses the same result

Keep the existing witness in the app's tests:

```go
gristtest.Generated(t, App)
```

An ordinary witness run writes nothing. A single failure includes all known
blocking findings rather than hiding a missing rule behind the first stale
binding. `GRIST_UPDATE=1 go test -run '^TestGenerated$'` applies the same mechanical
plan as the model command, then checks again. Unresolved handwritten work still
fails, and review warnings remain visible in the test output.

The preflight is produced from the catalog and files already used by generation.
There is no additional authoring configuration, binding association file or
second diagnostic workflow to maintain.
