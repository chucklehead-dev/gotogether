#!/usr/bin/env python3
"""Check native-to-model transport; conformance comes from the native report."""
import base64
from collections import Counter
from datetime import datetime, timezone
import json
from pathlib import Path
import re
import sys


def require(ok, message):
    if not ok:
        raise ValueError(message)


def compact(value):
    if isinstance(value, dict):
        return {k: compact(v) for k, v in value.items() if v not in ('', False, 0, None)}
    if isinstance(value, list):
        return [compact(v) for v in value]
    return value


def seconds(value):
    fraction = re.search(r'T\d{2}:\d{2}:\d{2}[.,](\d+)', value)
    require(not fraction or set(fraction.group(1)) <= {'0'}, 'subsecond native clock cannot use this bounded projection')
    at = datetime.fromisoformat(value.replace('Z', '+00:00'))
    require(at.microsecond == 0, 'subsecond native clock cannot use this bounded projection')
    delta = at - datetime(1970, 1, 1, tzinfo=timezone.utc)
    return delta.days * 86400 + delta.seconds - 200


def members_match(projected, native, batch=None):
    require(len(projected) == len(native), 'member multiplicity changed')
    remaining = list(native)
    for p in projected:
        candidates = [n for n in remaining if p['key'] == (n['key'] if batch else n['ref']['key'])]
        require(len(candidates) == 1, 'native occurrence key lost or duplicated')
        n = candidates[0]
        remaining.remove(n)
        provenance = batch or n
        require(provenance['fromMs'] % 1000 == provenance['toMs'] % 1000 == 0, 'subsecond read window')
        require(p['from'] == provenance['fromMs'] // 1000 - 200 and p['to'] == provenance['toMs'] // 1000 - 200, 'read window changed')
        require(p['observed'] == seconds(provenance['observedAt']), 'read clock changed')
        require(p['limited'] == provenance['limited'] and p['late'] == n['late'], 'native flags changed')
        decoded = base64.b64decode(p['body'], validate=True)
        require(base64.b64encode(decoded).decode() == p['body'], 'noncanonical byte transport')
        require(json.loads(decoded) == n['record'], 'native record changed in model transport')
        require(p['basis'] == ('' if batch else n['basis']), 'basis identity changed')


def native_gate(mode, trace, observations, cases):
    rows = [json.loads(line) for line in Path(trace).read_text().splitlines() if line.strip()]
    require(rows[0] == {'quint.format': 'gotogether.steps/1'}, 'missing native format record')
    steps = rows[1:]
    actual = json.loads(Path(observations).read_text())
    authored = json.loads(Path(cases).read_text())
    require(compact([x['case'] for x in actual]) == compact(authored), 'authored operation coverage changed')
    calls = [x for x in actual if x['case']['action'] not in ('mutateInput', 'mutateDraft')]
    require(len(steps) == len(calls) == 38, 'trace lost modeled operations')
    slots = {}
    for i, (s, n) in enumerate(zip(steps, calls)):
        c = n['case']
        require(s['quint.seq'] == i + 1 and s['quint.action'] == c['action'], 'native sequence/action changed')
        require(s['quint.ref'] == 'case:' + c['name'], 'case reference changed')
        require(s['quint.actor'] == s['quint.recorder'] == 'membership', 'actor identity changed')
        require(s['quint.outcome'] == 'ok' and not s.get('quint.error'), 'tool failure is not domain evidence')
        action = c['action']
        if action == 'reset':
            slots = {}
        elif action == 'advance':
            require(s['quint.arg.now'] == c['now'], 'authored clock changed')
        elif action == 'issue':
            b, cap = n['input'], n['capability']
            slots[c.get('slot', 0)] = n
            require(s['quint.arg.observed'] == c.get('observed', 0), 'authored read clock changed')
            require(s['quint.arg.slot'] == c.get('slot', 0), 'slot changed')
            require(s['quint.arg.connection'] == b['connection'] == c['connection'], 'issue owner changed')
            require(s['quint.arg.selector'] == b['selector']['environment'] == c['selector'], 'issue scope changed')
            require([row['record']['Body'] for row in b['rows']] == c['bodies'], 'authored input bodies changed')
            require(all(row['record']['timestamp_ns'] == str((200 + c.get('observed', 0) - 1) * 1000000000) for row in b['rows']), 'authored event clocks changed')
            members_match(json.loads(s['quint.arg.members']), b['rows'], b)
            require(s['quint.obs.expires'] == seconds(cap['expiresAt']), 'native expiry changed')
            require(s['quint.obs.basis'] == cap['basis'], 'native basis changed')
            require((s['quint.obs.status'] == 'ok') == (not n.get('error')), 'issue error changed')
        elif action == 'resolve':
            refs = n['refs']
            request_slots = json.loads(s['quint.arg.slots'])
            request_keys = json.loads(s['quint.arg.keys'])
            require(request_slots == [r['slot'] for r in c['refs']], 'request slot aliases changed')
            require(request_keys == [r['key'] for r in refs], 'request keys changed')
            require(len(refs) == len(c['refs']), 'authored reference count changed')
            for alias, ref, authored_ref in zip(request_slots, refs, c['refs']):
                require(ref['token'] == slots[alias]['capability']['token'], 'opaque capability alias changed')
                issued_rows = slots[alias]['input']['rows']
                index = authored_ref['row']
                wanted = issued_rows[index]['key'] if 0 <= index < len(issued_rows) else 'forged'
                require(ref['key'] == wanted, 'authored row reference changed')
            require(s['quint.arg.connection'] == c['connection'] and s['quint.arg.selector'] == c['selector'], 'request owner changed')
            d = n.get('draft')
            require(s['quint.obs.hasDraft'] == (d is not None), 'draft presence changed')
            missing = mode == 'missing' and c['name'] == 'before-mutation'
            require(('quint.obs.members' not in s) == missing, 'missing-evidence control drifted')
            if not missing:
                members_match(json.loads(s['quint.obs.members']), d['members'] if d else [])
            require(s['quint.obs.connection'] == (d['connection'] if d else ''), 'returned owner changed')
            require(s['quint.obs.selector'] == (d['selector']['environment'] if d else ''), 'returned scope changed')
            require((s['quint.obs.status'] == 'ok') == (not n.get('error')), 'resolve error changed')
            if d:
                requested = Counter((r['token'], r['key']) for r in refs)
                returned = Counter((m['ref']['token'], m['ref']['key']) for m in d['members'])
                require(requested == returned, 'returned native proof references changed')
    return steps


def check(mode, report, status, trace, observations, cases):
    expected = {'faithful': 0, 'contrast': 1, 'missing': 2}[mode]
    require(status == expected, f'{mode}: process exit {status}, expected {expected}')
    r = json.loads(Path(report).read_text())
    require(r['format'] == 'gotogether.report/2' and r['command'] == 'validate' and r['exit'] == expected, 'wrong native report')
    steps = native_gate(mode, trace, observations, cases)
    if mode == 'missing':
        require(r['verdict'] == 'error' and 'members' in r.get('error', ''), 'missing observation became a model verdict')
    else:
        require(r['verdict'] == ('pass' if mode == 'faithful' else 'fail'), 'wrong conformance verdict')
        actor = r['actors'][0]
        require(actor['conformance'] == r['verdict'], 'native actor conformance disagrees')
        if mode == 'contrast':
            failures = actor['failures']
            require(len(failures) == 1 and failures[0]['kind'] == 'state', 'source contrast did not produce observed-state mismatch')
            record = failures[0]['record']
            require(record['ref'] == 'case:expired-at-boundary' and record['action'] == 'resolve', 'contrast failed at another contract edge')
            require(record['obs']['hasDraft'] and record['obs']['status'] == 'ok', 'contrast did not actually return expired proof')
            require(steps[record['seq'] - 1]['quint.ref'] == record['ref'], 'failure reference lost')
    print(json.dumps({'mode': mode, 'exit': status, 'steps': len(steps), 'verdict': r['verdict'], 'claim': 'native conformance; no invented model post-state'}))


if __name__ == '__main__':
    try:
        require(len(sys.argv) == 7, 'usage: check_evidence.py MODE REPORT EXIT STEPS NATIVE CASES')
        check(sys.argv[1], sys.argv[2], int(sys.argv[3]), *sys.argv[4:])
    except (ValueError, KeyError, TypeError, OSError) as error:
        print(f'Membership evidence check failed: {error}', file=sys.stderr)
        sys.exit(1)
