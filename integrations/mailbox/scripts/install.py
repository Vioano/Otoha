#!/usr/bin/env python3
"""Copy the self-contained skill into a team's Git checkout; no login or messaging."""
import argparse
from datetime import datetime, timezone
import json
from pathlib import Path
import shutil
import subprocess
import tempfile
import uuid

SOURCE = Path(__file__).resolve().parents[1] / 'skills/otoha-mailbox'
IGNORE = shutil.ignore_patterns('__pycache__', '*.pyc', '*.pyo', '.DS_Store', '._*')


def git(project, *args):
    return subprocess.run(['git', '-C', str(project), *args], check=True,
                          capture_output=True, text=True, encoding='utf-8').stdout.strip()


def contents(root):
    result = {}
    for path in root.rglob('*'):
        if path.is_symlink():
            raise ValueError('Skill contains a symlink; inspect and copy it manually.')
        rel = path.relative_to(root)
        if '__pycache__' in rel.parts or path.name.startswith('._') or path.name == '.DS_Store':
            continue
        if path.is_file() and path.suffix not in ('.pyc', '.pyo'):
            result[str(rel)] = path.read_bytes()
    return result


def install(project, agent='generic', update=False):
    root = Path(git(project, 'rev-parse', '--show-toplevel')).resolve()
    common = Path(git(root, 'rev-parse', '--path-format=absolute', '--git-common-dir'))
    base = '.claude' if agent == 'claude' else '.agents'
    target = root / base / 'skills/otoha-mailbox'
    if any(p.is_symlink() for p in (root / base, target.parent, target)):
        raise ValueError('Destination is linked; inspect and copy it manually.')
    source_contents = contents(SOURCE)
    if target.exists():
        if not target.is_dir():
            raise ValueError('Destination exists and is not a directory.')
        if contents(target) == source_contents:
            return {'status': 'unchanged', 'skill_dir': str(target)}
        if not update:
            raise ValueError('Existing skill differs. Review it, then use --update to back up and replace it.')
    target.parent.mkdir(parents=True, exist_ok=True)
    backup = None
    with tempfile.TemporaryDirectory(prefix='.otoha-mailbox-', dir=target.parent) as temp:
        staged = Path(temp) / 'otoha-mailbox'
        shutil.copytree(SOURCE, staged, ignore=IGNORE)
        if target.exists():
            stamp = datetime.now(timezone.utc).strftime('%Y%m%dT%H%M%SZ')
            backup = common / 'otoha-mailbox/skill-backups' / (stamp + '-' + uuid.uuid4().hex[:8])
            shutil.copytree(target, backup)
            shutil.rmtree(target)
        try:
            staged.rename(target)
        except OSError:
            if backup and not target.exists():
                shutil.copytree(backup, target)
            raise
    return {'status': 'updated' if backup else 'installed', 'skill_dir': str(target),
            'backup': str(backup) if backup else None}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--project', required=True, help='Existing team Git checkout.')
    parser.add_argument('--agent', choices=('generic', 'codex', 'claude'), default='generic')
    parser.add_argument('--update', action='store_true', help='Back up and replace an existing installation.')
    args = parser.parse_args()
    try:
        print(json.dumps(install(args.project, args.agent, args.update), ensure_ascii=False))
    except (OSError, ValueError, subprocess.CalledProcessError) as exc:
        parser.exit(2, 'otoha-mailbox install: ' + str(exc) + '\n')


if __name__ == '__main__':
    main()
