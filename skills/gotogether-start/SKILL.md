---
name: gotogether-start
description: Start here for checking Go code against a Quint model with gotogether - picks the workflow (model-based tests with connect, Hegel with qhegel, trace validation with qobs/binding/validate, simulated fault injection with qenv and qtest.System, metastability with qload/qmeta, coverage, mutants, sweeps, fuzzing, Choreo models, grist apps, a migration of existing code) and the skill to load next. Use when a task mentions Quint and Go together and it is not yet clear which part of the toolkit applies, or when asked what the toolkit can do.
---

# gotogether: which tool for which question

## Choose your path

| Question | Skill |
|---|---|
| A new component: what order, which tests, CI? | gotogether-workflow (model first) |
| A team arrives with existing code and no model | gotogether-migration (walked through in examples/migrate-payments) |
| Writing the model; what it should allow | gotogether-model, gotogether-patterns |
| Does my code do what the model's traces do? | gotogether-mbt |
| Does the code accept inputs the model forbids? | gotogether-hegel |
| Do real runs conform? Is it linearizable? | gotogether-traces |
| Correct under crashes, lost responses, isolation levels? | gotogether-faults |
| Does it recover after a spike or a slow dependency? | gotogether-metastability |
| An event-sourced app whose model is generated | grist |
| A test failed: whose bug? | gotogether-workflow's verdicts reference |

The full table (liveness, journeys, external drivers, coverage, gosim,
weaving, Kubernetes, PCT, fuzzing): [references/questions.md](references/questions.md).
The first hour in your own module, the glossary and the first commands:
[references/first-hour.md](references/first-hour.md). CI (the Action, the
tiers, a model compiled for a pipeline with no quint):
[references/ci.md](references/ci.md).

## Steps

1. Run the smallest example: `git clone
   https://github.com/chucklehead-dev/gotogether && cd
   gotogether/examples/hello && go test -v .` (quint on PATH).
2. Read `model/hello.qnt`, `hello.binding.yaml`, `hello.go`,
   `hello_test.go`, in that order.
3. Model your component first and simulate it.
4. Scaffold a binding and a System test; record steps until the faithful
   system conforms.
5. Plant a mutant (`testdata/mutants/<name>`) and state how it is caught.
6. Shrink its failure with Hegel; validate real runs.

## Checklist

- [ ] quint ≥ 0.32 on PATH (`quint --version`); Go ≥ 1.26.1.
- [ ] `gotogether` runs (`go install github.com/chucklehead-dev/gotogether/cmd/gotogether@latest`,
  or in your module `go get -tool github.com/chucklehead-dev/gotogether/cmd/gotogether` and `go tool gotogether help`).
- [ ] The skill for the question is loaded before writing code.

## Results

| You see | Means | Next |
|---|---|---|
| tests `SKIP`: `quint not installed` | `qtest.RequireQuint` skipped them | install quint (`GOTOGETHER_STRICT=1` makes it fail) |
| under `TestMutants`: `as expected, some run showing an apply step the model cannot take: 23 of 30 runs`, then `step 2, apply(id=1): the model's apply(id = 1) is disabled: ...` | a catch: a mutant's build, its runs failing as its claim says (`TestMutant` itself SKIPs in the app's build) | its `navigate:` line opens the step; `GOTOGETHER_VERBOSE=1` prints the whole report |
| `30 runs, all conform (quint confirmed 2)` | every run conforms; quint judged a sample of them too | `GOTOGETHER_VERBOSE=1` for the evaluator's breakdown |
| `no step was recorded; the run's doubles saw a request to bank refused, nothing listening` | a client ran before its server listened | start the server's process first, or retry a refused connection |
| `NO VERDICT (needs quint)` from `validate -compiled` | the Go evaluator alone could not decide it | validate without `-compiled` ([references/ci.md](references/ci.md)) |
| `gotogether lint` errors | the binding, an annotation or simulated code does not fit | each names the fix |
| a verdict you cannot place | a claim's report | gotogether-workflow's verdicts reference |

## Run it, do not infer it

Run hello and read its output before reading the toolkit's docs; run
`gotogether help` and each command's `-h` rather than guessing flags.
