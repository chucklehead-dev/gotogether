# From requirements to the model, and its first verdicts

From the gotogether-migration skill (its SKILL.md says when to read this).

## The model

- One action per operation, named after the domain outcome, with the
  parameters the code observes (gotogether-model's conventions).
- Each refusal an outcome value (`result: str` with `"ok"`,
  `"refunded"`, ...), its condition the action's guard for that value.
- The environment's doubles as weak as documented (gotogether-patterns
  §2): a processor that applies and loses the answer, a redelivering
  topic, a store at the isolation level production runs.
- Each requirement's comment beside what states it (requirements.md).

## The binding

`gotogether scaffold -spec model/payments.qnt -module payments -out
payments.binding.yaml` writes one entry per action, each parameter the
step field of its name. Map what the code records otherwise (an outcome
as `result: '{{.outcome}}'`, an observation as `'{{.obs.found}}'`), the
outcomes the errors name (`outcomes: { ok: '"ok"', conflict: '"conflict"' }`),
and size the constants per run with templates: `{{ set "key" }}` (the
set of a field's values), `{{ max "amount" }}`, `{{ count "refund" }}`
(an action's steps), `{{ procs }}`, `add`, `atLeast`. `gotogether lint
-binding payments.binding.yaml -src .` then says which bound actions the
code records nowhere, and which recorded ones the model lacks.

## Its scenarios and claims

The binding's `simulate` section is where the requirements are checked
on the model alone:

```yaml
simulate:
  possible:
    - name: R1 charge, capture, settle, refund
      steps: [charge, capture, settle, 'refund(result = "ok")']
    - name: R7 second refund refused
      steps: ['refund(result = "ok")', 'refund(result = "refunded")']
  holds: [refundedAtMostOnce, neverNegative]
  allows: [settledOncePerDay]   # the code must hold it; a bad implementation could break it
```

and, in Go, the same scenarios as claims over a simulation's runs:

```go
var refundTwice = qtest.NamedScenario("R7 second refund refused",
	qtest.Takes("refund").Outcome("ok"), qtest.Takes("refund").Outcome("refunded"))

func TestScenariosPossible(t *testing.T) { qtest.Model(t, "payments.binding.yaml").Expect(t, qtest.Possible(refundTwice)) }
```

`gotogether simulate -binding payments.binding.yaml` reports each
`possible` reached with its witness, or FAIL ("the model forbids it",
when the search saw the whole graph). A scenario the model forbids is a
requirement it misstates: fix the model before any code is judged.

## The first verdicts on existing runs

Before changing code, judge what the system already does:

- **Its tests**: wrap a legacy test's body in `qtest.Record(t,
  "payments.binding.yaml", func(*qobs.Recorder) { ... })` once the code
  records its steps (`qobs.Record` at each operation's effect, or
  `//quint:action` woven, so production code does not change), and
  `.Expect(t, qtest.Conforms())`.
- **Its traces**: `gotogether validate -binding payments.binding.yaml
  -convert spans.yaml spans.json`.
- **Its logs**: `gotogether validate -binding payments.binding.yaml
  sample.jsonl`; a log that starts mid-history needs a snapshot step or
  `resume:` (the gotogether-traces skill's partial-logs reference).

Each disagreement is one of three, decided with the owners and recorded
on the requirement:
- **the code's bug**: keep the run as a mutant's test, fix later under
  the model;
- **the model's mistake**: fix the model, say why in the commit;
- **an undocumented requirement**: add it, with the run as its source.

Then the migration is a model-first project like any other: the
gotogether-workflow skill's steps from "code" on, every change judged
by the model before it merges.
