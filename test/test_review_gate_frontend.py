"""Local frontend-adapter/Stop compatibility; synthetic facts are not visual QA.

Runs only when the installed frontend skill is available. Its files are copied
to a temporary directory; its real records, hook registry and policy are never
written. FRONTEND_ACCEPTANCE_SKILL may select another installed distribution.
"""
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import unittest

INSTALLED = Path(os.environ.get('FRONTEND_ACCEPTANCE_SKILL',
                 str(Path.home() / '.codex/skills/frontend-acceptance')))


@unittest.skipUnless((INSTALLED / 'scripts/otoha_review.py').is_file(),
                     'requires an installed frontend adapter using the shared core')
class FrontendSharedCoreIntegration(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.root = Path(self.tmp.name)
        self.skill = self.root / 'frontend-acceptance'
        for relative in ('scripts/acceptance.py', 'scripts/native_stop.py', 'scripts/otoha_review.py',
                         'references/questions.json', 'references/policy.json',
                         'references/questions39.snapshot.json'):
            source = INSTALLED / relative
            if not source.exists():
                continue
            target = self.skill / relative
            target.parent.mkdir(parents=True, exist_ok=True)
            shutil.copyfile(source, target)
        # Disable peer only in the test-owned copy to isolate engine/adapter/Stop.
        (self.skill / 'references/policy.json').write_text('{"require_review": false}\n')
        self.cli = self.skill / 'scripts/acceptance.py'
        self.hook = self.skill / 'scripts/native_stop.py'
        self.state = self.root / 'frontend-run.json'
        self.actor = 'synthetic-review-integration'
        self.tool_call = 'synthetic-tool-call'
        self.turn = 'synthetic-turn'
        self.env = dict(os.environ, CODEX_THREAD_ID=self.actor, CODEX_TOOL_CALL_ID=self.tool_call,
                        CODEX_HOME=str(self.root / 'codex-home'),
                        FRONTEND_ACCEPTANCE_REGISTRY=str(self.root / 'hook-registry'))

    def frontend(self, *args, code=0):
        result = subprocess.run([sys.executable, str(self.cli), *map(str, args),
                                 '--state', str(self.state), '--json'],
                                capture_output=True, text=True, env=self.env)
        self.assertEqual(result.returncode, code, result.stdout + result.stderr)
        return json.loads(result.stdout)

    def event(self, name, *, turn=None):
        event = {'hook_event_name': name, 'session_id': self.actor,
                 'turn_id': turn or self.turn, 'tool_use_id': self.tool_call}
        result = subprocess.run([sys.executable, str(self.hook)], input=json.dumps(event),
                                capture_output=True, text=True, env=self.env)
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        return json.loads(result.stdout)

    def complete_observations(self, *, duplicate):
        report = self.frontend('start', '--target-kind', 'static', '--content-revision', 'synthetic-fixture-v1',
                               '--surface', 'fixture=Synthetic fixture|fixture-static-state')
        choices = {
            'layout.coverage': 'named_surfaces', 'layout.editorial': 'absent',
            'layout.guide_requested': 'no', 'layout.duplicate': 'present' if duplicate else 'absent',
            'layout.necessary': 'present', 'layout.relation_carrier': 'arranged_objects',
            'layout.without_supporting_copy': 'objects_located',
            'interaction.controls': 'present', 'interaction.ran': 'executed',
            'interaction.observed': 'none_listed',
            'prototype.scope': 'new', 'prototype.artifact': 'ui_mockup',
            'prototype.implemented': 'same_structure', 'reference.observed': 'project',
            'theme.requirement': 'both', 'theme.observed': 'two_readable',
            'theme.initial': 'dark', 'theme.remembered': 'restored',
            'authorship.assigned': 'none', 'request.expected': 'still', 'request.actual': 'still',
            'interaction.selection_detail': 'select_only',
            'interaction.keyboard_focus_target': 'named_control_or_region',
        }
        recorded = []
        while report['next_question'] is not None:
            ident = report['next_question']
            base = ident.partition('@')[0]
            self.assertIn(base, choices, 'Installed definition changed; update this explicit compatibility vector.')
            report = self.frontend('observe', ident, choices[base], '--evidence',
                                   'Synthetic compatibility fixture: supplied fact vector; no actual visual inspection claimed.')
            recorded.append(ident)
            self.assertLessEqual(len(recorded), 30, 'Question traversal must make progress.')
        self.assertIn('layout.without_supporting_copy@fixture', recorded)
        self.assertIn('interaction.keyboard_focus_target', recorded)
        return report

    def test_pass_finish_and_same_turn_stop_allow(self):
        report = self.complete_observations(duplicate=False)
        self.assertEqual(report['status'], 'pass')
        finished = self.frontend('finish')
        self.assertEqual(finished['status'], 'pass')
        self.assertIsNotNone(json.loads(self.state.read_text())['finished_at'])
        self.assertEqual(self.event('PostToolUse'), {})
        self.assertEqual(self.event('Stop'), {})

    def test_blocked_finish_and_same_turn_stop_block(self):
        report = self.complete_observations(duplicate=True)
        self.assertEqual(report['status'], 'blocked')
        finished = self.frontend('finish', code=1)
        self.assertEqual(finished['status'], 'blocked')
        self.assertIsNone(json.loads(self.state.read_text())['finished_at'])
        self.assertEqual(self.event('PostToolUse'), {})
        stopped = self.event('Stop')
        self.assertEqual(stopped['decision'], 'block')
        self.assertIn('blocked', stopped['reason'])
        self.assertEqual(self.event('Stop', turn='another-synthetic-turn'), {})


if __name__ == '__main__':
    unittest.main()
