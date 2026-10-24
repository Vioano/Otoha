"""Installation in temporary repositories; no network or user configuration."""
import importlib.util
from pathlib import Path
import subprocess
import tempfile
import unittest

SOURCE = Path(__file__).resolve().parents[1] / 'scripts/install.py'
spec = importlib.util.spec_from_file_location('install_mailbox', SOURCE)
m = importlib.util.module_from_spec(spec)
spec.loader.exec_module(m)


class InstallTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix='mailbox 中文 ')
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name).resolve()
        subprocess.run(['git', 'init', '-q', str(self.root)], check=True)

    def test_install_and_repeat_do_not_change_project_rules(self):
        rules = self.root / 'AGENTS.md'
        rules.write_text('Existing project rules', encoding='utf-8')
        result = m.install(self.root)
        target = Path(result['skill_dir'])
        self.assertTrue((target / 'SKILL.md').is_file())
        self.assertTrue((target / 'LICENSE').is_file())
        self.assertEqual(m.install(self.root)['status'], 'unchanged')
        self.assertEqual(rules.read_text(encoding='utf-8'), 'Existing project rules')
        self.assertFalse((self.root / '.codex').exists())
        self.assertEqual(list(target.rglob('*.pyc')), [])

    def test_update_requires_flag_and_keeps_backup_of_local_changes(self):
        target = Path(m.install(self.root)['skill_dir'])
        local = target / 'SKILL.md'
        local.write_text('Local team customization', encoding='utf-8')
        with self.assertRaises(ValueError):
            m.install(self.root)
        self.assertEqual(local.read_text(encoding='utf-8'), 'Local team customization')
        updated = m.install(self.root, update=True)
        backup = Path(updated['backup'])
        self.assertEqual((backup / 'SKILL.md').read_text(encoding='utf-8'), 'Local team customization')
        self.assertNotEqual(local.read_text(encoding='utf-8'), 'Local team customization')
        self.assertTrue(str(backup).startswith(str(self.root / '.git')))

    def test_claude_install_and_external_script_execution(self):
        target = Path(m.install(self.root, agent='claude')['skill_dir'])
        self.assertEqual(target.parent, self.root / '.claude/skills')
        import sys
        result = subprocess.run([sys.executable, str(target / 'scripts/mailbox.py'), '--help'],
                                capture_output=True, text=True)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn('check', result.stdout)

    def test_non_git_project_is_rejected_without_installing(self):
        with tempfile.TemporaryDirectory() as temp:
            with self.assertRaises(subprocess.CalledProcessError):
                m.install(temp)
            self.assertFalse((Path(temp) / '.agents').exists())


if __name__ == '__main__':
    unittest.main()
