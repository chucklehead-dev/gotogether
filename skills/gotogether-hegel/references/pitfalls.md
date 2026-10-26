# Pitfalls


- Hegel runs cases sequentially. With `hegel.WithConcurrency`, the rules
  of one case share its recorder. Record under the lock that orders the
  effect, so the recording order is the effect order.
- A test case that records nothing is vacuously fine: `AllowEmpty`, and
  the checker treats it as OK.
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
