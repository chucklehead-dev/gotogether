- The examples' holds invariants proved by induction with Veil (ROADMAP
  V3), each in a `TestModelInductive` (not done, skipped, without quint
  0.35 and Veil): for every value of the constants bank's and
  grpcledger's as they were, hello's `exactlyOnce` stated pointwise with a
  ghost bijection, jobqueue's, kvstore's and broadcast's with a ghost key
  set beside a map whose keys shrink, checkout's new `noOversell`,
  grpcledger's and retrystorm's under `assume`s on their constants, and
  migrate-payments' three, with lemmas in `<model>Proof` modules named by
  the bindings' `simulate.inductive`; wallet's `noOverdraft` for its
  binding's instance only (`walletInstance`). Settle-conditioned
  invariants step never settles (checkout's `settledCorrectly`, crossdb's,
  broadcast's `convergedWhenSettled`) are left out: a proof would be
  vacuous.
