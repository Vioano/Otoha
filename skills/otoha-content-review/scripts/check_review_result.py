#!/usr/bin/env python3
"""Check an existing content-review verdict and its declared input snapshot.

This reads review results; it does not perform visual judgment, authenticate a
reviewer, or prove that a model actually opened the declared inputs. Exit codes:
0 = submitted verdicts pass and required inputs/coverage match; 1 = reported
unresolved blockers; 2 = missing/unknown evidence or an input/coverage mismatch.

Supported report fields are overall_verdict, viewed_pages[{page,image_path,
page_verdict,findings}], process_review.verdict and optional required_unverified.
Manifest inputs contain path and sha256. By default all image inputs are page
inputs; optional review_pages explicitly selects page paths from inputs when
the same manifest also holds reference images. Paths are relative to the file
that declares them. not_verified is reported without guessing its applicability.
"""

from __future__ import annotations

import argparse
import hashlib
import json
from pathlib import Path
import re
import sys
import unicodedata


IMAGE_SUFFIXES = {'.png', '.jpg', '.jpeg', '.webp', '.gif', '.bmp', '.tif', '.tiff', '.svg'}
PASS = {'pass', 'passed', '通过'}
FAIL = {'fail', 'failed', 'blocked', '不通过', '需返修'}


def file_hash(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open('rb') as stream:
        for chunk in iter(lambda: stream.read(1024 * 1024), b''):
            digest.update(chunk)
    return digest.hexdigest()


def verdict(value: object) -> str:
    if not isinstance(value, str):
        return 'unknown'
    normalized = unicodedata.normalize('NFKC', value).strip().lower()
    if normalized in PASS:
        return 'pass'
    if normalized in FAIL or re.match(r'^(不通过|需返修)\s*[(:：]', normalized):
        return 'blocked'
    return 'unknown'


def declared_path(value: object, owner: Path) -> Path | None:
    if not isinstance(value, str) or not value.strip():
        return None
    path = Path(value)
    try:
        return (path if path.is_absolute() else owner.parent / path).resolve()
    except (OSError, ValueError):
        return None


def check(report_path: Path, manifest_path: Path, artifact_path: Path | None = None) -> dict:
    report_path, manifest_path = report_path.resolve(), manifest_path.resolve()
    result = {
        'status': 'unverified',
        'exit_code': 2,
        'report': str(report_path),
        'manifest': str(manifest_path),
        'blocked': [],
        'unverified': [],
        'inputs': [],
        'coverage': {},
        'limits': [
            'Checks submitted verdicts, declared page coverage and current input hashes only.',
            'Does not authenticate reviewer independence, actual image reading or visual judgment.',
            'An execution receipt with returncode=0 is not a quality verdict.',
        ],
    }

    def issue(kind: str, code: str, message: str, **details) -> None:
        result[kind].append({'code': code, 'message': message, **details})

    def inspect_findings(findings: object, scope: str, page: object = None) -> None:
        location = {'scope': scope}
        if page is not None:
            location['page'] = page
        if not isinstance(findings, list):
            issue('unverified', 'findings.format', 'findings must be a list.', **location)
            return
        for finding in findings:
            if not isinstance(finding, dict):
                issue('unverified', 'finding.format', 'Finding must be an object.', **location)
                continue
            severity = str(finding.get('severity', '')).strip().lower()
            if severity not in {'blocker', 'major', 'minor', 'info'}:
                issue('unverified', 'finding.severity', 'Finding needs a known severity.',
                      severity=severity, **location)
                continue
            status = finding.get('status')
            if status is not None and not isinstance(status, str):
                issue('unverified', 'finding.status', 'Finding status must be a string when supplied.', **location)
            resolved = (finding.get('resolved') is True or
                        (isinstance(status, str) and status in {'resolved', 'closed'}))
            if severity in {'blocker', 'major'} and not resolved:
                issue('blocked', 'page.finding' if page is not None else 'review.finding',
                      'Unresolved blocking finding.', element=finding.get('element'), severity=severity,
                      observation=finding.get('observation'), reason=finding.get('reason'), **location)

    try:
        report = json.loads(report_path.read_text(encoding='utf-8'))
        manifest = json.loads(manifest_path.read_text(encoding='utf-8'))
    except (OSError, UnicodeError, json.JSONDecodeError) as exc:
        issue('unverified', 'input.read', str(exc))
        return result
    if not isinstance(report, dict) or not isinstance(manifest, dict):
        issue('unverified', 'input.object', 'Report and manifest must be JSON objects.')
        return result
    result['report_sha256'] = file_hash(report_path)
    result['manifest_sha256'] = file_hash(manifest_path)
    claimed_manifest_hash = report.get('manifest_sha256')
    if claimed_manifest_hash is not None and claimed_manifest_hash != result['manifest_sha256']:
        issue('unverified', 'manifest.binding', 'Report manifest_sha256 does not match this manifest.')
    result['binding_method'] = ('declared manifest hash plus page paths and current input hashes'
                                if claimed_manifest_hash is not None
                                else 'declared page paths and current input hashes; no report-to-manifest attestation')

    entries = manifest.get('inputs')
    declared: dict[Path, dict] = {}
    if not isinstance(entries, list) or not entries:
        issue('unverified', 'manifest.inputs', 'Manifest needs a nonempty inputs list.')
        entries = []
    for index, entry in enumerate(entries):
        if not isinstance(entry, dict):
            issue('unverified', 'manifest.entry', 'Input entry must be an object.', index=index)
            continue
        path = declared_path(entry.get('path'), manifest_path)
        expected = entry.get('sha256')
        if path is None or not isinstance(expected, str) or not re.fullmatch(r'[0-9a-fA-F]{64}', expected):
            issue('unverified', 'manifest.entry', 'Input requires a path and SHA-256.', index=index)
            continue
        if path in declared:
            issue('unverified', 'manifest.duplicate', 'Input path is declared more than once.', path=str(path))
            continue
        record = {'path': str(path), 'expected_sha256': expected.lower(), 'hash_matches': False}
        declared[path] = record
        try:
            record['actual_sha256'] = file_hash(path)
            record['hash_matches'] = record['actual_sha256'] == expected.lower()
            if not record['hash_matches']:
                issue('unverified', 'input.changed', 'Current input differs from the reviewed snapshot.', path=str(path))
        except OSError as exc:
            issue('unverified', 'input.missing', str(exc), path=str(path))
        result['inputs'].append(record)

    if artifact_path is not None:
        artifact = artifact_path.resolve()
        result['artifact'] = str(artifact)
        if artifact not in declared:
            issue('unverified', 'artifact.unbound', 'Requested artifact is not bound by the input manifest.', path=str(artifact))

    if 'review_pages' in manifest:
        selected = manifest['review_pages']
        expected_pages: set[Path] = set()
        if not isinstance(selected, list) or not selected:
            issue('unverified', 'coverage.selection', 'review_pages must be a nonempty list of input paths.')
        else:
            for value in selected:
                path = declared_path(value, manifest_path)
                if path is None or path not in declared:
                    issue('unverified', 'coverage.selection', 'Selected review page is not a declared input.', path=str(path))
                elif path.suffix.lower() not in IMAGE_SUFFIXES:
                    issue('unverified', 'coverage.selection', 'Selected review page must be an image input.', path=str(path))
                elif path in expected_pages:
                    issue('unverified', 'coverage.selection', 'Selected review page is duplicated.', path=str(path))
                else:
                    expected_pages.add(path)
    else:
        expected_pages = {path for path in declared if path.suffix.lower() in IMAGE_SUFFIXES}
    if not expected_pages:
        issue('unverified', 'coverage.empty', 'No page-image inputs are selected for this review.')

    for name, value in [('overall', report.get('overall_verdict')),
                        ('process', report.get('process_review', {}).get('verdict')
                         if isinstance(report.get('process_review'), dict) else None)]:
        state = verdict(value)
        if state == 'blocked':
            issue('blocked', f'{name}.verdict', 'Review explicitly requires repair.', verdict=value)
        elif state == 'unknown':
            issue('unverified', f'{name}.unknown', 'Review needs an explicit pass or failure verdict.', verdict=value)
    if isinstance(report.get('whole_deck_review'), dict) and 'verdict' in report['whole_deck_review']:
        value = report['whole_deck_review']['verdict']
        state = verdict(value)
        if state == 'blocked':
            issue('blocked', 'whole_deck.verdict', 'Whole-deck review explicitly requires repair.', verdict=value)
        elif state == 'unknown':
            issue('unverified', 'whole_deck.unknown', 'Supplied whole-deck verdict is not explicit.', verdict=value)
    inspect_findings(report.get('findings', []), 'overall')
    for key in ['process_review', 'whole_deck_review']:
        if isinstance(report.get(key), dict):
            inspect_findings(report[key].get('findings', []), key)

    pages = report.get('viewed_pages')
    seen_paths: set[Path] = set()
    seen_numbers: set[int] = set()
    if not isinstance(pages, list) or not pages:
        issue('unverified', 'coverage.pages', 'Report needs nonempty viewed_pages.')
        pages = []
    for entry in pages:
        if not isinstance(entry, dict):
            issue('unverified', 'coverage.page', 'Page observation must be an object.')
            continue
        page_number = entry.get('page')
        path = declared_path(entry.get('image_path'), report_path)
        if not isinstance(page_number, int) or isinstance(page_number, bool) or page_number < 1 or page_number in seen_numbers:
            issue('unverified', 'coverage.number', 'Page requires a unique positive page number.', page=page_number)
        else:
            seen_numbers.add(page_number)
        if path is None:
            issue('unverified', 'coverage.path', 'Page needs image_path.', page=page_number)
        elif path not in expected_pages:
            issue('unverified', 'coverage.unbound', 'Reported page is outside selected manifest page inputs.', page=page_number, path=str(path))
        elif path in seen_paths:
            issue('unverified', 'coverage.duplicate', 'Page image is reported more than once.', page=page_number, path=str(path))
        else:
            seen_paths.add(path)
        state = verdict(entry.get('page_verdict'))
        if state == 'blocked':
            issue('blocked', 'page.verdict', 'Page explicitly requires repair.', page=page_number,
                  path=str(path), verdict=entry.get('page_verdict'))
        elif state == 'unknown':
            issue('unverified', 'page.unknown', 'Page needs an explicit pass or failure verdict.', page=page_number,
                  path=str(path), verdict=entry.get('page_verdict'))
        inspect_findings(entry.get('findings', []), 'page', page_number)

    missing = sorted(str(path) for path in expected_pages - seen_paths)
    if missing:
        issue('unverified', 'coverage.missing', 'Manifest page images were not covered by the report.', paths=missing)
    result['coverage'] = {'expected_pages': len(expected_pages), 'reported_pages': len(pages),
                          'matched_pages': len(seen_paths), 'missing_paths': missing}
    required_unverified = report.get('required_unverified', [])
    if not isinstance(required_unverified, list):
        issue('unverified', 'required.format', 'required_unverified must be a list.')
    elif required_unverified:
        issue('unverified', 'required.unverified', 'Required review observations remain unverified.', items=required_unverified)
    result['outside_or_unspecified_verification'] = report.get('not_verified', [])
    if result['blocked']:
        result['status'], result['exit_code'] = 'blocked', 1
    elif result['unverified']:
        result['status'], result['exit_code'] = 'unverified', 2
    else:
        result['status'], result['exit_code'] = 'pass', 0
    return result


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument('--report', required=True, type=Path)
    parser.add_argument('--manifest', required=True, type=Path)
    parser.add_argument('--artifact', type=Path, help='Also require this exact artifact in the input snapshot.')
    parser.add_argument('--json', action='store_true', help='Print structured results.')
    args = parser.parse_args()
    result = check(args.report, args.manifest, args.artifact)
    if args.json:
        print(json.dumps(result, ensure_ascii=False, indent=2))
    else:
        print(f"Review result: {result['status']}")
        print(f"Page coverage: {result['coverage']}")
        for kind in ['blocked', 'unverified']:
            for item in result[kind]:
                location = f" page {item['page']}" if 'page' in item else ''
                print(f"{kind}: [{item['code']}]{location} {item['message']}")
        print('This checks submitted results and input identity, not the truth of visual judgment.')
    return result['exit_code']


if __name__ == '__main__':
    sys.exit(main())
