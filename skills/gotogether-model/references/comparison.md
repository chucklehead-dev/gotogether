# Reviewing a model change

Run `gotogether model-diff -before old.binding.yaml -after new.binding.yaml
-observe phase,attempts -json` (on one command line). Choose actual contract
observations explicitly; the tool does not infer which behavior is intended.

The typed `comparison` field in `gotogether.report/2` contains both graph extents,
instances, projection and product-search budget. `added` means after-only observed
state sequences; `removed` is before-only. Compare finite prefixes including init
and every transition, without hiding stutters or comparing action labels,
fairness, infinite liveness or general refinement mappings.

Read directional statuses and witness evidence. Native replay confirms only
source admission; target exclusion rests on a complete graph search. Native
misses, incomplete graphs and exhausted product budgets cannot earn equivalence
or a confirmed difference. Exit 0 is equivalence for these instances, 1 a
source-confirmed difference, and 2 inconclusive/tool error. Do not loosen the
model merely to remove a witness: decide whether the changed behavior belongs in
the contract, then check the implementation against that decision.

Full scope: https://github.com/chucklehead-dev/gotogether/blob/main/docs/model-comparison.md.
