# Fencing or trusting the lease: the design

The lock grants leases in epochs, and a holder writes to the store while it
believes it holds one. The question this document settles is the one the
example's tests then check in code: **does the store need a fencing token,
or is the lease enough?** The model below answers it for each design; the
table at the end is what `gotogether simulate -matrix` found, written here
by it (design_test.go fails if the table is not the model's).

This is a design model, not the requirement the code is checked against
(that is [../model/lease.qnt](../model/lease.qnt), which the code's runs
replay): it compares the alternatives, so that the choice is recorded with
its reason.

## The state

Two holders contend for at most three epochs. `last` is the newest epoch
the store has accepted a write of; two ghosts record what the properties
are about.

```quint
module leaseDesign {
  // the design's switches: does the store check the epoch (fencing), and
  // do the holders' clocks tell the truth ("true") or not ("lying": a
  // holder paused, or with a skewed or jumping clock, still believes in a
  // lease that has ended)
  const FENCING: bool
  const CLOCKS: str

  pure val HOLDERS = Set("a", "b")
  pure val EPOCHS = 3

  var epoch: int          // the latest epoch granted
  var holder: int -> str  // who was granted each epoch
  var last: int           // the epoch of the newest write the store accepted
  var stale: bool         // ghost: a write was accepted behind a newer one
  var late: bool          // ghost: a write was accepted from an ended lease

  action init = all {
    epoch' = 0, holder' = Map(), last' = 0, stale' = false, late' = false,
  }
```

## The actions

The lock grants the next epoch to either holder. A holder writes with its
epoch while it believes its lease is current: with true clocks only the
newest lease's holder believes so; with lying clocks any holder that was
ever granted one may. A fencing store refuses a write behind the newest it
has accepted.

```quint
  action grant(h: str): bool = all {
    epoch < EPOCHS,
    epoch' = epoch + 1,
    holder' = holder.put(epoch + 1, h),
    last' = last, stale' = stale, late' = late,
  }

  action write(h: str, e: int): bool = all {
    holder.keys().contains(e),
    holder.get(e) == h,
    CLOCKS == "lying" or e == epoch,
    not(FENCING) or e >= last,
    epoch' = epoch,
    holder' = holder,
    last' = if (e > last) e else last,
    stale' = (stale or e < last),
    late' = (late or e < epoch),
  }

  action step = {
    nondet h = HOLDERS.oneOf()
    nondet e = 1.to(EPOCHS).oneOf()
    any { grant(h), write(h, e) }
  }
```

## What each design must keep

`noStaleWrite` is the requirement: once the store has accepted a write of
some epoch, it accepts none of an older one, so a holder whose lease ended
cannot overwrite its successor's work. `noLateWrite` is stronger, and no
design here promises it: a holder whose lease ended never writes at all.

```quint
  val noStaleWrite = not(stale)
  val noLateWrite = not(late)
}
```

## The decision

<!-- gotogether:matrix -->
| | fence | trusted clocks | lease only |
|---|---|---|---|
| noStaleWrite | holds | holds | breaks in 4 steps |
| noLateWrite | breaks in 3 steps | holds | breaks in 3 steps |
<!-- /gotogether:matrix -->

Fencing keeps `noStaleWrite` whatever the clocks do. A lease alone keeps it
only while every clock tells the truth, and breaks it as soon as one holder
is paused or its clock moves. Neither stops a late write (`noLateWrite`): under
fencing an ended lease's holder may still write before its successor does,
and that is harmless, since the successor's first write makes it stale. The
store fences. The code's tests show the same in a simulation: the
`NoFencing` mutant conforms with true clocks and breaks under a pause, a
late write, a clock jump, skew or drift (../README.md).
