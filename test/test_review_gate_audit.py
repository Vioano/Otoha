"""Independent protocol regressions: branch isolation and actual CLI boundaries."""
import copy
import importlib.util
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[1]
SPEC = importlib.util.spec_from_file_location('review_audit_core', ROOT / 'packages/review-gate/otoha_review.py')
CORE = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(CORE)
CLI = ROOT / 'scripts/review-gate.py'


def pack():
    return {'protocol': 'otoha.review/v1', 'id': 'audit.example',
            'areas': [{'id': 'delivery', 'name': 'Delivery'}],
            'questions': [
                {'id': 'kind', 'area': 'delivery', 'prompt': 'Which file extension was observed?',
                 'choices': [{'id': 'text', 'label': '.txt'}, {'id': 'deck', 'label': '.pptx'}]},
                {'id': 'played', 'area': 'delivery', 'type': 'boolean',
                 'prompt': 'Did an embedded audio stream play?', 'required_when': {'kind': ['deck']}}],
            'rules': []}


def observe(value, *, choice=False):
    return {'choice' if choice else 'value': value, 'evidence': 'Measured in the declared artifact.'}


class EngineAudit(unittest.TestCase):
    def run_pack(self, data, observations):
        return CORE.evaluate(data, CORE.validate_definition(data), observations)

    def test_negated_inactive_observation_does_not_trigger_gate(self):
        data = pack()
        data['rules'] = [{'id': 'audio.not-played', 'area': 'delivery',
                          'when': {'not': {'question': 'played', 'op': 'eq', 'value': True}},
                          'outcome': 'blocked', 'reason': 'Audio has not played.'}]
        report = self.run_pack(data, {'kind': observe('text', choice=True)})
        self.assertTrue(report['released'])
        self.assertFalse(report['rules'][0]['matched'])

    def test_not_missing_stays_unknown(self):
        data = pack()
        data['rules'] = [{'id': 'audio.not-played', 'area': 'delivery',
                          'when': {'not': {'question': 'played', 'op': 'eq', 'value': True}},
                          'outcome': 'blocked', 'reason': 'Audio has not played.'}]
        report = self.run_pack(data, {'kind': observe('deck', choice=True)})
        self.assertIsNone(report['rules'][0]['matched'])
        self.assertEqual(report['status'], 'unverified')

    def test_unknown_score_stays_unverified_until_observed(self):
        data = pack()
        data['scoring'] = {'minimum': 10, 'items': [
            {'id': 'playback', 'when': {'question': 'played', 'op': 'eq', 'value': True}, 'points': 10}]}
        report = self.run_pack(data, {'kind': observe('deck', choice=True)})
        self.assertEqual(report['status'], 'unverified')
        self.assertFalse(report['released'])
        report = self.run_pack(data, {'kind': observe('deck', choice=True), 'played': observe(False)})
        self.assertEqual(report['status'], 'blocked')

    def test_unknown_waiver_cannot_resolve_confirmed_finding(self):
        data = pack()
        data['rules'] = [{'id': 'all-decks', 'area': 'delivery', 'when': {'kind': ['deck']},
                          'outcome': 'blocked', 'reason': 'Example finding.', 'waivable': True}]
        data['waivers'] = [{'id': 'played-exception', 'rule_id': 'all-decks',
                            'when': {'question': 'played', 'op': 'eq', 'value': True},
                            'reason': 'Explicit policy exception.'}]
        report = self.run_pack(data, {'kind': observe('deck', choice=True)})
        self.assertFalse(report['released'])
        self.assertEqual(report['status'], 'blocked')
        self.assertEqual(report['waivers'], [])

    def test_waiving_one_finding_does_not_hide_another(self):
        data = pack()
        data['rules'] = [
            {'id': 'one', 'area': 'delivery', 'when': {'kind': ['text']},
             'outcome': 'blocked', 'reason': 'First finding.', 'waivable': True},
            {'id': 'two', 'area': 'delivery', 'when': {'kind': ['text']},
             'outcome': 'needs_arbitration', 'reason': 'Second finding.'}]
        data['waivers'] = [{'id': 'first-only', 'rule_id': 'one', 'when': {'kind': ['text']},
                            'reason': 'Explicit policy exception.'}]
        data['scoring'] = {'minimum': 10, 'items': [{'id': 'points', 'when': {'kind': ['text']}, 'points': 999}]}
        report = self.run_pack(data, {'kind': observe('text', choice=True)})
        self.assertFalse(report['released'])
        self.assertEqual(report['status'], 'needs_arbitration')
        self.assertEqual({row['rule_id'] for row in report['findings']}, {'one', 'two'})

    def test_observer_verdict_field_is_rejected(self):
        data = pack()
        facts = {'kind': dict(observe('text', choice=True), status='pass')}
        with self.assertRaises(ValueError):
            self.run_pack(data, facts)

    def test_boolean_cannot_fill_number_question(self):
        data = pack()
        data['questions'][1] = {'id': 'count', 'area': 'delivery', 'type': 'number', 'prompt': 'How many files?'}
        with self.assertRaises(ValueError):
            self.run_pack(data, {'kind': observe('text', choice=True), 'count': observe(True)})

    def test_unknown_cannot_carry_known_value(self):
        data = pack()
        with self.assertRaises(ValueError):
            self.run_pack(data, {'kind': observe('deck', choice=True),
                                 'played': dict(observe(True), unknown=True)})


class CLIAudit(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.root = Path(self.tmp.name)
        self.rules = self.root / 'rules.json'
        self.state = self.root / 'state.json'
        self.artifact = self.root / 'artifact.txt'
        self.rules.write_text(json.dumps(pack()), encoding='utf-8')
        self.artifact.write_text('version one', encoding='utf-8')
        self.env = dict(os.environ, CODEX_THREAD_ID='audit-maker')

    def cli(self, *args, actor=None):
        env = dict(self.env, CODEX_THREAD_ID=actor or self.env['CODEX_THREAD_ID'])
        result = subprocess.run([sys.executable, str(CLI), *map(str, args), '--json'],
                                capture_output=True, text=True, env=env)
        return result.returncode, json.loads(result.stdout)

    def complete(self):
        code, _ = self.cli('start', '--rules', self.rules, '--state', self.state, '--artifact', self.artifact)
        self.assertEqual(code, 0)
        code, _ = self.cli('observe', 'kind', '--choice', 'text', '--evidence', 'The file has extension .txt.', '--state', self.state)
        self.assertEqual(code, 0)
        code, report = self.cli('finish', '--state', self.state)
        self.assertEqual(code, 0)
        self.assertTrue(report['released'])

    def test_changed_artifact_invalidates_finished_run(self):
        self.complete()
        self.artifact.write_text('version two', encoding='utf-8')
        code, report = self.cli('finish', '--state', self.state)
        self.assertEqual(code, 1)
        self.assertFalse(report['released'])

    def test_changed_rules_cannot_regrade_old_run(self):
        self.complete()
        self.rules.write_text(self.rules.read_text() + '\n', encoding='utf-8')
        code, report = self.cli('finish', '--state', self.state)
        self.assertEqual(code, 2)
        self.assertFalse(report['released'])

    def test_other_actor_can_read_but_cannot_finish(self):
        self.complete()
        code, _ = self.cli('status', '--state', self.state, actor='audit-reader')
        self.assertEqual(code, 0)
        code, report = self.cli('finish', '--state', self.state, actor='audit-reader')
        self.assertEqual(code, 2)
        self.assertFalse(report['released'])

    def test_duplicate_json_policy_key_rejected(self):
        self.rules.write_text('{"protocol":"otoha.review/v1","protocol":"other"}', encoding='utf-8')
        code, report = self.cli('validate', '--rules', self.rules)
        self.assertEqual(code, 2)
        self.assertFalse(report['released'])

    def test_unknown_typed_answer_is_saved_as_unverified(self):
        self.cli('start', '--rules', self.rules, '--state', self.state)
        self.cli('observe', 'kind', '--choice', 'deck', '--evidence', 'File extension .pptx.', '--state', self.state)
        code, report = self.cli('observe', 'played', '--unknown', '--evidence', 'Playback not observed.', '--state', self.state)
        self.assertEqual(code, 0)
        self.assertEqual(report['status'], 'unverified')
        self.assertFalse(report['released'])


if __name__ == '__main__':
    unittest.main()
