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
//   - the page, as a browser explorer saw it: each request a click sent
//     answered (submit, cancel: what it sent, its HTTP status and the
//     job its answer named), and each change of the job the page shows
//     (show).
// Every action takes what it is given, so a bug is a state the
// invariants rule out rather than a step the model cannot take.
module webjobs {
  type Job = { key: str, status: str } // status: "running", "ready" or "cancelled"

  // the server's jobs, by id
  var jobs: str -> Job
  // ghosts of the server's: how often each job was committed, and the
  // jobs it ever cancelled
  var commits: str -> int
  var everCancelled: Set[str]
  // the page's: the jobs each submission key was answered with (2xx),
  // the cancels acknowledged (200, cancelled), the requests that failed
  // (no answer, or 5xx), and the status the page last showed of each job
  var answers: str -> Set[str]
  var acked: Set[str]
  var failed: int
  var shown: str -> str
  // ghost: the server drained (shut down, every job ended): the journey's end
  var done: bool

  action init = all {
    jobs' = Map(), commits' = Map(), everCancelled' = Set(),
    answers' = Map(), acked' = Set(), failed' = 0, shown' = Map(),
    done' = false,
  }

  pure def statusOf(js: str -> Job, id: str): str = if (js.keys().contains(id)) js.get(id).status else "none"
  pure def countOf(m: str -> int, id: str): int = if (m.keys().contains(id)) m.get(id) else 0
  pure def answersOf(m: str -> Set[str], k: str): Set[str] = if (m.keys().contains(k)) m.get(k) else Set()
  pure def fails(code: int): bool = code == 0 or code >= 500

  // server: a job created for a submission's key
  action created(key: str, id: str): bool = all {
    jobs' = jobs.put(id, { key: key, status: "running" }),
    commits' = commits, everCancelled' = everCancelled,
    answers' = answers, acked' = acked, failed' = failed, shown' = shown, done' = done,
  }

  // server: a job's report committed
  action finished(id: str): bool = all {
    jobs.keys().contains(id),
    jobs' = jobs.set(id, { ...jobs.get(id), status: "ready" }),
    commits' = commits.put(id, countOf(commits, id) + 1), everCancelled' = everCancelled,
    answers' = answers, acked' = acked, failed' = failed, shown' = shown, done' = done,
  }

  // server: a job stopped before its report was committed
  action cancelled(id: str): bool = all {
    jobs.keys().contains(id),
    jobs' = jobs.set(id, { ...jobs.get(id), status: "cancelled" }),
    commits' = commits, everCancelled' = everCancelled.union(Set(id)),
    answers' = answers, acked' = acked, failed' = failed, shown' = shown, done' = done,
  }

  // server: shut down after every job it ran had ended (or so it says:
  // allResolved checks)
  action drained: bool = all {
    done' = true,
    jobs' = jobs, commits' = commits, everCancelled' = everCancelled,
    answers' = answers, acked' = acked, failed' = failed, shown' = shown,
  }

  // page: a submission of key answered, with HTTP status code and the
  // job its answer named (id "" for none)
  action submit(key: str, code: int, id: str, status: str): bool = all {
    answers' = if (200 <= code and code < 300) answers.put(key, answersOf(answers, key).union(Set(id))) else answers,
    failed' = if (fails(code)) failed + 1 else failed,
    jobs' = jobs, commits' = commits, everCancelled' = everCancelled, acked' = acked, shown' = shown, done' = done,
  }

  // page: a cancel of job id answered, with code and the status its
  // answer named (409 and the status it had: too late)
  action cancel(id: str, code: int, status: str): bool = all {
    acked' = if (code == 200 and status == "cancelled") acked.union(Set(id)) else acked,
    failed' = if (fails(code)) failed + 1 else failed,
    jobs' = jobs, commits' = commits, everCancelled' = everCancelled, answers' = answers, shown' = shown, done' = done,
  }

  // page: job id shown with status
  action show(id: str, status: str): bool = all {
    shown' = shown.put(id, status),
    jobs' = jobs, commits' = commits, everCancelled' = everCancelled,
    answers' = answers, acked' = acked, failed' = failed, done' = done,
  }

  action step = {
    nondet key = Set("k1", "k2").oneOf()
    nondet id = Set("j1", "j2").oneOf()
    nondet status = Set("accepted", "ready", "cancelled").oneOf()
    nondet code = Set(0, 200, 202, 409, 500).oneOf()
    any {
      created(key, id),
      finished(id),
      cancelled(id),
      drained,
      submit(key, code, id, status),
      cancel(id, code, status),
      show(id, status),
    }
  }

  // a submission's key creates at most one job: a double click, or a
  // retried request, asks once
  val oneJobPerKey: bool = jobs.keys().forall(a => jobs.keys().forall(b =>
    a == b or jobs.get(a).key != jobs.get(b).key))

  // every job a submission was answered with is a job the server created
  // for that submission
  val answerIsItsJob: bool = answers.keys().forall(k => answers.get(k).forall(id =>
    jobs.keys().contains(id) and jobs.get(id).key == k))

  // the page shows only jobs the server has
  val shownIsAJob: bool = shown.keys().forall(id => jobs.keys().contains(id))

  // a job shown ready is one whose report the server committed
  val readyIsCommitted: bool = shown.keys().forall(id =>
    shown.get(id) == "ready" implies statusOf(jobs, id) == "ready")

  // a cancel acknowledged is a cancel done, and a job shown cancelled is
  // one the server stopped
  val cancelHonoured: bool =
    acked.forall(id => everCancelled.contains(id)) and
    shown.keys().forall(id => shown.get(id) == "cancelled" implies statusOf(jobs, id) == "cancelled")

  // every request the page sent was answered, and not with a server error
  val noFailedRequest: bool = failed == 0

  // a job is committed at most once
  val finishedOnce: bool = commits.keys().forall(id => commits.get(id) <= 1)

  // a job cancelled is never committed
  val noCommitAfterCancel: bool = everCancelled.forall(id => countOf(commits, id) == 0)

  // nothing is running
  val idle: bool = jobs.keys().forall(id => jobs.get(id).status != "running")

  // a job is running (within: idle again within the bound)
  val running: bool = not(idle)

  // a job the server committed while the page still shows it running
  // (within: the page shows it ready, or cancelled, soon after)
  val unseenReady: bool = shown.keys().exists(id => shown.get(id) == "accepted" and statusOf(jobs, id) == "ready")
  val readySeen: bool = not(unseenReady)

  // the journey's end, for the server: it drained, and every job it
  // created ended, ready or cancelled
  val allResolved: bool = done and idle

  // the journey's end, for the page: every job it was answered with is
  // shown ended
  val pageShowsEnds: bool = answers.keys().forall(k => answers.get(k).forall(id =>
    shown.keys().contains(id) and (shown.get(id) == "ready" or shown.get(id) == "cancelled")))
}
