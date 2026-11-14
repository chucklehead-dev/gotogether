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

Use `-out review` to keep confirmed native witness modules and hashed source
copies alongside `report.json`. The destination must be fresh. Replay paths and
`args` in each witness's typed `replay` object are relative to that directory:
change into it and pass the argument vector to Quint. The artifact confirms only
source admission; exporting does not strengthen target exclusion or equivalence.
An unconfirmed candidate receives no replay artifact.

The exact historical hello completion change is a scope control: adding `done`
and occurrence claims leaves `applied,count` state sequences equivalent because
an existing action already stuttered at completion. Retain those action/finish
checks; do not treat state-language equivalence as approval of a changed contract.

Each graph has a default 256 MiB estimated retained-storage budget. Set
`-memory N` in MiB to change it. The graph's `memoryUsed`, `memoryScope` and
`bound.memoryBytes` retain that evidence; compilation, transient evaluation and
process heap/RSS are excluded. A memory-truncated input is inconclusive.
