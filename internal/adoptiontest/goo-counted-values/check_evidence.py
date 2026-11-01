#!/usr/bin/env python3
"""Check native evidence and exact integers against observed public responses.

Classification comes from the independent model's native report. This checker
never synthesizes a model post-state when the backend did not provide one.
"""
import json
import re
import sys
from pathlib import Path

PROPERTIES = {'countDomain', 'positiveGroups', 'stateClassification'}
CASE_NAMES = ('empty', 'above-float-exactness', 'max-uint64', 'sum-beyond-uint64')
MAX_COUNT = (1 << 64) - 1


def require(condition, message):
    if not condition:
        raise ValueError(message)


def count(text, label):
    require(type(text) is str and re.fullmatch(r'0|[1-9][0-9]*', text), f'{label}: noncanonical decimal string')
    value = int(text)
    require(value <= MAX_COUNT, f'{label}: count exceeds individual uint64 domain')
    return value


def integer_list(text, label):
    require(type(text) is str, f'{label}: native collection must be JSON text')
    values = json.loads(text)
    require(type(values) is list and all(type(v) is int for v in values), f'{label}: collection lost integer types')
    return values


def public_evidence(mode, steps_path, results_path):
    rows = [json.loads(line) for line in Path(steps_path).read_text().splitlines() if line.strip()]
    require(rows and rows[0] == {'quint.format': 'gotogether.steps/1'}, 'missing native format record')
    steps = rows[1:]
    results = json.loads(Path(results_path).read_text())
    require(type(results) is list and len(results) == len(steps) == 4, 'expected all four real executor observations')
    require(tuple(item.get('case', {}).get('name') for item in results) == CASE_NAMES, 'case coverage/order changed')
    missing = []
    for i, (record, observed) in enumerate(zip(steps, results)):
        case = observed['case']
        actual = observed['result']
        label = case['name']
        require(observed.get('reads') == 2, f'{label}: executor did not make both reads')
        require(actual.get('version') == 'goo/counted-values/v1', f'{label}: wrong public response format')
        require(record.get('quint.seq') == i + 1 and type(record.get('quint.seq')) is int, f'{label}: sequence changed')
        require(record.get('quint.action') == 'lookup' and record.get('quint.recorder') == record.get('quint.actor') == 'counted', f'{label}: wrong native action/identity')
        require(record.get('quint.ref') == 'case:' + label, f'{label}: missing native case reference')
        require(record.get('quint.outcome') == 'ok' and not record.get('quint.error'), f'{label}: executor/tool error cannot establish a model result')
        total = count(case['total'], label + ' source total')
        source_groups = [count(c, label + ' source group') for c in case['groups']]
        require(type(record.get('quint.arg.total')) is int and record['quint.arg.total'] == total, f'{label}: source total rounded or changed')
        require(integer_list(record.get('quint.arg.groups'), label + ' source groups') == source_groups, f'{label}: source groups rounded or changed')
        actual_count = count(actual['count'], label + ' public count')
        actual_group_strings = [g['count'] for g in actual['groups']]
        actual_groups = [count(c, label + ' public group') for c in actual_group_strings]
        if 'quint.obs.count' not in record:
            missing.append(i)
        else:
            require(type(record['quint.obs.count']) is int and record['quint.obs.count'] == actual_count, f'{label}: observed count rounded or changed')
        require(record.get('quint.obs.rawCount') == actual['count'], f'{label}: raw public count changed')
        require(integer_list(record.get('quint.obs.groups'), label + ' observed groups') == actual_groups, f'{label}: observed groups changed')
        require(type(record.get('quint.obs.rawGroups')) is str and json.loads(record['quint.obs.rawGroups']) == actual_group_strings, f'{label}: raw public groups changed')
        require(record.get('quint.obs.state') == actual['state'], f'{label}: observed state differs from actual public response')
    require(len(missing) == (1 if mode == 'missing' else 0), 'missing-count control must omit exactly one count observation')
    # These are the frozen source stimuli, not a state-classification table.
    require([(item['case']['total'], item['case']['groups']) for item in results] == [
        ('0', []), ('9007199254740993', ['9007199254740993']),
        ('18446744073709551615', ['18446744073709551615']),
        ('0', ['18446744073709551615', '1']),
    ], 'large-integer source boundary cases changed')
    if mode == 'contrast':
        last = steps[-1]
        require(last['quint.obs.state'] == 'no_data', 'contrast did not actually misclassify the overflowing sum')
        require(last['quint.arg.total'] == 0 and sum(integer_list(last['quint.arg.groups'], 'contrast groups')) == 1 << 64, 'contrast did not exercise mathematical uint64-sum overflow')
    return steps


def check(mode, report, status, steps_path, results_path):
    expected = {'faithful': 0, 'contrast': 1, 'missing': 2}[mode]
    require(status == expected, f'{mode}: process exit {status}, expected {expected}')
    require(report.get('format') == 'gotogether.report/1' and report.get('command') == 'validate', 'wrong native report format/command')
    require(type(report.get('exit')) is int and report['exit'] == expected, 'report exit differs from process result')
    steps = public_evidence(mode, steps_path, results_path)
    inputs = report.get('inputs', [])
    require(len(inputs) == 1 and inputs[0].get('steps') == 4 and inputs[0].get('format') == 'steps', 'report input coverage changed')
    if mode == 'missing':
        require(report.get('verdict') == 'error', 'missing evidence was counted as a model failure')
        message = report.get('error', '')
        require('obs.count' in message and 'which the step does not have' in message, 'wrong missing-observation diagnostic')
        require(not report.get('actors'), 'missing evidence acquired actor/model verdicts')
        print(json.dumps({'mode': mode, 'exit': status, 'verdict': 'error', 'claim': 'missing evidence, not a model counterexample'}))
        return
    require('error' not in report, 'tool error cannot establish a semantic model verdict')
    actors = report.get('actors', [])
    require(len(actors) == 1 and actors[0].get('actor') == 'counted' and actors[0].get('steps') == 4, 'wrong actor coverage')
    actor = actors[0]
    receipt = report.get('receipt', {})
    actor_receipt = actor.get('receipt', {})
    require(receipt.get('scope') == actor_receipt.get('scope') == 'collected_history' and receipt.get('otherExecutions') == 'not_checked', 'native history scope changed')
    require(receipt.get('actors') == [actor_receipt], 'aggregate/actor receipts differ')
    require(actor.get('judgedBy') == actor_receipt.get('judgedBy') == 'quint', 'big-integer judgment did not come from Quint')
    checks = actor_receipt.get('checks', [])
    require(len(checks) == 4 and {(c.get('kind'), c.get('name')) for c in checks} == {('conformance', 'history')} | {('invariant', name) for name in PROPERTIES}, 'selected native checks changed')
    if mode == 'faithful':
        require(report.get('verdict') == receipt.get('status') == actor.get('verdict') == actor.get('conformance') == 'pass', 'faithful execution did not pass')
        require(not actor.get('failures') and all(c.get('status') == 'pass' for c in checks), 'faithful checks were unjudged or failed')
        invariants = actor.get('invariants', [])
        require(len(invariants) == 3 and {c.get('name') for c in invariants if c.get('verdict') == 'pass'} == PROPERTIES, 'faithful invariant coverage changed')
        print(json.dumps({'mode': mode, 'exit': status, 'verdict': 'pass', 'steps': len(steps), 'judgedBy': 'quint'}))
        return
    require(report.get('verdict') == receipt.get('status') == actor.get('verdict') == actor.get('conformance') == 'fail', 'contrast is not native conformance failure')
    require(report.get('reason') == 'mismatch after lookup', 'contrast drifted to another model failure')
    failures = actor.get('failures', [])
    require(len(failures) == 1, 'contrast must identify one precise failure')
    failure = failures[0]
    require(failure.get('kind') == 'state' and failure.get('check') == 'conformance', 'contrast did not fail observed-state conformance')
    record = failure.get('record', {})
    require(record.get('seq') == 4 and record.get('action') == 'lookup' and record.get('ref') == 'case:sum-beyond-uint64', 'contrast points to the wrong observed call')
    require(type(record.get('args', {}).get('total')) is int and record['args']['total'] == 0, 'failure total changed or rounded')
    require(integer_list(record.get('args', {}).get('groups'), 'failure groups') == [MAX_COUNT, 1], 'failure groups changed or rounded')
    require(record.get('obs', {}).get('state') == 'no_data', 'failure did not retain actual wrong public state')
    require({c.get('name') for c in checks if c.get('kind') == 'invariant' and c.get('status') == 'not_checked'} == PROPERTIES, 'invariants after nonconformance were falsely judged')
    print(json.dumps({'mode': mode, 'exit': status, 'verdict': 'fail', 'sequence': 4, 'judgedBy': 'quint', 'modelPostStateAvailable': bool(failure.get('state')), 'claim': 'native observed-state mismatch; no invented model post-state'}))


def main(argv):
    require(len(argv) == 6, 'usage: check_evidence.py faithful|contrast|missing REPORT PROCESS_STATUS STEPS RESULTS')
    mode, report_path, status, steps_path, results_path = argv[1:]
    require(mode in ('faithful', 'contrast', 'missing'), 'unknown check mode')
    check(mode, json.loads(Path(report_path).read_text()), int(status), steps_path, results_path)


if __name__ == '__main__':
    try:
        main(sys.argv)
    except (ValueError, KeyError, TypeError, OSError, json.JSONDecodeError) as error:
        print(f'Counted-values evidence check failed: {error}', file=sys.stderr)
        sys.exit(1)
