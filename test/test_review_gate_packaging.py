"""Packaging/install contract tests; fake Go output does not claim binary QA."""
import argparse
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tarfile
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[1]
spec = importlib.util.spec_from_file_location('review_package_manager', ROOT / 'scripts/private-team/manage.py')
manager = importlib.util.module_from_spec(spec)
spec.loader.exec_module(manager)


class ResourceInstallTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.cleanup)
        self.directory = Path(self.temp.name)
        self.source = self.directory / 'candidate'
        self.source.mkdir()

    def cleanup(self):
        for parent, dirs, files in os.walk(self.temp.name):
            os.chmod(parent, 0o755)
        self.temp.cleanup()

    def write(self, relative, content='resource'):
        path = self.source / relative
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(content)
        return path

    def candidate(self):
        self.write('bin/otoha')
        for name in manager.BACKENDS:
            self.write('services/collaboration/' + name)

    def install(self, version='test-review'):
        surreal = self.directory / 'surreal'
        surreal.write_text('#!/bin/sh\nprintf "surreal 3.3.5\\n"\n')
        surreal.chmod(0o755)
        args = argparse.Namespace(root=str(self.directory / 'releases-root'),
                                  data_root=str(self.directory / 'data-root'), unit_prefix='test-review',
                                  user='test-review', version=version, source=str(self.source),
                                  surreal_bin=str(surreal))
        deployment = manager.Deployment(args)
        deployment.require_root = lambda: None  # Test-owned dirs; no systemd/data operation occurs.
        deployment.install()
        return deployment.release(version)

    def test_old_candidate_remains_installable_without_optional_resources(self):
        self.candidate()
        release = self.install()
        self.assertTrue((release / 'bin/otoha').is_file())
        self.assertFalse((release / 'scripts/review-gate.py').exists())

    def test_installed_resources_hashed_readonly_and_private_paths_excluded(self):
        self.candidate()
        self.write('packages/review-gate/otoha_review.py', 'shared-core')
        self.write('scripts/review-gate.py', 'entry')
        self.write('skills/otoha-content-review/SKILL.md', 'skill')
        self.write('skills/otoha-visualize/assets/view-state.js', 'bridge')
        self.write('skills/otoha-visualize/__pycache__/cache.pyc')
        self.write('skills/otoha-visualize/assets/._view-state.js')
        self.write('work/private.json', 'not a resource')
        external = self.directory / 'external-private.txt'
        external.write_text('not a resource')
        (self.source / 'skills/otoha-visualize/private-link').symlink_to(external)
        release = self.install()
        manifest = json.loads((release / 'private-team-release.json').read_text())
        for relative in ('packages/review-gate/otoha_review.py', 'scripts/review-gate.py',
                         'skills/otoha-content-review/SKILL.md', 'skills/otoha-visualize/assets/view-state.js'):
            path = release / relative
            self.assertEqual(path.read_bytes(), (self.source / relative).read_bytes())
            self.assertEqual(manifest['sha256'][relative], hashlib.sha256(path.read_bytes()).hexdigest())
            self.assertEqual(path.stat().st_mode & 0o777, 0o444)
        for relative in ('work/private.json', 'skills/otoha-visualize/private-link',
                         'skills/otoha-visualize/__pycache__/cache.pyc',
                         'skills/otoha-visualize/assets/._view-state.js'):
            self.assertFalse((release / relative).exists())

    def test_cli_without_core_is_rejected_before_install(self):
        self.candidate()
        self.write('scripts/review-gate.py')
        with self.assertRaisesRegex(ValueError, 'shared core'):
            self.install()
        self.assertFalse((self.directory / 'releases-root').exists())

    def test_symlinked_core_directory_cannot_supply_external_source(self):
        self.candidate()
        self.write('scripts/review-gate.py')
        external = self.directory / 'external-core'
        external.mkdir()
        (external / 'otoha_review.py').write_text('external')
        (self.source / 'packages').mkdir()
        (self.source / 'packages/review-gate').symlink_to(external, target_is_directory=True)
        with self.assertRaisesRegex(ValueError, 'shared core'):
            self.install()


@unittest.skipUnless(shutil.which('node') and shutil.which('git'), 'requires Node and Git packaging tools')
class ClientPackageTests(unittest.TestCase):
    def test_tar_contains_review_resources_and_fingerprint_tracks_them(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary) / 'repo'
            root.mkdir()
            def write(relative, content='fixture'):
                path = root / relative
                path.parent.mkdir(parents=True, exist_ok=True)
                path.write_text(content)
                return path
            write('scripts/package-cli.mjs', (ROOT / 'scripts/package-cli.mjs').read_text())
            for relative in ('scripts/runtime-prepare.py', 'scripts/runtime-assets.json', 'apps/cli/go.mod',
                             'apps/cli/LICENSE', 'NOTICE.md', 'docs/AGENT-ENTRY.md'):
                write(relative)
            for name in manager.BACKENDS:
                write('services/collaboration/' + name)
            write('packages/review-gate/otoha_review.py', 'core')
            write('scripts/review-gate.py', 'entry')
            bridge = write('skills/otoha-visualize/assets/view-state.js', 'bridge-v1')
            write('skills/otoha-content-review/SKILL.md', 'content skill')
            write('skills/otoha-visualize/__pycache__/cache.pyc')
            write('skills/otoha-visualize/assets/._bridge')
            private = Path(temporary) / 'private.txt'
            private.write_text('private')
            (root / 'skills/otoha-visualize/private-link').symlink_to(private)
            fake_go = Path(temporary) / 'fake-go'
            fake_go.write_text('#!' + sys.executable + '\nimport pathlib,sys\np=pathlib.Path(sys.argv[sys.argv.index("-o")+1]);p.write_text("packaging test binary")\n')
            fake_go.chmod(0o755)
            env = dict(os.environ, OTOHA_GO=str(fake_go))
            subprocess.run(['git', 'init', '-q'], cwd=root, check=True, capture_output=True)
            subprocess.run(['git', 'add', '.'], cwd=root, check=True, capture_output=True)
            subprocess.run(['git', '-c', 'user.name=Fixture', '-c', 'user.email=fixture@example.invalid',
                            'commit', '-qm', 'fixture'], cwd=root, check=True, capture_output=True)
            write('skills/otoha-visualize/untracked-private.json', 'not versioned')
            fingerprints = []
            for index in (1, 2):
                if index == 2:
                    bridge.write_text('bridge-v2')
                output = Path(temporary) / ('out-' + str(index))
                result = subprocess.run(['node', 'scripts/package-cli.mjs', '--version', 'test-review',
                                         '--out', str(output)], cwd=root, env=env, capture_output=True, text=True)
                self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
                manifest = json.loads((output / 'manifest.json').read_text())
                fingerprints.append(manifest['source_inputs_sha256'])
                for archive in output.glob('*.tar.gz'):
                    with tarfile.open(archive) as package:
                        prefix = archive.name.removesuffix('.tar.gz') + '/'
                        names = {name.removeprefix(prefix) for name in package.getnames()}
                        self.assertIn('packages/review-gate/otoha_review.py', names)
                        self.assertIn('scripts/review-gate.py', names)
                        self.assertIn('skills/otoha-visualize/assets/view-state.js', names)
                        self.assertIn('skills/otoha-content-review/SKILL.md', names)
                        self.assertFalse(any('__pycache__' in name or '._bridge' in name or 'private-link' in name
                                             or 'untracked-private' in name for name in names))
            self.assertNotEqual(*fingerprints)


if __name__ == '__main__':
    unittest.main()
