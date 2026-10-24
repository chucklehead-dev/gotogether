# gosim, the native runtime and weaving (qgosim, qweave, qenv/weave, qcover, qmut)

Gotchas for agents (and humans) before changing qgosim, qweave, qenv/weave, the native runtime, coverage or mutants; AGENTS.md has the general ones.

- `qweave -cover` instruments the main module only, never the toolkit's
  own packages (examples aside): qcover's `Hit` would count itself. A
  block's code is attributed to its task's next step, so a client whose
  step a service records shows as "run by no step": read that list as
  code no step of its own task explains, not as dead code.
- `qweave -mutate` switches mutants through qrun, like `-cover`, so it
  mutates the main module only (or `-coverpkg`). A survivor is often an
  unobserved effect, not unneeded code: a step that records nothing the
  binding compares (checkout's cancel never carried the stock). Mutants
  equivalent under the environment (dropped acks, retries' boundaries)
  survive by right: read the report, do not assert a score (in an
  example's System.Mutate).
- The toolkit's own packages are mutated only when `-coverpkg` names
  them, never qmut, qrun, qcover, internal/hooks or qenv/goroutine.go (the
  code a mutant's switch calls to find its run: mutated, it called
  itself). `GOTOGETHER_MUTANT=<id>` turns one mutant on for the whole
  process (unit tests carry no run), `=list` prints them;
  `ci/mutate-core.sh <pkg> [max]` runs a package's tests once a mutant
  (`JOBS=n` at a time). There the nightly does hold a bar: a core
  package's survivors are each read, and the matrix's `max` counts the
  equivalent ones.
- Woven code (`qenv/weave`): weaving rewrites calls of `sync` methods in
  the root module and `//qenv:go` go statements. A method value
  (`return st.mu.RUnlock`) or an unmarked `go` escapes, so check with
  `gotogether lint -weave DIR`. So does a promoted call through an embedded
  `sync.Mutex` (`t.Lock()` on `struct{ sync.Mutex }`): Orchestrion
  matches the receiver's type, so name the field. The lint flags the
  embedding. Inside a simulation, every user of a woven
  object must be one of its tasks (call `Close` from a task, not from the
  test goroutine). Woven calls look up the goroutine (about 1 µs), so
  weave test builds only.
- gosim (`qgosim`, `examples/gosim`) translates everything the simulated
  package imports: keep Hegel and other native code out of it. Hegel
  adapters live in qhegel (`Chooser`, `FindTrigger`), and the Glomers one in `maelstrom/hegelnet`, never
  next to code a simulation runs. `internal/simdeps` fails if qenv, qobs,
  qtrace, qload, qmeta, any package that imports qenv (qhegel aside)
  or a simulated example depends on Hegel, purego or cgo, and names the
  import chain. It writes the
  translation into the module's `.gosim/`, which `gofmt -l .` at the root
  reports: it is gitignored, ignore it.
- qenv's native runtime (`qenv.NewNative`) keeps one-task-at-a-time
  semantics with a lock the running task gives up at seams, and a
  condition variable broadcast when it does. A waiter that wakes and is
  not ready must not broadcast again: two waiters would wake each other
  forever, and gosim's virtual time would never pass. Its processes'
  tasks start on the process's machine (a spawn queue its main goroutine
  serves), and a machine crash is requested before the crash hooks run,
  which may restart it.
- Without a Platform, the native runtime's `Close` waits for every
  task's goroutine to exit (a sleep ends at Close), so a simulation in a
  `testing/synctest` bubble leaves none behind (the bubble panics if one
  remains). A task blocked outside a seam (a real lock, a channel) would
  hang Close: keep tasks' waits in qenv's seams.
- The gosim fork is its own module, github.com/chucklehead-dev/gosim:
  upstream history plus our commits, its `FORK.md` saying what they
  change. Change the fork there, push, then bump qgosim's and
  examples/gosim's requirement (`go get github.com/chucklehead-dev/gosim@<commit>`).
  It translates one Go release's standard library (Go 1.26) and refuses
  another. Choosers with entropy
  (`qenv.EntropyOf`: Seeded, qhegel.Choices, a ChoiceLog or Replay of
  them) hand gosim bytes in chunks for every choice but faults. A chooser
  without it (a plain wrapper) falls back to one round trip per choice,
  about 9 times slower.
- Weaving is qweave's (its own module, x/tools): `qweave go test ./...`
  weaves the package graph with the aspects of the packages the tool file
  imports and runs the go command with `-overlay`; qgosim hands gosim the
  same overlay (`GOSIM_OVERLAY`). For gosim, the module's `gosim.tool.go`
  (else `orchestrion.tool.go`) names them: list qobs for `//quint:action`,
  never qenv/weave (gosim simulates sync itself). qweave weaves method
  values and promoted calls that Orchestrion misses; `gotogether lint -weave`
  is about Orchestrion builds. A new join point or advice goes in
  `qweave/weave.go`, with a fixture test. On the native runtime with a
  Platform, qenv takes no lock across user code (gosim is cooperative):
  never put a real lock or a multi-send loop where a crash or a goroutine
  switch could strand it (DESIGN §15.8), and compare times without
  monotonic readings (`Sim.Now` already drops them).
