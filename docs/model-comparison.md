# Comparing model behavior

`gotogether model-diff` compares two finite model instances on an explicit list
of observed variables. Use it to review a changed guard or a model mutant before
trusting it to judge code. It does not infer the intended contract: an added
behavior might be a deliberate feature or an accidentally weakened model.

```sh
gotogether model-diff -before old.binding.yaml -after new.binding.yaml \
  -observe phase,attempts -states 1000 -pairs 10000 -json
```

Both bindings must expose the same named observation variables. The nonempty
`-observe` list is required; duplicates and missing variables are errors. A shared
`-const NAME=expr,...` selects the finite instance, overriding each binding's
constants. The report records the compared bindings, modules, constants,
projection, graph bounds and each directional comparison's extent.

The compared language consists of every finite sequence of projected states,
including the initial state and one observation after every transition. Action
names, fairness and infinite-run liveness are not compared. A hidden variable
updated alongside existing transitions can leave behavior unchanged. A new
hidden-only transition adds an observable repeated state and can change behavior:
there is no implicit stutter hiding or refinement mapping.

The product search tracks a concrete source state and every target state still
compatible with the entire observed prefix. This matters for hidden
nondeterminism: matching each projected edge separately can invent a target path
by combining incompatible hidden states. The two directions are separate:
`added` is behavior admitted after the change but not before; `removed` reverses
the models. Witnesses are shortest by number of model transitions.

The repository acceptance case is a finite handshake with one optional retry.
Compare `validate/model/testdata/compare/before.yaml` with `after.yaml` to find
an acknowledgment admitted before sending; compare `before.yaml` with `ghost.yaml`
on `phase,retries` for unchanged observed behavior despite internal updates.

Both graphs must be complete before this slice compares them. A state or depth
cut, unsupported evaluator construct, or model runtime error is inconclusive.
`-pairs` separately caps the product nodes in each directional inclusion search.
`-work` caps target-state candidate processing; the report records its actual
usage and identifies which budget cut a direction. Target subsets are shared
internally rather than copied for every source state.
Exhausting that budget is also inconclusive, never evidence of equivalence.
An equivalence result applies to these finite instances, these projected state
sequences, and the Go evaluator's supported semantics. It is not a proof over
arbitrary constants or a general refinement proof.

A witness retains its projected sequence, concrete source states and an exact
Quint replay. Native replay checks original `init` and `step`, with every full
state asserted. A successful replay confirms source admission. Exclusion rests
on the complete target graph and product search; a failed nondeterministic native
sample cannot establish target rejection. If no candidate witness can be confirmed
natively, the comparison remains inconclusive and retains the explicit evidence.
The report keeps these two evidence bases separate.

Text and `-json` derive from the same comparison result. JSON uses the current
`gotogether.report/2` document's typed `comparison` field rather than a second
format. Exit 0 means equivalent for the explicit instances, 1 means a difference
with source replay evidence, and 2 means inconclusive or an operational error.
Inspect per-direction statuses and native replay evidence, not just the exit code.
