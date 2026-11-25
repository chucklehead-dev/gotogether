# Code you cannot rewrite against seams: weave it

If the code uses `sync.Mutex`, `sync.RWMutex`, `sync.WaitGroup` or `go`
statements and you want it unchanged (a stand-in that must track the
real code, or the real code), weave it:
- add `_ "github.com/chucklehead-dev/gotogether/qenv/weave"` to the
  project's `orchestrion.tool.go`. Then test with qweave (`qweave go test
  ./...`, from the qweave module in this repository), or with
  Orchestrion (`go run github.com/DataDog/orchestrion go test ./...`);
- mark each go statement `//qenv:go` (its call must be a function
  literal). Under qweave that is all: it weaves lock method values
  (`return mu.RUnlock`) and calls promoted through an embedded lock too.
  Under Orchestrion, also turn method values into calls
  (`func() { mu.RUnlock() }`) and name embedded locks
  (`mu sync.Mutex`). `gotogether lint -weave DIR` lists what Orchestrion
  misses;
- code started in a woven goroutine uses `context.Background()` (or the
  goroutine's own context), not one captured from the task that started
  it: qenv resolves a context-less call to the goroutine's own task, and
  another task's context makes it sleep and wait as that task;
- pass the code its clock if it takes one (`Now: s.Now`);
- skip simulation tests in unwoven builds. A `//quint:action` probe that
  records only when woven tells you which build you are in
  (examples/edgepublish/sim `Woven`).

Inside a simulation the woven locks are scheduling points, so seeds and
Hegel explore their interleavings. Outside one, the real methods run.

## Lock invariants

A property of the state a mutex guards that must hold whenever the lock
is released (Gobra's mutex invariant, checked rather than proved): attach
it in the run, after `qenv.New`, from a task or the goroutine that made
the simulation:

```go
s := qenv.New(c)
qenv.Guard(s.Context(), &acct.mu, "ledgerMatchesBalance", acct.consistent)
```

- `holds` runs at every woven `Unlock` of that `*sync.Mutex` or
  `*sync.RWMutex` (not `RUnlock`), the lock still held. It may be false
  inside the critical section; at the release it must be true, or the
  task panics with `qenv: lock invariant <name> (guarded at <file:line>)
  broken at Unlock (<file:line>) by task <task> (step <n>)`, and the
  exploration fails that run, replayable by its seed. Through a method
  value (`unlock := l.Lock()` returning `mu.Unlock`) the site is the
  line that made it.
- `holds` only reads: a seam inside it (Yield, Wait, Sleep, Go, a choice,
  a lock, Notify) or a recorded step panics with the fix, so a guarded
  run makes the choices of the unguarded one.
- It checks an internal invariant the model does not see (an index that
  matches its rows, a ledger that sums to its balance): what the model
  sees, a binding already checks.
- Outside a simulation it does nothing; under gosim it refuses (gosim's
  `sync` reaches no qenv Unlock); guards end with the simulation's Close.

With qweave, declare it on the field instead, and nothing registers it:

```go
type Account struct {
	mu   sync.Mutex //quint:guards conserved
	a, b int
}

func (x *Account) conserved() bool { return x.a+x.b == 100 }
```

- The directive names methods of the struct (`func() bool`, several
  checked in order), in the field's line comment or its doc. qweave weaves
  every `Unlock` of that field (a call, a defer, a method value) into one
  that checks them as Guard's are, bound to the struct the Unlock reached
  it through: zero values and every constructor are covered. The error
  names `Account.conserved`, guarded at the field's line.
- A use of the field other than as its methods' receiver (`&x.mu` handed
  on, a `sync.Locker`, `sync.NewCond`, a copy) is refused: an Unlock
  through it would go unchecked. Use `qenv.Guard`, which sees every
  simulated Unlock of the mutex, for such a lock. Embedded and pointer
  locks are refused too, as are unexported invariants of an Unlock in
  another package.
- qweave only: an Orchestrion build weaves the plain Unlock and checks
  nothing (`gotogether lint -weave` flags the field); under gosim's tool
  file (no qenv/weave) qweave weaves nothing and reports each field
  skipped (`qweave -v`).
