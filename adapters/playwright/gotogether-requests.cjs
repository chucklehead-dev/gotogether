// gotogether-requests: a Playwright page's requests as a gotogether
// request log (protocol gotogether.requests/1, package qdriver), which
// `gotogether validate` reads beside the server's steps.
//
// Each request the test's classify names a model call is an invocation,
// written when Playwright routes it (before the browser sends it on, so
// before the server can act on it), with what it sent. Only that
// request's own response completes it, written when its body has been
// read (the driver had the whole answer then): its status and the
// answer's fields. Every record is written when its event happens, none
// waiting behind another's body, so a killed test loses at most the
// line being written. A request that
// failed (no answer) is answered with code 0 and the error. A request
// with no response stays pending, so a run that must finish does not.
// What the page then shows is a step of its own (watch, step), which
// completes nothing: a poll's answer or another request's may have made
// it true.
//
// Every routed call carries a correlation id in a header (x-request-id
// unless named; one the page set itself is kept), which the record's
// corr names and a server can log.
//
// No dependency: Node's fs alone. Playwright is the caller's.
'use strict';

const fs = require('fs');

const PROTOCOL = 'gotogether.requests/1';

// now is the test process's clock in milliseconds since the epoch, with
// microseconds, never going back (the protocol's times are in order).
let last = 0;
function now() {
  const t = performance.timeOrigin + performance.now();
  last = Math.max(last, t);
  return last;
}

// iso formats milliseconds since the epoch as RFC 3339 with microseconds.
function iso(ms) {
  const whole = Math.floor(ms);
  const micros = Math.floor((ms - whole) * 1000);
  return new Date(whole).toISOString().replace(/Z$/, String(micros).padStart(3, '0') + 'Z');
}

// parse reads a body as JSON if it is, else returns the text ('' as null).
function parse(text) {
  if (text === null || text === undefined || text === '') return null;
  try {
    return JSON.parse(text);
  } catch {
    return text;
  }
}

// defaultAnswer is an answer's fields: the JSON body's own scalar fields.
function defaultAnswer(res) {
  const out = {};
  if (res.body && typeof res.body === 'object' && !Array.isArray(res.body)) {
    for (const [k, v] of Object.entries(res.body)) {
      if (v === null || ['string', 'number', 'boolean'].includes(typeof v)) out[k] = v;
    }
  }
  return out;
}

// classify is opts.classify of req: the call it makes, or null.
function classify(opts, req) {
  if (typeof opts.classify !== 'function') {
    throw new Error('gotogether-requests: give classify(req) => {action, args} | null: which requests are model calls, and what each sent');
  }
  const call = opts.classify(req);
  if (call && !call.action) throw new Error(`gotogether-requests: classify named no action for ${req.method} ${req.url}`);
  return call || null;
}

// correlation is the correlation id a call carries: the request's own
// header if it has one, else prefix-id.
function correlation(corr, id, req) {
  return (req.headers && req.headers[corr.header]) || `${corr.prefix}-${id}`;
}

// RequestLog writes the protocol's lines for a driver's events: request,
// response, failed and step, called in the order they happened, each
// with its time. It knows nothing of Playwright: requestLog feeds it a
// page's events, and replay a recorded sequence of them.
//
// opts:
//   header       the log's header: recorder (required here; requestLog's
//                default is "playwright"), actor, process, thread
//   classify     (req) => {action, args, thread?} | null: which requests are
//                model calls, and what each sent (never what the page showed
//                before); req is {url, method, headers, body}
//   answer       (res, call) => args: the answer's fields the model reads;
//                res is {status, headers, body}, call {action, args}
//                (default: the JSON body's scalar fields)
//   correlation  {header: 'x-request-id', prefix: 'run'}: a call's id is
//                prefix-n unless the request carries the header already
//   ref          (req, call) => string: a request's ref (default "METHOD /path")
class RequestLog {
  constructor(opts, write) {
    if (!opts || !opts.header || !opts.header.recorder) {
      throw new Error('gotogether-requests: name the log\'s recorder (header: {recorder: "ui"}): its records are numbered in it');
    }
    this.opts = opts;
    this.corr = { header: 'x-request-id', prefix: 'run', ...(opts.correlation || {}) };
    this.corr.header = this.corr.header.toLowerCase();
    this.write = write;
    this.calls = new Map(); // id -> {action, args}
    this.line({ protocol: PROTOCOL, ...opts.header });
  }

  line(obj) {
    this.write(JSON.stringify(obj) + '\n');
  }

  // request is a call the page is about to send (id unique, the call
  // classify made of req, corr its correlation id).
  request(id, time, req, call, corr) {
    this.calls.set(id, call);
    const ref = this.opts.ref ? this.opts.ref(req, call) : `${req.method} ${new URL(req.url).pathname}`;
    const rec = { event: 'request', req: String(id), action: call.action, time: iso(time) };
    if (call.args && Object.keys(call.args).length) rec.args = call.args;
    if (corr) rec.corr = corr;
    if (ref) rec.ref = ref;
    if (call.thread) rec.thread = call.thread;
    this.line(rec);
  }

  // response is a call's own answer: its status and body.
  response(id, time, res) {
    const call = this.calls.get(id);
    if (!call || call.answered) return;
    call.answered = true;
    const args = (this.opts.answer || defaultAnswer)(res, call) || {};
    const rec = { event: 'response', req: String(id), time: iso(time), code: res.status };
    if (Object.keys(args).length) rec.args = args;
    this.line(rec);
  }

  // failed is a call that got no answer: code 0, and why; answer sees
  // {status: 0, body: null, error}, so a completion has the fields its
  // action takes either way.
  failed(id, time, error) {
    const call = this.calls.get(id);
    if (!call || call.answered) return;
    call.answered = true;
    const e = String(error || 'no answer');
    const args = (this.opts.answer || defaultAnswer)({ status: 0, headers: {}, body: null, error: e }, call) || {};
    const rec = { event: 'response', req: String(id), time: iso(time), code: 0, error: e };
    if (Object.keys(args).length) rec.args = args;
    this.line(rec);
  }

  // step is something the driver saw that answers no request.
  step(time, action, args, extra) {
    const rec = { event: 'step', action, time: iso(time) };
    if (args && Object.keys(args).length) rec.args = args;
    for (const k of ['obs', 'ref', 'thread']) if (extra && extra[k]) rec[k] = extra[k];
    this.line(rec);
  }
}

// requestLog records page's calls into the request log at opts.path
// (RequestLog's options, and):
//   path     the log's file, each line written when its event happens (a
//            killed test keeps every record of what happened before, a
//            torn last line at worst)
//   match    the route pattern of the requests to look at (default '**/*')
//   bodyTimeout  how long an answer's body is waited for (default 10s:
//            a stream that never ends is answered with no body)
//   events   a file to write the events to as well (JSON lines), for
//            replay: a recording a test without a browser can feed
//            through RequestLog again
//   uniqueRecorder  false keeps header.recorder as given; by default the
//            recorder is header.recorder (default "playwright") with
//            -<pid>-<n> after it, n counting this process's logs: two
//            pages, or two workers, never number their records in one
//            recorder (gotogether validate refuses two logs naming one)
// It returns {step(action, args, extra), watch(selector, map), close()}:
// step writes a step now; watch writes map(text)'s {action, args} each
// time the selector's text changes in the page (null: no step); close
// stops routing, waits for the answers being read and closes the files.
let logs = 0; // requestLog's calls in this process: a recorder's suffix
async function requestLog(page, opts) {
  const fd = fs.openSync(opts.path, 'w');
  const efd = opts.events ? fs.openSync(opts.events, 'w') : null;
  const event = (e) => efd !== null && fs.writeSync(efd, JSON.stringify(e) + '\n');
  let driver = 'playwright';
  try {
    driver += ' ' + require('playwright/package.json').version;
  } catch {
    // a version is free text: the name alone
  }
  const header = { driver, ...opts.header };
  header.recorder = header.recorder || 'playwright';
  logs++;
  if (opts.uniqueRecorder !== false) header.recorder += `-${process.pid}-${logs}`;
  const log = new RequestLog({ ...opts, header }, (s) => fs.writeSync(fd, s));
  event({ type: 'start', header: log.opts.header, correlation: log.corr });
  // each record is written when its event happens, on the clock read
  // then, so the log's times are in order and nothing waits in memory
  // behind another: a response when its body has been read (the driver
  // had the whole answer then), other lines meanwhile
  const record = (e, write) => {
    event(e);
    write();
  };
  const reading = new Set(); // bodies being read: close waits for them
  // a call is recorded when routed, before the browser sends it on: the
  // server cannot have acted on it yet (Playwright's request event comes
  // after the browser sent it, often after the server took it)
  const ids = new WeakMap();
  const answering = new Set(); // calls whose answer came: a later requestfailed (the body's load) is not theirs
  let next = 0;
  const handler = async (route) => {
    const r = route.request();
    const time = now();
    const req = { url: r.url(), method: r.method(), headers: r.headers(), body: parse(r.postData()) };
    const call = classify(opts, req);
    if (!call) return route.fallback();
    const id = ++next;
    ids.set(r, id);
    const corr = correlation(log.corr, id, req);
    record({ type: 'request', id, time, request: req }, () => log.request(id, time, req, call, corr));
    const given = req.headers[log.corr.header];
    await route.fallback(given ? undefined : { headers: { ...r.headers(), [log.corr.header]: corr } });
  };
  await page.route(opts.match || '**/*', handler);
  const onResponse = (res) => {
    const id = ids.get(res.request());
    if (id === undefined) return;
    answering.add(id);
    let timer;
    const wait = new Promise((done) => {
      timer = setTimeout(done, opts.bodyTimeout || 10000, null);
      timer.unref();
    });
    const p = Promise.race([res.text().then(parse, () => null), wait])
      .finally(() => clearTimeout(timer))
      .then((body) => {
        const time = now();
        const r = { status: res.status(), headers: res.headers(), body };
        record({ type: 'response', id, time, response: r }, () => log.response(id, time, r));
      })
      .finally(() => reading.delete(p));
    reading.add(p);
  };
  const onFailed = (r) => {
    const id = ids.get(r);
    if (id === undefined || answering.has(id)) return;
    const time = now();
    const error = (r.failure() || {}).errorText || 'no answer';
    record({ type: 'failed', id, time, error }, () => log.failed(id, time, error));
  };
  page.on('response', onResponse);
  page.on('requestfailed', onFailed);
  const step = (action, args, extra, at) => {
    const time = at === undefined ? now() : at;
    record({ type: 'step', time, action, args, extra }, () => log.step(time, action, args, extra));
    return Promise.resolve();
  };
  let watches = 0;
  return {
    step: (action, args, extra) => step(action, args, extra),
    async watch(selector, map) {
      const name = `__gotogetherWatch${++watches}`;
      await page.exposeBinding(name, (_source, text) => {
        const time = now();
        const s = map(text);
        if (s) step(s.action, s.args, s, time);
      });
      const observe = ({ name, selector }) => {
        let seen;
        const look = () => {
          const el = document.querySelector(selector);
          const text = el ? el.textContent : null;
          if (text !== seen) {
            seen = text;
            window[name](text);
          }
        };
        const start = () => {
          new MutationObserver(look).observe(document.documentElement, { subtree: true, childList: true, characterData: true, attributes: true });
          look();
        };
        if (document.readyState === 'loading') document.addEventListener('DOMContentLoaded', start);
        else start();
      };
      await page.addInitScript(observe, { name, selector });
      if (page.url() !== 'about:blank') await page.evaluate(observe, { name, selector });
    },
    async close() {
      await page.unroute(opts.match || '**/*', handler).catch(() => {});
      page.off('response', onResponse);
      page.off('requestfailed', onFailed);
      await Promise.all([...reading]);
      fs.closeSync(fd);
      if (efd !== null) fs.closeSync(efd);
    },
  };
}

// replay feeds a recorded events file (requestLog's events) through
// RequestLog with opts' classify and answer, writing the log to write:
// the same lines the test wrote, without a browser.
function replay(eventsText, opts, write) {
  let log = null;
  for (const line of eventsText.split('\n')) {
    if (!line.trim()) continue;
    const e = JSON.parse(line);
    switch (e.type) {
      case 'start':
        log = new RequestLog({ ...opts, header: e.header, correlation: e.correlation }, write);
        break;
      case 'request': {
        const call = classify(opts, e.request);
        if (call) log.request(e.id, e.time, e.request, call, correlation(log.corr, e.id, e.request));
        break;
      }
      case 'response':
        log.response(e.id, e.time, e.response);
        break;
      case 'failed':
        log.failed(e.id, e.time, e.error);
        break;
      case 'step':
        log.step(e.time, e.action, e.args, e.extra);
        break;
      default:
        throw new Error(`gotogether-requests: an event of type ${e.type}`);
    }
  }
}

module.exports = { PROTOCOL, RequestLog, requestLog, replay };

// node gotogether-requests.cjs replay events.jsonl config.cjs: the log
// a recorded run's events make, on stdout (config exports classify and
// answer, as the test gave them).
if (require.main === module) {
  const [cmd, events, config] = process.argv.slice(2);
  if (cmd !== 'replay' || !events || !config) {
    process.stderr.write('usage: node gotogether-requests.cjs replay events.jsonl config.cjs\n');
    process.exit(2);
  }
  replay(fs.readFileSync(events, 'utf8'), require(require('path').resolve(config)), (s) => process.stdout.write(s));
}
