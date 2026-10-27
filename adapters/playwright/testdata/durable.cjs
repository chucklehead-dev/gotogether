// node durable.cjs ADAPTER DIR: requestLog on a page double (no
// browser) whose answer's body is still being read when the page shows
// a step and a second call is routed. It prints the log as it is on
// disk then, before close: what a test killed at that moment leaves.
// qdriver's TestPlaywrightDurable reads it: every record of what had
// happened is there (a first version queued them behind the body).
'use strict';

const fs = require('fs');
const path = require('path');
const { requestLog } = require(path.resolve(process.argv[2]));

const listeners = {};
let routed;
const page = {
  route: async (_match, handler) => {
    routed = handler;
  },
  unroute: async () => {},
  on: (name, f) => {
    listeners[name] = f;
  },
  off: () => {},
  url: () => 'about:blank',
};
const request = (url) => ({ url: () => url, method: () => 'POST', headers: () => ({}), postData: () => '{"key":"k1"}' });
const route = (req) => ({ request: () => req, fallback: async () => {} });

(async () => {
  const file = path.join(process.argv[3], 'requests.jsonl');
  const log = await requestLog(page, {
    path: file,
    header: { recorder: 'ui' },
    classify: (req) => ({ action: 'submit', args: { key: req.body.key } }),
    bodyTimeout: 60000,
  });
  const first = request('http://app/jobs');
  await routed(route(first));
  let answer;
  listeners.response({ request: () => first, status: () => 202, headers: () => ({}), text: () => new Promise((done) => (answer = done)) });
  await log.step('show', { id: 'j1' });
  await routed(route(request('http://app/jobs')));
  process.stdout.write(fs.readFileSync(file, 'utf8'));
  process.stdout.write('--\n');
  answer('{"id":"j1","status":"accepted"}');
  await log.close();
  process.stdout.write(fs.readFileSync(file, 'utf8'));
})().catch((e) => {
  console.error(e);
  process.exit(1);
});
