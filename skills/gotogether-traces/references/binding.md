# Binding rules, lint and analyzers

Rules:
- A parameter with no template is the step field of its name, rendered as
  a Quint literal. Set, list and map parameters (type aliases included)
  are converted from the recorded JSON by `toSet`, `toList` and `toMap`;
  one whose type holds a record or a sum (or a collection of
  collections) by `toQuint`, which decodes the field's JSON by a type of
  the model: `{{ toQuint "List[Event]" .obs.events }}` in an `expect`.
  Records are JSON objects (keys the fields' names, or Go's field names),
  every field and no other; a sum's value is `"Tag"`, `{"Tag": v}`,
  `{"tag": "Tag", "value": v}` or the payload record's fields beside
  `"tag"`, and `null` is `None`; a map is an object or a list of `[key,
  value]` pairs; a nil slice is empty; ITF's own encodings (`#bigint`,
  `#tup`, `#set`, `#map`) are read too. It refuses a number with a
  fraction and a time where an int goes, naming where: record an
  integer unit (cents, `time.UnixMilli`); a `str` takes any string. Two
  map keys that decode to one (`"1"`, `"01"`) and a key two record
  fields match are refused. `.step.seq` and `.outcome` can be passed. qobs records a slice or a map as JSON but a struct
  by `fmt.Sprint`: record a struct's `json.Marshal` text, or a slice.
  A string is written as Quint writes one, its characters as they are
  between quotes (Quint has no escapes: a tab, a newline, a backslash
  are themselves); one holding a double quote has no Quint literal and
  is refused, naming the field: record an id or a code instead.
  A constant is a Quint expression or a template over the whole run:
  `{{ set "key" }}` (the Quint set of a field's values across its
  steps; several names union them), `{{ max "amount" }}`, `{{ count
  "write" }}` (an action's steps), `{{ procs }}` (its processes),
  `add`, `atLeast` (`{{ atLeast 5 (max "amount") }}`).
  Give a template only to map a field differently, e.g.
  `args: { o: '{{.outcome}}' }`. An action that needs nothing else can
  be `name: {}`.
- A run that records two sides (a service's commits and its clients'
  calls) gives each side actors of its own (`acct`, `acct.clients`) and
  each a binding naming them: `actors: ['!*.clients']`, `actors:
  ['*.clients']` (globs, `*` crossing `/`, `!` excludes; none: every
  actor; the others are named in an info issue). In qtest,
  `sys.WithBinding("calls.binding.yaml", qtest.Linearize())` explores
  the same runs with the other binding.
- A failed step whose templates ignore `{{.outcome}}` must be mapped in
  the action's `outcomes` (`skip`, or `call` to replay it as a success).
  The tool refuses to guess.
- An invariant `A implies B` is vacuous in a run where A never holds;
  explorations find A in the model and `qtest.NotVacuous()` fails when no
  run met it. For another form, name the antecedent:
  `antecedents: { doneImpliesPerformed: 'jobs.keys().exists(j => jobs.get(j) == Done)' }`.
- To also emit a model-level ITF in-process, write argument templates as
  Quint literals (no operator calls). `expect` may be any Quint boolean.

A binding's JSON Schema (`schemas/gotogether.binding-1.json`, one per
format and major; conversion rules' `gotogether.convert-1.json`) lets an
editor's YAML language server check it as it is written: name it on the
file's first line, `# yaml-language-server:
$schema=https://raw.githubusercontent.com/chucklehead-dev/gotogether/main/schemas/gotogether.binding-1.json`
(or map `*.binding.yaml` to it in the editor's settings).

Then lint: `gotogether lint -binding bank.binding.yaml -src ./pkg`. It checks
the binding against its schema (every problem at its line, the nearest
key for a misspelled one), then against `quint typecheck`, the annotations against the
binding, and the literal action of every `qobs.Record` call against the
model (a misspelled action gets a did-you-mean).

In the editor and in CI, the same checks run as analyzers (qanalysis):
- `quintvet ./...` (built from `qanalysis/cmd/quintvet`, or
  `go vet -vettool=$(which quintvet)`) runs the simulation, weaving and
  annotation checks, and quintctx (a `context.Background()` where the
  function has a ctx or a test's t); `-quintaction.binding
  app.binding.yaml` adds the binding's. Findings with a mechanical
  rewrite carry it as a fix, which `quintvet -fix ./...` applies:
  quintsim's (`go f()` to `qenv.Go`, `time.Now` to `qenv.Now`),
  quintctx's (pass the ctx, `t.Context()`), quintweave's (`//qenv:go`
  above a function literal's `go`, a method value called from a
  function literal), quintaction's (a returned local named as the
  result, a misspelled parameter, a misspelled action named as the
  model's nearest in a directive or a `qobs.Record` call, a field the
  binding uses recorded from the parameter of its name). In golangci-lint,
  enable the `gotogether` module plugin.
- An annotation's expressions run on return: use parameters, the
  receiver and named results. quintaction names a local it sees.
- `actionmap -coverage summary.json ./...` (the summary of a woven
  coverage run) lists per action what its code can reach but no run ran,
  and what its steps ran that it cannot reach (callers' and setup code).

