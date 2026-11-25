- I/O specifications (ROADMAP CW12, P1; DESIGN §112): a step's effects
  are compared with its action's effects def in the model
  (`def payEffects(order: str): List[Effect]`, over the post-state), as one
  more conjunct of its expect, judged by quint and the Go evaluator; a
  difference is `Mismatch(action)`, its report naming the effects made and
  expected, the first differing effect and field, and the fix
  (`validate.Failure.Effects`, the report document's `effects`). The
  binding's `effects: { kinds: { Ctor: { from, where, fields, same } } }`
  maps recorded effects onto the model's constructors; an action's
  `effects:` names its def (default `<action>Effects`, `"-"` none) and
  `match:` its mode (`exact`, `retried`, `set`).
- Effect records in `gotogether.steps/1` (an optional record kind):
  `quint.effect` (the kind, its fields `quint.arg.*`, `quint.call` the
  invocation open where it was made) and, on a step, `quint.effects` (the
  effect records its task made before it; present, the step's effects
  were observed). `qobs.Effect`, `Recorder.Effect`, `Call.Scope` (an
  effect under it is the call's), `qobs.ObserveEffects`;
  `qtrace.Reconstruct` attaches them (`Step.Effects`, `Trace.Unattributed`,
  `qtrace.Effect`); `Recorder.Count` leaves them out.
- qenv's doubles record what the code sends, always: `Service.Call`
  (`rpc.<name>.request`, the request's fields by `qenv.EffectFields`;
  `Service.Effect` replaces it), `Queue.Publish`
  (`queue.<name>.publish`), simlog's `Produce` (`log.<name>.produce`),
  qhttp's Transport (`http.<server>.request`).
- `qtrace.Mutations` (and `qtest.MutateSteps`) gain `effect-drop`,
  `effect-dup`, `effect-swap` and `effect-corrupt`.
- examples/checkout: a `pay` step (a call around the capture's retries)
  with `payEffects`, the `CaptureUnits` mutant (caught only by them),
  `Config.NoCancels`; `NoIdempotencyKey` is now `Mismatch("pay")` at its
  first retry under a fresh key.
