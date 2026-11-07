// Admission depends on the test's verdict, not a successful empty go command.
const fs = require('node:fs');

function assertPassed(events) {
  const passed = events.some(e => e.Test === 'TestPlaywright' && e.Action === 'pass');
  const skipped = events.some(e => e.Test === 'TestPlaywright' && e.Action === 'skip');
  if (!passed || skipped || events.some(e => e.Action === 'fail')) {
    throw new Error('admit-playwright: TestPlaywright must run and pass; skipped, failed or unmatched tests do not establish admission');
  }
}

if (require.main === module) {
  try {
    const events = fs.readFileSync(process.argv[2], 'utf8')
      .split('\n').filter(Boolean).map(line => JSON.parse(line));
    for (const event of events) {
      if (event.Output) process.stdout.write(event.Output);
    }
    assertPassed(events);
  } catch (error) {
    console.error(error.message);
    process.exitCode = 1;
  }
}

module.exports = { assertPassed };
