// The golden tests' mapping (qdriver's TestPlaywrightGolden): examples/webjobs'
// playwright/webjobs.cjs, which recorded journey.events.jsonl.
'use strict';

module.exports = {
  // POST /jobs {"key"} is submit(key); POST /jobs/{id}/cancel is cancel(id)
  classify(req) {
    const path = new URL(req.url).pathname;
    if (req.method === 'POST' && path === '/jobs') return { action: 'submit', args: { key: req.body && req.body.key } };
    const m = req.method === 'POST' && path.match(/^\/jobs\/([^/]+)\/cancel$/);
    if (m) return { action: 'cancel', args: { id: decodeURIComponent(m[1]) } };
    return null;
  },
  // the job the answer named: "" for none (a 500, no body)
  answer(res) {
    const job = res.body && typeof res.body === 'object' ? res.body : {};
    return { id: job.id || '', status: job.status || '' };
  },
  // #result is the job shown, as JSON: each change a show step
  show(text) {
    if (!text) return null;
    try {
      const job = JSON.parse(text);
      return job.id ? { action: 'show', args: { id: job.id, status: job.status } } : null;
    } catch {
      return null;
    }
  },
};
