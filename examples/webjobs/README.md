# webjobs: a web app explored by Bombadil, checked by the model

webjobs ([webjobs/](webjobs)) is a standard-library web app: one HTML
page with its JavaScript inline (no build), and a server. The page asks
for a report; the server accepts it at once (202, the job's id) and
writes it in the background; the page polls until the job is ready, and
may cancel it while it runs. Acceptance and completion are different
events, and the page shows both.

[Bombadil](https://github.com/antithesishq/bombadil), Antithesis's
property-based browser explorer, drives the page: it clicks, waits and
checks its own properties, and writes a trace (`trace.jsonl`) and
screenshots. gotogether did not drive the run, so the run's record is
Bombadil's. An adapter ([bombadiltrace](bombadiltrace)) turns it into
steps, the server records its own effects where it makes them, and the
model judges both together.

| file | what it is |
|---|---|
| [webjobs/server.go](webjobs/server.go), [jobs.go](webjobs/jobs.go), [index.html](webjobs/index.html) | the app: `POST /jobs {"key"}` (202, or the key's job again), `GET /jobs/{id}`, `POST /jobs/{id}/cancel` (200, or 409 too late); the page |
| [model/webjobs.qnt](model/webjobs.qnt) | the server's jobs, what the page showed for each, a ghost `done` (the server drained); the properties below |
| [webjobs.binding.yaml](webjobs.binding.yaml) | every parameter the step's field of its name; `complete: true` (a journey is judged finished); a `within` |
| [bombadil/spec.ts](bombadil/spec.ts) | Bombadil's spec: custom actions `ui.submit` (a double click) and `ui.cancel`, the `result` extract (`{id, status, key}`), its own properties |
| [bombadiltrace](bombadiltrace/bombadiltrace.go) | the adapter: trace.jsonl to invocation records and steps |
| [harness.go](harness.go) | `RunLive` (the server on 127.0.0.1:0, Bombadil headless, the drain), `Steps` (a journey's server log and page steps), run-info.json |
| [testdata/runs](testdata/runs) | recorded journeys, one faithful and one per mutant: Bombadil's trace, two of its screenshots, the server's log, run-info.json |
| [testdata/mutants](testdata/mutants) | each mutant's files, replacing webjobs' of the same name |
| [cmd/webjobs](cmd/webjobs/main.go) | the app as a program, `-steps` for its step log |

## Where the steps come from

- **The server** records each effect where it makes it, on recorder
  `server`, under the lock that orders it: `created(key, id)`,
  `finished(id)` (the commit, before any reader can see it ready),
  `cancelled(id)`, and once it stopped taking requests and every job
  ended, `drained`, then a settle (`qobs.Settled`).
- **The page** keeps a log of its requests (`#log`: the last three
  submits and cancels, numbered `n` from 1 as sent, each with what it
  sent and, once it came, its own answer: the HTTP status, 0 for none,
  and the job the answer named). The spec's `result` extract reads it
  with the job shown. From Bombadil's trace (recorder `ui`):
  - each request a click sent is an **invocation record**, never a
    step: `submit(key)` with the key it sent, `cancel(id)` with the job.
    Its time is the state's before the click, which must be the entry's
    custom action: a request no click sent is refused;
  - only its own answer completes it: `submit(key, code, id, status)`,
    `cancel(id, code, status)`, at the state that first shows the
    answer. A poll's answer or another request's completes nothing: a
    cancel never answered stays pending though the job is then shown
    ready, and a double click is two calls, each answered by its own
    request;
  - each change of the job shown is a step of its own (`show(id,
    status)`), whatever caused it;
  - a request whose answer the trace never shows stays pending, so the
    run does not finish (`TestClickWithNoOutcome`);
  - each record's ref is its line and screenshot
    (`trace.jsonl:90 screenshot=screenshots/1793042053467570.jpeg`);
  - the records keep Bombadil's times (microseconds since the epoch),
    its calls (each completion's `Call` its invocation's `Seq`) and its
    order (one thread, the tab). They are written as steps, never
    recorded again through a `qobs.Recorder`, which would stamp them
    with the time of the conversion (`TestTimesKept` shows what that
    hides);
  - a trace the mapping cannot read is refused, naming the line: a
    click with no `result` snapshot, a request no click sent, requests
    that fell out of the log between two snapshots, a log out of order,
    an unmapped custom action. A torn last line is a lost tail: the
    steps before it, and no verdict for the finish.
- The two recorders are ordered by wall clock: one host, the same clock.
- A live run's page takes `?until=<ms since the epoch>`: it stops new
  submissions 2s before Bombadil's time limit is up (counted from
  Bombadil's start), so a journey ends with its jobs shown ended, and
  the page's eventual val is fair to it.

| property | what it says |
|---|---|
| `oneJobPerKey` | a submission's key creates at most one job: the page's double click asks once |
| `answerIsItsJob` | every job a submission was answered with is the job created for its key |
| `shownIsAJob` | the page shows only jobs the server has |
| `readyIsCommitted` | a job shown ready is one whose report the server committed |
| `cancelHonoured` | a cancel acknowledged (200, cancelled) is one the server made, and a job shown cancelled is one it stopped |
| `noFailedRequest` | every request the page sent was answered as its API says: a submit 202 or 200, a cancel 200 or 409; no answer, a server error or any other status is a failure |
| `createdOnce` | a job id is created once: a server that made a job again would give one id two lives |
| `finishedOnce` | a job is committed at most once |
| `noCommitAfterCancel` | a job cancelled is never committed |
| `allResolved` (eventual) | the server's end: it drained, every job it created ready or cancelled |
| `pageShowsEnds` (eventual) | the page's end: every job it was answered with shown ready or cancelled |
| `within 3s after running: idle` | a job started ends within 3s |
| `within 500ms after unseenReady: readySeen` | a job committed while the page shows it running is shown ended within 500ms (the page polls every 100ms) |

## What each test catches

| test | mutant | check | verdict |
|---|---|---|---|
| `TestClient` | none | the real server driven through the page's API by a Go client (`Client`: eight journeys, each a double-clicked submission, polls every 20ms, a cancel in half of them, every fourth submission sent once more after its job ended), no browser | `Finishes()` |
| `TestRecorded` | none | the recorded faithful journey (a 6s Bombadil run: 9 jobs, 6 ready, 3 cancelled), then the CLI: `gotogether validate -manifest -run-info` and `validate -replay` | `Finishes()`; the manifest replays |
| `TestProbes` | none: constructed journeys, each a way a review found the first mapping and model passed | seven runs | a cancel never answered, then a poll's ready: `Pending("cancel")`, `None(Finishes())`; the page stuck at accepted: `Violates(EventuallyName("pageShowsEnds"))`; ready shown 900ms after the commit: the 500ms `within`; finished twice: `finishedOnce`; committed after its cancel: `noCommitAfterCancel`; a double click's second request answered 500: `noFailedRequest`; a key renewed between the snapshot and the click: `Finishes()` (the key is the one sent) |
| `TestClickWithNoOutcome` | none: the recorded journey cut at its first click, its requests unanswered | one run | `Pending("submit")`, `None(Finishes())` |
| `TestTornTrace` | none: the trace's last line cut in half | one run | `Conforms()`, `Undecided(Finishes(), "lost its tail")` |
| `TestTimesKept` | readyEarly's recorded run, and the faithful one, with the page's steps recorded again through a `qobs.Recorder` | three runs | as written: `Violates("readyIsCommitted")`; re-recorded: it conforms (the bug hidden), and the faithful journey does not finish (its page acts after the server's settle) |
| `TestMutants/readyEarly` | a job reads ready once its report is written, 250ms before the server commits it | `-short`: the client's run of the mutant's server, and its recorded run; else a live 45s run | `Violates("readyIsCommitted")` |
| `TestMutants/doubleJob` | the key is not looked up: the page's double click creates two jobs | the same | `Violates("oneJobPerKey")` |
| `TestMutants/cancelIgnored` | a cancel is answered "cancelled" and the job runs on to ready | the same | `Violates("cancelHonoured")` |
| `TestMutants/cancelBroken` | a cancel of a running job answers 500 and does nothing; the page ignores the error | the same | `Violates("noFailedRequest")` |
| `TestMutants/statusStale` | a poll never says ready: the page shows the job running until a cancel's 409 tells it, or for good | the same | `Violates("within 500ms after unseenReady: readySeen")` |
| `TestMutants/commitAfterCancel` | the report is committed whatever a cancel said | the same | `Violates("noCommitAfterCancel")` |
| `TestMutants/rerunFinished` | a key asked again once its job ended runs the job again in place: the same id, back to running | the client's run in both tiers (every fourth journey sends its submission once more, late); no page sends a key again once its job ended, so no Bombadil run or recording reaches it | `Violates("createdOnce")` |
| `TestLive` | none | Bombadil explores the faithful app for 45s; the manifest replays; then `--reproduce` of the run against a new server (the same seed) | `Finishes()`, and Bombadil's own properties hold (its exit 2 fails the run); the reproduction finishes too, or diverges (Bombadil's reproduction is best effort, and the page's timing is not replayed) and what it replayed conforms |
| `TestObservationMutants` | none: the recorded faithful journey with one mistake in its log (`qtest.MutateSteps`, four mutants of each op spread over its 59 steps); full mode only, and nightly | 20 checks of the journey | every dropped step killed, a job's creation recorded twice killed (`createdOnce`), a submit's code 199 killed (`noFailedRequest`); a submit or a cancel recorded twice, and every retime (within its actor's next step), survive (below) |
| `bombadiltrace` tests | none | constructed traces | the mapping (times, calls, refs), calls completed only by their own answers, each refusal, a torn tail |

Rates were measured on 2026-10-26 in a sandbox with Bombadil 0.7.9 and
Chromium 141 (Playwright's build; `--chrome-grant-permissions
local-network-access`), 320x200, four live runs at a time. "Caught" is
the claim in the table above holding of the run; for the faithful app,
`Finishes()` and its reproduction check.

| run | faithful | readyEarly | doubleJob | cancelIgnored | cancelBroken | statusStale | commitAfterCancel |
|---|---|---|---|---|---|---|---|
| live, 45s (the nightly's) | 6 of 6 | 6 of 6 | 6 of 6 | 6 of 6 | 6 of 6 | 6 of 6 | 6 of 6 |
| live, 6s (a recording's) | 10 of 10 | 9 of 10 | 10 of 10 | 9 of 10 | 10 of 10 | 9 of 10 | 10 of 10 |
| `Client`, seeds 1 to 10 (`-short` runs seed 1) | 10 of 10 | 10 of 10 | 10 of 10 | 10 of 10 | 10 of 10 | 10 of 10 | 10 of 10 |

rerunFinished (client only): 10 of 10 seeds. After `createdOnce` and the
submit's codes were added, six more live 45s faithful runs finished (6
of 6), and the client's faithful runs with the late retry 10 of 10. In
the first three, two reproductions that diverged failed the 500ms
`within`: the server committed a job the stopped page never polled for.
A diverged reproduction is now judged up to its page's last record (a
prefix of the journey); the next three diverged after 437, 25 and 130
of their runs' 814, 774 and 848 steps, each prefix conforming.

What the live numbers say: 6 of 6 bounds a 45s run's miss rate below
39% at 95% confidence, and no more. A sharper estimate needs an
assumption: a 45s run holds seven 6s windows, and if each window misses
independently with the 6s rate (at most 1 in 10 measured, at most 39%
at 95%), a night's run of a mutant misses with a chance under 0.4^7,
about 0.2%. The windows are not independent (one Bombadil run, one
server), so read that as an order of magnitude. A 6s run misses when no
job reaches the state the mutant breaks: a job shown by polling
(readyEarly), a cancel of a running job (cancelIgnored; Bombadil
cancels with weight 1 in 15), a job committed before a cancel's answer
(statusStale). The per-push tier does not depend on these rates: its
client run makes the cancels and polls itself, and catches every
mutant at each of ten seeds.

The recorded runs are real Bombadil runs, chosen after the fact: each
the first 6s run (`WEBJOBS_RECORD=1`) that its claim caught, and for
the faithful app the first with both a ready and a cancelled job (it
was the first run). commitAfterCancel's took two runs, the rest one.
They are 70 to 135 KB each with two screenshots, more than a recording
without the page's request log would be. They are regression tests of
the adapter and the model, not of the server: the client's runs are.

**Ablations (measured once, not asserted).** An adapter that completes
every outstanding call at the next change of the job shown (the first
version's mapping) passes `TestProbes`' cancel never answered as
finished (`TestOnlyItsOwnAnswerCompletesACall` fails too). With no
invocation records (a click leaves nothing), `TestClickWithNoOutcome`
fails: no call is pending. With the page's steps recorded again through
a Recorder (validation's time), readyEarly's recorded run conforms
(`TestTimesKept` states it). With the page's cutoff counted from its load
instead of from Bombadil's start, one of five faithful 45s runs ended
with its last job shown running (`pageShowsEnds` and the 500ms `within`
failed): the page went on submitting until Bombadil stopped. And a
reproduction that diverged was first judged with the server's settle,
which a page stopped mid-job does not reach: it is judged as a prefix
now.

## What the recording is checked for

The overlays plant bugs in the server; observation mutants plant them
in the log (skills/gotogether-traces/references/observation-mutants.md):
one step dropped, recorded twice, swapped with its recorder's next, a
field corrupted, retimed, and ask whether the journey's check still
passes. `TestObservationMutants` judges four of each op (the journey has
662), spread over the log, in about 70s. For a step log the CLI prints
the same table (`gotogether validate -binding webjobs.binding.yaml
-mutate-steps -mutate-limit 4 steps.jsonl`); the page's half of a
journey is Bombadil's trace, which only bombadiltrace reads, so the test
judges the joined steps with `qtest.MutateSteps`.

| op | mutants | killed | survived | what the survivors say |
|---|---|---|---|---|
| drop | 4 | 4 | 0 | a job's creation dropped leaves its finish rejected; a click's answer dropped leaves its call pending (`Finishes`) |
| dup | 4 | 2 | 2 | the page's answers and acks are sets, so a submit or a cancel recorded twice changes nothing; a creation recorded twice violates `createdOnce` (it survived before the ghost count of creations: a server that recreated a finished job reset it to running unseen; the rerunFinished mutant is that server) |
| swap | 4 | 2 | 2 | two submits, and a submit and a show, commute; a creation swapped with its job's finish is rejected, and a cancellation swapped with the next job's creation (taking its later time) violates `cancelHonoured` |
| corrupt | 4 | 4 | 0 | a submit answered 199 violates `noFailedRequest` (a submit's answer is 202 or 200, anything else a failure; it survived when only 0 and 5xx counted); a key emptied violates `answerIsItsJob`; a show's status removed is refused by the binding |
| retime | 4 | 0 | 4 | a step moves at most to its actor's next step, so every retime keeps the order the model reads (a creation stays before the submit it answers); only a job's run and a committed job shown are bounded in time, and the four sampled moves (18ms to 111ms, a drain by 28µs) stay within both bounds |

The 199 and the second creation were the two a reader acts on: each
was a recording the check passed that the server never produced, so
each was a claim to add to the model, and both are now (12 of 20
killed; with retime bounded by its actor's next step, the two retimes
it killed before, by moving a creation past its submit, are no longer
made). The survivors left are equivalent by the model's design: answers
are sets, two submits or a submit and a show commute, and no bound reads
a request's time or a move this small.

## What -short runs, and its miss chance

Per push (`go test -short`, the examples loop in CI) runs what can
regress with a push: `TestClient` (the faithful server, in process),
`TestMutants` (each mutant's overlay builds `cmd/webjobs`, about 1.5s,
and the client drives that program from the test process), `TestRecorded`
(the real trace format, faithful), three of `TestProbes` (a cancel never
answered, the page stuck at accepted, the key sent), `TestTimesKept`'s
readyEarly half, and the adapter's tests. The full run adds the CLI's
manifest replay, the mutants' recorded runs (`TestRecordedMutants`),
the probes whose claim a mutant's client run states per push too, the
re-recorded faithful journey, `TestClickWithNoOutcome`, `TestTornTrace`,
`TestObservationMutants` and the live runs.

Under -short the client runs 12 journeys at seed 1, alternating between
running to ready and cancelling (every fourth sent again late). Four
such journeys caught each mutant at each of seeds 1 to 10 (10 of 10;
four at random, without alternating, 8 to 10 of 10), which bounds a
4-journey block's miss rate below 26% at 95%; 12 journeys are three
such blocks, so a mutant's miss chance per push is under 0.26^3, about
2%, if the blocks miss independently (they share a server and a seed).

Measured on 2026-10-26, back to back on a 4-core machine that other
work loaded to about 16: the -short run took 147s wall and 286 CPU-s
before this cut (a633d81), and 74s and 146 CPU-s after. With the
machine near quiet (load 1.4 and 6.6 at their starts), the run before
took 77s and the run after 43s (`ci/test-times.txt` budgets 45).

## How it runs

```sh
go test -short ./...                       # the client's runs (faithful, each mutant's overlay), the faithful recording, three probes, the adapter; about 40 s quiet
go test ./...                              # adds the live runs: bombadil and a Chrome on PATH
WEBJOBS_RECORD=1 go test -run 'TestLive$|TestMutants$' .   # records 6s runs into testdata/runs
./run-demo.sh                              # the recorded journeys, then cmd/webjobs answering one key twice
MUTATION=ready-early ./run-demo.sh         # or double-job, cancel-ignored, cancel-broken, status-stale, commit-after-cancel, rerun-finished; LIVE=1 live
```

The live tests skip under `-short`, and out of it skip without
`bombadil` on PATH (`qtest.Optional`: a failure under
`GOTOGETHER_STRICT=1`, as in the nightly job that installs it). Install
it with `npm i -g @antithesishq/bombadil@0.7.9`; Bombadil finds Chrome as
`google-chrome` (or `CHROME=`). An older Chromium does not know the
`local-network` permission Bombadil grants by default:
`BOMBADIL_ARGS="--chrome-grant-permissions local-network-access"`.

The mutants are overlays, as in [examples/payd](../payd): a directory of
`testdata/mutants/` whose files replace webjobs' of the same name, built
with `go test -overlay` by `TestMutants`, which runs `TestMutant` in
each binary. Under `-short` that runs the mutant's server with the Go
client and judges the mutant's recorded run; out of it, a live run of
the mutant's server.

What a settle covers here: the server's settle is recorded after
Bombadil exited, the HTTP server stopped and every job ended, and both
logs are files their writers closed. In a deployment with a collector
between the app and the check, a driver whose every click was answered
does not say the collector delivered every step: see
[skills/gotogether-traces/references/external-drivers.md](../../skills/gotogether-traces/references/external-drivers.md).
