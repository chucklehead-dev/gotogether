// A Playwright journey through webjobs' page: node journey.cjs ORIGIN DIR
// writes DIR/requests.jsonl (the page's calls, through the adapter in
// adapters/playwright) and DIR/events.jsonl (the events, for a replay
// without a browser). Three journeys: a double-clicked submission run to
// its end, one cancelled at once, one more run to its end.
'use strict';

const path = require('path');
const { chromium } = require('playwright');
const { requestLog } = require('../../../adapters/playwright/gotogether-requests.cjs');
const webjobs = require('./webjobs.cjs');

// shown is the job the page shows ({} for none); ended waits until the
// page shows a job other than before's, ended (ready or cancelled)
const shown = () => {
  const t = document.querySelector('#result').textContent;
  return t === '' ? {} : JSON.parse(t);
};
async function ended(page, before) {
  await page.waitForFunction(
    (before) => {
      const t = document.querySelector('#result').textContent;
      const job = t === '' ? {} : JSON.parse(t);
      return job.id && job.id !== before.id && job.status !== 'accepted';
    },
    before,
    { polling: 20 },
  );
  return page.evaluate(shown);
}

(async () => {
  const [origin, dir] = process.argv.slice(2);
  const browser = await chromium.launch(process.env.CHROMIUM ? { executablePath: process.env.CHROMIUM } : {});
  try {
    const page = await browser.newPage();
    const log = await requestLog(page, {
      path: path.join(dir, 'requests.jsonl'),
      events: path.join(dir, 'events.jsonl'),
      header: { recorder: 'ui', actor: 'webjobs', process: 'webjobs', thread: 'page' },
      correlation: { prefix: 'pw' },
      classify: webjobs.classify,
      answer: webjobs.answer,
    });
    await log.watch('#result', webjobs.show);
    await page.goto(origin);
    // a double click: one key, two requests, one job
    await page.locator('#submit').dblclick();
    let job = await ended(page, {});
    // a submission cancelled while it runs
    await page.locator('#submit').click();
    await page.locator('#cancel:enabled').click();
    job = await ended(page, job);
    // one more, to its end
    await page.locator('#submit').click();
    await ended(page, job);
    await log.close();
  } finally {
    await browser.close();
  }
})().catch((e) => {
  console.error(e);
  process.exit(1);
});
