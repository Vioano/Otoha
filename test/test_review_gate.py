"""Policy-engine behavior and legacy evaluator compatibility."""
import copy
import hashlib
import importlib.util
import json
from pathlib import Path
import unittest

ROOT = Path(__file__).resolve().parents[1]
SPEC = importlib.util.spec_from_file_location('otoha_review', ROOT / 'packages/review-gate/otoha_review.py')
engine = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(engine)


def pack():
    return {'protocol': engine.PROTOCOL, 'areas': [{'id': 'delivery', 'name': 'Delivery'}],
            'questions': [
                {'id': 'media', 'area': 'delivery', 'prompt': 'Embedded video count?', 'type': 'number'},
                {'id': 'played', 'area': 'delivery', 'prompt': 'Was the embedded video played?', 'type': 'boolean',
                 'required_when': {'question': 'media', 'op': 'gt', 'value': 0}},
                {'id': 'copies', 'area': 'delivery', 'prompt': 'Number of text copies?', 'type': 'number'}],
            'rules': [{'id': 'playback', 'area': 'delivery', 'when': {'all': [
                {'question': 'media', 'op': 'gt', 'value': 0},
                {'question': 'played', 'op': 'eq', 'value': False}]},
                       'outcome': 'blocked', 'reason': 'Video has not been played.'}]}


def obs(**values):
    return {key: {'value': value, 'evidence': 'Measured in current artifact.'} for key, value in values.items()}


def report(data, observations):
    return engine.evaluate(data, engine.validate_definition(data), observations)


class GateTests(unittest.TestCase):
    def test_inactive_playback_is_not_missing(self):
        result = report(pack(), obs(media=0, copies=1))
        self.assertTrue(result['released'])
        self.assertEqual(result['facts'][1]['status'], 'inactive')
        self.assertEqual(result['waivers'], [])

    def test_negating_inactive_does_not_activate_rule_or_child(self):
        data = pack()
        negated = {'not': {'question': 'played', 'op': 'eq', 'value': True}}
        data['rules'][0]['when'] = negated
        data['questions'].append({'id': 'child', 'area': 'delivery', 'type': 'text',
                                  'prompt': 'Visible followup name?', 'required_when': negated})
        result = report(data, obs(media=0, copies=1))
        self.assertTrue(result['released'])
        self.assertFalse(result['rules'][0]['matched'])
        self.assertEqual(result['facts'][-1]['status'], 'inactive')
        data['rules'][0]['when'] = {'all': [{'question': 'media', 'op': 'gt', 'value': 0},
                                           {'question': 'played', 'op': 'eq', 'value': False}]}
        self.assertTrue(report(data, obs(media=0, copies=1))['released'])

    def test_missing_unknown_evidence_and_not_remain_unverified(self):
        data = pack()
        data['rules'][0]['when'] = {'not': {'question': 'played', 'op': 'eq', 'value': True}}
        for observations in (obs(media=1, copies=1), obs(media=1, played=None, copies=1)):
            result = report(data, observations)
            self.assertEqual(result['status'], 'unverified')
            self.assertIsNone(result['rules'][0]['matched'])
            self.assertFalse(result['released'])
        observations = obs(media=1, played=True, copies=1)
        observations['played']['evidence'] = ''
        self.assertFalse(report(data, observations)['released'])
        observations['played'].update(value=None, unknown=True, evidence='Player did not load.')
        self.assertFalse(report(data, observations)['released'])

    def test_all_any_short_circuit_under_unknown(self):
        data = pack()
        data['rules'][0]['when'] = {'any': [{'question': 'copies', 'op': 'gt', 'value': 1},
                                           {'question': 'played', 'op': 'eq', 'value': False}]}
        self.assertTrue(report(data, obs(media=1, copies=2))['rules'][0]['matched'])
        self.assertIsNone(report(data, obs(media=1, copies=1))['rules'][0]['matched'])

    def test_score_cannot_offset_blocker_or_unknown(self):
        data = pack()
        data['scoring'] = {'minimum': 5, 'items': [{'id': 'copy', 'when': {'question': 'copies', 'op': 'eq', 'value': 1}, 'points': 10}]}
        result = report(data, obs(media=1, played=False, copies=1))
        self.assertEqual(result['score']['earned'], 10)
        self.assertEqual(result['status'], 'blocked')
        self.assertFalse(result['released'])
        self.assertEqual(report(data, obs(media=1, copies=1))['status'], 'unverified')

    def test_unknown_score_does_not_claim_known_failure(self):
        data = pack()
        data['scoring'] = {'minimum': 5, 'items': [{'id': 'copy', 'when': {'question': 'copies', 'op': 'eq', 'value': 1}, 'points': 10}]}
        result = report(data, obs(media=0))
        self.assertEqual(result['status'], 'unverified')
        result = report(data, obs(media=0, copies=2))
        self.assertEqual(result['status'], 'blocked')

    def test_summary_priority_retains_every_finding(self):
        data = pack()
        for outcome in ('needs_arbitration', 'on_hold', 'unverified'):
            data['rules'].append({'id': outcome, 'area': 'delivery', 'when': {'question': 'copies', 'op': 'eq', 'value': 2},
                                  'outcome': outcome, 'reason': outcome})
        result = report(data, obs(media=1, played=False, copies=2))
        self.assertEqual(result['status'], 'blocked')
        self.assertEqual({item['status'] for item in result['findings']}, set(engine.OUTCOMES))
        data['rules'] = data['rules'][1:]
        self.assertEqual(report(data, obs(media=0, copies=2))['status'], 'needs_arbitration')
        data['rules'] = data['rules'][1:]
        self.assertEqual(report(data, obs(media=0, copies=2))['status'], 'on_hold')

    def test_policy_waiver_retains_original_finding(self):
        data = pack()
        data['rules'][0]['waivable'] = True
        data['waivers'] = [{'id': 'static', 'rule_id': 'playback',
                           'when': {'question': 'copies', 'op': 'eq', 'value': 0}, 'reason': 'Preapproved static-only scope.'}]
        result = report(data, obs(media=1, played=False, copies=0))
        self.assertTrue(result['released'])
        self.assertEqual(result['findings'][0]['status'], 'blocked')
        self.assertTrue(result['findings'][0]['resolved'])
        self.assertEqual(result['waivers'][0]['waiver_id'], 'static')
        result = report(data, obs(media=1, played=False))
        self.assertFalse(result['released'])
        self.assertEqual(result['waivers'], [])
        data['rules'][0]['waivable'] = False
        with self.assertRaises(ValueError): engine.validate_definition(data)

    def test_validation_rejects_unsupported_and_wrong_types(self):
        for value in (True, float('inf'), float('nan'), '2'):
            with self.assertRaises(ValueError): report(pack(), obs(media=value, copies=1))
        data = pack()
        for mutation in (
                lambda d: d.update(passing_score=1),
                lambda d: d['questions'][0].update(required_when={'question': 'played', 'op': 'eq', 'value': True}),
                lambda d: d['rules'][0].update(when={'question': 'missing', 'op': 'eq', 'value': 1}),
                lambda d: d['rules'][0].update(when={'question': 'media', 'op': 'gt', 'value': True}),
                lambda d: d['rules'][0].update(when={'all': []}),
                lambda d: d['rules'][0].update(outcome='pass'),
                lambda d: d['questions'].append(copy.deepcopy(d['questions'][0]))):
            candidate = copy.deepcopy(data)
            mutation(candidate)
            with self.assertRaises(ValueError): engine.validate_definition(candidate)
        for extra in ({'status': 'pass'}, {'unknown': True}):
            observations = obs(media=0, copies=1)
            observations['copies'].update(extra)
            with self.assertRaises(ValueError): report(pack(), observations)

    def test_choice_and_typed_text(self):
        data = {'protocol': engine.PROTOCOL, 'areas': [{'id': 'a', 'name': 'A'}],
                'questions': [{'id': 'x', 'area': 'a', 'prompt': 'Current state?', 'choices': [
                    {'id': 'ready', 'label': 'Ready'}, {'id': 'unknown', 'label': 'Not observed', 'unknown': True}]},
                    {'id': 'name', 'area': 'a', 'type': 'text', 'prompt': 'Visible accessible name?'}],
                'rules': [{'id': 'r', 'area': 'a', 'when': {'all': [{'x': ['ready']},
                           {'question': 'name', 'op': 'in', 'value': ['Save', 'Submit']}]},
                           'outcome': 'on_hold', 'reason': 'Fixture condition.'}]}
        result = report(data, {'x': {'choice': 'ready', 'evidence': 'Visible'}, 'name': {'value': 'Save', 'evidence': 'AX name'}})
        self.assertEqual(result['status'], 'on_hold')
        result = report(data, {'x': {'choice': 'unknown', 'evidence': 'No access'}, 'name': {'value': 'Save', 'evidence': 'AX name'}})
        self.assertEqual(result['status'], 'unverified')


class LegacyParityTests(unittest.TestCase):
    def test_legacy_new_policy_sections_cannot_be_silently_ignored(self):
        data = json.loads((ROOT / 'test/fixtures/review-gate/frontend-v2.json').read_text())
        questions = engine.validate_definition(data)
        for section, value in (('scoring', {'minimum': 1, 'items': []}), ('waivers', [])):
            candidate = dict(data, **{section: value})
            with self.subTest(section=section):
                with self.assertRaisesRegex(ValueError, 'require protocol'):
                    engine.validate_definition(candidate)
                with self.assertRaisesRegex(ValueError, 'require protocol'):
                    engine.evaluate(candidate, questions, {})

    def test_legacy_exact_report_matches_frozen_original(self):
        fixtures = ROOT / 'test/fixtures/review-gate'
        definitions = {
            'base': json.loads((fixtures / 'frontend-v2.json').read_text()),
            'two-surfaces': json.loads((fixtures / 'frontend-v2-two-surfaces.json').read_text())}
        vectors = json.loads((fixtures / 'frontend-v2-vectors.json').read_text())['vectors']
        self.assertGreaterEqual(len(vectors), 264)
        for vector in vectors:
            data = definitions[vector['definition']]
            questions = engine.validate_definition(data)
            observations = {question['id']: {'choice': question['choices'][answer[0]]['id'],
                           'evidence': 'Recorded fixture observation.' if answer[1] else ''}
                            for question, answer in zip(data['questions'], vector['answers']) if answer is not None}
            result = engine.evaluate(data, questions, observations)
            canonical = json.dumps(result, ensure_ascii=False, sort_keys=True, separators=(',', ':')).encode()
            self.assertEqual(hashlib.sha256(canonical).hexdigest(), vector['report_sha256'])


if __name__ == '__main__':
    unittest.main()
