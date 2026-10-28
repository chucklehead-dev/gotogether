---
name: gotogether-start
description: Start here for checking Go code against a Quint model with gotogether - picks the workflow (model-based tests with connect, Hegel with qhegel, trace validation with qobs/binding/validate, simulated fault injection with qenv and qtest.System, metastability with qload/qmeta, coverage, mutants, sweeps, fuzzing, Choreo models, grist apps) and the skill to load next. Use when a task mentions Quint and Go together and it is not yet clear which part of the toolkit applies, or when asked what the toolkit can do.
---

# gotogether: which tool for which question

Find the question, then load the skill it names.

| Question | Skill |
|---|---|
| I have code and no model; what should the model allow? | gotogether-model, gotogether-patterns |
| Does my code do what the model's traces do? | gotogether-mbt |
| Does the code accept inputs the model forbids? | gotogether-hegel |
| Do runs of the real system conform? Is it linearizable? | gotogether-traces |
| Is it correct under crashes, lost responses, isolation levels? | gotogether-faults |
| Does it recover after a spike or a slow dependency? | gotogether-metastability |
| An event-sourced app whose model is generated | grist |
| Starting a component, setting up CI, or a test failed: whose bug? | gotogether-workflow |

## Steps

1. Run the smallest example: `git clone
   https://github.com/chucklehead-dev/gotogether && cd
   gotogether/examples/hello && go test -v .` (quint on PATH).
2. Read `model/hello.qnt`, `hello.binding.yaml`, `hello.go` and
   `hello_test.go`, in that order.
3. Write your component's model first and simulate it (gotogether-model).
4. Scaffold a binding and a System test (`gotogether scaffold`), record
   each step where it takes effect until the faithful system conforms.
5. Plant a mutant (`testdata/mutants/<name>`) and state how it is caught.
6. Shrink its failure with Hegel, then validate real runs.

## References (when to read each)

- [references/questions.md](references/questions.md): the full table,
  each question with its tool (linearizability, liveness, journeys,
  external drivers, coverage, gosim, weaving, Kubernetes, PCT, fuzzing).
- [references/first-hour.md](references/first-hour.md): the first hour
  in a module of your own, the glossary, the first commands and the
  shape of every simulated test (hello's).
