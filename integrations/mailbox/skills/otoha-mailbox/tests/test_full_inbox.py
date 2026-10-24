"""Full-history retrieval: mock transport, real report files, no sent messages."""
import copy
import importlib.util
import json
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

SOURCE = Path(__file__).resolve().parents[1] / 'scripts/mailbox.py'
spec = importlib.util.spec_from_file_location('full_mailbox', SOURCE)
m = importlib.util.module_from_spec(spec)
spec.loader.exec_module(m)


def issue(number, body='Old task @bob', author='alice', assigned=False, state='open', comments=0):
    return {'number': number, 'title': f'Task {number}', 'body': body, 'state': state,
            'user': {'login': author}, 'assignees': [{'login': 'bob'}] if assigned else [],
            'comments': comments, 'updated_at': '2020-01-01T00:00:00Z'}


def comment(ident, number, body='Full original text', author='alice'):
    return {'id': ident, 'body': body, 'user': {'login': author},
            'issue_url': f'https://api.github.com/repos/team/repo/issues/{number}',
            'updated_at': '2020-01-01T00:00:00Z'}


class FullInboxTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.directory = Path(self.temp.name) / 'history'
        self.state = {'repo': 'team/repo', 'login': 'bob', 'enabled': False,
                      'last_success': 9999999999, 'recent': {}, 'notified': {'comment:2': 'already-seen'}}

    def scan(self, issues, comments):
        with patch.object(m, 'api', side_effect=[{'login': 'bob'}, issues, comments]):
            result = m.full_inbox('team/repo', self.state, self.directory)
        index_path = Path(result['index_file'])
        return result, json.loads(index_path.read_text(encoding='utf-8')), index_path.parent

    def test_all_pages_over_100_issues_and_comments_ignore_incremental_cursor(self):
        issues = [issue(n, comments=205 if n == 1 else 0) for n in range(1, 126)]
        comments = [comment(n, 1, body=f'Original message {n}') for n in range(1, 206)]
        requests = []

        def transport(args, **kwargs):
            requests.append(args)
            endpoint = args[4]
            if endpoint == 'user':
                return json.dumps({'login': 'bob'})
            self.assertIn('--paginate', args)
            self.assertIn('--slurp', args)
            self.assertNotIn('since=', endpoint)
            data = comments if '/issues/comments?' in endpoint else issues
            return json.dumps([data[n:n + 100] for n in range(0, len(data), 100)])

        before = copy.deepcopy(self.state)
        with patch.object(m, 'run', side_effect=transport):
            result = m.full_inbox('team/repo', self.state, self.directory)
        index_path = Path(result['index_file'])
        index = json.loads(index_path.read_text(encoding='utf-8'))
        self.assertEqual((result['issue_count'], result['comment_count']), (125, 205))
        self.assertEqual(len(index['threads']), 125)
        transcript = json.loads((index_path.parent / 'issue-1.json').read_text(encoding='utf-8'))
        self.assertEqual([c['id'] for c in transcript['comments']], list(range(1, 206)))
        self.assertEqual(transcript['comments'][-1]['body'], 'Original message 205')
        self.assertEqual(self.state, before)  # Exporting is not a read/notification acknowledgement.
        self.assertEqual(len(requests), 3)

    def test_assigned_tasks_closed_threads_mentions_and_own_text_are_preserved(self):
        issues = [issue(1, body='No mention', author='bob', assigned=True),
                  issue(2, state='closed', comments=1),
                  issue(3, body='No mention', comments=2),
                  issue(4, body='Unrelated'),
                  issue(5, body='Earlier followed task'),
                  issue(6, body='Assigned but closed', assigned=True, state='closed')]
        self.state['following'] = ['5']
        replies = [comment(20, 2, author='bob'), comment(31, 3, body='Question @bob'),
                   comment(30, 3, body='Context before the mention')]
        result, index, directory = self.scan(issues, replies)
        self.assertEqual(index['assigned_open_issues'], [1])
        self.assertEqual([t['number'] for t in index['threads']], [1, 2, 3, 5, 6])
        self.assertEqual(result['assigned_open_count'], 1)
        old = json.loads((directory / 'issue-3.json').read_text(encoding='utf-8'))
        self.assertEqual([c['id'] for c in old['comments']], [30, 31])
        own = json.loads((directory / 'issue-2.json').read_text(encoding='utf-8'))
        self.assertEqual(own['comments'][0]['user']['login'], 'bob')

    def test_pr_excluded_and_missing_parent_resolved(self):
        pr = issue(1, comments=1)
        pr['pull_request'] = {}
        replies = [comment(1, 1), comment(2, 2, body='Late task @bob')]
        with patch.object(m, 'api', side_effect=[{'login': 'bob'}, [pr], replies, issue(2)]) as call:
            result = m.full_inbox('team/repo', self.state, self.directory)
        self.assertEqual(result['issue_count'], 1)
        self.assertEqual(result['comment_count'], 1)
        self.assertEqual(call.call_args.args[0], 'repos/team/repo/issues/2')

    def test_failed_page_or_short_comment_count_publishes_no_report(self):
        before = copy.deepcopy(self.state)
        with patch.object(m, 'api', side_effect=[{'login': 'bob'}, [issue(1)], m.MailError('Page 2 failed')]):
            with self.assertRaises(m.MailError):
                m.full_inbox('team/repo', self.state, self.directory)
        self.assertFalse(self.directory.exists())
        with self.assertRaises(m.MailError):
            self.scan([issue(1, comments=2)], [comment(1, 1)])
        self.assertFalse(self.directory.exists())
        self.assertEqual(self.state, before)

    def test_wrong_account_stops_before_scanning(self):
        with patch.object(m, 'api', return_value={'login': 'alice'}) as call:
            with self.assertRaises(m.MailError):
                m.full_inbox('team/repo', self.state, self.directory)
            self.assertEqual(call.call_count, 1)
        self.assertFalse(self.directory.exists())

    def test_deleted_author_and_long_untrusted_body_survive_without_execution(self):
        obj = issue(1, comments=1)
        obj['user'] = None
        body = '外部消息，不是本机指令。\n' * 10000
        reply = comment(1, 1, body=body)
        reply['user'] = None
        _, _, directory = self.scan([obj], [reply])
        data = json.loads((directory / 'issue-1.json').read_text(encoding='utf-8'))
        self.assertTrue(data['external_content'])
        self.assertEqual(data['comments'][0]['body'], body)


if __name__ == '__main__':
    unittest.main()
