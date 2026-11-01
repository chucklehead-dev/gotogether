"""Negative controls for evidence transport, independent of conformance replay."""
import base64
import copy
import json
from pathlib import Path
import tempfile
import unittest

import check_evidence as evidence


class NativeGateTests(unittest.TestCase):
    def setUp(self):
        self.root = tempfile.TemporaryDirectory()
        self.addCleanup(self.root.cleanup)
        self.dir = Path(self.root.name)
        connection, token, basis = 'a' * 64, 'b' * 64, 'c' * 64
        record = {'Body': 'original', 'timestamp_ns': '199000000000'}
        member = {'key': 'original-key', 'body': base64.b64encode(json.dumps(record).encode()).decode(),
                  'from': -60, 'to': 0, 'observed': 0, 'basis': '', 'late': False, 'limited': False}
        issue = {'name': 'issued', 'action': 'issue', 'connection': connection,
                 'selector': 'test', 'bodies': ['original']}
        resolve = {'name': 'before-mutation', 'action': 'resolve', 'connection': connection,
                   'selector': 'test', 'refs': [{'slot': 0, 'row': 0}]}
        ref = {'token': token, 'key': member['key']}
        native_member = {'ref': ref, 'fromMs': 140000, 'toMs': 200000,
                         'observedAt': '1970-01-01T00:03:20Z', 'basis': basis,
                         'limited': False, 'late': False, 'record': record}
        self.actual = [
            {'case': {'name': 'initial', 'action': 'reset'}},
            {'case': issue, 'input': {'connection': connection, 'selector': {'environment': 'test'},
                'fromMs': 140000, 'toMs': 200000, 'observedAt': '1970-01-01T00:03:20Z',
                'limited': False, 'rows': [{'key': member['key'], 'record': record, 'late': False}]},
             'capability': {'token': token, 'basis': basis, 'expiresAt': '1970-01-01T00:05:20Z'}},
            {'case': resolve, 'refs': [ref], 'draft': {'connection': connection,
                'selector': {'environment': 'test'}, 'members': [native_member]}},
        ]
        self.actual += [{'case': {'name': f'padding-{i}', 'action': 'reset'}} for i in range(35)]
        self.cases = [copy.deepcopy(n['case']) for n in self.actual]
        self.steps = []
        for i, n in enumerate(self.actual):
            c = n['case']
            self.steps.append({'quint.seq': i + 1, 'quint.action': c['action'],
                'quint.ref': 'case:' + c['name'], 'quint.actor': 'membership',
                'quint.recorder': 'membership', 'quint.outcome': 'ok'})
        self.steps[1].update({'quint.arg.observed': 0, 'quint.arg.slot': 0,
            'quint.arg.connection': connection, 'quint.arg.selector': 'test',
            'quint.arg.members': json.dumps([member]), 'quint.obs.expires': 120,
            'quint.obs.basis': basis, 'quint.obs.status': 'ok'})
        returned = dict(member, basis=basis)
        self.steps[2].update({'quint.arg.slots': '[0]', 'quint.arg.keys': json.dumps([member['key']]),
            'quint.arg.connection': connection, 'quint.arg.selector': 'test',
            'quint.obs.hasDraft': True, 'quint.obs.members': json.dumps([returned]),
            'quint.obs.connection': connection, 'quint.obs.selector': 'test', 'quint.obs.status': 'ok'})
        # Fixture mutation must not change a request and response simultaneously.
        self.actual = copy.deepcopy(self.actual)
        self.actual[2]['refs'] = copy.deepcopy(self.actual[2]['refs'])

    def gate(self, mode='faithful'):
        trace, native, cases = [self.dir / n for n in ('steps.jsonl', 'native.json', 'cases.json')]
        trace.write_text('\n'.join(json.dumps(x) for x in [{'quint.format': 'gotogether.steps/1'}] + self.steps))
        native.write_text(json.dumps(self.actual))
        cases.write_text(json.dumps(self.cases))
        return evidence.native_gate(mode, trace, native, cases)

    def test_control_passes(self):
        self.assertEqual(len(self.gate()), 38)

    def test_unknown_native_request_token_rejected(self):
        self.actual[2]['refs'][0]['token'] = 'd' * 64
        with self.assertRaisesRegex(ValueError, 'capability alias'):
            self.gate()

    def test_unknown_native_return_token_rejected(self):
        self.actual[2]['draft']['members'][0]['ref']['token'] = 'd' * 64
        with self.assertRaisesRegex(ValueError, 'proof references'):
            self.gate()

    def test_changed_projected_body_rejected(self):
        members = json.loads(self.steps[2]['quint.obs.members'])
        members[0]['body'] = base64.b64encode(b'{"Body":"changed","timestamp_ns":"199000000000"}').decode()
        self.steps[2]['quint.obs.members'] = json.dumps(members)
        with self.assertRaisesRegex(ValueError, 'native record changed'):
            self.gate()

    def test_subsecond_read_window_rejected(self):
        self.actual[2]['draft']['members'][0]['fromMs'] += 1
        with self.assertRaisesRegex(ValueError, 'subsecond read window'):
            self.gate()

    def test_subsecond_native_clock_rejected(self):
        self.actual[2]['draft']['members'][0]['observedAt'] = '1970-01-01T00:03:20.000001Z'
        with self.assertRaisesRegex(ValueError, 'subsecond native clock'):
            self.gate()

    def test_submicrosecond_native_clock_rejected(self):
        # datetime truncates nanoseconds, while the native Go clock retains them.
        self.actual[2]['draft']['members'][0]['observedAt'] = '1970-01-01T00:03:20.000000001Z'
        with self.assertRaisesRegex(ValueError, 'subsecond native clock'):
            self.gate()

    def test_missing_observation_cannot_be_faithful(self):
        del self.steps[2]['quint.obs.members']
        with self.assertRaisesRegex(ValueError, 'missing-evidence control'):
            self.gate()
        self.assertEqual(len(self.gate('missing')), 38)

    def test_consistently_shifted_request_row_rejected(self):
        # A coherent native/projection pair still must execute the authored row.
        self.actual[1]['case']['bodies'].append('second')
        self.cases[1]['bodies'].append('second')
        row = copy.deepcopy(self.actual[1]['input']['rows'][0])
        row['key'] = 'second-key'
        row['record']['Body'] = 'second'
        self.actual[1]['input']['rows'].append(row)
        members = json.loads(self.steps[1]['quint.arg.members'])
        second = dict(members[0], key='second-key',
                      body=base64.b64encode(json.dumps(row['record']).encode()).decode())
        members.append(second)
        self.steps[1]['quint.arg.members'] = json.dumps(members)
        self.actual[2]['refs'][0]['key'] = 'second-key'
        self.steps[2]['quint.arg.keys'] = '["second-key"]'
        returned = self.actual[2]['draft']['members'][0]
        returned['ref']['key'] = 'second-key'
        returned['record'] = copy.deepcopy(row['record'])
        self.steps[2]['quint.obs.members'] = json.dumps([dict(second, basis=returned['basis'])])
        with self.assertRaises(ValueError):
            self.gate()

    def test_consistently_changed_authored_body_rejected(self):
        record = dict(self.actual[1]['input']['rows'][0]['record'], Body='changed')
        self.actual[1]['input']['rows'][0]['record'] = record
        members = json.loads(self.steps[1]['quint.arg.members'])
        members[0]['body'] = base64.b64encode(json.dumps(record).encode()).decode()
        self.steps[1]['quint.arg.members'] = json.dumps(members)
        with self.assertRaises(ValueError):
            self.gate()


if __name__ == '__main__':
    unittest.main()
