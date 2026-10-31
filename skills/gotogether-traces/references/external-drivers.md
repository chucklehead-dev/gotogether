# Runs a driver made: Bombadil, Playwright, your own

Read this when the run to check was driven by a tool gotogether did not
run: a browser explorer (Bombadil), an end-to-end suite (Playwright,
Cypress), a load generator, a replayed production session. What the
driver sent and what came back is then an input, a **request log** in a
small protocol the driver writes (package `qdriver`), and `gotogether
validate` reads it beside the server's steps:

```sh
gotogether validate -binding app.binding.yaml server.jsonl requests.jsonl
```

- Playwright: [adapters/playwright](../../../adapters/playwright) writes
  the log from a page's routed requests and responses (copy one file).
- Bombadil: package `qdriver/bombadil` reads its `trace.jsonl` (with a
  page that logs its requests) and makes the log, or the steps.
- Anything else: write the protocol yourself (below); it is JSONL.

[examples/webjobs](../../../examples/webjobs) is the whole case: a web
app explored by Bombadil (recorded runs checked offline, live runs
nightly) and driven by Playwright (`TestPlaywright`), both through the
request log.

## A request is an invocation, its own answer the completion

A driver knows what it did (a click) and, separately, what it then saw.
Only an observation the request caused says its operation happened, and
the page's display rarely says which request caused what: a poll's
answer, a second click's and a cancel's can all change one status. So
the log records requests and their own answers, and maps:

- each request sent is an **invocation record**, never a step, with the
  arguments **the request sent** (the key in its body, the id in its
  URL), not the state shown before the click, which a late answer may
  have changed between the driver's snapshot and its click;
- its completion is **that request's own answer**: its status and the
  answer's fields, at the time the driver saw it;
- several clicks are several calls, each completed only by its own
  answer: a double click is two requests, and the second is answered
  when its own response is seen (Playwright's webjobs journey: the
  second request answered first, 202, the first 200);
- any other change the driver sees (a poll's status, the page's text) is
  a step of its own, which completes nothing;
- a request never answered stays pending though the page then shows the
  job ended for another reason, and `qtest.Finishes()` (or `gotogether
  validate -require-finish`, or the binding's `complete: true`) fails
  the run as `incomplete (pending calls)`. A request sent never stands
  in for an observed success: an adapter that writes the click as the
  step, or completes it with the next change of the page, passes a page
  that never answered (webjobs' cancelBroken mutant: a cancel answered
  500 and ignored, the job shown ready by a poll). It caught a driver
  bug too: webjobs' Playwright journey once closed its log when the page
  still showed the job before, its last submission unanswered.

## The protocol: `gotogether.requests/1`

One JSON object per line; the first is the header, every other a
record. `qdriver.Read` (and the validator) refuses a field the protocol
does not name, a later version (with the fix: upgrade), a response to
no request or a second one, a response with no `code`, a time going
back, naming the line; a torn last line is a lost tail.

```json
{"protocol": "gotogether.requests/1", "recorder": "ui", "actor": "webjobs", "process": "webjobs", "thread": "page", "driver": "playwright 1.56.1"}
{"event": "request", "req": "1", "action": "submit", "time": "2026-10-27T06:41:00.356060Z", "args": {"key": "k1"}, "corr": "pw-1", "ref": "POST /jobs"}
{"event": "request", "req": "2", "action": "submit", "time": "2026-10-27T06:41:00.357160Z", "args": {"key": "k1"}, "corr": "pw-2", "ref": "POST /jobs"}
{"event": "response", "req": "2", "time": "2026-10-27T06:41:00.369632Z", "code": 202, "args": {"id": "j1", "status": "accepted"}}
{"event": "step", "action": "show", "time": "2026-10-27T06:41:00.374479Z", "args": {"id": "j1", "status": "accepted"}}
{"event": "response", "req": "1", "time": "2026-10-27T06:41:00.375131Z", "code": 200, "args": {"id": "j1", "status": "accepted"}}
```

| field | on | what it is |
|---|---|---|
| `protocol` | header | `gotogether.requests/1`, exactly (required) |
| `recorder` | header | the steps' recorder (required): numbered in it, so two logs joined must not share one (`gotogether validate` refuses two that do; Playwright's adapter makes each its own) |
| `actor`, `process`, `thread` | header | default `default`, the recorder, the recorder (one tab's program order) |
| `driver` | header | free text |
| `event` | record | `request`, `response` or `step` |
| `time` | record | RFC 3339, the driver's clock, never going back (required) |
| `req` | request, response | the request's identity in the log (a string); a response names its request's |
| `action` | request, step | the model's action |
| `args` | any | a request's: what it sent; a response's: the answer's fields; a step's: its arguments |
| `code` | response | the answer's status, 0 if none came (required) |
| `error` | response | why none came |
| `outcome` | response | the step's (default `ok`: the call completed with its code, and the model judges the code) |
| `obs` | response, step | observations of the state after it |
| `corr` | request | the id the request carried to the server (a request-id header): written into its steps' refs (`corr=pw-1`) |
| `thread` | request, step | another tab's program order (a response is in its request's) |
| `order` | any | domain clocks (`qtrace.Step.Order`) |
| `ref` | any | the artifact it came from (a screenshot, a trace line); a response without one takes its request's |

A completion's arguments are what the request sent, the answer's fields
it did not send, the answer's value of each field it did send as
`answer_<field>` (a server that applied another key than the one sent:
read `{{.answer_key}}` to judge it; `Log.Warnings` and the validator
name each that differs), then `code`: the call's arguments are the
request's, whatever its answer says. A request may not send an argument
named `code`. Nested values (an object, an array) are a step's JSON
text, as qobs records them: `toQuint` (a structured parameter's
default) reads them by the model's type, and an integer past int64
stays whole. Its steps keep the driver's times
and order: `Seq` is the record's line among records, a completion's
`Call` its request's, its `Invoked` the request's time.

In Go: `qdriver.ReadFile(path)` (a `*qtrace.TornTail` error with the log
for a torn one), `Log.Steps()` for `qtest.Validate`, `qdriver.New(header,
records)` to build one (checked as Read checks it), `Log.Write(w)`.

```go
log, err := qdriver.ReadFile("test-results/requests.jsonl")
if err != nil { /* refused: the error names the line and the fix */ }
qtest.Validate(t, "app.binding.yaml", append(serverSteps, log.Steps()...)).Expect(t, qtest.Finishes())
```

## Bombadil: `qdriver/bombadil`

Bombadil's trace has states, not requests: each line the state after an
action (`Custom` for a spec's own), with the spec's extracts. Make the
page keep a log of its requests, numbered as sent, each with what it
sent and, once it came, its own answer, and read it in an extract:

```js
// in the page: {n, action, ...what it sent, code: null, answer: null}, then the answer's
function sent(action, args) { const r = { n: requests.length + 1, action, ...args, code: null, answer: null }; requests.push(r); show(); return r; }
// in the spec: extract(({document}) => ({log: JSON.parse(document.querySelector('#log').textContent), ...}))
```

```go
log, err := bombadil.LogFile("journey/trace.jsonl", bombadil.Options{ // log.Steps() for qtest.Validate
	Header:   qdriver.Header{Recorder: "ui", Actor: "webjobs", Process: "webjobs"},
	Snapshot: "result",                                   // the spec's extract with the log
	Clicks:   map[string][]string{"ui.submit": {"submit"}, "ui.cancel": {"cancel"}}, // custom action -> what it sends
	Show:     &bombadil.Show{Key: "id", Fields: []string{"id", "status"}}, // the display: a show step per change
})
```

- A request is an invocation at the state before the click that sent
  it, which must be one of `Clicks`' custom actions: a request no click
  sent is refused, as is a custom action not in `Clicks`.
- Its answer completes it at the state that first shows it.
- An entry's fields are `n`, `action`, `code`, `answer`, and what it sent
  (every other field); `Fields` renames them (webjobs' page writes `op`
  and `job`), and `AnswerFields` fills an answer the page left null (a
  500's) with `""`, so every completion has its action's arguments.
- Refs are `trace.jsonl:N screenshot=screenshots/F`: the line and the
  screenshot of the state.
- Refused, naming the line: a click with no snapshot, requests that fell
  out of the page's log between two snapshots (it lists only the last
  few), a log out of order. A torn last line is a lost tail.
- `bombadil.Log` (`LogFile`) gives the request log: its `Steps` for
  `qtest.Validate`, or `Log.Write` it and `gotogether validate` reads it.
  `bombadil.States` (experimental: Bombadil's format is not versioned)
  gives the trace's states as read, for your own mapping (webjobs'
  `journey.FromTrace` makes a rerunnable list of actions from them).

Running it: `bombadil browser test --headless --no-sandbox
--time-limit 45s --output-path DIR ORIGIN spec.ts` exits 0 (no
violation of the spec's properties), 2 (a violation) or 1 (an error);
`--exit-on-violation` stops at the first. It writes `DIR/trace.jsonl`
(per line: `timestamp` in microseconds, the `action` that led to the
state, null for the first, a `Custom` action as `{"Custom": {"name":
...}}`, the `state`'s `url` and `screenshot`, the extracts' `snapshots`
as `{name, value}`, and `violations`) and `DIR/screenshots/`. It refuses
a `DIR` that has a trace unless `--output-path-overwrite`. Install with
`npm i @antithesishq/bombadil@0.7.9` (the package carries the binaries),
and a Chrome or Chromium on PATH; an older Chrome needs
`--chrome-grant-permissions` without the `local-network` permission it
does not know.

## Playwright: adapters/playwright

`requestLog(page, {path, header, classify, answer})` routes the page's
requests: each one `classify` names a call (`{action, args}` from the
request's URL and body; null for a poll or an asset) is written when
routed, before the browser sends it on, so before the server can act on
it (Playwright's `request` event comes after the browser sent it, often
after the server took it), with a correlation id in its `x-request-id`
header; its response when Playwright reports it, with `answer`'s fields
(code 0 and the error for a failed request). `log.watch('#result',
map)` writes a step each time the element's text changes; `log.close()`
waits for the answers being read. The adapter's
[README](../../../adapters/playwright/README.md) has a test; its golden
test feeds recorded events through it without a browser.

## Your own driver

Write the protocol: the header, then a `request` line when it sends a
request (before the system can act on it: an invocation after its
effect breaks a real-time check), a `response` line with the request's
own status when its answer comes (code 0 when none came), a `step` line
for what it sees that answers no request. Write each line as it happens
(a killed driver keeps its log, a torn last line at worst), never all
at the end. Never re-record an archived run through `qobs.Record` or
`qobs.Invoke`: a recorder stamps each step with the time it is called,
validation's time, and an archived journey then follows every server
step in time, so the order between the driver and the server, which a
model of "the page shows ready only after the commit" is about, is lost
(webjobs' `TestTimesKept`: re-recorded, the readyEarly mutant's run
conforms).

Across hosts, the driver's and the server's clocks order their steps
only if in sync to within `qtrace.RealTimeSlack` (1 ms; `-skew`,
`qtest.ClockSkew` set the bound a linearization assumes, and its verdict
names the orders it took from clocks): give the records an order key
both sides carry (`order`, a request id's sequence) instead.

## Refs: the way back to the artifact

Give every record a `ref` (`quint.ref`): the trace's line and the
screenshot of the state it describes (`trace.jsonl:42
screenshot=screenshots/1793042049689466.jpeg`), or the request (`POST
/jobs`). A failing step's ref is printed with the failure and kept in
the manifest, so a reader opens the screenshot of the state the model
rejected; a request's `corr` is in it too. A ref is no model data: no
verdict depends on it.

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

A Go program (a test that checks a recorded journey, a CI job's own
tool) does the same without the CLI: package `validate/cli` is it.
`cli.Check` is the check `gotogether validate` makes (its fields are the
flags), its `Judged` the report (`Close` removes a temporary `Dir`) and
`Manifest()` the manifest, and `cli.Replay` is `validate -replay`
(webjobs' `replays`). A package of its own: it reads OTLP/JSON, and
`validate`, which every test links, does not bring OpenTelemetry's SDK:

```go
j, err := cli.Check{Binding: "webjobs.binding.yaml", Files: []string{"server.jsonl", "page.jsonl"},
	RunInfo: "run-info.json"}.Judge(ctx)
if err != nil { /* a tool error: an unreadable input, quint failing */ }
defer j.Close()
m, _ := j.Manifest() // j.OK, j.Verdict: the run's verdict, as the CLI's result line
_ = m.Write("run.json")
read, _ := validate.ReadManifest("run.json")
r, err := cli.Replay(ctx, read, validate.Options{}) // refuses a changed input (err)
_ = r.Same                                          // r.Got.Verdict is r.Want; r.Differs says where not
```

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
- **Registered component checks.** Supply `ActionShrink.System` and leave
  `Binding` empty to reuse a system's binding, options and assessment on every
  candidate and final rerun. `Rerun` still drives a fresh environment;
  `System.Run` is not used. The default criterion preserves named local/protocol
  failures by role, target, binding, kind and name, with multiplicity, alongside
  primary failures. Missing or unjudged checks are undecided; peer-agreement and
  component-conformance-only identities remain unsupported. Inspect structured
  `ComponentFailures` and shrink.json instead of parsing the criterion's text.
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
  so far with `Minimal` false and `NotMinimal` saying why. `Minimal`
  also needs the shrunk sequence's own `Rate` measured and a last rerun
  of no actions made after the search: a `Budget` keeps `Baseline`+1
  reruns back for them, so a budget the search runs out of still
  measures the rate; a time out before them leaves `Minimal` false.
- **What it reports**: `Actions`, the minimal `Run` (checked as
  `Validate` checks steps: its report and step log), `Reruns`,
  `Reproduced`, `NotReproduced`, `Undecided`, `Diverged`, `Errors`, the
  original's `Baseline` rate and the minimal sequence's `Rate` ("8 of
  10"). Under `GOTOGETHER_EVIDENCE` the minimal run writes its manifest
  (`gotogether.run/2`, driver `qtest.ShrinkActions` unless
  `qtest.Driver` is among `Opts`), with `actions.json` (the shrunk
  actions) and `shrink.json` (the counts) as hashed artifacts, each
  shrink's in a directory of its own: `validate -replay` judges it
  again.

A shrunk sequence that does not reproduce is a sequence the failure was
not seen in, not a pass of it: a rate below 1 says the failure depends on
more than the actions (timing, data), and a rerun that diverged says
the driver could not do them.

### Outside go test: `gotogether shrink`

A driver that is not Go (a Playwright script, Bombadil's replay, a
shell-free load tool) shrinks with the CLI, which runs the same search
(package `qshrink`, which `ShrinkActions` wraps) with the same verdict
rules: the failure kept (`SameFailure` of the original's first decided
failure; the CLI takes no other Keep), no verdict is no pass, the reset
contract checked, `Minimal` only with the shrunk sequence's rate
measured.

```sh
gotogether shrink -binding app.binding.yaml -actions journey.json -out shrunk \
  -diverged-exit 3 -reset ./reset-db -reruns 1 \
  -- ./play -actions {actions} -steps {steps}
```

- `-actions` is a JSON array of the driver's actions, any JSON each
  (the `actions.json` ShrinkActions writes reads back as one).
- The rerun is the command after `--`, run per candidate as argv (never
  a shell), `{actions}` replaced with a file of the candidate's actions
  (a JSON array, a subsequence of the original's) and `{steps}` with
  where it must write the steps it recorded (a step log, or OTLP/JSON;
  an empty file for none); both are also in its environment
  (`GOTOGETHER_ACTIONS`, `GOTOGETHER_STEPS`). Exit 0 is a rerun that
  ran; the `-diverged-exit` status is one that could not do its actions
  (`ErrDiverged`: counted, never a pass); any other is a failed rerun
  (three in a row end the shrink, exit 2). The paths it is given are
  absolute (a driver may change its directory); a rerun or reset past
  `-rerun-timeout` is killed and counts as a failed rerun, named; a child
  the driver leaves running (a server) may keep its output open for 5 s,
  no longer. A torn step log (its last line cut off) is read up to the
  cut, its recorders' tails lost (no verdict): the report counts and
  names such reruns (`-v` prints each), and one torn before its first
  step is a failed rerun. Ctrl-C ends the shrink and removes its reruns'
  files.
- `-reset` (a program, or a JSON array of its argv) runs before every
  rerun; without it each rerun must start its own fresh system, the only
  setup safe against leaked state.
- The calibration's knobs are flags (`-baseline`, `-reruns`, `-need`,
  `-alpha`, `-budget`, `-time`), and the check's (`-linearize`,
  `-linearize-key`, `-resolve-pending`, `-invariants`).
- It writes `actions.json` (the shrunk actions), `shrink.json` (what the
  shrink did), `steps.jsonl` (a rerun of them that showed the failure)
  and `manifest.json` (driver `gotogether shrink`, the two JSON files as
  its artifacts): `gotogether validate -replay shrunk/manifest.json`
  judges it again. It exits 0 for a 1-minimal sequence, 1 for one not
  minimal (the files written, the report saying why), 2 for a refusal
  (the reset contract broken, an original that does not reproduce) or a
  tool error.

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
