# Writing a connect driver: skeleton, decoding, the TDD loop, debugging, fault scripts

From the gotogether-mbt skill (its SKILL.md says when to read this).

`connect.Config.FromEnv()` returns `(connect.Config, error)`. Explicit nonzero
numeric fields override their environment variables. Selected nonempty values
must parse without overflow; budgets are nonnegative (zero uses defaults), and
verbosity is 0–2. Invalid configuration fails before starting Quint or creating
the caller-selected trace directory, and existing trace files remain intact. Correct
the named field or variable rather than treating this as a model verdict.

## Model-based testing: Quint drives Go (package `connect`)

`connect` is the Go counterpart of the Rust quint-connect crate:
`quint run --mbt` generates random traces of the model; each trace is
replayed on a fresh driver; after every step the implementation's state is
compared with the model's.

## Skeleton

```go
type driver struct{ sut *Bank }

func (d *driver) Step(s *connect.Step) error {
	return connect.Switch{
		"init":    func(*connect.Step) error { d.sut = NewBank(); return nil },
		"deposit": func(s *connect.Step) error {
			return d.sut.Deposit(connect.MustPick[string](s, "account"), connect.MustPick[int](s, "amount"))
		},
		// "otherComponentAction": connect.Skip,
	}.Step(s)
}

// Compared after every step, init included. Only the fields of this type
// are compared: project the part of the model the implementation has.
type state struct{ Balances map[string]int }

func (d *driver) State() (any, error) { return state{Balances: d.sut.Balances()}, nil }

func TestModel(t *testing.T) {
	connect.Run(t, connect.Config{Spec: "model/bank.qnt", Main: "bankDesign", MaxSamples: 50, MaxSteps: 30},
		func(testing.TB) connect.Driver { return &driver{} })
}
```

Mapping from quint-connect: `#[quint_run]` → `connect.Run`; `#[quint_test]`
→ `connect.Test`; `switch!` → `connect.Switch` (key `"_"` is the catch-all);
`param?` → `connect.OptPick[T]`; `impl State` → `State() (any, error)`;
`Config{state, nondet}` → `Config.StatePath`, `Config.NondetPath`.

## Own each trace's resources

`connect.Run` and `connect.Test` take a `*testing.T` and a factory
`func(testing.TB) connect.Driver`. The factory receives the synchronous
`trace_<index>` child test, not the outer test. Use its `Context` for operations
and its `Cleanup` for server shutdown or joining owned workers. Go cancels the
child context before cleanup; cleanup completes before the next trace starts.
The factory handle exposes no `Parallel` method: replay remains sequential and
stops at the first failing trace. Do not cast the handle to start parallel tests.

Select a trace with `QUINT_SEED=0x... go test -run '^TestModel$/trace_7$'`.
Generation still runs once; unselected traces are neither read nor driven.
Native `go test -json` records child results, skips, and structured
`gotogether.trace_index` and `gotogether.seed` attributes. Generated trace counts
are not replay coverage; skipped/filtered traces supply no conformance evidence.
`connect.Replay` itself leaves the supplied driver's lifetime with its caller.

## Decoding model values (`itf.Decode`, used by Pick and State)

- Quint `int` → any Go integer (overflow is an error) or `*big.Int`.
- `str`, `bool` → `string`, `bool`.
- `List`, `Set`, tuple → slice or array (sets in canonical order); a `Set[T]`
  also decodes into `map[T]bool`.
- `a -> b` → `map[A]B`; a record → struct (fields matched by `itf:"name"`
  tag, else by name ignoring case and `_`; extra model fields are ignored).
- An enum (`type Phase = A | B`) → a `string` type; `Some(x)`/`None` → a
  pointer.
- A sum type with payloads → a struct with `Tag string \`itf:",tag"\`` and a
  `\`itf:",value"\`` field, or a field named like each variant.
- A type with `UnmarshalITF(itf.Value) error` decodes itself.
- A value read from the model's state for the code to use (an environment's
  output: `s.Get("pointer")`) decodes with `itf.DecodeAs[Pointer](v)` or
  `itf.Decode(v, &dst)`.
- A driver that compares the model's state with the code's unexported state
  (a field the API does not return) lives in the package, as an internal
  test file (`package snapshot`, not `snapshot_test`).

## The TDD loop (one action at a time)

1. Run the test. `unimplemented action "X" (picks: a, b)` names the next
   action to implement and the picks available.
2. Add the `"X"` case: decode the picks the action's parameters use, call
   the implementation, and return its error.
3. Run again. On `states diverge`, read the diff:
   `.Balances[bob]: model 16, implementation 17`, which is printed with the
   step, its picks and the model's next state.
   - If the model's state is right, fix the code.
   - If the projection is wrong (a field mapped incorrectly), fix `State()`.
     Its type's fields must be exported and named after the model record's
     (`itf:"-"` leaves one out): a field that compares nothing fails the
     check, naming the record's fields.
   - The spec is ground truth: change it only when the requirements say
     the model is wrong, and say so.
4. Repeat until a run with `MaxSamples` ≥ 50 passes. Then raise `MaxSteps`.

## Reproducing and debugging

- A failure prints `reproduce with QUINT_SEED=0x...`. These variables are
  read at run time, with no rebuild: `QUINT_SEED`, `QUINT_VERBOSE=1|2`
  (log every step, or every state too), `QUINT_MAX_SAMPLES`,
  `QUINT_MAX_STEPS`, `QUINT_BACKEND=rust|typescript`, `QUINT_BIN`.
- Each trace gets a fresh driver, but `init` still runs first: reset there
  too if the driver holds external resources.
- A handler's or a `CheckStep`'s panic (a failed `MustPick` or `MustEval`,
  an assertion) fails that step with its message.
- For something that is not a plain state comparison (learned id mappings,
  reading a store), implement `CheckStep(s *connect.Step) error`
  (`connect.StepChecker`).

## Comparing with the model's own definitions (`Step.Eval`)

A projection the model defines (`def balanceOf(a: str): int`, `val
leader`) is compared with the model's definition, not with a copy of it
in Go: `connect.MustEval[int](s, "balanceOf", "alice")` evaluates the
def in the step's post-state (the whole of `After`, whatever
`StatePath` says), each Go argument encoded with `itf.Encode`.

```go
func (d *bank) CheckStep(s *connect.Step) error {
	for _, a := range []string{"alice", "bob"} {
		if model, impl := connect.MustEval[int](s, "balanceOf", a), d.sut.Balance(a); model != impl {
			return fmt.Errorf("balanceOf(%s): model %d, implementation %d", a, model, impl)
		}
	}
	return nil
}
```

The Go evaluator computes it, on the model `connect.Run` or
`connect.Test` compiled once (with `Config.Require`'s compile when it is
set, else at the first `Eval`; a run that never calls it compiles
nothing). Steps of a plain `connect.Replay` have no model: replay through
`Run` or `Test`. Errors name the step, the def and the fix: `no definition
of` (a misspelling), `is a variable` (use `s.Get`), `takes 1 argument (a),
not 0`, `is undefined in this state` (the model's own error: a key the
map lacks), `outside the Go evaluator's fragment` (compute that one in the
driver).
- For concurrent implementations, make the effects schedulable: put a gate
  at the effect seam (each write blocks until the handler of that model step
  releases it). See `examples/edgepublish/mbt`.

## Two implementations of one interface (`connect.Differential`)

Two implementations that each agree with the model can disagree with
each other where the model never goes: golem's memory and Valkey stores,
on a negative budget. Replay both on the same traces:

```go
connect.Run(t, cfg, func(testing.TB) connect.Driver {
	return &connect.Differential{Impls: map[string]connect.Driver{
		"memory": &driver{store: newMemory()},
		"valkey": &driver{store: newValkey()},
	}}
})
```

A step fails where one implementation fails and another does not, or
where their observations differ: `Observe() (any, error)` (an
`Observer`: replies, errors chosen, more than the model's state), else
`State()`. Each `StateDriver` is still compared with the model, and each
`StepChecker`'s own `CheckStep` run, unless `IgnoreModel` is set: a
driver's check usually compares with the model, which a varied input
leaves behind. The design's own traces rarely reach the
difference, since its guards keep inputs inside it: generate traces of a
looser model (the same actions without their guards, as
`connect/testdata/bank_inputs.qnt` drops the balance check) and set
`IgnoreModel`, so its states only drive the inputs. An input every
implementation refuses is then agreement, not a failure.

Or keep the design's traces and vary their inputs: `Vary` runs once per
step before the implementations and may replace its picks (a copy), so
every implementation gets the same one. Inside Hegel, with
`qhegel.VaryTraces`, it draws them, and Hegel shrinks a disagreement to
the smallest input:

```go
qhegel.VaryTraces(ht, traces, func(tc hegel.TestCase) connect.Driver {
	return &connect.Differential{Impls: impls(), IgnoreModel: true, Vary: func(s *connect.Step) error {
		if s.Has("budget") {
			s.Picks["budget"] = itf.Value{Kind: itf.Int, I: big.NewInt(int64(hegel.Draw(tc, hegel.Integers(-100, 100))))}
		}
		return nil
	}}
}, qhegel.VaryOptions{})
```

## Driving a whole program through its faults (`qtest.FaultScript`)

A driver calls the code once per step. For a program that runs by
itself (a process with its own loop), let the trace choose only the
environment: its failures and crashes.

```go
faults := qtest.FaultMap{
	"writeFail": func(*connect.Step) *qtest.Fault { return &qtest.Fault{Kind: "journal.write", Option: 1, Recorded: true} },
	"restart":   func(*connect.Step) *qtest.Fault { return &qtest.Fault{Kind: "nemesis.crash.writer", Recorded: true} },
}
sys := qtest.System{Binding: "journal.binding.yaml", Run: func(c qenv.Chooser) error {
	s := qenv.New(c); defer s.Close()
	// ... deploy the program's processes
	return s.Run(time.Minute)
}}
sys.FollowTraces(t, connect.Config{Spec: "journal.qnt", MaxSamples: 8}, faults).Expect(t, qtest.Conforms())
```

`FollowTraces` generates the traces, scripts each one's faults
(`qtest.FaultScript`), runs the system on the script, fails a run that
could not follow its trace (`Script.Remaining`), and checks every run.

- Each mapped step is a fault: a fault point's or a double's kind and
  option, or `nemesis.crash.<process>`. It fires at the first choice of
  its kind once the run has recorded the steps before it in the trace.
  No other fault happens, and the schedule is nominal (or `sc.Base`'s).
- `Recorded` says whether the program records the step itself (a failed
  write, a restart). Steps it does not record don't count toward the
  next fault's wait.
- A fault left in `Remaining()` means the program could not follow its
  trace. A model check that
  fails means it followed the trace's faults but did the wrong thing
  (connect/faults_test.go: a restart that forgot the durable count).

## Drivers for one part of a system

From golem's use (#144):

- **A driver for one action.** `connect.Switch{"publishSnapshot": check,
  "_": connect.Skip}`: `"_"` handles every action the switch does not
  name. A driver that does not implement `State()` (is no
  `connect.StateDriver`) is compared with nothing: return no projection
  by not having one, not by returning `nil`.
- **The model as an oracle.** For a pure function (a ledger in, a snapshot
  out), compute the function at every state the model reaches and compare
  it with what the model's own action computed (`s.Get` after the step).
  The model's traces bring the inputs no hand-written case would: out of
  order, after a failover, at any point.
- **Bind the methods instead of writing handlers.** `connect.Bind(newImpl)`
  makes the implementation at `init` (once per trace, with the trace's
  `testing.TB`); `.Action(name, (*T).Method, picks...)` binds an action,
  its picks named in the method's parameter order (Go cannot read
  parameter names), each decoded into its parameter's type. A leading
  `context.Context` is given the trace's; a pointer parameter is
  optional, nil when the model did not pick it; a final `error` fails the
  step. A result besides the error must be checked against the model's
  def (`.Returns(name, def, picks...)`, through `Step.Eval`), or the
  driver refuses to start. `.Handle(name, func(T, *Step) error)` takes an
  action no method fits, `.Skip(names...)` another component's,
  `.State(project)` and `.Check(func(T, *Step) error)` the projection and
  a check after each step; `b.Driver` is the factory `connect.Run` takes.
  A malformed binding (a count of picks not the method's, a method of
  another type, an action bound twice) panics where it is built, naming
  the fix. examples/bank's `TestModelDrivesBank` is bound this way.
- **Reach the state that matters.** Name a boolean val true where the
  bug could show (a publication with a gap in the ledger) in
  `Config.Require`: the run replays the shortest path to it first, counts
  the sampled states where it held, and fails when nothing reached it.
  golem's mutant passed at 100 samples, where 26 of 4,609 publications had
  a gap. Require the state *after* the step under test, not the one
  before it: a witness ends at the state it reaches, so the step whose
  behaviour that state was meant to exercise is never replayed. golem
  required "a probe in flight and a call waiting that has tried every
  deployment" (the state just before the gateway chooses where the call
  goes): the witness stopped there, and a mutant ignoring the probe
  passed. "That call's last attempt sent past the probing deployment",
  the state after the choice, caught it.
- **Code that makes its own choices.** Where the code chooses (which
  deployment an attempt takes, when a call gives up) and the model leaves
  the choice open, give the model a switch (golem's `ORDERED`) that makes
  the instance the test drives choose as the code does, while the
  design's variants keep every choice open. The driver then compares the
  code's choice with the model's step instead of making it.
- **Arrange the clocks a guard needs.** Where the model enables an action
  the code guards by time (`takeLease` fires whenever; the code takes a
  lease only once it has lapsed by the taker's clock), give each replica
  its own clock and move the taker's past the lease before the code
  tries, as golem's elector driver does. A clock the driver leaves still
  (a paused leader's) keeps that node's belief, as in the model.
- **Find the round trips inside one action.** `actionmap -roundtrips
  ./...` (qanalysis/cmd/actionmap) lists each `connect.Switch` action
  whose code makes more than one call into shared state (a store's or a
  client's method taking a context): golem's `complete` reaches the
  store's Complete and Add, then a reacquire and a replay. Each list is an
  interleaving no connect trace makes; split the action there or run the
  code in qenv.
- **History ghosts belong in the driver.** A model's ghost (every call
  ever waived) is the driver's to accumulate from the steps it drives;
  code that keeps one to be projected keeps it for ever (a leak in
  production).
- **When the code chooses.** Where the model picks (`replayOne(gw, e)`)
  and the code decides on its own (FIFO), make the code's choice
  controllable from the test, or let the code drive and the model judge
  (qhegel, trace validation): a driver that rearranges the code's internal
  queue to match the model's pick tests the rearrangement.
- **Virtual time.** quint is external I/O, which a `testing/synctest`
  bubble cannot wait on. Generate and read the traces outside the bubble,
  then replay them inside it, where the code's timers run on the bubble's
  clock:

  ```go
  files, cfg, err := connect.GenerateRun(t.Context(), connect.Config{Spec: "model.qnt", MaxSamples: 50})
  if err != nil {
  	t.Fatal(err)
  }
  traces, err := connect.ReadTraces(t.Context(), cfg, files)
  if err != nil {
  	t.Fatal(err)
  }
  synctest.Test(t, func(t *testing.T) {
  	for _, tr := range traces {
  		if err := connect.Replay(tr, newDriver(), connect.ReplayOptions{}); err != nil {
  			t.Fatal(err)
  		}
  	}
  })
  ```
- **Skipping without quint.** `qtest.RequireQuint(t)` skips a connect
  test as qtest's and qhegel's own skip.
- **Ablation from your repository.** A mutant is a file of
  `testdata/mutants/<name>/` built in its package's place; `qtest.Mutants`
  runs any test (a connect test too) in each mutant's binary
  (examples/hello's `TestMutant`). qweave `-mutate` generates them for a
  `qtest.System` (gotogether-faults' coverage-and-mutants reference).

## Limits

- Model-driven testing never exercises what the model forbids: the model
  does not take disabled steps. To find code that accepts forbidden inputs,
  let Hegel drive and the model judge (see the gotogether-hegel skill).
- A driver that applies one model step to every instance at once (every
  gateway takes the new snapshot in one call) hides the skew between them,
  as the model's own atomic step does (gotogether-patterns, §2): drive one
  instance per step, as the model should take them.
- `quint test` traces have no `mbt::` annotations. Use `connect.Run`, or
  `NondetPath` when the spec records the action in its state (Choreo's
  `extensions.actionTaken`).
- When the model chooses where the implementation decides on its own
  (the model leases any pending job, the queue always the oldest), model
  traces ask for steps the code cannot be made to take. Either make the
  choice controllable (a test hook), or check the code's own runs instead
  (trace validation, Hegel). See `examples/jobqueue`.
