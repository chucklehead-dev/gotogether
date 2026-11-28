- A `quint --version` probe that does not answer in time is an error naming
  its bound and `GOTOGETHER_QUINT_PROBE_TIMEOUT` (a Go duration, 30s unset;
  it was 10s), never a missing quint: `simulate -inductive` and
  `qtest.Inductive` reported it "not done: no quint (context deadline
  exceeded)" and the test skipped. The bound starts once the probe holds an
  admission slot, so waiting behind running proofs is no timeout; a quint
  that is not there is still not done. `gotogether doctor` and quint's
  admission report the same error.
- A counterexample to induction whose states Veil cannot decode ("the model
  has no post-state; ...", "the model's map domain jobs has non-enumerable
  keys") is a counterexample with Veil's reason in place of the states (no
  verdict; qtest fails the claim), not a run error.
- A proof by induction says what it covers: "for every value of the
  constants" only of those the proof module leaves open; an instance
  module's proof is "for this instance's constants only (N = 3, ...)", one
  that fixes some names both, and one with no constants says so
  (`model.InductiveVerdict.Open`/`Fixed`, the report's `open`/`fixed`).
- The lemma-negates-antecedent vacuity warning quotes the antecedent's text
  by characters, as quint's source map counts them: after a non-ASCII
  character it named checkout's `settled` " settle".
