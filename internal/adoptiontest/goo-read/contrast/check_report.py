#!/usr/bin/env python3
"""Verify this frozen Read rehearsal's semantic evidence, not generic exit success."""
import json
import sys
from pathlib import Path

CASES = (
    'valid_count', 'valid_ratio', 'unknown_top', 'unknown_source',
    'unknown_measure', 'unknown_predicate', 'extra_document', 'invalid_version',
    'missing_subject_key', 'unsupported_type', 'no_measures',
    'invalid_predicate_type', 'invalid_ratio_reference', 'invalid_binding',
    'malformed_yaml', 'empty_input',
)
PROPERTIES = {'boundedCase', 'acceptedShape'}


def require(condition, message):
    if not condition:
        raise ValueError(message)


def native_steps(path, contrast=False, missing=False):
    rows = [json.loads(line) for line in Path(path).read_text().splitlines() if line.strip()]
    require(rows and rows[0] == {'quint.format': 'gotogether.steps/1'}, 'missing native format record')
    records = rows[1:]
    require(len(records) == 16, 'capture must contain every one of the 16 real Read calls')
    require([r.get('quint.arg.caseID') for r in records] == list(range(16)), 'case coverage/order changed')
    for case, record in enumerate(records):
        require(record.get('quint.action') == 'read', 'capture includes an unrelated action')
        require(type(record.get('quint.arg.caseID')) is int, 'case ID is not an integer input')
        require(record.get('quint.seq') == case + 1, 'capture sequence is incomplete')
        require(record.get('quint.recorder'), 'capture has no native recorder identity')
        accepted = case < (6 if contrast else 2)
        if missing and case == 0:
            require('quint.obs.accepted' not in record, 'missing-observation control did not remove accepted')
        else:
            require(record.get('quint.obs.accepted') is accepted, f'case {case} acceptance is not the actual expected result')
        require(record.get('quint.obs.hasError') is (not accepted), f'case {case} error observation changed')
        require(record.get('quint.obs.nilDefinition') is (not accepted), f'case {case} definition presence changed')
        require(record.get('quint.obs.fieldCount') == (3 if accepted else 0), f'case {case} field count changed')
        count = 3 if case in (1, 5) else 1
        require(record.get('quint.obs.measureCount') == (count if accepted else 0), f'case {case} measure count changed')
        require(record.get('quint.obs.name') == ('cohort' if accepted else ''), f'case {case} returned name changed')
    return records


def check(mode, report, status, steps=None):
    require(report.get('format') == 'gotogether.report/2', 'wrong report format')
    expected = {'faithful': 0, 'graph': 0, 'contrast': 1, 'missing': 2}[mode]
    require(type(status) is int and status == expected, f'{mode} process exit {status}, expected {expected}')
    require(type(report.get('exit')) is int and report.get('exit') == expected, 'report exit disagrees with the expected process result')
    if mode == 'graph':
        require(report.get('command') == 'simulate' and report.get('verdict') == 'pass', 'graph did not pass')
        checks = report.get('checks', [])
        reached = {c.get('name') for c in checks if c.get('kind') == 'possible' and c.get('verdict') == 'reached'}
        held = {c.get('name') for c in checks if c.get('kind') == 'invariant' and c.get('verdict') == 'holds'}
        require(reached == set(CASES), 'not every authored input scenario is reachable')
        require(held == PROPERTIES, 'selected structural safety properties did not hold')
        graph = report.get('graph')
        require(isinstance(graph, dict), 'structured graph scope is required; notes are not proof')
        require(graph.get('scope') == 'model_state_graph', 'wrong graph scope')
        require(graph.get('complete') is True, 'graph exploration is incomplete')
        require(all(type(graph.get(k)) is int for k in ('states', 'starts', 'transitions', 'depth')), 'graph extent must use integer counts')
        require(graph.get('states') == 17 and graph.get('starts') == 1 and graph.get('transitions') == 272 and graph.get('depth') == 1, 'frozen graph extent changed')
        require(isinstance(graph.get('bound'), dict) and all(type(graph['bound'].get(k)) is int for k in ('states', 'depth')) and graph['bound'] == {'states': 100000, 'depth': 0}, 'graph search limits changed')
        require(graph.get('cut', '') == '' and graph.get('stopped', '') == '', 'graph was cut or stopped')
        return
    require(report.get('command') == 'validate', 'wrong validation command')
    require(steps is not None, 'native captured steps are required')
    native_steps(steps, contrast=mode == 'contrast', missing=mode == 'missing')
    inputs = report.get('inputs', [])
    require(len(inputs) == 1 and inputs[0].get('steps') == 16, 'report omits or truncates captured inputs')
    if mode == 'missing':
        require(report.get('verdict') == 'error', 'missing evidence was counted as a model defect')
        error = report.get('error', '')
        require('"obs.accepted"' in error and 'which the step does not have' in error, 'wrong missing-field diagnostic')
        require(not report.get('actors'), 'missing evidence acquired model actor verdicts')
        return
    require('error' not in report, 'tool error cannot establish a semantic verdict')
    actors = report.get('actors', [])
    require(len(actors) == 1 and actors[0].get('steps') == 16, 'wrong actor coverage')
    actor = actors[0]
    receipt = report.get('receipt', {})
    require(receipt.get('scope') == 'collected_history' and receipt.get('otherExecutions') == 'not_checked', 'history scope changed')
    checks = actor.get('receipt', {}).get('checks', [])
    require({(c.get('kind'), c.get('name')) for c in checks} == {('conformance', 'history'), ('invariant', 'boundedCase'), ('invariant', 'acceptedShape')} and len(checks) == 3, 'selected checks are missing or changed')
    require(receipt.get('actors') == [actor.get('receipt')], 'aggregate and actor receipts disagree')
    if mode == 'faithful':
        require(report.get('verdict') == receipt.get('status') == actor.get('verdict') == actor.get('conformance') == 'pass', 'faithful execution or replay did not pass')
        require(not actor.get('failures'), 'faithful result contains a failure')
        require({c.get('name') for c in checks if c.get('kind') == 'invariant' and c.get('status') == 'pass'} == PROPERTIES, 'faithful properties were not judged')
        require(all(c.get('status') == 'pass' for c in checks), 'faithful report contains an unjudged check')
        return
    require(report.get('verdict') == receipt.get('status') == actor.get('verdict') == actor.get('conformance') == 'fail', 'contrast is not a model failure')
    require(report.get('reason') == 'mismatch after read', 'contrast drifted to another failure')
    failures = actor.get('failures', [])
    require(len(failures) == 1, 'contrast must identify one precise conformance failure')
    failure = failures[0]
    require(failure.get('kind') == 'state' and failure.get('check') == 'conformance', 'not the expected state mismatch')
    record = failure.get('record', {})
    require(record.get('action') == 'read' and record.get('args', {}).get('caseID') == 2, 'wrong action or authored case')
    obs = record.get('obs', {})
    require(obs.get('accepted') is True and obs.get('hasError') is False and obs.get('nilDefinition') is False, 'contrast did not actually accept the unknown field')
    require(obs.get('fieldCount') == 3 and obs.get('measureCount') == 1, 'wrong accepted-definition shape')
    require(failure.get('stateAfter') is True, 'model state is not the post-read state')
    state = failure.get('state', {})
    require(state.get('accepted') == 'false' and state.get('fieldCount') == '0' and state.get('measureCount') == '0' and state.get('lastCase') == '2', 'frozen model no longer requires this input to be rejected')
    require({c.get('name') for c in checks if c.get('kind') == 'invariant' and c.get('status') == 'not_checked'} == PROPERTIES, 'invariants after nonconformance were falsely judged')


def main(argv):
    require(len(argv) in (4, 5), 'usage: check_report.py faithful|contrast|missing|graph REPORT PROCESS_STATUS [STEPS]')
    mode, path, status = argv[1:4]
    require(mode in ('faithful', 'contrast', 'missing', 'graph'), 'unknown mode')
    check(mode, json.loads(Path(path).read_text()), int(status), argv[4] if len(argv) == 5 else None)


if __name__ == '__main__':
    try:
        main(sys.argv)
    except (ValueError, KeyError, TypeError, OSError, json.JSONDecodeError) as error:
        print(f'Read rehearsal check failed: {error}', file=sys.stderr)
        sys.exit(1)
