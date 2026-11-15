"""Quality verdicts and current artifacts, rather than a review runner exit code."""
import hashlib
import importlib.util
import json
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest


ROOT = Path(__file__).resolve().parents[1]
SCRIPT = ROOT / 'skills/otoha-content-review/scripts/check_review_result.py'
SPEC = importlib.util.spec_from_file_location('content_review_result', SCRIPT)
MODULE = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(MODULE)


class ContentReviewResultTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix='otoha-review-result-')
        self.root = Path(self.temp.name)
        self.report_path = self.root / 'review.json'
        self.manifest_path = self.root / 'manifest.json'
        self.artifact = self.root / 'deck.pptx'
        self.artifact.write_bytes(b'synthetic deck version 1')
        self.images = []
        for page in [1, 2]:
            image = self.root / f'slide-{page:02}.png'
            image.write_bytes(f'synthetic page {page}'.encode())
            self.images.append(image)
        self.manifest = {'inputs': [
            {'path': path.name, 'sha256': hashlib.sha256(path.read_bytes()).hexdigest()}
            for path in self.images + [self.artifact]
        ]}
        self.report = {'overall_verdict': '通过', 'process_review': {'verdict': '通过'},
                       'viewed_pages': [{'page': i + 1, 'image_path': path.name,
                                         'page_verdict': '通过', 'findings': []}
                                        for i, path in enumerate(self.images)]}

    def tearDown(self):
        self.temp.cleanup()

    def run_gate(self):
        self.report_path.write_text(json.dumps(self.report), encoding='utf-8')
        self.manifest_path.write_text(json.dumps(self.manifest), encoding='utf-8')
        return MODULE.check(self.report_path, self.manifest_path, self.artifact)

    def test_complete_pass_with_current_artifact(self):
        result = self.run_gate()
        self.assertEqual(result['exit_code'], 0, result)
        self.assertEqual(result['coverage']['matched_pages'], 2)
        self.assertTrue(all(x['hash_matches'] for x in result['inputs']))

    def test_runner_success_does_not_override_negative_quality(self):
        self.report['execution'] = {'returncode': 0}
        self.report['overall_verdict'] = '不通过'
        self.report['viewed_pages'][0]['page_verdict'] = '需返修'
        result = self.run_gate()
        self.assertEqual((result['status'], result['exit_code']), ('blocked', 1))
        self.assertTrue(any(x.get('page') == 1 for x in result['blocked']))

    def test_unresolved_major_finding_blocks_even_with_pass_labels(self):
        self.report['viewed_pages'][1]['findings'] = [
            {'severity': 'major', 'element': 'branch arrow', 'observation': 'Points at the wrong object.'}
        ]
        result = self.run_gate()
        self.assertEqual(result['exit_code'], 1)
        self.assertEqual(result['blocked'][0]['page'], 2)

    def test_process_and_whole_deck_findings_are_not_ignored(self):
        for scope in ['process_review', 'whole_deck_review']:
            with self.subTest(scope=scope):
                self.report.setdefault(scope, {})['findings'] = [{'severity': 'blocker', 'element': 'sequence'}]
                result = self.run_gate()
                self.assertEqual(result['exit_code'], 1)
                self.assertTrue(any(x.get('scope') == scope for x in result['blocked']))
                self.report[scope].pop('findings')

    def test_unknown_finding_severity_is_unverified(self):
        self.report['viewed_pages'][0]['findings'] = [{'severity': 'maybe important'}]
        self.assertEqual(self.run_gate()['exit_code'], 2)

    def test_explicit_whole_deck_failure_blocks_pass_labels(self):
        self.report['whole_deck_review'] = {'verdict': '不通过'}
        result = self.run_gate()
        self.assertEqual(result['exit_code'], 1)
        self.assertTrue(any(x['code'] == 'whole_deck.verdict' for x in result['blocked']))

    def test_nonimage_cannot_replace_reviewed_pages(self):
        self.manifest['review_pages'] = [self.artifact.name]
        self.report['viewed_pages'] = [{'page': 1, 'image_path': self.artifact.name,
                                        'page_verdict': '通过', 'findings': []}]
        result = self.run_gate()
        self.assertEqual(result['exit_code'], 2)
        self.assertEqual(result['coverage']['expected_pages'], 0)

    def test_invalid_status_and_path_return_structured_diagnostics(self):
        self.report['viewed_pages'][0]['findings'] = [{'severity': 'minor', 'status': []}]
        result = self.run_gate()
        self.assertEqual(result['exit_code'], 2)
        self.assertTrue(any(x['code'] == 'finding.status' for x in result['unverified']))
        self.report['viewed_pages'][0]['findings'] = []
        self.manifest['inputs'][0]['path'] = '\x00'
        result = self.run_gate()
        self.assertEqual(result['exit_code'], 2)
        self.assertTrue(any(x['code'] == 'manifest.entry' for x in result['unverified']))

    def test_changed_page_hash_is_unverified(self):
        self.images[0].write_bytes(b'new page with old review')
        result = self.run_gate()
        self.assertEqual((result['status'], result['exit_code']), ('unverified', 2))
        self.assertTrue(any(x['code'] == 'input.changed' for x in result['unverified']))

    def test_changed_artifact_hash_is_unverified(self):
        self.artifact.write_bytes(b'new PPT with old screenshots')
        result = self.run_gate()
        self.assertEqual(result['exit_code'], 2)

    def test_missing_page_cannot_be_replaced_by_overall_pass(self):
        self.report['viewed_pages'].pop()
        result = self.run_gate()
        self.assertEqual(result['exit_code'], 2)
        self.assertEqual(result['coverage']['matched_pages'], 1)
        self.assertTrue(any(x['code'] == 'coverage.missing' for x in result['unverified']))

    def test_failed_process_blocks_passed_pages(self):
        self.report['process_review']['verdict'] = '不通过（前置依据缺失）'
        result = self.run_gate()
        self.assertEqual(result['exit_code'], 1)
        self.assertTrue(any(x['code'] == 'process.verdict' for x in result['blocked']))

    def test_missing_requested_artifact_binding_is_unverified(self):
        self.manifest['inputs'].pop()
        result = self.run_gate()
        self.assertEqual(result['exit_code'], 2)
        self.assertTrue(any(x['code'] == 'artifact.unbound' for x in result['unverified']))

    def test_unknown_verdict_and_explicit_required_gap_do_not_pass(self):
        for value in ['looks good', '通过但尚未审查布局', None]:
            with self.subTest(verdict=value):
                self.report['overall_verdict'] = value
                self.assertEqual(self.run_gate()['exit_code'], 2)
        self.report['overall_verdict'] = '通过'
        self.report['required_unverified'] = ['native playback']
        self.assertEqual(self.run_gate()['exit_code'], 2)

    def test_informational_out_of_scope_gaps_are_not_guessed(self):
        self.report['not_verified'] = ['A later live performance, outside this static review.']
        result = self.run_gate()
        self.assertEqual(result['exit_code'], 0, result)
        self.assertEqual(len(result['outside_or_unspecified_verification']), 1)

    def test_reference_images_can_be_excluded_by_explicit_page_selection(self):
        self.manifest['inputs'].append({'path': 'reference.png', 'sha256': hashlib.sha256(b'reference').hexdigest()})
        (self.root / 'reference.png').write_bytes(b'reference')
        self.manifest['review_pages'] = [path.name for path in self.images]
        self.assertEqual(self.run_gate()['exit_code'], 0)

    def test_cli_uses_nonzero_quality_exit_with_json_diagnostics(self):
        self.report['overall_verdict'] = '不通过'
        self.run_gate()
        completed = subprocess.run([sys.executable, str(SCRIPT), '--report', str(self.report_path),
                                    '--manifest', str(self.manifest_path), '--artifact', str(self.artifact), '--json'],
                                   text=True, capture_output=True)
        self.assertEqual(completed.returncode, 1, completed.stderr)
        self.assertEqual(json.loads(completed.stdout)['status'], 'blocked')


if __name__ == '__main__':
    unittest.main()
