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
    header: { recorder: 'ui', actor: 'webjobs', process: 'webjobs' },
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
  answer's fields (`answer`), at the time Playwright reported the
  response. A request that failed (no answer: a refused connection, an
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
- Lines are written in the order the events happened, each as it comes
  (a killed test keeps what it wrote; a torn last line is a lost tail),
  on the test process's clock with microseconds, never going back. The
  server's steps are joined to them by wall time: one host, or clocks in
  sync to within 1 ms (else give both an order key).

One log per page (one tab's program order): a second page gets its own
`requestLog` and recorder. Requests a service worker makes bypass
`page.route` and are not seen. A redirect's 3xx is its call's answer; the
request it leads to is classified on its own.

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
