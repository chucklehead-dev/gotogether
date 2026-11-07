const assert = require('node:assert/strict');
const test = require('node:test');
const { assertPassed } = require('./playwright-result.cjs');

test('accepts the real journey pass with a successful package', () => {
  assert.doesNotThrow(() => assertPassed([
    { Action: 'run', Test: 'TestPlaywright' },
    { Action: 'pass', Test: 'TestPlaywright' },
    { Action: 'pass', Package: 'webjobs' },
  ]));
});

for (const [name, events] of [
  ['empty selection', [{ Action: 'pass', Package: 'webjobs' }]],
  ['skipped journey', [{ Action: 'skip', Test: 'TestPlaywright' }]],
  ['printed PASS text', [{ Action: 'output', Output: '--- PASS: TestPlaywright' }]],
  ['another test', [{ Action: 'pass', Test: 'TestPlaywrightGolden' }]],
  ['unfinished journey', [{ Action: 'run', Test: 'TestPlaywright' }]],
  ['failed journey', [{ Action: 'fail', Test: 'TestPlaywright' }]],
  ['package failed after test passed', [{ Action: 'pass', Test: 'TestPlaywright' }, { Action: 'fail', Package: 'webjobs' }]],
  ['mixed skip and pass', [{ Action: 'skip', Test: 'TestPlaywright' }, { Action: 'pass', Test: 'TestPlaywright' }]],
]) {
  test('rejects ' + name, () => {
    assert.throws(() => assertPassed(events), /do not establish admission/);
  });
}
