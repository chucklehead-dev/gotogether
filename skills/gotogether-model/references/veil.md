# Proving invariants with Veil (simulate -inductive, qtest.Inductive)

quint 0.35's Veil backend proves a holds invariant by induction for every
value of the constants: init establishes the inductive invariant (the
invariant and its lemmas) and every action of `step` preserves it. It
needs quint >= 0.35 and `quint veil install` (about 5 GB and 10 minutes,
once; gotogether never installs it). Without them the claim is not done,
never failed. It adds to `qtest.Proves` (the instance's graph), never
replaces it: claim both.

```yaml
simulate:
  holds: [doneImpliesPerformed]
  inductive:
    spec: model/jobqueue.qnt      # default: the binding's spec
    module: jobqueueProof         # default: the binding's module
    lemmas:
      doneImpliesPerformed: [enqueuedKeys, workingEnqueued, finishedPerformed, performedNonNeg]
```

```sh
gotogether lint -veil -binding m.binding.yaml   # the idioms Veil refuses, with rewrites
gotogether simulate -binding m.binding.yaml -inductive          # every holds invariant
gotogether simulate -binding m.binding.yaml -inductive=a,b -inductive-out cti/
```

In a test: `qtest.Model(t, b).Expect(t, qtest.Proves("p"),
qtest.Inductive("p").With("lemma1"))`.

## Write the model so Veil can prove it

- **Pointwise, universally quantified invariants.** State a property of
  each element (`JOBS.forall(j => ...)`), not of a set's size or a fold.
  `S.size() == 0` is `S == Set()`; `S.size() <= 1` is
  `S.forall(a => S.forall(b => a == b))`. A count equal to a set's size
  (`count == applied.size()`) becomes a ghost bijection between the set
  and `1.to(count)`, but that bijection records the order of arrival and
  grows the state graph factorially. Where the instance's graph is the
  model's proof (examples/hello's tutorial), keep the size and leave the
  invariant to `Proves`.
- **Ghost key sets, total maps.** Veil's maps are total: a map whose keys
  shrink (`m.keys().exclude(Set(k)).mapBy(...)`) cannot be proved. Keep a
  ghost `Set[K]` of live keys beside the map, or make the map total over a
  constant domain (`JOBS.mapBy(_ => Absent)`) with a value meaning absent.
  This is Veil's limitation, not a fault of the model. A ghost key set
  leaves the map, and so the binding, as it was (examples/jobqueue,
  kvstore, glomers' broadcast: `live.exclude(Set(k)).mapBy(...)`).
- **No match inside mapBy.** Compare with the constructors (`if
  (status.get(k) == Done) ...`), or match outside it.
- **No map literal with variable keys.** `Map(src -> -1, dest -> 1)` may
  have equal keys: write `Map(src -> -1).put(dest, 1)`.
- **Assume constant ranges in the main module.** `assume stockNat = STOCK
  >= 0` where Veil proves it: an assume in an imported module is ignored
  (restate it in the proof module under a name of its own).
- **Settle-conditioned properties are eventual, not holds.** `settled
  implies P` where step never settles holds vacuously, and its proof says
  nothing: list it in `eventually`. simulate warns of it (and of a lemma
  that negates an antecedent, and of an antecedent the instance never
  reaches).
- **Lemmas in a `<model>Proof` module.** Zero to four lemmas usually do;
  keep them in a module that imports the model (`module jobqueueProof {
  import jobqueue.* ... }`), so the model stays the specification.

## What Veil cannot do

- Folds and sizes over a constant set, for every value of the constant
  (`JOINT.fold(...)`): prove them on the instance (`qtest.Proves`), or
  keep the fold's value as a ghost each action updates.
- List folds over state logs (grist's generated models): refused.
- Records in sets or as map keys (Choreo's messages, edgepublish's
  manifests): refused.

## Results

| Line | Means | Next |
|---|---|---|
| `proved   p: inductive, so it holds ... for every value of the constants N, ...` | a proof for all instances | keep `qtest.Inductive` beside `Proves` |
| `proved   p: ... for this instance's constants only (N = 3, ...), not for every value of them` | an instance module's proof: these values alone | prove the base module where Veil can, else keep it as the instance's |
| `warning: vacuous? ...` | the proof may say nothing | make the property eventual, or let step reach its antecedent |
| `no verdict p: not inductive: a counterexample to induction after a` | a state satisfying the invariant that `a` leaves; p may still hold | read both states, add a lemma excluding the first (unreachable) one |
| `... at the initializer` | init's state is outside it | `simulate -graph`: p itself may be violated |
| `its states: Veil could not show them: <reason>` | a counterexample to induction whose states Veil cannot decode (no post-state; a map keyed by int) | as for any counterexample; key such a map by str or an enumeration to see them |
| `simulate -inductive: cannot ask quint ...: quint --version did not answer within 30s` | quint is there, the machine is loaded: an error, not a skip | `GOTOGETHER_QUINT_PROBE_TIMEOUT=2m`, or fewer proofs at once |
| `refused  p: not in Veil's fragment: <reason>` | outside Veil's fragment; no failure | `gotogether lint -veil`, rewrite as above |
| `not done p: ...` | no quint 0.35 or no Veil | install as it says, or rely on `Proves` |
