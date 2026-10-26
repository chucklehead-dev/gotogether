# Runs a driver made: Bombadil, Playwright

Read this when the run to check was driven by a tool gotogether did not
run: a browser explorer (Bombadil), an end-to-end suite (Playwright,
Cypress), a load generator, a replayed production session. The driver's
own record of the run (its trace, its screenshots) is then an input, and
an adapter you write turns it into steps. [examples/webjobs](../../../examples/webjobs)
is the whole case: a web app explored by Bombadil, its adapter
(`bombadiltrace`), recorded runs checked offline and a live run nightly.

## A click is an invocation, an outcome is a completion

A driver records what it did (a click, a request sent) and, separately,
what it then saw (a DOM snapshot, a response). Only the second says the
operation happened. Map them so:

- each driver action that starts an operation is an **invocation
  record** (`qtrace.Step{Invocation: true, Seq: n, ...}`), never a step;
- the first observation that shows the operation's outcome is its
  **completion**: a step whose `Call` is the invocation's `Seq`, at the
  observation's time, with what was observed as its arguments;
- an observation no action explains (a status that changed on its own,
  a poll's answer) is a step of its own, or, if the page's contract says
  it cannot happen, a refusal of the trace (an error naming the line),
  never a guess.

A click whose outcome the trace never shows stays an invocation:
`qtrace.Reconstruct` keeps it in `Trace.Pending`, and `qtest.Finishes()`
(or `gotogether validate -require-settle`, or the binding's
`complete: true`) fails the run as `incomplete (pending calls)`. A
recorded click never stands in for an observed success: an adapter that
writes the click as the step would pass a page that never answered.

```go
// the click, at the state before it (the click came after)
steps = append(steps, qtrace.Step{Action: "submit", Invocation: true, Seq: 7, Recorder: "ui",
	Time: before, Ref: "trace.jsonl:12 screenshot=screenshots/a.jpeg", Args: map[string]any{"key": key}})
// its outcome, at the snapshot that first shows it
steps = append(steps, qtrace.Step{Action: "submit", Call: 7, Seq: 8, Recorder: "ui", Outcome: "ok",
	Time: shown, Ref: "trace.jsonl:13 screenshot=screenshots/b.jpeg", Args: map[string]any{"key": key, "id": id, "status": status}})
```

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
  steps), and each completion's `Call` its own invocation's `Seq`; an
  observation that answers several clicks on one thing (the page shows
  one status per job) completes each of them;
- **causal order**: one `Thread` for one tab (its actions are in program
  order); across the driver and the servers, wall time, which orders
  them only on one host or with clocks in sync to within
  `qtrace.RealTimeSlack` (1ms). Across hosts, give the steps an order
  key both sides carry (`order.<domain>`: a request id's sequence)
  instead.

## Refs: the way back to the artifact

Give every record `Ref` (`quint.ref`): the trace's line and the
screenshot of the state it describes (`trace.jsonl:42
screenshot=screenshots/1793036762193238.jpeg`). A failing step's ref is
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
on a prefix that looks settled. Until a collector can state that it is
complete through a time (a watermark, not built yet: roadmap E7), read
the steps from files the processes themselves wrote and closed
(`qobs.FileSink` with `PerStep` or `Synced`), or wait for the pipeline
by its own means before validating.

## A journey's manifest

`gotogether validate -manifest run.json -run-info run-info.json
server.jsonl page.jsonl` records the run for replay: the inputs' hashes,
the binding and spec, the verdict and failing step's ref, and from
`run-info.json` what gotogether did not see:

```json
{"name": "webjobs faithful",
 "driver": {"name": "bombadil", "version": "0.7.9", "seed": "server 1793036761185206408", "origin": "https://github.com/.../actions/runs/...",
            "args": ["bombadil", "browser", "test", "--headless", "--no-sandbox", "--output-path", "<journey>/bombadil", "--time-limit", "45s", "http://127.0.0.1:42765/", "bombadil/spec.ts"]},
 "artifacts": [{"name": "bombadil trace", "path": "trace.jsonl"}, {"name": "screenshot", "path": "screenshots/1793036762193238.jpeg"}]}
```

Bombadil's own choices are its trace's (`--reproduce DIR` replays its
actions, best effort: it stops where the page offers other actions than
it did, so a timing-dependent page may diverge), so its "seed" is the
system's: what made the server's choices. List the driver's output
directory's files as artifacts: each is hashed, and `validate -replay
run.json` warns if one changed. In a test, `qtest.Driver` and
`qtest.Artifacts` put the same in the manifest written under
`GOTOGETHER_EVIDENCE`.

## Gating and strict mode

A live driver run needs the driver (and, for Bombadil, a Chrome):

- skip it under `-short` with a plain `t.Skip` (a per-push tier checks
  recorded runs instead: check in one run per mutant, recorded once with
  the real driver, small: a 3s Bombadil run is about 30 KB with two
  screenshots);
- out of `-short`, call `qtest.Optional(t, "bombadil not on PATH ...")`
  when the driver is missing: a skip locally, a failure under
  `GOTOGETHER_STRICT=1`, so the CI job that installs it cannot pass by
  skipping;
- state the run's verdict with `Expect(t, qtest.Finishes())`, not
  `Conforms()`: a journey cut short conforms.

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

Playwright is the same shape: a test's `page.click` is the invocation
(record it when the action starts), the `expect(...).toHaveText` that
first passes is the completion (record it with the time it passed), and
its trace's screenshots and the HTML report are the refs and artifacts.
