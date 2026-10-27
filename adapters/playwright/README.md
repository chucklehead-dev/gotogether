# Playwright: a page's requests as a request log

[gotogether-requests.cjs](gotogether-requests.cjs) records a Playwright
page's model calls as a request log, the protocol `gotogether
validate` reads beside a server's steps (`gotogether.requests/1`,
package [qdriver](../../qdriver/qdriver.go)). No dependency but Node's
`fs`; Playwright is yours. Copy the file (and its
[.d.ts](gotogether-requests.d.ts)) into your tests.

```js
const { requestLog } = require('./gotogether-requests.cjs');

test('a report', async ({ page }, testInfo) => {
  const log = await requestLog(page, {
    path: testInfo.outputPath('requests.jsonl'),
    header: { recorder: 'ui', actor: 'webjobs', process: 'webjobs' }, // recorder ui-<pid>-<n>
    correlation: { prefix: testInfo.testId },   // x-request-id: <testId>-<n>
    classify(req) {                             // which requests are model calls
      const path = new URL(req.url).pathname;
      if (req.method === 'POST' && path === '/jobs') return { action: 'submit', args: { key: req.body.key } };
      return null;                              // a poll, an asset: no call
    },
    answer: (res) => ({ id: res.body?.id ?? '', status: res.body?.status ?? '' }),
  });
  await log.watch('#result', (text) => text && { action: 'show', args: JSON.parse(text) });
  await page.goto('/');
  await page.locator('#submit').dblclick();
  await expect(page.locator('#result')).toContainText('ready');
  await log.close();
});
```

```sh
gotogether validate -binding webjobs.binding.yaml server.jsonl test-results/*/requests.jsonl
```

## What it writes, and why

- **A call is written when Playwright routes it**: `page.route` holds the
  request before the browser sends it on, so its invocation's time
  precedes anything the server did with it. Playwright's `request` event
  comes later, after the browser sent it, and often after the server took
  it (measured: 1 ms after the server's own record of it), which would put
  an invocation after its effect.
- **Its arguments are what the request sent** (`classify`: the key in its
  body, the id in its URL), never what the page showed before the click,
  which a late answer may have changed.
- **Only its own response completes it**: the status (`code`) and the
  answer's fields (`answer`), at the time its body had been read (the
  driver had the whole answer then; measured with Chromium 141, about
  50 ms after Playwright's `response` event, where the headers had come
  5 ms after the request: a completion later than the answer, which a
  real-time check reads as a wider call, never a narrower one). A request that failed (no answer: a refused connection, an
  abort) completes with code 0 and the error; `answer` sees `{status: 0,
  body: null, error}`, so the completion still has the fields its action
  takes. A request never answered stays a request: pending, so a run that
  must finish (`qtest.Finishes`, `validate -require-finish`, a binding's
  `complete: true`) does not. Don't wait for `toHaveText` and call the
  click done: a poll or another request may have made it true.
- **What the page then shows is a step of its own** (`watch(selector,
  map)`: a step each time the element's text changes, through a
  `MutationObserver` in the page; `step(action, args)` writes one now). It
  completes nothing.
- **A correlation id** goes in each call's request (`x-request-id:
  <prefix>-<n>`, unless the page set the header itself, which is kept)
  and in the record's `corr`, which every step of the call carries in its
  ref (`POST /jobs corr=pw-2`): a failure names the server's records to
  look for, and a server that logs the header joins them.
- Lines are written when their events happen, none waiting behind
  another's body (a killed test loses at most the line being written: a
  torn last line, a lost tail), on the test process's clock with
  microseconds, never going back. The server's steps are joined to them
  by wall time: one host, or clocks in sync to within 1 ms (else give
  both an order key). (A first version wrote the lines in the order of
  Playwright's events, each response at its headers' time, and queued
  every later line behind an answer's body: a test killed while one was
  read lost them all.)
- **Each log has its own recorder**: the header's `recorder` (default
  `playwright`) with `-<pid>-<n>` after it, n counting the process's
  logs, so two pages, or two workers, never number their records in one
  recorder. `gotogether validate` refuses two request logs naming one
  (their records would collide: once, the second's were dropped as
  duplicates and a failing second run passed). `uniqueRecorder: false`
  keeps the name as given, for one log alone.

One log per page (one tab's program order): a second page gets its own
`requestLog`, and its own recorder.

## Limits

Checked with Playwright 1.56.1 and its Chromium 141:
- **A mocked answer is not the server's.** A `page.route` the test
  registers *after* `requestLog` runs first (Playwright tries the latest
  route first): a request it fulfills never reaches the adapter's route
  and is in no record. One registered *before* runs after the
  adapter's: the call is written, and the mock's answer completes it as
  if the server had answered (`Response.serverAddr()` is null for it,
  the adapter does not look). Don't classify a mocked request as a model
  call, or the model judges an answer the server never gave.
- **Cookies are kept.** The adapter re-sends a call with its
  correlation header added to `request.headers()`, which leaves out the
  cookie headers; Chromium sent the page's cookie anyway (measured: a
  `Set-Cookie` from the page's document, then a routed `fetch`, arrived
  with `Cookie`). Another browser may not: if a server sees routed calls
  without their session, have the page set the correlation header
  itself, and the adapter passes the request on unchanged.
- Requests a service worker makes bypass `page.route` and are not seen.
  A redirect's 3xx is its call's answer; the request it leads to is
  classified on its own.

## Testing it without a browser

`events: 'events.jsonl'` writes the events as well, and `node
gotogether-requests.cjs replay events.jsonl config.cjs` (config exports
`classify` and `answer`) writes the same log from them. qdriver's
`TestPlaywrightGolden` replays [testdata](testdata)'s events (a real
Chromium's journey through examples/webjobs, recorded by its
`TestPlaywright`, and a hand-written one with a failed request, a 500, a
page's own request id and a call never answered) and compares the log
with the golden one, then reads it through the Go reader.
examples/webjobs' `TestPlaywright` runs a real page against the real
server end to end and judges the run with the model (full mode; the
nightly installs `playwright@1.56.1` and its Chromium).
