- qenv's doubles record a send wherever `qobs.Effect` would: under an open
  call's scope (`Call.Scope`) on that call's recorder, whatever the
  simulation's context carries, else on the recorder `Record` would use.
  `Service.Call`'s default request and `Queue.Publish` once asked
  `qobs.From` alone, so a call invoked on a recorder the simulation did not
  carry lost them (its scope's `qobs.Effect` recorded). A pending call now
  keeps such a send; one after `Done` is Late.
- `qobs.EffectFunc(ctx, build)` records an effect as `qobs.Effect` does,
  calling `build` for its kind and fields only when a recorder takes it: a
  double's send that no run records builds and allocates nothing.
