# Runs a driver made: Bombadil, Playwright

Read this when the run to check was driven by a tool gotogether did not
run: a browser explorer (Bombadil), an end-to-end suite (Playwright,
Cypress), a load generator, a replayed production session. The driver's
own record of the run (its trace, its screenshots) is then an input, and
an adapter you write turns it into steps. [examples/webjobs](../../../examples/webjobs)
is the whole case: a web app explored by Bombadil, its adapter
(`bombadiltrace`), recorded runs checked offline and a live run nightly.

## A click is an invocation, its own answer is the completion

A driver records what it did (a click) and, separately, what it then
saw (a DOM snapshot). Only an observation the click caused says its
operation happened, and the page's display rarely says which click
caused what: a poll's answer, a second click's answer and a cancel's
answer can all change the same status. So make the page (or the
driver's custom action) expose each request's own answer, and map:

- each request a driver action sent is an **invocation record**
  (`qtrace.Step{Invocation: true, Seq: n, ...}`), never a step, with
  the arguments **the request sent** (from the page's log of it: the
  key in its body, the id in its URL), not the state shown before the
  click, which a late answer may have changed between the driver's
  snapshot and its click;
- its completion is **the first observation of that request's own
  answer** (its status code and body, from the page's log): a step
  whose `Call` is the invocation's `Seq`, at that observation's time;
- several clicks are several calls, each completed only by its own
  answer: a double click is two requests, and the second is answered
  when its own response is seen, not when the first's job is shown;
- any other change the page shows (a poll's status, a stale answer) is
  a step of its own, which completes nothing;
- a request the page never answered (an error it swallowed, a hang)
  stays pending though the page then shows the job ended for another
  reason.

webjobs' page keeps `#log`, its last three requests, each `{n, op, key
or id, code, job}`, numbered as sent; the spec's extract reads it, and
the adapter refuses a trace whose log skips requests between two
snapshots. A request no driver action sent is refused too.

A click whose outcome the trace never shows stays an invocation:
`qtrace.Reconstruct` keeps it in `Trace.Pending`, and `qtest.Finishes()`
(or `gotogether validate -require-settle`, or the binding's
`complete: true`) fails the run as `incomplete (pending calls)`. A
recorded click never stands in for an observed success: an adapter that
writes the click as the step, or completes it with the next change of
the page, passes a page that never answered (webjobs' cancelBroken
mutant: a cancel answered 500 and ignored, the job shown ready by a
poll).

```go
// the request the click sent, at the state before the click
steps = append(steps, qtrace.Step{Action: "submit", Invocation: true, Seq: 7, Recorder: "ui",
	Time: before, Ref: "trace.jsonl:12 screenshot=screenshots/a.jpeg", Args: map[string]any{"key": sentKey}})
// its own answer, at the snapshot whose log first shows it
steps = append(steps, qtrace.Step{Action: "submit", Call: 7, Seq: 8, Recorder: "ui", Outcome: "ok",
	Time: answered, Ref: "trace.jsonl:13 screenshot=screenshots/b.jpeg",
	Args: map[string]any{"key": sentKey, "code": int64(202), "id": id, "status": status}})
```

State the page's end too: a model whose page may stay "accepted" for
good after the server finished passes a page that never polls again.
webjobs says it twice: an eventual val (`pageShowsEnds`: every job the
page was answered with shown ended) and a `within` (a job committed is
shown ended within 500ms). A journey a driver cuts at a time limit can
be mid-job: let the page stop new journeys before the limit (webjobs'
`?until=<ms since the epoch>`, 2s before the limit), so its last job
ends on screen.

## Keep the journey's own times, calls and order

Write the steps as the driver's trace has them (`qtrace.Step` values
passed to `qtest.Validate`, or written with `qtrace.WriteJSONL` for
`gotogether validate`), never by calling `qobs.Record` or `qobs.Invoke`
while the adapter reads an archived trace. A recorder stamps each step
with the time it is called, validation's time, and numbers it in its own
sequence. An archived journey then follows every server step in time:
the order between the page and the server, which a model of "the page
shows ready only after the commit" is about, is lost, and the bug it
would catch passes (webjobs' `TestTimesKept`: re-recorded, the readyEarly
mutant's run conforms, and the faithful journey's page acts after the
server's settle, which then settles nothing). So:

- **times**: each record's `Time` is the driver's (Bombadil's
  `timestamp`, microseconds since the epoch); an invocation's is the
  state before the action, a completion's the state that shows it;
- **call identities**: `Seq` dense from 1 per recorder (a gap is lost
  steps), and each completion's `Call` its own invocation's `Seq`, from
  the request's own identity (the page's request number), never matched
  up by what the page shows next; `qtrace.Reconstruct` reports a
  completion timed before its invocation record, or numbered before it,
  as an error;
- **causal order**: one `Thread` for one tab (its actions are in program
  order); across the driver and the servers, wall time, which orders
  them only on one host or with clocks in sync to within
  `qtrace.RealTimeSlack` (1ms). Across hosts, give the steps an order
  key both sides carry (`order.<domain>`: a request id's sequence)
  instead.

## Refs: the way back to the artifact

Give every record `Ref` (`quint.ref`): the trace's line and the
screenshot of the state it describes (`trace.jsonl:42
screenshot=screenshots/1793042049689466.jpeg`). A failing step's ref is
printed with the failure and kept in the manifest, so a reader opens
the screenshot of the state the model rejected. A ref is no model data:
no verdict depends on it.

## What a settle covers

A settle (`qobs.Settled`) says the system was quiet: the binding's
eventual vals are judged in the last state, and only then. Record it
where quiet is known, after the driver finished: webjobs' server records
it once Bombadil exited, the HTTP server stopped taking requests and
every job ended (`drained`, then the settle). It covers the system the
recorder sees. It does not say the telemetry is complete: a driver
whose every call was answered is not a collector that drained. Steps
still in an exporter's queue, a collector's batch or a log shipper are
absent from the input, and a run validated before they arrive is judged
on a prefix that looks settled. Read the steps from files the
processes themselves wrote and closed (`qobs.FileSink` with `PerStep`
or `Synced`), or state how far the pipeline has delivered: a watermark
(`gotogether validate -complete-through r=T`, `*=T` for every recorder;
`qobs.Recorder.CompleteThrough`) says the recorders' steps through T are
in. A settle it does not reach then settles nothing, and a step due
after it is uncollected, no verdict, never a FAIL (partial-logs.md,
"Absent steps").

## A journey's manifest

`gotogether validate -manifest run.json -run-info run-info.json
server.jsonl page.jsonl` records the run for replay: the inputs' hashes,
the binding and spec, the verdict and failing step's ref, and from
`run-info.json` what gotogether did not see:

```json
{"name": "webjobs faithful",
 "driver": {"name": "bombadil", "version": "0.7.9", "seed": "server 1793042048987864801", "origin": "https://github.com/.../actions/runs/...",
            "args": ["bombadil", "browser", "test", "--headless", "--no-sandbox", "--output-path", "<journey>/bombadil", "--time-limit", "45s", "http://127.0.0.1:42765/", "bombadil/spec.ts"]},
 "artifacts": [{"name": "bombadil trace", "path": "trace.jsonl"}, {"name": "screenshot", "path": "screenshots/1793042049689466.jpeg"}]}
```

Bombadil's own choices are its trace's (`--reproduce DIR` replays its
actions, best effort: it stops where the page offers other actions than
it did, so a timing-dependent page may diverge), so its "seed" is the
system's: what made the server's choices. List the driver's output
directory's files as artifacts: each is hashed, and `validate -replay
run.json` warns if one changed. In a test, `qtest.Driver` and
`qtest.Artifacts` put the same in the manifest written under
`GOTOGETHER_EVIDENCE`.

## Shrinking a run you did not drive

A driver's failing journey is long (a Bombadil exploration clicks for
45 s); the failure needs a few of its actions. `qtest.ShrinkActions`
reruns subsequences of them on the real system and keeps the smallest
that still fails the same way (ddmin, 1-minimal: every sequence with one
action fewer was rerun without reproducing it):

```go
shrunk := qtest.ShrinkActions(t, journey, qtest.ActionShrink[Action]{ // Action: your driver's action type
	Binding: "app.binding.yaml",
	Rerun:   play, // func(ctx, []Action) ([]qtrace.Step, error): does them on a fresh system
})
shrunk.Expect(t, qtest.Violates("finishedOnce"), qtest.AtMostActions(2))
```

- **The rerun**: `Rerun` does the actions in order and returns every
  step the run recorded (the server's and the driver's), as `Validate`
  would take them. An action it cannot do on the page as it is (a
  cancel with no job shown, a reproduction that went elsewhere) returns
  `qtest.ErrDiverged`: the rerun counts for nothing, never a pass. Any
  other error is a failed rerun, and three in a row end the test with
  it.
- **A fresh environment per rerun** (`Reset` nil, `Rerun` starting a new
  server, as webjobs' does) is the only setup safe against leaked
  state. A `qtest.Resetter` that reuses one system is checked, but the
  checks catch leaked *work*, not every leaked *state*:
  - before anything else, a `Reset` then a rerun of no actions, twice,
    must record the same steps (compared as the model sees them:
    recorders', actors', processes' and threads' names may differ, as
    may times). An environment's own steps repeat and pass (a fresh
    server's `drained` and settle at every shutdown); a job the first
    left running, or a poller Reset did not stop, shows as a
    difference;
  - the rerun of no actions is repeated after the original's reruns and
    at the end, where work the actions left (a job still pending) would
    show, and must not reproduce the failure: a failure no actions show
    is refused (it needs none of them, or state survives Reset), as is
    the empty sequence reproducing it during the shrink;
  - the original's first two reruns with a decided failure must fail
    alike: two ways of failing are a leak or a system that really fails
    both ways, and then `Keep` must name the one to shrink;
  - a shrunk sequence that then reproduces far less often than the
    original did (its `Rate` 0, or its upper bound below the original's
    lower bound) is reported not minimal: what it showed during the
    shrink came from state earlier reruns left, or from chance.
  State the actions leave that changes later reruns without a step of
  its own (a counter Reset forgets, a cache) passes every check: a
  shrink then ends on a sequence that fails only after the others.
- **The failure kept** is the original's (`qtest.SameFailure` of its
  first rerun with a decided failure: each failing actor's first
  failure in the replay's order, its kind and name, a state mismatch
  with the observed fields that differ from the model's, whatever ids
  and arguments), so a shrink does not drift to another bug a smaller
  sequence shows. `Keep` names another (`Violates(...)`,
  `Rejects(...)`); one a passing run meets (`Conforms`, `Finishes`) is
  refused.
- **No verdict is no pass**: a rerun with no verdict for the failure
  kept (a lost tail, an invariant of a conforming actor left unjudged,
  a search out of its budget) is `Undecided`, counted apart and not
  among a candidate's reruns; a sequence with one action fewer decided
  only by such reruns leaves the result not minimal.
- **Calibration**: the original is rerun `Baseline` (10) times first. A
  failure that never shows there is refused ("does not reproduce:
  calibrate the rerun"): make the rerun deterministic (the same actions,
  the same data, timings that do not race) before shrinking. A failure
  shown at a rate p is rerun up to n = ⌈ln α / ln(1−p)⌉ times per
  candidate (p the rate's lower bound at 1−α, α 0.05), stopping at its
  first reproduction: a candidate that did not reproduce in n reruns is
  taken as passing, wrongly with chance at most α. So `Minimal` holds
  with chance at least 1−α for each removal, not certainly. The rerun
  Keep's default is taken from is counted in the rate, which leans a
  rare failure's rate up (the lower bound makes up for part of it). A
  failure seen in every baseline rerun costs two reruns per passing
  candidate; set `Calibration.Reruns: 1` for a driver known to be
  deterministic. `Budget` (reruns) and `Time` bound the shrink; at
  either, or near the test's deadline, it returns the smallest sequence
  so far with `Minimal` false and `NotMinimal` saying why.
- **What it reports**: `Actions`, the minimal `Run` (checked as
  `Validate` checks steps: its report and step log), `Reruns`,
  `Reproduced`, `NotReproduced`, `Undecided`, `Diverged`, `Errors`, the
  original's `Baseline` rate and the minimal sequence's `Rate` ("8 of
  10"). Under `GOTOGETHER_EVIDENCE` the minimal run writes its manifest
  (`gotogether.run/1`, driver `qtest.ShrinkActions` unless
  `qtest.Driver` is among `Opts`), with `actions.json` (the shrunk
  actions) and `shrink.json` (the counts) as hashed artifacts, each
  shrink's in a directory of its own: `validate -replay` judges it
  again.

A shrunk sequence that does not reproduce is a sequence the failure was
not seen in, not a pass of it: a rate below 1 says the failure depends on
more than the actions (timing, data), and a rerun that diverged says
the driver could not do them.

## Gating and strict mode

A live driver run needs the driver (and, for Bombadil, a Chrome):

- skip it under `-short` with a plain `t.Skip` (a per-push tier checks
  recorded runs instead: check in one run per mutant, recorded once with
  the real driver, small: a 6s Bombadil run of webjobs is 70 to 135 KB with two
  screenshots);
- out of `-short`, call `qtest.Optional(t, "bombadil not on PATH ...")`
  when the driver is missing: a skip locally, a failure under
  `GOTOGETHER_STRICT=1`, so the CI job that installs it cannot pass by
  skipping;
- state the run's verdict with `Expect(t, qtest.Finishes())`, not
  `Conforms()`: a journey cut short conforms;
- fail the run on the driver's own violation exit (Bombadil's 2): its
  properties are checks too, and logging their violation only lets a
  run pass that the driver itself rejected;
- run the real server in the per-push tier without the driver too: a Go
  client making the page's requests and recording them as the adapter
  would (webjobs' `Client`), so a regression of the server fails on
  push, not only in a recorded trace.

Bombadil specifics: `bombadil browser test --headless --no-sandbox
--time-limit 45s --output-path DIR ORIGIN spec.ts` exits 0 (no
violation of the spec's properties), 2 (a violation) or 1 (an error);
`--exit-on-violation` stops at the first. It writes `DIR/trace.jsonl`
(per line: `timestamp`, the `action` that led to the state, null for the
first, a `Custom` action as `{"Custom": {"name": ...}}`, the `state`'s
`url` and `screenshot`, the extracts' `snapshots` as `{name, value}`,
named after the spec's `extract` consts, and `violations`) and
`DIR/screenshots/`. It refuses a `DIR` that has a trace unless
`--output-path-overwrite`. Install with `npm i
@antithesishq/bombadil@0.7.9` (the package carries the binaries), and a
Chrome or Chromium on PATH; an older Chrome needs
`--chrome-grant-permissions` without the `local-network` permission it
does not know.

Playwright is the same shape: record the invocation when the click
sends its request (`page.waitForRequest`), and complete it from that
request's own response (`await request.response()`: its status and
body), not from the first `expect(...).toHaveText` that passes, which
a poll or another request may have made true. Its trace's screenshots
and the HTML report are the refs and artifacts.
