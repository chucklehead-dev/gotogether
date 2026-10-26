// webjobs: reports generated in the background, asked for from a web
// page. The page submits a report (a key it makes per submission, so a
// double click asks once), the server accepts it at once (202, the job's
// id) and runs the job; the page polls until the job is ready, and may
// cancel it while it runs. Acceptance is not completion: a job accepted
// may be cancelled, and a page that shows "ready" must be showing a
// report the server finished.
//
// Two observers record the steps, and the model joins them:
//   - the server, at each effect's site: a job created (created), its
//     commit (finished), its cancellation (cancelled), and the server
//     shut down with every job ended (drained);
//   - the page, as a browser explorer saw it: a submission's outcome
//     (submit: the id and status the page showed for the click), a
//     cancel's outcome (cancel: the status shown after the click), and a
//     job's status changing on the page with no click of its own (ready).
// Every action takes what it is given, so a bug is a state the
// invariants rule out rather than a step the model cannot take.
module webjobs {
  type Job = { key: str, status: str } // status: "running", "ready" or "cancelled"

  // the server's jobs, by id
  var jobs: str -> Job
  // a submission's key -> the id the page showed for it
  var answered: str -> str
  // a job's id -> the status the page last showed for it
  var shown: str -> str
  // ghost: the server drained (shut down, every job ended): the journey's end
  var done: bool

  action init = all {
    jobs' = Map(),
    answered' = Map(),
    shown' = Map(),
    done' = false,
  }

  pure def statusOf(js: str -> Job, id: str): str = if (js.keys().contains(id)) js.get(id).status else "none"

  // server: a job created for a submission's key
  action created(key: str, id: str): bool = all {
    jobs' = jobs.put(id, { key: key, status: "running" }),
    answered' = answered, shown' = shown, done' = done,
  }

  // server: a job's report committed
  action finished(id: str): bool = all {
    jobs.keys().contains(id),
    jobs' = jobs.set(id, { ...jobs.get(id), status: "ready" }),
    answered' = answered, shown' = shown, done' = done,
  }

  // server: a job stopped before its report was committed
  action cancelled(id: str): bool = all {
    jobs.keys().contains(id),
    jobs' = jobs.set(id, { ...jobs.get(id), status: "cancelled" }),
    answered' = answered, shown' = shown, done' = done,
  }

  // server: shut down after every job it ran had ended (or so it says:
  // allResolved checks)
  action drained: bool = all {
    done' = true,
    jobs' = jobs, answered' = answered, shown' = shown,
  }

  // page: a submission of key answered on the page with job id, shown
  // with status
  action submit(key: str, id: str, status: str): bool = all {
    answered' = answered.put(key, id),
    shown' = shown.put(id, status),
    jobs' = jobs, done' = done,
  }

  // page: a cancel of job id answered on the page with status (cancelled,
  // or ready when the job had finished first)
  action cancel(id: str, status: str): bool = all {
    shown' = shown.put(id, status),
    jobs' = jobs, answered' = answered, done' = done,
  }

  // page: job id shown ready by polling, with no click of its own
  action ready(id: str): bool = all {
    shown' = shown.put(id, "ready"),
    jobs' = jobs, answered' = answered, done' = done,
  }

  action step = {
    nondet key = Set("k1", "k2").oneOf()
    nondet id = Set("j1", "j2").oneOf()
    nondet status = Set("accepted", "ready", "cancelled").oneOf()
    any {
      created(key, id),
      finished(id),
      cancelled(id),
      drained,
      submit(key, id, status),
      cancel(id, status),
      ready(id),
    }
  }

  // a submission's key creates at most one job: a double click, or a
  // retried request, asks once
  val oneJobPerKey: bool = jobs.keys().forall(a => jobs.keys().forall(b =>
    a == b or jobs.get(a).key != jobs.get(b).key))

  // the id the page shows for a submission is a job the server created
  // for that submission
  val answerIsItsJob: bool = answered.keys().forall(k =>
    jobs.keys().contains(answered.get(k)) and jobs.get(answered.get(k)).key == k)

  // the page shows only jobs the server has
  val shownIsAJob: bool = shown.keys().forall(id => jobs.keys().contains(id))

  // a job shown ready is one whose report the server committed
  val readyIsCommitted: bool = shown.keys().forall(id =>
    shown.get(id) == "ready" implies statusOf(jobs, id) == "ready")

  // a job shown cancelled is one the server stopped: a cancel
  // acknowledged is a cancel done
  val cancelHonoured: bool = shown.keys().forall(id =>
    shown.get(id) == "cancelled" implies statusOf(jobs, id) == "cancelled")

  // nothing is running
  val idle: bool = jobs.keys().forall(id => jobs.get(id).status != "running")

  // a job is running (within: idle again within the bound)
  val running: bool = not(idle)

  // the journey's end: the server drained, and every job it created
  // ended, ready or cancelled
  val allResolved: bool = done and idle
}
