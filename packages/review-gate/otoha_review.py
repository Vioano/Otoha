"""Deterministic observation/policy evaluation; no domain-specific collectors.

The frontend adapter calls the same evaluator without ``protocol`` and receives
its historical report shape. New packs use ``otoha.review/v1``. Null typed
values and choices marked unknown are unresolved observations, never waivers.
"""
import math

PROTOCOL = 'otoha.review/v1'
OUTCOMES = ('blocked', 'needs_arbitration', 'on_hold', 'unverified')
OPS = ('eq', 'ne', 'in', 'lt', 'lte', 'gt', 'gte')


def _number(value):
    return type(value) in (int, float) and math.isfinite(value)


def _keys(value, allowed, required, label):
    if not isinstance(value, dict) or not required <= value.keys() or value.keys() - allowed:
        raise ValueError(f'{label}: invalid or unsupported fields')


def _text(value):
    return isinstance(value, str) and bool(value.strip())


def _rows(rows, label):
    if not isinstance(rows, list):
        raise ValueError(f'{label}: expected array')
    result = {}
    for row in rows:
        if not isinstance(row, dict) or not _text(row.get('id')) or row['id'] in result:
            raise ValueError(f'{label}: invalid or duplicate id')
        result[row['id']] = row
    return result


def _type(question):
    return question.get('type', 'choice')


def condition_refs(condition):
    """Question references in stable order, for diagnostics and cycle checks."""
    if 'all' in condition or 'any' in condition:
        children = condition.get('all', condition.get('any'))
        return list(dict.fromkeys(ref for child in children for ref in condition_refs(child)))
    if 'not' in condition:
        return condition_refs(condition['not'])
    if 'question' in condition:
        return [condition['question']]
    return list(condition)


def validate_definition(data):
    """Validate the finite policy vocabulary and return questions indexed by id."""
    if not isinstance(data, dict):
        raise ValueError('definition must be an object')
    generic = 'protocol' in data
    if not generic and any(section in data for section in ('scoring', 'waivers')):
        raise ValueError('scoring/waivers require protocol otoha.review/v1')
    if generic:
        if data['protocol'] != PROTOCOL:
            raise ValueError('unsupported review protocol')
        _keys(data, {'protocol', 'id', 'title', 'description', 'version', 'areas', 'questions', 'rules', 'scoring', 'waivers'},
              {'protocol', 'areas', 'questions', 'rules'}, 'definition')
    areas = _rows(data.get('areas'), 'areas')
    questions = _rows(data.get('questions'), 'questions')
    rules = _rows(data.get('rules'), 'rules')
    if not areas or not questions:
        raise ValueError('areas/questions must be nonempty')
    for area in areas.values():
        if not _text(area.get('name')):
            raise ValueError('area name required')
        if generic:
            _keys(area, {'id', 'name', 'description'}, {'id', 'name'}, 'area')
    for question in questions.values():
        kind = _type(question)
        if question.get('area') not in areas or not _text(question.get('prompt')) or kind not in ('choice', 'boolean', 'number', 'text'):
            raise ValueError(f'invalid question: {question["id"]}')
        if generic:
            _keys(question, {'id', 'area', 'prompt', 'type', 'choices', 'required_when', 'description'},
                  {'id', 'area', 'prompt'}, 'question')
        if kind == 'choice':
            choices = _rows(question.get('choices'), 'choices')
            if not choices:
                raise ValueError('choices must be nonempty')
            for choice in choices.values():
                if not _text(choice.get('label')) or type(choice.get('unknown', False)) is not bool:
                    raise ValueError('invalid choice')
                if generic:
                    _keys(choice, {'id', 'label', 'unknown', 'description'}, {'id', 'label'}, 'choice')
        elif 'choices' in question:
            raise ValueError('typed questions cannot declare choices')

    def check(condition):
        if not isinstance(condition, dict) or not condition:
            raise ValueError('condition must be a nonempty object')
        if 'all' in condition or 'any' in condition:
            operator = 'all' if 'all' in condition else 'any'
            children = condition[operator]
            if set(condition) != {operator} or not isinstance(children, list) or not children:
                raise ValueError('all/any require nonempty condition arrays')
            for child in children:
                check(child)
        elif 'not' in condition:
            if set(condition) != {'not'}:
                raise ValueError('not requires one condition')
            check(condition['not'])
        elif 'question' in condition:
            _keys(condition, {'question', 'op', 'value'}, {'question', 'op', 'value'}, 'fact condition')
            ident, op, value = condition['question'], condition['op'], condition['value']
            if ident not in questions or op not in OPS:
                raise ValueError('unknown fact or operator')
            kind = _type(questions[ident])
            values = value if op == 'in' else [value]
            if not isinstance(values, list) or not values:
                raise ValueError('in requires a nonempty array')
            if op in ('lt', 'lte', 'gt', 'gte') and kind != 'number':
                raise ValueError('ordered comparison requires number question')
            for item in values:
                valid = (_number(item) if kind == 'number' else type(item) is bool if kind == 'boolean'
                         else isinstance(item, str))
                if kind == 'choice':
                    valid = isinstance(item, str) and item in {row['id'] for row in questions[ident]['choices']}
                if not valid:
                    raise ValueError('condition value does not match question type')
        else:
            for ident, choices in condition.items():
                if ident not in questions or _type(questions[ident]) != 'choice' or not isinstance(choices, list) or not choices:
                    raise ValueError('invalid legacy condition reference')
                if any(choice not in {row['id'] for row in questions[ident]['choices']} for choice in choices):
                    raise ValueError('invalid condition choice')
    for question in questions.values():
        if 'required_when' in question:
            check(question['required_when'])
    visiting, visited = set(), set()
    def visit(ident):
        if ident in visiting:
            raise ValueError(f'required_when dependency cycle: {ident}')
        if ident in visited:
            return
        visiting.add(ident)
        for ref in condition_refs(questions[ident].get('required_when', {})):
            visit(ref)
        visiting.remove(ident)
        visited.add(ident)
    for ident in questions:
        visit(ident)
    for rule in rules.values():
        if rule.get('area') not in areas or rule.get('outcome') not in OUTCOMES or not _text(rule.get('reason')):
            raise ValueError('invalid rule area/outcome/reason')
        if generic:
            _keys(rule, {'id', 'area', 'when', 'outcome', 'reason', 'waivable', 'description'},
                  {'id', 'area', 'when', 'outcome', 'reason'}, 'rule')
        if type(rule.get('waivable', False)) is not bool:
            raise ValueError('waivable must be boolean')
        check(rule.get('when'))
    if 'scoring' in data:
        scoring = data['scoring']
        _keys(scoring, {'minimum', 'items'}, {'minimum', 'items'}, 'scoring')
        if not _number(scoring['minimum']) or scoring['minimum'] < 0:
            raise ValueError('minimum must be a finite nonnegative number')
        for item in _rows(scoring['items'], 'score items').values():
            _keys(item, {'id', 'when', 'points', 'description'}, {'id', 'when', 'points'}, 'score item')
            if not _number(item['points']) or item['points'] < 0:
                raise ValueError('points must be a finite nonnegative number')
            check(item['when'])
    for waiver in _rows(data.get('waivers', []), 'waivers').values():
        _keys(waiver, {'id', 'rule_id', 'when', 'reason', 'description'}, {'id', 'rule_id', 'when', 'reason'}, 'waiver')
        rule = rules.get(waiver['rule_id'])
        if not rule or not rule.get('waivable') or not _text(waiver['reason']):
            raise ValueError('waiver requires explicit waivable rule and reason')
        check(waiver['when'])
    return questions


def validate_observations(questions, observations, *, strict=False):
    if not isinstance(observations, dict) or observations.keys() - questions.keys():
        raise ValueError('observations contain unknown question ids')
    for ident, observation in observations.items():
        kind = _type(questions[ident])
        field = 'choice' if kind == 'choice' else 'value'
        allowed = {field, 'evidence', 'unknown'} if strict else {field, 'evidence', 'at', 'unknown'}
        _keys(observation, allowed, {field, 'evidence'}, 'observation')
        if not isinstance(observation['evidence'], str):
            raise ValueError('evidence must be text')
        value = observation[field]
        if 'unknown' in observation and (kind == 'choice' or observation['unknown'] is not True or value is not None):
            raise ValueError('unknown=true is only valid with a null typed value')
        if kind == 'choice':
            valid = value in [item['id'] for item in questions[ident]['choices']]
        elif value is None:
            valid = True
        elif kind == 'boolean':
            valid = type(value) is bool
        elif kind == 'number':
            valid = _number(value)
        else:
            valid = isinstance(value, str)
        if not valid:
            raise ValueError(f'observation value does not match question: {ident}')


def evaluate(data, questions, observations):
    """Calculate a report from facts; observers never provide result fields."""
    generic = 'protocol' in data
    if not generic and any(section in data for section in ('scoring', 'waivers')):
        raise ValueError('scoring/waivers require protocol otoha.review/v1')
    if generic:
        validated = validate_definition(data)
        if questions != validated:
            raise ValueError('question index differs from validated definition')
    validate_observations(questions, observations)
    active, visiting = {}, set()
    inactive = object()
    def observed(ident):
        observation = observations.get(ident)
        if observation is None or not observation['evidence'].strip():
            return False
        if _type(questions[ident]) == 'choice':
            return not next(item for item in questions[ident]['choices'] if item['id'] == observation['choice']).get('unknown')
        return observation['value'] is not None
    def fact(ident, predicate):
        applicable = required(ident)
        if applicable is False:
            return inactive if generic else False
        if applicable is None or not observed(ident):
            return None
        value = observations[ident]['choice' if _type(questions[ident]) == 'choice' else 'value']
        return predicate(value)
    def combine(values, operator):
        if operator == 'all' and False in values: return False
        if operator == 'any' and True in values: return True
        if None in values: return None
        if inactive in values: return inactive
        return operator == 'all'
    def condition(when):
        value = raw_condition(when)
        return False if value is inactive else value
    def raw_condition(when):
        if 'all' in when or 'any' in when:
            operator = 'all' if 'all' in when else 'any'
            values = [raw_condition(child) for child in when[operator]]
            return combine(values, operator)
        if 'not' in when:
            value = raw_condition(when['not'])
            return value if value is None or value is inactive else not value
        if 'question' in when:
            value, op = when['value'], when['op']
            def compare(actual):
                if op == 'eq': return actual == value
                if op == 'ne': return actual != value
                if op == 'in': return actual in value
                if op == 'lt': return actual < value
                if op == 'lte': return actual <= value
                if op == 'gt': return actual > value
                if op == 'gte': return actual >= value
                raise ValueError('unknown operator')
            return fact(when['question'], compare)
        values = [fact(ident, lambda actual, choices=choices: actual in choices) for ident, choices in when.items()]
        return combine(values, 'all')
    def required(ident):
        if ident not in active:
            if ident in visiting:
                raise ValueError(f'required_when 存在循环：{ident}')
            visiting.add(ident)
            active[ident] = condition(questions[ident]['required_when']) if 'required_when' in questions[ident] else True
            visiting.remove(ident)
        return active[ident]
    facts, issues, next_question = [], [], None
    for ident, question in questions.items():
        applicable = required(ident)
        observation = observations.get(ident)
        value_field = 'choice' if _type(question) == 'choice' else 'value'
        row = {'question_id': ident, 'area': question['area'], 'surface': question.get('surface'), 'required': applicable,
               value_field: observation[value_field] if observation else None,
               'evidence': observation['evidence'] if observation else None}
        reason = None
        if applicable is False:
            row['status'] = 'inactive'
        elif applicable is None:
            row['status'] = 'unverified'
            reason = '是否需要此观察尚未确定：先完成 required_when 所引用的观察。'
        elif observation is None:
            row['status'] = 'unverified'
            if next_question is None: next_question = ident
            reason = '尚未记录此项实际观察。'
        else:
            row['status'] = 'observed' if observed(ident) else 'unverified'
            if row['status'] == 'unverified':
                reason = '记录为未查看/无法判断，或没有实际观察证据。'
        if reason:
            issues.append({'area': question['area'], 'surface': question.get('surface'), 'question_id': ident,
                           'status': 'unverified', 'reason': reason})
        facts.append(row)
    rule_results, applied = [], []
    for rule in data['rules']:
        match = condition(rule['when'])
        result = {'rule_id': rule['id'], 'area': rule['area'], 'surface': rule.get('surface'), 'matched': match,
                  'status': rule['outcome'] if match else ('unverified' if match is None else 'clear'), 'reason': rule['reason']}
        rule_results.append(result)
        if match is True or match is None:
            issue = {'area': rule['area'], 'surface': rule.get('surface'), 'rule_id': rule['id'],
                     'question_ids': condition_refs(rule['when']), 'status': result['status'],
                     'reason': rule['reason'] if match else '条件事实尚未确定；待核验规则：' + rule['reason']}
            if generic:
                issue['resolved'] = False
                if match is True and rule.get('waivable'):
                    for waiver in data.get('waivers', []):
                        if waiver['rule_id'] == rule['id'] and condition(waiver['when']) is True:
                            issue.update(resolved=True, waiver_id=waiver['id'])
                            applied.append({'waiver_id': waiver['id'], 'rule_id': rule['id'], 'reason': waiver['reason']})
                            break
            issues.append(issue)
    score = None
    if generic and 'scoring' in data:
        scoring, item_results, earned = data['scoring'], [], 0
        for item in scoring['items']:
            match = condition(item['when'])
            points = item['points'] if match is True else 0
            earned += points
            item_results.append({'id': item['id'], 'matched': match, 'earned': points, 'points': item['points']})
        score = {'earned': earned, 'minimum': scoring['minimum'], 'maximum': sum(item['points'] for item in scoring['items']),
                 'items': item_results, 'satisfied': earned >= scoring['minimum']}
        if not score['satisfied']:
            pending = sum(item['points'] for item in item_results if item['matched'] is None)
            score_status = 'unverified' if earned + pending >= scoring['minimum'] else 'blocked'
            issues.append({'area': data['areas'][0]['id'], 'rule_id': 'scoring.minimum', 'status': score_status,
                           'reason': 'Score is below the policy minimum.', 'resolved': False})
    def aggregate(selected):
        statuses = {issue['status'] for issue in selected if not issue.get('resolved', False)}
        return next((status for status in OUTCOMES if status in statuses), 'pass')
    area_results = [{'area': area['id'], 'name': area['name'],
                     'status': aggregate(issue for issue in issues if issue['area'] == area['id'])} for area in data['areas']]
    per_surface = [dict(surface, status=aggregate(issue for issue in issues if issue.get('surface') == surface['id']))
                   for surface in data.get('surfaces', [])]
    report = {'status': aggregate(issues), 'areas': area_results, 'issues': issues, 'facts': facts,
              'rules': rule_results, 'next_question': next_question,
              'next_surface': questions[next_question].get('surface') if next_question else None, 'per_surface': per_surface}
    if generic:
        report.update(protocol=PROTOCOL, released=report['status'] == 'pass', score=score, waivers=applied,
                      findings=[dict(issue, resolved=issue.get('resolved', False)) for issue in issues])
    return report
