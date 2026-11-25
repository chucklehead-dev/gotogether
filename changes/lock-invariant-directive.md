- `//quint:guards <method>...` on a `sync.Mutex` or `sync.RWMutex` field
  declares a lock invariant without registering it (ROADMAP LI2): qweave
  weaves every Unlock of the field (a call, a defer, a method value) into
  the new `woven.MutexUnlockGuarded` or `RWUnlockGuarded`, which checks the
  named methods of the struct (`func() bool`, bound to the struct the
  Unlock reached the field through) as `qenv.Guard` checks its invariant:
  just before the release, the lock held, every seam refused, a false one
  panicking the task with a `*qenv.LockInvariantError` named
  `Type.method`, guarded at the field's line. Zero values and every
  constructor are covered. qweave refuses a use of the field other than
  as its methods' receiver (its address taken, a copy: an Unlock through
  it would go unchecked), an embedded or pointer lock, a directive naming
  no `func() bool` method, an unexported invariant of an Unlock in another
  package, and a core whose `woven` lacks the guarded unlocks (an added
  ABI1 function, no rename). qweave only: `gotogether lint -weave` flags
  such a field (an Orchestrion build checks nothing), and under gosim's
  tool file each field is reported skipped.
