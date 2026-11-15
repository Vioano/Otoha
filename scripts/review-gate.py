#!/usr/bin/env python3
"""Run a version-bound observation questionnaire; the engine computes the result."""
import argparse
from datetime import datetime, timezone
import hashlib
import json
import os
from pathlib import Path
import sys

ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(ROOT / 'packages/review-gate'))
import otoha_review


def now():
    return datetime.now(timezone.utc).isoformat(timespec='seconds')


def digest(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def unique_object(pairs):
    result = {}
    for key, value in pairs:
        if key in result:
            raise ValueError('重复 JSON 字段：' + key)
        result[key] = value
    return result


def read_json(path):
    return json.loads(path.read_text(encoding='utf-8'), object_pairs_hook=unique_object,
                      parse_constant=lambda value: (_ for _ in ()).throw(ValueError('非法 JSON 数值：' + value)))


def save(path, state):
    path.parent.mkdir(parents=True, exist_ok=True)
    temporary = path.with_name(path.name + '.tmp')
    temporary.write_text(json.dumps(state, ensure_ascii=False, indent=2, allow_nan=False) + '\n', encoding='utf-8')
    temporary.replace(path)


def definition(path):
    data = read_json(path)
    if data.get('protocol') != 'otoha.review/v1':
        raise ValueError('通用题包需要 protocol: otoha.review/v1；前端旧题包由原适配器使用')
    questions = otoha_review.validate_definition(data)
    return data, questions


def load_run(path):
    state = read_json(path)
    if state.get('schema') != 'otoha.review-run/v1':
        raise ValueError('不支持的审查记录格式')
    rules = Path(state['rules_path'])
    if digest(rules) != state['rules_sha256']:
        raise ValueError('题包已改变；旧记录不可按新题包放行，请新建一轮')
    if digest(Path(otoha_review.__file__)) != state['engine_sha256']:
        raise ValueError('判定引擎已改变；请新建一轮以绑定当前引擎')
    data, questions = definition(rules)
    stale = [row['path'] for row in state['artifacts']
             if not Path(row['path']).is_file() or digest(Path(row['path'])) != row['sha256']]
    report = otoha_review.evaluate(data, questions, state['observations'])
    if stale:
        finding = {'rule_id': 'run.artifact-changed', 'status': 'unverified',
                   'reason': '声明的产物已改变或缺失：' + ', '.join(stale)}
        report['issues'].append(finding)
        report['findings'].append(dict(finding, resolved=False))
        if report['status'] == 'pass':
            report['status'] = 'unverified'
        report['released'] = False
    report['binding'] = {key: state[key] for key in ('rules_path', 'rules_sha256', 'engine_sha256', 'artifacts')}
    return state, data, questions, report


def display(report, questions, as_json):
    if as_json:
        print(json.dumps(report, ensure_ascii=False, indent=2, allow_nan=False))
        return
    print('程序计算：' + report['status'])
    score = report.get('score')
    if score:
        print('评分：' + json.dumps(score, ensure_ascii=False))
    for issue in report['issues']:
        state = 'waived:' + issue['waiver_id'] if issue.get('resolved') else issue['status']
        print(f"[{state}] {issue.get('rule_id', issue.get('question_id', ''))}: {issue['reason']}")
    ident = report.get('next_question')
    if ident:
        question = questions[ident]
        print(f"下一项 {ident}：{question['prompt']}")
        for choice in question.get('choices', []):
            print(f"  {choice['id']}: {choice['label']}")
    print('范围：仅计算题包和已提交事实；观察真实性与最终用户验收分别核对。')


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    commands = parser.add_subparsers(dest='command', required=True)
    validate = commands.add_parser('validate')
    validate.add_argument('--rules', required=True, type=Path)
    validate.add_argument('--json', action='store_true')
    for command in ('start', 'next', 'status', 'observe', 'finish'):
        sub = commands.add_parser(command)
        sub.add_argument('--state', required=True, type=Path)
        sub.add_argument('--json', action='store_true')
        if command == 'start':
            sub.add_argument('--rules', required=True, type=Path)
            sub.add_argument('--artifact', action='append', type=Path, default=[])
        if command == 'observe':
            sub.add_argument('question_id')
            value = sub.add_mutually_exclusive_group(required=True)
            value.add_argument('--choice')
            value.add_argument('--value', help='JSON 标量，例如 3.2、true 或 "原文"')
            value.add_argument('--unknown', action='store_true')
            sub.add_argument('--evidence', required=True)
    args = parser.parse_args()
    try:
        if args.command == 'validate':
            data, questions = definition(args.rules.expanduser().resolve())
            result = {'status': 'valid', 'id': data.get('id'), 'questions': len(questions),
                      'rules': len(data['rules']), 'sha256': digest(args.rules.expanduser())}
            print(json.dumps(result, ensure_ascii=False, indent=2))
            return 0
        path = args.state.expanduser().resolve()
        if args.command == 'start':
            if path.exists():
                raise ValueError('记录已存在；继续观察或另建一轮，不覆盖旧记录')
            rules = args.rules.expanduser().resolve()
            data, questions = definition(rules)
            artifacts = []
            for target in args.artifact:
                target = target.expanduser().resolve()
                artifacts.append({'path': str(target), 'sha256': digest(target)})
            state = {'schema': 'otoha.review-run/v1', 'rules_path': str(rules),
                     'rules_sha256': digest(rules), 'engine_sha256': digest(Path(otoha_review.__file__)),
                     'artifacts': artifacts, 'actor_id': os.environ.get('CODEX_THREAD_ID'),
                     'started_at': now(), 'finished_at': None, 'observations': {}, 'history': []}
            save(path, state)
        state, data, questions, report = load_run(path)
        if args.command in ('observe', 'finish') and state.get('actor_id'):
            if os.environ.get('CODEX_THREAD_ID') != state['actor_id']:
                raise ValueError('只能由创建记录的宿主会话写入；status/next 可只读重算')
        if args.command == 'observe':
            ident = args.question_id
            if ident not in questions:
                raise ValueError('题包中不存在此问题：' + ident)
            evidence = args.evidence.strip()
            if not evidence or len(evidence) > 2000:
                raise ValueError('evidence 需要 1 至 2000 字符的实际观察或无法确认的原因')
            question = questions[ident]
            if args.choice is not None:
                fact = {'choice': args.choice, 'evidence': evidence}
            elif args.unknown:
                if question.get('type', 'choice') == 'choice':
                    raise ValueError('选择题请使用题包标记 unknown 的选项')
                fact = {'value': None, 'unknown': True, 'evidence': evidence}
            else:
                fact = {'value': json.loads(args.value, parse_constant=lambda value: (_ for _ in ()).throw(ValueError('非法数值'))),
                        'evidence': evidence}
            observations = dict(state['observations'], **{ident: fact})
            proposed = otoha_review.evaluate(data, questions, observations)
            row = next(row for row in proposed['facts'] if row['question_id'] == ident)
            if row['required'] is not True:
                raise ValueError('此题当前不适用或适用条件尚未确定，请先记录分支所需事实')
            state['history'].append({'at': now(), 'question_id': ident,
                                     'previous': state['observations'].get(ident)})
            state['observations'] = observations
            state['finished_at'] = None
            save(path, state)
            state, data, questions, report = load_run(path)
        if args.command == 'finish':
            if report['released']:
                state['finished_at'] = state['finished_at'] or now()
                save(path, state)
            display(report, questions, args.json)
            return 0 if report['released'] else 1
        display(report, questions, args.json)
        return 0
    except (OSError, ValueError, KeyError, TypeError) as error:
        result = {'status': 'error', 'released': False, 'reason': str(error)}
        print(json.dumps(result, ensure_ascii=False), file=sys.stderr if not args.json else sys.stdout)
        return 2


if __name__ == '__main__':
    raise SystemExit(main())
