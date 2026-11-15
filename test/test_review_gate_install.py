"""The shared-core helper updates only the evaluator of a known adapter."""
import ast
import json
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[1]
INSTALLER = ROOT / 'scripts/sync-review-core.py'
ENGINE = ROOT / 'packages/review-gate/otoha_review.py'


class CoreInstallTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.skill = Path(self.tmp.name) / 'frontend-acceptance'
        (self.skill / 'scripts').mkdir(parents=True)
        (self.skill / 'references').mkdir()
        self.adapter = self.skill / 'scripts/acceptance.py'
        self.questions = self.skill / 'references/questions.json'
        self.questions.write_bytes(b'{"preserve":"exact bytes"}\n')
        self.native_stop = self.skill / 'scripts/native_stop.py'
        self.native_stop.write_bytes(b'# Existing host integration; preserve.\n')
        self.original = ('# Existing frontend adapter\n'
                         'def evaluate(data, questions, observations):\n'
                         '    return {"status": "old"}\n\n'
                         'def target_check():\n'
                         '    return "frontend-target-stays"\n')
        self.adapter.write_text(self.original, encoding='utf-8')

    def run_install(self, *, apply=False, code=0):
        command = [sys.executable, str(INSTALLER), '--frontend-skill', str(self.skill)]
        if apply:
            command.append('--apply')
        result = subprocess.run(command, text=True, capture_output=True)
        self.assertEqual(result.returncode, code, result.stdout + result.stderr)
        return json.loads(result.stdout) if code == 0 else None

    def test_dry_run_writes_nothing(self):
        report = self.run_install()
        self.assertFalse(report['applied'])
        self.assertTrue(report['adapter_changed'])
        self.assertEqual(self.adapter.read_text(), self.original)
        self.assertFalse((self.skill / 'scripts/otoha_review.py').exists())

    def test_apply_is_local_import_idempotent_and_preserves_adapter(self):
        questions = self.questions.read_bytes()
        host = self.native_stop.read_bytes()
        first = self.run_install(apply=True)
        updated = self.adapter.read_text()
        tree = ast.parse(updated)
        imports = [node for node in tree.body if isinstance(node, ast.ImportFrom)]
        self.assertTrue(any(node.module == 'otoha_review' and node.level == 0 for node in imports))
        self.assertNotIn(str(ROOT), updated)
        self.assertIn('def target_check():\n    return "frontend-target-stays"\n', updated)
        self.assertEqual((self.skill / 'scripts/otoha_review.py').read_bytes(), ENGINE.read_bytes())
        self.assertEqual(self.questions.read_bytes(), questions)
        self.assertEqual(self.native_stop.read_bytes(), host)
        second = self.run_install(apply=True)
        self.assertFalse(second['adapter_changed'])
        self.assertEqual(first['engine_sha256'], second['engine_sha256'])
        self.assertEqual(self.adapter.read_text(), updated)
        result = subprocess.run([sys.executable, '-c',
                                 'import acceptance; assert callable(acceptance.evaluate); '
                                 'assert acceptance.target_check()=="frontend-target-stays"'],
                                cwd=self.skill / 'scripts', capture_output=True, text=True)
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)

    def test_signature_drift_is_rejected_without_writes(self):
        for signature in ('data, questions, observations, extra',
                          'data, questions, observations, *, required',
                          'data, questions, observations, /',
                          'data, questions, observations, **extras'):
            original = f'def evaluate({signature}):\n    return None\n'
            self.adapter.write_text(original)
            self.run_install(apply=True, code=2)
            self.assertEqual(self.adapter.read_text(), original)
            self.assertFalse((self.skill / 'scripts/otoha_review.py').exists())

    def test_aliased_import_is_not_assumed_to_bind_evaluate(self):
        original = 'from otoha_review import evaluate as another_name\n'
        self.adapter.write_text(original)
        self.run_install(apply=True, code=2)
        self.assertEqual(self.adapter.read_text(), original)
        self.assertFalse((self.skill / 'scripts/otoha_review.py').exists())


if __name__ == '__main__':
    unittest.main()
