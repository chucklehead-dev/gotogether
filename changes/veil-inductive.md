- `gotogether simulate -inductive[=a,b]` proves a binding's `simulate.holds`
  invariants by induction with quint's Veil backend (`quint verify
  --backend veil --inductive-invariant`), for every value of the constants
  (DESIGN §115). Outcomes are never conflated: proved; a counterexample to
  induction (no verdict: both states, the action, the ITF kept,
  `-inductive-out`); refused (outside Veil's fragment, Veil's reason; never a
  failure); undecided; not done (no quint >= 0.35 or no Veil, saying how to
  install it: gotogether never installs it). A proved implication is checked
  for vacuity: a lemma negating its antecedent, a settle flag step never
  sets, an antecedent the instance never reaches. `simulate.inductive`
  (`spec`, `module`, `lemmas`) names the proof module and each invariant's
  lemmas; `model.Report.Inductive`, `model.InductiveVerdict`,
  `model.VeilStatus`, `SimulateOptions.Inductive`/`InductiveOut`, and the
  report's checks of kind `inductive` (`DocCheck.Inductive`).
- `qtest.Inductive(inv).With(lemmas...)` claims the proof in
  `qtest.Model(...).Expect`: a counterexample to induction fails the test (no
  verdict); a refusal or a machine without Veil skips it, never fails it.
  It does not replace `qtest.Proves`.
- `gotogether lint -veil` (`-veil-errors`) notes what Veil refuses or proves
  only per instance, each with its rewrite (`model.veil.size`,
  `keysremove`, `matchmapby`, `maplit`, `fold`, `assume`), and holds
  invariants a settle flag step never sets makes vacuous
  (`model.veil.vacuous`); `lint.Options.Veil`, `VeilErrors`.
