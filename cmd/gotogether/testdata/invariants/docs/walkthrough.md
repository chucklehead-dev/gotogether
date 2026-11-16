# The counter, walked through

The counter of [model/counter.qnt](../model/counter.qnt) counts up to its
cap. Its invariants, where the code enforces each and which tests check
it (generated: `gotogether invariants -spec model/counter.qnt -write
docs/walkthrough.md ./counter`, from the fixture's directory; the test
`TestInvariantsGolden` fails when it is stale):

<!-- gotogether:invariants -->
| property | meaning | enforced by | checked by |
|---|---|---|---|
| `withinCap` | n never exceeds the cap. | `Counter.Inc` [../counter/counter.go:18](../counter/counter.go#L18) | `TestInc` [../counter/counter_test.go:8](../counter/counter_test.go#L8)<br>`TestReset` [../counter/counter_test.go:24](../counter/counter_test.go#L24) |
| `nonNegative` | n is never negative. | `Counter.Reset` [../counter/counter.go:29](../counter/counter.go#L29) | `TestReset` [../counter/counter_test.go:24](../counter/counter_test.go#L24) |
| `atCap` | | — | — |
<!-- /gotogether:invariants -->

`atCap` is no invariant: nothing enforces it, and no test checks it.
