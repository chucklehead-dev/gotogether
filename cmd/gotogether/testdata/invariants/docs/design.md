# The counter's design

What the counter promises, each claim citing the model's property that
makes it true or the assumption it rests on (`gotogether invariants
-spec model/counter.qnt -claims docs/design.md ./counter`, from the
fixture's directory; `TestInvariantsClaims` runs it):

Claim: the counter never passes its cap (prop: withinCap), and never
goes below zero (prop: counterBase.nonNegative).

- **Claim:** one caller at a time is assumed, not enforced
  (assume: oneCaller).
- A list item without the marker says what it likes and cites nothing.

> Claim: n + 1 never overflows in the model (assume: noOverflow).

Prose that sounds like a claim, "the counter is linearizable", is none:
it has no marker, and no heuristic reads it as one.

```markdown
Claim: an example in a fence is no claim.
```
