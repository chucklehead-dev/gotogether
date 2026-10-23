// A lock that grants leases in epochs, and the store it guards. The
// requirement: once the store has accepted a write of some epoch, it
// accepts none of an older one (a holder whose lease ended must not
// overwrite its successor's work). The model allows any granted holder to
// write, so it is the store that must refuse stale epochs (fencing): a
// lease checked against a clock alone cannot keep it.
module lease {
  // who may hold the lock
  const HOLDERS: Set[str]

  var epoch: int           // the latest epoch granted
  var holder: int -> str   // who was granted each epoch
  var last: int            // the epoch of the latest write accepted
  var stale: bool          // ghost: a write was accepted after a newer epoch's

  action init = all { epoch' = 0, holder' = Map(), last' = 0, stale' = false }

  // the lock grants the next epoch
  action grant(h: str, e: int): bool = all {
    HOLDERS.contains(h),
    e == epoch + 1,
    epoch' = e,
    holder' = holder.put(e, h),
    last' = last,
    stale' = stale,
  }

  // the store accepts a write from the holder of a granted epoch
  action write(h: str, e: int): bool = all {
    holder.keys().contains(e),
    holder.get(e) == h,
    epoch' = epoch,
    holder' = holder,
    last' = if (e > last) e else last,
    stale' = (stale or e < last),
  }

  action step = {
    nondet h = HOLDERS.oneOf()
    nondet e = 1.to(epoch + 1).oneOf()
    any { grant(h, e), write(h, e) }
  }

  val noStaleWrite = not(stale)
}
