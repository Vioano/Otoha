"""Reject old product names in active source; retain legal/source provenance."""
from pathlib import Path
import re

root = Path(__file__).resolve().parents[1]
ignored = {'.git', 'node_modules', 'dist', '__pycache__', '.archify-design', '.otoha'}
pattern = re.compile(r'atlas(?!sian)', re.I)
failures = []
for path in root.rglob('*'):
    relative = path.relative_to(root)
    # Audit active product sources. User-supplied root-level reference exports
    # are historical evidence, not product wording to rewrite or publish.
    if len(relative.parts) == 1 and path.name not in {'README.md', 'AGENTS.md', 'package.json', '.gitignore'}:
        continue
    if any(part in ignored for part in relative.parts) or path.is_symlink() or not path.is_file():
        continue
    if relative.parts[:2] == ('docs', 'provenance') or path.name in {'LICENSE', 'NOTICE.md', 'check-names.py'}:
        continue
    if pattern.search(str(relative)):
        failures.append(str(relative) + ': filename')
    try:
        content = path.read_text()
    except (UnicodeDecodeError, OSError):
        continue
    for number, line in enumerate(content.splitlines(), 1):
        if pattern.search(line):
            failures.append(f'{relative}:{number}')
if failures:
    print('\n'.join(failures))
    raise SystemExit(1)
print('Otoha naming check passed; provenance, licenses and unrelated third-party brands are preserved.')
