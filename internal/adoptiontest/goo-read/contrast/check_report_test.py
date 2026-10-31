"""Negative controls for the pinned semantic verifier; real output is checked by reproduce.sh."""
import copy
import json
import tempfile
import unittest
from pathlib import Path

from check_report import CASES, check


class SemanticVerifierTest(unittest.TestCase):
    def setUp(self):
        self.directory = tempfile.TemporaryDirectory()
        self.addCleanup(self.directory.cleanup)
        self.steps = Path(self.directory.name) / 'steps.jsonl'
        self.rows = [{'quint.format': 'gotogether.steps/1'}]
        for case in range(16):
            accepted = case < 2
            self.rows.append({'quint.action': 'read', 'quint.arg.caseID': case,
                              'quint.recorder': 'capture', 'quint.seq': case + 1,
                              'quint.obs.accepted': accepted, 'quint.obs.hasError': not accepted,
                              'quint.obs.nilDefinition': not accepted,
                              'quint.obs.fieldCount': 3 if accepted else 0,
                              'quint.obs.measureCount': (3 if case == 1 else 1) if accepted else 0,
                              'quint.obs.name': 'cohort' if accepted else ''})
        self.write_rows()
        checks = [{'kind': 'conformance', 'name': 'history', 'status': 'pass'},
                  {'kind': 'invariant', 'name': 'boundedCase', 'status': 'pass'},
                  {'kind': 'invariant', 'name': 'acceptedShape', 'status': 'pass'}]
        actor_receipt = {'checks': checks}
        self.report = {'format': 'gotogether.report/1', 'command': 'validate', 'verdict': 'pass', 'exit': 0,
                       'inputs': [{'steps': 16}],
                       'receipt': {'status': 'pass', 'scope': 'collected_history', 'otherExecutions': 'not_checked',
                                   'actors': [actor_receipt]},
                       'actors': [{'steps': 16, 'verdict': 'pass', 'conformance': 'pass', 'receipt': actor_receipt}]}

    def write_rows(self):
        self.steps.write_text(''.join(json.dumps(r) + '\n' for r in self.rows))

    def contrast(self):
        for case in range(2, 6):
            self.rows[case + 1].update({'quint.obs.accepted': True, 'quint.obs.hasError': False,
                                       'quint.obs.nilDefinition': False, 'quint.obs.fieldCount': 3,
                                       'quint.obs.measureCount': 3 if case == 5 else 1, 'quint.obs.name': 'cohort'})
        self.write_rows()
        result = copy.deepcopy(self.report)
        result.update({'verdict': 'fail', 'exit': 1, 'reason': 'mismatch after read'})
        result['receipt']['status'] = 'fail'
        actor = result['actors'][0]
        actor.update({'verdict': 'fail', 'conformance': 'fail', 'failures': [{
            'kind': 'state', 'check': 'conformance', 'stateAfter': True,
            'state': {'accepted': 'false', 'fieldCount': '0', 'measureCount': '0', 'lastCase': '2'},
            'record': {'action': 'read', 'args': {'caseID': 2},
                       'obs': {'accepted': True, 'hasError': False, 'nilDefinition': False,
                               'fieldCount': 3, 'measureCount': 1}}}]})
        for c in actor['receipt']['checks']:
            c['status'] = 'fail' if c['kind'] == 'conformance' else 'not_checked'
        return result

    def rejected(self, mode, report, status):
        with self.assertRaises(ValueError):
            check(mode, report, status, self.steps)

    def test_known_shapes_support_negative_controls(self):
        check('faithful', self.report, 0, self.steps)
        check('contrast', self.contrast(), 1, self.steps)

    def test_any_nonzero_is_not_a_contrast_catch(self):
        report = self.contrast()
        self.rejected('contrast', report, 2)
        report['exit'] = 2
        report['verdict'] = 'error'
        self.rejected('contrast', report, 2)

    def test_other_failure_cannot_receive_credit(self):
        for part, value in [('kind', 'invariant'), ('check', 'tool')]:
            with self.subTest(part=part):
                report = self.contrast()
                report['actors'][0]['failures'][0][part] = value
                self.rejected('contrast', report, 1)
        for part, value in [('action', 'other'), ('args', {'caseID': 3})]:
            with self.subTest(part=part):
                report = self.contrast()
                report['actors'][0]['failures'][0]['record'][part] = value
                self.rejected('contrast', report, 1)

    def test_model_must_still_require_rejection(self):
        report = self.contrast()
        report['actors'][0]['failures'][0]['state']['accepted'] = 'true'
        self.rejected('contrast', report, 1)

    def test_missing_or_truncated_capture_is_rejected(self):
        self.rows.pop()
        self.write_rows()
        self.rejected('faithful', self.report, 0)
        self.steps.unlink()
        with self.assertRaises(FileNotFoundError):
            check('faithful', self.report, 0, self.steps)

    def test_unobserved_or_fabricated_acceptance_is_rejected(self):
        del self.rows[3]['quint.obs.accepted']
        self.write_rows()
        self.rejected('faithful', self.report, 0)

    def test_missing_selected_check_is_rejected(self):
        self.report['actors'][0]['receipt']['checks'].pop()
        self.rejected('faithful', self.report, 0)

    def test_unknown_obligation_is_not_a_pass(self):
        self.report['actors'][0]['receipt']['checks'][1]['status'] = 'not_checked'
        self.rejected('faithful', self.report, 0)

    def graph(self):
        return {'format': 'gotogether.report/1', 'command': 'simulate', 'exit': 0, 'verdict': 'pass',
                'checks': [{'kind': 'possible', 'name': case, 'verdict': 'reached'} for case in CASES] +
                          [{'kind': 'invariant', 'name': prop, 'verdict': 'holds'} for prop in ('boundedCase', 'acceptedShape')],
                'graph': {'scope': 'model_state_graph', 'states': 17, 'starts': 1, 'transitions': 272,
                          'depth': 1, 'complete': True, 'bound': {'states': 100000, 'depth': 0}},
                'notes': ['graph   complete: 17 states, 272 transitions, depth 1, 0.0s']}

    def test_structured_graph_scope(self):
        report = self.graph()
        check('graph', report, 0)
        for mutation in ('absent', 'incomplete', 'wrong_bound', 'wrong_count', 'stopped', 'wrong_scope'):
            with self.subTest(mutation=mutation):
                report = self.graph()
                if mutation == 'absent':
                    del report['graph']
                elif mutation == 'incomplete':
                    report['graph']['complete'] = False
                elif mutation == 'wrong_bound':
                    report['graph']['bound']['states'] = 1
                elif mutation == 'wrong_count':
                    report['graph']['transitions'] = 271
                elif mutation == 'wrong_scope':
                    report['graph']['scope'] = 'sampled_traces'
                else:
                    report['graph']['stopped'] = 'unsupported expression'
                self.rejected('graph', report, 0)

    def test_missing_control_must_name_the_absent_observation(self):
        del self.rows[1]['quint.obs.accepted']
        self.write_rows()
        report = {'format': 'gotogether.report/1', 'command': 'validate', 'exit': 2, 'verdict': 'error',
                  'inputs': [{'steps': 16}], 'error': 'field "obs.accepted", which the step does not have'}
        check('missing', report, 2, self.steps)
        report['error'] = 'unrelated compiler error'
        self.rejected('missing', report, 2)


if __name__ == '__main__':
    unittest.main()
