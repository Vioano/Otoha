"""Normalize descriptive message fields; never choose an authorization scope."""
import pathlib
import re


def valid_ref(ref):
    return (isinstance(ref, dict) and isinstance(ref.get('repo'), str)
            and re.fullmatch(r'[\w.-]+/[\w.-]+', ref['repo'])
            and re.fullmatch(r'[0-9a-f]{40}|[0-9a-f]{64}', str(ref.get('commit', '')))
            and isinstance(ref.get('path'), str) and bool(ref['path'])
            and not ref['path'].startswith('/')
            and '..' not in pathlib.PurePosixPath(ref['path']).parts
            and '\x00' not in ref['path'])


def normalize(body, valid_target, valid_reply):
    """Keep useful text; preserve rejected descriptions as untrusted metadata."""
    payload = {key: body.get(key, default) for key, default in (
        ('text', ''), ('uid', ''), ('summary', ''), ('body', None),
        ('class', 'queued'), ('kind', 'message'), ('refs', []),
        ('reply_to', None), ('to_sessions', []), ('delivery', 'tool'))}
    warnings, declared = [], {}

    def correct(field, value):
        declared[field] = payload.get(field, body.get(field))
        payload[field] = value
        warnings.append(field + '_normalized')

    if payload['kind'] not in ('message', 'task', 'result', 'question', 'status'):
        correct('kind', 'message')
    if payload['class'] not in ('queued', 'interrupt'):
        correct('class', 'queued')
    if not isinstance(payload['uid'], str):
        correct('uid', '')
    if not isinstance(payload['summary'], str):
        correct('summary', '')
    if payload['body'] is not None and not isinstance(payload['body'], str):
        correct('body', None)
    if payload['delivery'] not in ('tool', 'chat', 'both'):
        correct('delivery', 'tool')
    for field, maximum, validator in (
            ('refs', 20, valid_ref), ('to_sessions', 50, valid_target)):
        values = payload[field]
        accepted = [v for v in values[:maximum] if validator(v)] if isinstance(values, list) else []
        if accepted != values:
            correct(field, accepted)
    if payload['reply_to'] is not None and not valid_reply(payload['reply_to']):
        correct('reply_to', None)
    for field, maximum in (('agent', 80), ('task', 500)):
        if field in body:
            value = body[field]
            if isinstance(value, str) and len(value) <= maximum:
                payload[field] = value
            else:
                correct(field, '')
    if 'source' in body:
        payload['source'] = body['source']
        if payload['source'] not in ('human', 'agent', 'unspecified'):
            correct('source', 'unspecified')
    if warnings:
        payload['warnings'] = warnings
        payload['declared_metadata'] = declared
    return payload
