- `connect.Bind(...).Effects(binding.Effects{...})` compares what each step
  of a model-based test sends with the model's `<action>Effects` def
  (`initEffects` for the constructor), as validate compares a recorded
  step's (DESIGN §115, golem's ask on #144): effects recorded through the
  trace's context (a method's `context.Context`, the new `Step.Context`,
  the new `connect.BindContext`'s constructor; `qobs.Effect`, a qenv
  double in a simulation made under it) while the step ran, rendered by
  the same mapping, normalized by `EffectsOf(action, def, match)`'s mode
  (exact, retried, set; `"-"` compares none), and judged in the trace's
  post-state by the Go evaluator, on quint's path and the compiled
  model's. A difference is a `StepError` of kind `"effects"`
  (`StepError.Effects`, a `connect.EffectsMismatch`), reported before the
  step's own error, its Returns or Observes check, the state and Check;
  an effect recorded after a step returned fails the next step, naming
  it. The mapping, each def's parameters and result type, and each
  constructor's fields are checked before any trace. A send through
  `context.Background()` is not recorded (a missing send fails, an extra
  one passes).
- `binding.Effects.Check(def, call, match, made)` is the effects'
  normalization validate's plan and connect share, and
  `binding.EffectCheck.Undeclared` reports a step's mapped effects where
  its action has no def. `validate.EffectsMismatch` is now an alias of
  the type connect reports too, which gains `Undeclared` (`undeclared` in
  a report's JSON), `Headline`, `Render` and `Action`, and two fields no
  report carries (`Mapping`, `Drop`).
- Verdict change in validate: with an effects mapping (`effects.kinds`),
  a step of an action that has no effects def (none in the model, no
  `effects:` of its own) and made an effect of a mapped kind is now
  `Mismatch(action)` ("place made effects, and the model states none (it
  has no placeEffects)"), where it passed: the model states no effects
  for it, so none is its specification, as in connect. Opt such an
  action out with `effects: "-"`, or state its effects. No example's
  verdicts changed: checkout's captures are all `pay`'s.
