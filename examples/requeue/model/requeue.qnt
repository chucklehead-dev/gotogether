// A controller reconciles an object that needs a cloud resource: it
// applies the resource's spec, and requeues until the cloud reports the
// resource ready, then marks the object ready. The cloud provisions in two
// steps (pending, warming, ready), and treats an apply of a changed spec
// as a replacement: provisioning starts over.
//
// RESEND says what an apply after the first may do. The faithful
// controller applies the same spec every time, which the cloud ignores:
// a create is applied once, from nothing. One whose spec changes with
// every apply (a timestamp in it) restarts the provisioning at every
// requeue: a retry storm. Weak fairness does not rule it out: the
// cloud's advance to ready is enabled in warming only, and every requeue
// takes the resource back to pending, so it is never enabled throughout
// the cycle (gotogether simulate -graph: requeue.storm.binding.yaml's
// lasso; requeue.binding.yaml's model, RESEND false, is proved live).
module requeue {
  const RESEND: bool

  // the cloud's resource: "none", "pending", "warming", "ready"
  var phase: str
  // the object marked ready by the controller
  var ready: bool

  action init = all { phase' = "none", ready' = false }

  // the cloud applied a create (or, a changed spec resent, a replacement,
  // whatever the phase): provisioning starts over
  action create = all {
    RESEND or phase == "none",
    phase' = "pending",
    ready' = ready,
  }

  // the cloud's provisioning advances one step
  action advance = all {
    phase == "pending" or phase == "warming",
    phase' = if (phase == "pending") "warming" else "ready",
    ready' = ready,
  }

  // the controller saw the resource not ready yet and looks again later:
  // a requeue changes nothing (it is a stutter, which fairness counts as
  // no step)
  action requeue = all { not(ready), phase' = phase, ready' = ready }

  // the controller saw the resource ready and marked the object ready
  action finish = all { phase == "ready", not(ready), phase' = phase, ready' = true }

  action step = any { create, advance, requeue, finish }

  // the object is reconciled: judged when a run settles, and as cycles by
  // simulate -graph
  val reconciled = ready
}
