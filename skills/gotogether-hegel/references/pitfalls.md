# Pitfalls


- Hegel runs cases sequentially. With `hegel.WithConcurrency`, the rules
  of one case share its recorder. Record under the lock that orders the
  effect, so the recording order is the effect order.
- A test case that records nothing is vacuously fine: `AllowEmpty`, and
  the checker treats it as OK. In a machine whose every rule can be refused
  (a refusal records nothing), a shrunk case of refusals alone is the first
  failure to expect: set `AllowEmpty`.
- The machine produces the whole run: every step the model checks,
  the environment's included. Where the code needs a value another
  component would produce (a publisher's publication), the machine
  produces it, often with a few lines that mirror the model's action.
  connect avoids that: the model's state hands the value to the code
  (`Step.Get`), and the environment's actions are `connect.Skip`.
- A separate test module for qhegel (`replace => ../..`, keeping Hegel and
  purego out of the service's module) needs `go mod tidy` after a root
  dependency changes, or `go vet` fails in it: tidy it in the same change,
  and let CI run `go mod tidy -diff` there.
- `hegel-go` needs Go ≥ 1.26.1. Its engine is embedded (no Python, no
  server), and runs offline after the first download, which it keeps
  under `os.UserCacheDir()`: where `$HOME` is read-only (a sandbox, some
  CI containers), set `XDG_CACHE_HOME` to a writable directory.
- `hegel.TestCase` has `Errorf` and no `Fatalf`, and its `Errorf` aborts
  the case (unlike `testing.T`'s): report a failure with `Errorf` and
  return nothing after it you need to run.
- For distributed systems, embed `hegelnet.Network` (examples/glomers)
  in the machine. Hegel then schedules deliveries, losses, partitions
  and timers next to the client requests. Settle at the end of each case
  (heal, quiesce, record `settle`), so properties conditioned on
  quiescence are checked.
