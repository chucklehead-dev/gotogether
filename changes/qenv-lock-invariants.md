- `qenv.Guard(ctx, mu, name, holds)` attaches a lock invariant to a
  `*sync.Mutex` or `*sync.RWMutex` in the simulation ctx runs in (after
  Gobra's mutex invariants, checked at run time rather than proved):
  `holds` runs at every release of the lock's exclusive hold by a task
  (woven code's `Unlock` under qenv/weave's aspects or qweave, on `New`'s
  scheduler and the native runtime without a Platform; not `RUnlock`), the
  lock still held, so a critical section that leaves its state broken
  fails at the Unlock that published it: the task panics with a
  `*qenv.LockInvariantError` (`qenv: lock invariant <name> (guarded at
  <file:line>) broken at Unlock (<file:line>) by task <task> (step <n>):
  the critical section left it false; fix the code between Lock and
  Unlock`), which `Sim.Run` re-raises and `System.Explore` reports as a
  failing run its seed and the corpus replay. The invariant may be false
  inside the section. A seam (Yield, Wait, Sleep, Go, a choice, a lock,
  Notify) or a recorded step inside `holds` panics with the fix, so a
  passing guarded run makes the unguarded run's choices. The guards are
  the simulation's (a `qrun.Key` in its context) and end at `Close`;
  outside a simulation `Guard` does nothing, and under gosim (a Platform),
  whose sync no qenv Unlock sees, it refuses.
