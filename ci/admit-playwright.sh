#!/usr/bin/env bash
# Admit the pinned package, its bundled Chromium, and the real request-log journey.
set -euo pipefail

repo_root="$(cd -- "$(dirname -- "$0")/.." && pwd)"
playwright_version=1.65.0
node --test "$repo_root/ci/playwright-result.test.cjs"
npm i -g "playwright@$playwright_version"
NODE_PATH="$(npm root -g)"
export NODE_PATH
unset CHROMIUM GOTOGETHER_MUTANT
node "$NODE_PATH/playwright/cli.js" install --with-deps chromium

PLAYWRIGHT_ADMISSION_VERSION="$playwright_version" node - <<'JS'
const { chromium } = require('playwright');
const version = require('playwright/package.json').version;
if (version !== process.env.PLAYWRIGHT_ADMISSION_VERSION) {
  throw new Error('admit-playwright: resolved unexpected Playwright ' + version);
}
(async () => {
  const browser = await chromium.launch();
  try {
    console.log('Playwright ' + version + '; Chromium ' + browser.version());
  } finally {
    await browser.close();
  }
})().catch(error => { console.error(error); process.exitCode = 1; });
JS

# A successful go command can mean no tests matched or the journey skipped.
# Require the named test's actual pass event, preserving its diagnostic output.
result="$(mktemp)"
trap 'rm -f "$result"' EXIT
status=0
GOTOGETHER_STRICT=1 go -C "$repo_root/examples/webjobs" test -json -short=false \
  -count=1 -timeout 10m -run '^TestPlaywright$' . > "$result" || status=$?
node "$repo_root/ci/playwright-result.cjs" "$result"

exit "$status"
