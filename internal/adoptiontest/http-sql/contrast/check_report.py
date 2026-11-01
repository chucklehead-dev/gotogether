"""Assert semantic verdicts; tooling errors never count as source contrasts."""
import json
import sys

mode, path = sys.argv[1:]
with open(path) as stream:
    report = json.load(stream)
assert report['format'] == 'gotogether.report/2', report
if mode in ('pass', 'prefix'):
    assert report['exit'] == 0 and report['verdict'] == 'pass', report
    if mode == 'prefix':
        actor = report['actors'][0]
        assert actor['conformance'] == 'pass', actor
        assert any(p['kind'] == 'eventually' and p['status'] == 'not_checked'
                   for p in actor['receipt']['checks']), actor
        assert any(p['kind'] == 'eventually' and p['cause'] == 'uncollected'
                   for p in actor['absent']), actor
elif mode == 'incomplete':
    assert report['exit'] == 1 and report['verdict'] == 'fail', report
    assert 'incomplete' in report.get('reason', '') or 'incomplete' in report.get('result', ''), report
    assert all(a['conformance'] == 'pass' for a in report['actors']), report
elif mode == 'missing':
    assert report['exit'] == 2 and report['verdict'] == 'error', report
    assert 'obs.balance' in report['error'] and 'does not have' in report['error'], report
elif mode == 'mismatch':
    assert report['exit'] == 1 and report['verdict'] == 'fail', report
    assert report['reason'] == 'mismatch after finish', report
    failures = [f for a in report['actors'] for f in a.get('failures', [])]
    assert len(failures) == 1, failures
    failure = failures[0]
    assert failure['check'] == 'conformance' and failure['kind'] == 'state', failure
    assert failure['record']['action'] == 'finish', failure
    assert failure['record']['obs']['balance'] == 80, failure
    assert failure['record']['obs']['captures'] == 1, failure
    assert failure['state']['balance'] == '90', failure
else:
    raise AssertionError(mode)
