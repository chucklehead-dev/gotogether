# The first hour, the glossary and the first commands

From the gotogether-start skill (its SKILL.md says when to read this).

## The first hour

The `examples/...` paths in these skills are in the gotogether repository
(https://github.com/chucklehead-dev/gotogether/tree/main/examples), not in
your project: clone it to read and run them. A new HTTP service starts from `gotogether init -kind http NAME` (below);
a synchronous library starts from `gotogether init -kind library NAME`, with
ordinary observed-call tests and an immutable-snapshot source contrast;
in your own module, `go get
github.com/chucklehead-dev/gotogether@latest` (and `.../qhegel@latest`
for Hegel) and start from `gotogether scaffold`; the examples' `replace`
lines are only for working inside the repository. To run the CLI without
installing it, add it to the module as a tool (`go get -tool
github.com/chucklehead-dev/gotogether/cmd/gotogether`), then `go tool gotogether
...`: `go mod tidy` keeps a tool's requirements, where a plain `go get`
of the CLI is pruned by the next tidy (`missing go.sum entry for module
providing package go.opentelemetry.io/otel/sdk/trace`).

1. Run the smallest example: `git clone
   https://github.com/chucklehead-dev/gotogether && cd
   gotogether/examples/hello && go test -v .` (with quint on PATH).
2. Read it, in this order: `model/hello.qnt` (what may happen),
   `hello.binding.yaml` (recorded steps to the model's actions),
   `hello.go` (the system, written against qenv's seams), `hello_test.go`
   (the system explored by seeds, a mutant planted as an overlay in
   `testdata/mutants` caught, Hegel shrinking it).
3. For your component, write the model first and simulate its
   invariants (`gotogether simulate -binding`, `quint run --invariant`
   before a binding exists): gotogether-model.
4. Scaffold a binding and a System test, then record each model step
   where it takes effect, until the faithful system conforms.
5. Plant a mutant (a copy of a file in `testdata/mutants/<name>/`, its bug
   in it: `qtest.Mutants`) and state how it is caught (`Rejects`, `Mismatch`,
   `Violates`). A check no mutant can fail proves nothing.
6. Let Hegel shrink the mutant's failure (`qhegel.Shrink`), and carry the
   model to the real environment (`gotogether validate`).

## Glossary

- **Model**: the Quint spec, the ground truth. A failing check means
  fixing the code or, deliberately, the model.
- **Step**: one model action the code took, recorded with its arguments,
  its outcome and observations (`obs.<name>`) where it took effect.
- **Binding**: YAML mapping recorded steps to the model's actions, their
  arguments and expected post-states.
- **Run**: one execution's steps; with qenv, also the choices that made
  it (replayable).
- **System**: an application bound to its model (`qtest.System`): every
  workflow (Explore, Sweep, Fuzz, Mutate, Shrink) runs its `Run`.
- **Chooser**: what makes every choice of a simulated run: schedules,
  faults, delays. Seeded, Hegel's, a replay, a script.
- **Fault kind**: a named fault a double declares (`rpc.counter.request`:
  `drop`, `duplicate`). Option 0 of every choice is nominal: no fault, so
  shrinking removes faults.
- **Seam**: an interface the code uses for its environment (`qenv.DB`, a
  service, `qenv.Now`), so a double can stand in for it.
- **Claim**: what a test expects of its runs: `Conforms`, `Rejects`
  (a step the model cannot take), `Mismatch` (a state the model did not
  expect), `Violates` (an invariant), over runs `Some`, `All`, `None`.
- **Mutant**: the system with a bug on purpose, to show a check can fail.
- **Weaving**: rewriting code at build time (`qweave`, Orchestrion) to
  record steps or simulate `sync` without changing the source.

## A new HTTP service

```sh
gotogether init -kind http stock && cd stock && go test -short -v ./...
```

writes a module that passes as written (about 15 s after the first
build): `stock.go`, a handler recording each reservation where it takes
effect; `client.go`, a client that retries a refused connection and
nothing else; `model/stock.qnt` and `stock.binding.yaml` (its first line
names the binding's JSON Schema, for the editor); `sim_test.go`, the
service on `qenv/qhttp`, its server's process started before the client
(a client that starts first has every request refused until the server
calls Handle), `TestConforms` and `TestExercised` apart; `real_test.go`,
the same handler over `httptest` through `qobshttp.Handler`; and the
mutant `testdata/mutants/offByOne` (`>` for `>=`), caught as
`Some(Rejects("reserve"))`. Change the model first, then the handler and
the binding. grpc and queue kinds are not written yet: init names the
example to start from (examples/grpcledger, examples/streams).

## First commands

```sh
gotogether init -kind http stock                   # a new HTTP service's project, green as written
gotogether init -kind library snapshots            # a synchronous library, observing tests and an aliasing mutant
quint typecheck model.qnt
gotogether scaffold -spec model.qnt -module M -invariants safe -eventually complete -out m.binding.yaml  # choose your model's actual property names
gotogether scaffold -system -binding m.binding.yaml -pkg app -out system_test.go   # a qtest.System test to fill in (-http: on qenv/qhttp)
gotogether lint -binding m.binding.yaml -src .     # binding vs its schema and model, annotations vs binding
gotogether lint -sim .                             # code that escapes a simulation
gotogether validate -binding m.binding.yaml run.jsonl
```

The smallest simulated example is `examples/hello` (a counter, a lossy
network, a retrying client, one mutant): read it before the bigger ones.
Its shape is every simulated test's:

<!-- from examples/hello/hello_test.go -->
```go
func system() qtest.System {
	return qtest.System{Binding: "hello.binding.yaml", Run: Run, Mutant: qtest.Mutant()}
}

// lossy loses or duplicates one message in five.
var lossy = qenv.Rates{Default: 0.2}

func TestFaithfulConforms(t *testing.T) {
	qtest.RequireQuint(t)
	system().WithRates(lossy).Explore(t, qtest.ExploreConfig{Seeds: 30}).Expect(t, qtest.Conforms(),
		// ...
		qtest.Reaches("rpc.counter.request").Faulted().InAtLeast(0.3),
		// ...
		qtest.Exercised(),
		// ...
		qtest.Some(retried))
}

func TestMutant(t *testing.T) {
	switch qtest.Mutant() {
	// ...
	case "noDedupe":
		// ...
		system().WithRates(lossy).Explore(t, qtest.ExploreConfig{Seeds: 30}).
			Expect(t, qtest.Some(qtest.Rejects("apply")), qtest.Some(qtest.Conforms()),
				// ...
				qtest.None(retried))
		// ...
		qhegel.Shrink(t, system(), hegel.WithTestCases(50), hegel.WithSeed(1)).
			Expect(t, qtest.Rejects("apply"), qtest.ShrunkTo(1))
		// ...
	}
}

func TestMutants(t *testing.T) {
	// ...
	qtest.Mutants{}.Run(t)
}
```

In Go tests, `qtest.RequireQuint(t)` skips without quint; state what
each test expects with `.Expect(t, claims...)` on its run or
exploration, never by matching report text.
