#!/usr/bin/env python3
"""Vendor Otoha's shared evaluator into an existing frontend-acceptance adapter.

Default is a dry run. Existing questions, state, peer/target checks and hooks
are untouched. Run Otoha's review-gate tests before applying an engine update.
"""
import argparse
import ast
import hashlib
import json
from pathlib import Path
import sys

ROOT = Path(__file__).resolve().parents[1]
ENGINE = ROOT / 'packages/review-gate/otoha_review.py'


def prepare(source):
    tree = ast.parse(source)
    functions = [node for node in tree.body if isinstance(node, ast.FunctionDef) and node.name == 'evaluate']
    imports = [node for node in tree.body if isinstance(node, ast.ImportFrom)
               and node.module == 'otoha_review' and any(item.name == 'evaluate' for item in node.names)]
    if any(item.asname not in (None, 'evaluate') for node in imports
           for item in node.names if item.name == 'evaluate'):
        raise ValueError('evaluate 导入已改名；先核对适配器')
    if not functions and len(imports) == 1:
        return source
    if len(functions) != 1 or imports:
        raise ValueError('需要唯一的原 evaluate 函数或现有共享导入；未知适配器不改写')
    function = functions[0]
    if ([arg.arg for arg in function.args.args] != ['data', 'questions', 'observations']
            or function.decorator_list or function.args.defaults or function.args.vararg or function.args.kwarg):
        raise ValueError('evaluate 接口已改变；先核对新适配器')
    if function.args.kwonlyargs or function.args.posonlyargs:
        raise ValueError('evaluate 接口已改变；先核对新适配器')
    lines = source.splitlines(keepends=True)
    lines[function.lineno - 1:function.end_lineno] = ['from otoha_review import evaluate\n']
    result = ''.join(lines)
    compile(result, 'acceptance.py', 'exec')
    return result


def atomic_write(path, content):
    temporary = path.with_name(path.name + '.tmp')
    temporary.write_bytes(content)
    temporary.replace(path)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--frontend-skill', required=True, type=Path)
    parser.add_argument('--apply', action='store_true')
    args = parser.parse_args()
    try:
        skill = args.frontend_skill.expanduser().resolve()
        adapter = skill / 'scripts/acceptance.py'
        target = skill / 'scripts/otoha_review.py'
        original = adapter.read_text(encoding='utf-8')
        updated = prepare(original).encode()
        engine = ENGINE.read_bytes()
        compile(engine, str(ENGINE), 'exec')
        if args.apply:
            # Core first: the old adapter still works if the second write fails.
            atomic_write(target, engine)
            atomic_write(adapter, updated)
        print(json.dumps({'applied': args.apply, 'adapter': str(adapter),
                          'adapter_changed': original.encode() != updated,
                          'engine_sha256': hashlib.sha256(engine).hexdigest()}, ensure_ascii=False))
        return 0
    except (OSError, ValueError, SyntaxError) as error:
        print(str(error), file=sys.stderr)
        return 2


if __name__ == '__main__':
    raise SystemExit(main())
