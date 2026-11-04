# Independent LRU contract model

This model was authored from the agreed public API contract, without reading
simplelru's implementation, existing native tests, the observing driver, or a
source contrast. It uses an abstract finite map plus an oldest-to-newest list;
it does not model linked-list implementation details.

The bounded domain is three string keys, three integer values, and positive
capacities 1–3. Initialization creates capacity 2. It covers Add, Get, Peek,
Remove and positive Resize, their return values, complete observable entries,
Keys/Len, and per-operation callback multisets. Constructor failures, zero or
negative Resize, concurrent calls, Purge and arbitrary key/value types are
outside this model. Existing upstream tests already cover the ordinary APIs;
this is not a claim of previously untested behavior or independent-maintainer
adoption.

Callbacks are a map from `{key, value}` records to counts. Thus duplicate or
wrong-value callbacks remain observable without asserting undocumented callback
order. The current abstract operations remove each key at most once per call,
so their expected multiplicities are one; the observer must count duplicates.
Unused operation-result fields reset to false/zero on every step.

`observed` is the state-comparison projection. Internal capacity and state are
not exposed as implementation introspection. `last` is a sum type naming the
operation and its parameters. Both random `connect.Run` and named
`connect.Test` use `StatePath: []string{"observed"}` and
`NondetPath: []string{"last"}`. This is the existing tagged-action path, needed
because native named Quint tests do not emit MBT action metadata. No new driver
adapter or alternate runtime format is required.

Five named `mbt.*Test` runs force Get promotion, Peek non-promotion, shrinking
and growing, replacement, removal, and absent reads. `coherent` checks capacity,
unique key membership, and agreement of the observation with the abstract state.
A finite sampled run complements those exact witnesses; it is not an exhaustive
proof.

Model validation used Quint 0.33.0 with the Rust evaluator:

```sh
quint typecheck model/lru.qnt
quint test model/lru.qnt --backend rust
quint run model/lru.qnt --backend rust --max-samples 32 --max-steps 24 --seed 17 --invariant coherent --verbosity 0
```

As a model selftest control, a disposable copy omitting Get's promotion fails
`mbtGetPromotesTest` at its explicit recency expectation. The frozen model is
unchanged. This checks the scenario's discriminating assertion; it is separate
from any later contrast against the actual library or observing driver.
