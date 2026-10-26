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
- **The page**, from Bombadil's trace (recorder `ui`):
  - a custom action is an **invocation record**, never a step:
    `submit(key)` with the key the form showed before the click,
    `cancel(id)` with the job shown. Its time is the state's before it;
  - a job's id first shown completes the submissions outstanding for
    its key (`submit(key, id, status)`), a job's status changing
    completes the cancels outstanding for it (`cancel(id, status)`),
    and with none, a job shown ready by polling is a step of its own
    (`ready(id)`). One change answers every click outstanding on the
    job: the page shows one status per job, so a second click's answer
    can change nothing there;
  - a click whose outcome the trace never shows stays pending, so the
    run does not finish (`TestClickWithNoOutcome`);
  - each record's ref is its line and screenshot
    (`trace.jsonl:55 screenshot=screenshots/1793036764199218.jpeg`);
  - the records keep Bombadil's times (microseconds since the epoch),
    its calls (each completion's `Call` its invocation's `Seq`) and its
    order (one thread, the tab). They are written as steps, never
    recorded again through a `qobs.Recorder`, which would stamp them
    with the time of the conversion (`TestTimesKept` shows what that
    hides).
  - a trace outside the page's contract is refused, naming the line: a
    click with no `result` snapshot, an id no submission explains (two
    ids for one submit), a status no click or job explains, an unmapped
    custom action. A torn last line is a lost tail: the steps before it,
    and no verdict for the finish.
- The two recorders are ordered by wall clock: one host, the same clock.

| property | what it says |
|---|---|
| `oneJobPerKey` | a submission's key creates at most one job: the page's double click asks once |
| `answerIsItsJob` | the id the page shows for a submission is the job created for its key |
| `shownIsAJob` | the page shows only jobs the server has |
| `readyIsCommitted` | a job shown ready is one whose report the server committed |
| `cancelHonoured` | a job shown cancelled is one the server stopped |
| `allResolved` (eventual) | the journey's end: the server drained, every job it created ready or cancelled |
| `within 3s after running: idle` | a job started ends within 3s (it runs 200 to 600ms in the tests) |

## What each test catches

| test | mutant | check | verdict |
|---|---|---|---|
| `TestRecorded` | none | the recorded faithful journey (a 3s Bombadil run: 5 jobs, 2 ready, 3 cancelled), then the CLI: `gotogether validate -manifest -run-info` and `validate -replay` | `Finishes()`: conforms, settled, `allResolved`, every click answered; the manifest replays |
| `TestClickWithNoOutcome` | none: the recorded journey with its first submission's answer taken out of the trace | one run | `Pending("submit")`, `None(Finishes())`: the server created the job, and the page never showed it |
| `TestTornTrace` | none: the trace's last line cut in half | one run | conforms; no verdict for the finish (lost tail) |
| `TestTimesKept` | readyEarly's recorded run, and the faithful one, with the page's steps recorded again through a `qobs.Recorder` | three runs | as written: `Violates("readyIsCommitted")`; re-recorded: it conforms (the bug hidden), and the faithful journey does not finish (its page acts after the server's settle) |
| `TestMutants/readyEarly` | `readyEarly`: a job reads ready once its report is written, 250ms before the server commits it | its recorded run (`-short`); a live 45s run | `Violates("readyIsCommitted")` |
| `TestMutants/doubleJob` | `doubleJob`: the key is not looked up, so the page's double click creates two jobs | the same | `Violates("oneJobPerKey")` |
| `TestMutants/cancelIgnored` | `cancelIgnored`: a cancel is answered "cancelled" and the job runs on to ready | the same | `Violates("cancelHonoured")` |
| `TestLive` | none | Bombadil explores the faithful app for 45s; the manifest replays; then `--reproduce` of the run against a new server (the same seed) | `Finishes()`; the reproduction finishes too, or diverges (Bombadil's reproduction is best effort, and the page's timing is not replayed) and what it replayed conforms |
| `bombadiltrace` tests | none | constructed traces | the mapping (times, calls, refs), a pending click, each refusal, a torn tail |

Rates were measured on 2026-10-26 in a sandbox with Bombadil 0.7.9 and
Chromium 141 (Playwright's build; `--chrome-grant-permissions
local-network-access`), 320x200, four runs at a time:

| run | faithful | readyEarly | doubleJob | cancelIgnored |
|---|---|---|---|---|
| live, 45s (the nightly's) | 8 of 8 finish, manifests replay | 8 of 8 violate `readyIsCommitted` | 8 of 8 violate `oneJobPerKey` | 8 of 8 violate `cancelHonoured` |
| live, 3s (a recording's) | 20 of 20 finish | 16 of 20 | 20 of 20 | 9 of 20 |
| `--reproduce` of the faithful run | 13 of 20 reproduced (3s), 0 of 8 (45s): the rest diverged, what they replayed conforming | | | |

A 3s run misses when no job reaches the state the mutant breaks: a
ready job shown by polling (readyEarly), a cancel of a running job
(cancelIgnored; Bombadil cancels with weight 1 in 15). Every faithful
run conformed, so no claim of a mutant holds of the faithful app: the
recorded faithful run passes every invariant. The recorded runs are
real Bombadil runs of 3s: the faithful one is the first that had both a
ready and a cancelled job; doubleJob's and cancelIgnored's the first
recorded; readyEarly's the second (the first had no job shown ready).

**Ablations (measured once, not asserted).** With no invocation records
(a click leaves nothing, outcomes are plain steps), `TestClickWithNoOutcome`
fails: no call is pending, and the page that never answered passes.
With the page's steps recorded again through a Recorder (validation's
time), readyEarly's recorded run conforms (`TestTimesKept` states it).

## How it runs

```sh
go test -short ./...                       # the recorded journeys and adapter; each mutant's overlay built; about 30 s
go test ./...                              # adds the live runs: bombadil and a Chrome on PATH
WEBJOBS_RECORD=1 go test -run 'TestLive$|TestMutants$' .   # records 3s runs into testdata/runs
./run-demo.sh                              # the recorded journeys, then cmd/webjobs answering one key twice
MUTATION=ready-early ./run-demo.sh         # or double-job, cancel-ignored; LIVE=1 for a live run
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
each binary. Under `-short` that judges the mutant's recorded run; out of
it, a live run of the mutant's server.

What a settle covers here: the server's settle is recorded after
Bombadil exited, the HTTP server stopped and every job ended, and both
logs are files their writers closed. In a deployment with a collector
between the app and the check, a driver whose every click was answered
does not say the collector delivered every step: see
[skills/gotogether-traces/references/external-drivers.md](../../skills/gotogether-traces/references/external-drivers.md).
