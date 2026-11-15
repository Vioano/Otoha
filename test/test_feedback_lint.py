"""Human correction -> scoped checker, using disposable synthetic projects."""
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import unittest


ROOT = Path(__file__).resolve().parents[1]
# Build a deliberately rejected fixture at runtime. The project's active-source
# naming rule also applies to this test; test folders are not broadly exempt.
OLD_NAME = ''.join(chr(c) for c in (65, 116, 108, 97, 115))


class FeedbackLintTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix='otoha-feedback-lint-')
        self.root = Path(self.temp.name)
        (self.root / 'scripts').mkdir()
        shutil.copyfile(ROOT / 'scripts' / 'check-names.py', self.root / 'scripts' / 'check-names.py')

    def tearDown(self):
        self.temp.cleanup()

    def write(self, path, text):
        target = self.root / path
        target.parent.mkdir(parents=True, exist_ok=True)
        target.write_text(text, encoding='utf-8')

    def check(self):
        return subprocess.run([sys.executable, str(self.root / 'scripts' / 'check-names.py')],
                              capture_output=True, text=True, check=False)

    def test_active_source_regression_reports_rule_and_location(self):
        self.write('src/main.py', 'title = "Otoha"\n' + 'title = ' + repr(OLD_NAME) + '\n')
        result = self.check()
        self.assertEqual(result.returncode, 1)
        self.assertIn('src/main.py:2 [otoha/product-name]', result.stdout)
        self.assertIn('docs/FEEDBACK-TO-CHECKS.zh-CN.md#existing-rule', result.stdout)

    def test_public_entry_and_filename_are_checked(self):
        self.write('README.md', OLD_NAME)
        self.write('src/' + OLD_NAME.lower() + '.py', 'title = "Otoha"\n')
        result = self.check()
        self.assertEqual(result.returncode, 1)
        self.assertIn('README.md:1', result.stdout)
        self.assertIn('src/' + OLD_NAME.lower() + '.py: filename', result.stdout)

    def test_current_name_passes_and_provenance_exceptions_stay_valid(self):
        self.write('src/main.py', 'title = "Otoha"\n')
        self.write('README.md', '# Otoha\n')
        self.write('docs/provenance/history.md', 'Historical source: ' + OLD_NAME)
        self.write('NOTICE.md', 'Original attribution: ' + OLD_NAME)
        self.write('vendor/LICENSE', 'Copyright ' + OLD_NAME)
        self.write('vendor/third_party.txt', 'The unrelated third-party name is ' + OLD_NAME + 'sian.')
        result = self.check()
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)

    def test_correcting_the_violation_clears_it_without_widening_exemptions(self):
        self.write('src/main.py', OLD_NAME)
        self.assertEqual(self.check().returncode, 1)
        self.write('src/main.py', 'Otoha')
        self.assertEqual(self.check().returncode, 0)
        self.write('test/runtime_example.py', OLD_NAME)
        result = self.check()
        self.assertEqual(result.returncode, 1)
        self.assertIn('test/runtime_example.py:1', result.stdout)

    def test_exact_external_skill_reference_is_not_product_wording(self):
        external = 'system-' + OLD_NAME.lower()
        self.write('skills/guide.md', 'Optional external skill: `' + external + '`.')
        self.assertEqual(self.check().returncode, 0)
        self.write('skills/guide.md', 'Optional `' + external + '`, product name ' + OLD_NAME)
        result = self.check()
        self.assertEqual(result.returncode, 1)
        self.assertIn('skills/guide.md:1', result.stdout)

    def test_external_identifier_exception_does_not_allow_other_compound_names(self):
        self.write('src/main.py', 'Otoha-system-' + OLD_NAME.lower())
        self.assertEqual(self.check().returncode, 1)


if __name__ == '__main__':
    unittest.main()
