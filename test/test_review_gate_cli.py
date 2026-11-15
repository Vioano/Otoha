"""Exercise the public observation interface and frozen-run boundaries."""
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[1]
CLI = ROOT / 'scripts/review-gate.py'


class ReviewCLI(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.directory = Path(self.temp.name)
        self.rules = self.directory / 'rules.json'
        self.state = self.directory / 'run.json'
        self.artifact = self.directory / 'artifact.txt'
        self.artifact.write_text('revision one')
        self.rules.write_bytes((ROOT / 'examples/review-gate/artifact-review.json').read_bytes())
        self.env = dict(os.environ, CODEX_THREAD_ID='test-maker')

    def run_cli(self, command, *args, code=0, env=None):
        result = subprocess.run([sys.executable, str(CLI), command, '--json', *map(str, args)],
                                text=True, capture_output=True, env=env or self.env)
        self.assertEqual(result.returncode, code, result.stdout + result.stderr)
        return json.loads(result.stdout)

    def start(self):
        return self.run_cli('start', '--state', self.state, '--rules', self.rules, '--artifact', self.artifact)

    def observe(self, question, flag, value):
        return self.run_cli('observe', '--state', self.state, question, flag, value,
                            '--evidence', 'fixture: exact measured or recorded observation')

    def complete(self, delivery='native', media=False, played=False, ratio=0):
        self.start()
        self.observe('delivery', '--choice', delivery)
        self.observe('ratio_error', '--value', json.dumps(ratio))
        self.observe('covered', '--value', 'true')
        self.observe('has_media', '--value', json.dumps(media))
        if media:
            self.observe('played', '--value', json.dumps(played))
        self.observe('conflict', '--value', 'false')

    def test_complete_no_media_passes_but_not_missing_observations(self):
        self.assertFalse(self.start()['released'])
        result = self.run_cli('finish', '--state', self.state, code=1)
        self.assertEqual(result['status'], 'unverified')
        self.assertIsNone(json.loads(self.state.read_text())['finished_at'])

    def test_valid_run_passes_and_artifact_change_revokes_it(self):
        self.complete()
        self.assertTrue(self.run_cli('finish', '--state', self.state)['released'])
        self.artifact.write_text('revision two')
        result = self.run_cli('finish', '--state', self.state, code=1)
        self.assertFalse(result['released'])
        self.assertTrue(any(row.get('rule_id') == 'run.artifact-changed' for row in result['issues']))

    def test_policy_change_cannot_reuse_prior_record(self):
        self.complete()
        pack = json.loads(self.rules.read_text())
        pack['rules'] = []
        pack['waivers'] = []
        self.rules.write_text(json.dumps(pack))
        result = self.run_cli('finish', '--state', self.state, code=2)
        self.assertFalse(result['released'])
        self.assertIn('题包已改变', result['reason'])

    def test_static_exception_retains_waiver_and_native_delivery_is_held(self):
        self.complete(delivery='static', media=True)
        result = self.run_cli('finish', '--state', self.state)
        self.assertTrue(result['released'])
        self.assertTrue(result['waivers'])
        self.observe('delivery', '--choice', 'native')
        result = self.run_cli('finish', '--state', self.state, code=1)
        self.assertEqual(result['status'], 'on_hold')
        self.assertFalse(result['released'])

    def test_high_score_does_not_clear_conflicting_facts(self):
        self.complete()
        self.observe('conflict', '--value', 'true')
        result = self.run_cli('finish', '--state', self.state, code=1)
        self.assertEqual(result['status'], 'needs_arbitration')
        self.assertFalse(result['released'])

    def test_actor_cannot_write_other_runs_but_can_inspect(self):
        self.start()
        stranger = dict(self.env, CODEX_THREAD_ID='test-stranger')
        self.run_cli('status', '--state', self.state, env=stranger)
        result = self.run_cli('observe', '--state', self.state, 'delivery', '--choice', 'native',
                              '--evidence', 'observed', code=2, env=stranger)
        self.assertFalse(result['released'])
        self.assertEqual(json.loads(self.state.read_text())['observations'], {})

    def test_invalid_type_cannot_pollute_state(self):
        self.start()
        self.run_cli('observe', '--state', self.state, 'ratio_error', '--value', 'true',
                     '--evidence', 'wrong type', code=2)
        self.assertEqual(json.loads(self.state.read_text())['observations'], {})

    def test_duplicate_policy_keys_and_unsupported_pass_field_are_rejected(self):
        self.rules.write_text('{"protocol":"otoha.review/v1","protocol":"other"}')
        self.run_cli('validate', '--rules', self.rules, code=2)
        pack = json.loads((ROOT / 'examples/review-gate/artifact-review.json').read_text())
        pack['pass'] = True
        self.rules.write_text(json.dumps(pack))
        self.run_cli('validate', '--rules', self.rules, code=2)


if __name__ == '__main__':
    unittest.main()
