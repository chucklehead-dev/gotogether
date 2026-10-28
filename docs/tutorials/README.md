# Tutorials

Guided pages for a newcomer, each 15 to 30 minutes on an existing
example, with every command's real output (paths shortened, long lines
cut). Read the first one first: it sets up the CLI and quint.

| Tutorial | On | You learn |
|---|---|---|
| [Prove it for an instance](prove-it.md) | hello, twophase, quorumkv | `simulate` vs `simulate -graph`: complete, truncated, `holds` and `bounded`; the shortest counterexample replayed by quint (`-out`); deadlocks and `terminal:`; a design refuted |
| [Liveness: a system that can stay stuck](liveness.md) | requeue | eventual vals, `live` and the lasso, weak fairness per action, `-unfair` vs `-stops`, the runs' hot cycle |
| [Possibles and scenarios](scenarios.md) | hello, library | acceptance criteria as `possible:` and scenarios, `qtest.Model(...).Expect(t, qtest.Possible(...))`, `Some`/`None` over runs, a model too strict, grist's generated possibles |
| [Shipping a grist app to Kubernetes](grist-on-kubernetes.md) | counters | identity, probes, the drain and `-grace`, a rolling restart simulated, a store that goes back and `resume`, `catalog -diff`, `sizing.yaml` |
| [Validating production at volume](production-at-volume.md) | migrate-payments | a production log judged, why whole logs do not scale, `partition:`, `compile` and `validate -compiled` |
