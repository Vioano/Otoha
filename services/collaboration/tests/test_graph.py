"""Contract tests against an isolated SurrealDB 3.3.5 process.

Set OTOHA_TEST_SURREAL_BINARY to a verified local binary. No global installation,
real credentials, registered services, or existing database state are used.
"""
import os
import json
import pathlib
import secrets
import socket
import subprocess
import sys
import tempfile
import time
import unittest
import urllib.request

sys.path.insert(0, str(pathlib.Path(__file__).resolve().parents[1]))
from otoha_graph import GraphBackend, GraphError


@unittest.skipUnless(os.environ.get('OTOHA_TEST_SURREAL_BINARY'), 'set OTOHA_TEST_SURREAL_BINARY for real database tests')
class GraphDatabaseTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.temp = tempfile.TemporaryDirectory(prefix='otoha-graph-contract-')
        cls.directory = pathlib.Path(cls.temp.name)
        cls.password = secrets.token_urlsafe(32)
        cls.log = (cls.directory / 'database.log').open('ab')
        cls.start_database()

    @classmethod
    def start_database(cls):
        with socket.socket() as sock:
            sock.bind(('127.0.0.1', 0))
            cls.port = sock.getsockname()[1]
        cls.process = subprocess.Popen([
            os.environ['OTOHA_TEST_SURREAL_BINARY'], 'start', '--no-banner', '--log', 'warn',
            '--bind', '127.0.0.1:' + str(cls.port), 'surrealkv://' + str(cls.directory / 'data')],
            env={**os.environ, 'SURREAL_USER': 'graph-test', 'SURREAL_PASS': cls.password},
            stdout=cls.log, stderr=cls.log)
        opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
        for _ in range(100):
            if cls.process.poll() is not None:
                raise RuntimeError('isolated SurrealDB process exited')
            try:
                with opener.open('http://127.0.0.1:' + str(cls.port) + '/health', timeout=.2):
                    return
            except OSError:
                time.sleep(.05)
        cls.process.terminate()
        cls.process.wait(timeout=10)
        raise RuntimeError('isolated SurrealDB did not become healthy')

    @classmethod
    def tearDownClass(cls):
        cls.process.terminate()
        cls.process.wait(timeout=10)
        cls.log.close()
        cls.temp.cleanup()

    def setUp(self):
        self.binding = {'url': 'http://127.0.0.1:' + str(self.port), 'username': 'graph-test',
                        'password': self.password, 'namespace': 'graph_contract',
                        'database': 'test_' + secrets.token_hex(8),
                        'graph_id': 'fixture', 'project': 'team'}
        self.graph = GraphBackend(self.binding)

    def change(self, operations, revision=0, request_id='first', actor='alice'):
        return self.graph.mutate(actor, {'project': 'team', 'request_id': request_id,
                                       'expected_revision': revision, 'operations': operations})

    def inspect(self, id, kind='node'):
        return self.graph.query({'project': 'team', 'mode': 'inspect', 'kind': kind, 'id': id})

    def expect_error(self, code, function, *args, **kwargs):
        with self.assertRaises(GraphError) as error:
            function(*args, **kwargs)
        self.assertEqual(code, error.exception.code)

    def test_free_core_parallel_edges_self_loop_and_reserved_properties(self):
        self.assertEqual(0, self.graph.manifest()['source_version'])
        properties = {'id': 'business-id', 'in': {'custom': [None, '中文', True]}, 'type': 'anything'}
        self.change([{'op': 'node.add', 'id': 'a', 'properties': properties},
                     {'op': 'node.add', 'id': 'empty'},
                     {'op': 'edge.add', 'id': 'one', 'source': 'a', 'target': 'empty'},
                     {'op': 'edge.add', 'id': 'two', 'source': 'a', 'target': 'empty'},
                     {'op': 'edge.add', 'id': 'loop', 'source': 'a', 'target': 'a'}])
        self.assertEqual(properties, self.inspect('a')['nodes'][0]['properties'])
        self.assertEqual({}, self.inspect('empty')['nodes'][0]['properties'])
        self.assertEqual({'nodes': 2, 'edges': 3}, self.graph.manifest()['counts'])
        self.assertEqual('a', self.inspect('loop', 'edge')['edges'][0]['target'])

    def test_null_unset_literal_keys_and_filter(self):
        self.change([{'op': 'node.add', 'id': 'a', 'properties': {'x': None, 'a.b': 'old'}},
                     {'op': 'node.add', 'id': 'b'}])
        self.change([{'op': 'prop.set', 'kind': 'node', 'id': 'a', 'key': 'a.b', 'value': {'v': [1, 2]}},
                     {'op': 'prop.unset', 'kind': 'node', 'id': 'a', 'key': 'x'}], 1, 'second')
        self.assertEqual({'a.b': {'v': [1, 2]}}, self.inspect('a')['nodes'][0]['properties'])
        found = self.graph.query({'project': 'team', 'mode': 'filter', 'where': {'x': None}})
        self.assertEqual([], found['nodes'])
        self.change([{'op': 'prop.set', 'kind': 'node', 'id': 'b', 'key': 'x', 'value': None}], 2, 'third')
        found = self.graph.query({'project': 'team', 'mode': 'filter', 'where': {'x': None}})
        self.assertEqual(['b'], [n['id'] for n in found['nodes']])
        self.change([{'op': 'prop.unset', 'kind': 'node', 'id': 'a', 'key': 'a.b'}], 3, 'fourth')
        self.assertEqual({}, self.inspect('a')['nodes'][0]['properties'])

    def test_batch_rollback_conflict_and_incident_delete(self):
        self.change([{'op': 'node.add', 'id': 'a'}])
        self.expect_error('graph_endpoint_not_found', self.change,
                          [{'op': 'node.add', 'id': 'rolledback'},
                           {'op': 'edge.add', 'id': 'bad', 'source': 'rolledback', 'target': 'missing'}], 1, 'bad')
        self.assertEqual({'nodes': 1, 'edges': 0}, self.graph.manifest()['counts'])
        self.assertEqual(1, self.graph.manifest()['source_version'])
        self.expect_error('graph_request_not_found', self.graph.receipt, 'alice', 'bad')
        self.expect_error('graph_revision_conflict', self.change, [{'op': 'node.add', 'id': 'b'}], 0, 'stale')
        self.change([{'op': 'edge.add', 'id': 'loop', 'source': 'a', 'target': 'a'}], 1, 'loop')
        self.expect_error('graph_incident_edges', self.change, [{'op': 'node.remove', 'id': 'a'}], 2, 'delete')
        self.change([{'op': 'edge.remove', 'id': 'loop'}, {'op': 'node.remove', 'id': 'a'}], 2, 'clean-delete')
        self.assertEqual({'nodes': 0, 'edges': 0}, self.graph.manifest()['counts'])

    def test_idempotent_receipt_is_actor_scoped(self):
        operations = [{'op': 'node.add', 'id': 'a'}]
        first = self.change(operations)
        self.assertFalse(first['duplicate'])
        retry = self.change(operations)
        self.assertTrue(retry['duplicate'])
        self.assertEqual(first['request_sha256'], retry['request_sha256'])
        self.assertEqual(1, retry['source_version'])
        self.expect_error('graph_request_id_conflict', self.change, [{'op': 'node.add', 'id': 'other'}])
        self.expect_error('graph_request_not_found', self.graph.receipt, 'bob', 'first')
        self.assertEqual(1, self.graph.receipt('alice', 'first')['source_version'])
        self.assertEqual(1, self.graph.manifest()['counts']['nodes'])

    def test_filter_pagination_and_pinned_source(self):
        self.change([{'op': 'node.add', 'id': n, 'properties': {'status': 'todo'}} for n in ['a', 'b', 'c']])
        first = self.graph.query({'project': 'team', 'mode': 'filter', 'where': {'status': 'todo'}, 'limit': 2})
        self.assertEqual(['a', 'b'], [n['id'] for n in first['nodes']])
        self.assertFalse(first['complete'])
        self.assertTrue(first['truncated'])
        second = self.graph.query({'project': 'team', 'mode': 'filter', 'limit': 2,
                                   'after': first['next_after'], 'source_version': first['source_version']})
        self.assertEqual(['c'], [n['id'] for n in second['nodes']])
        self.assertTrue(second['complete'])
        self.change([{'op': 'prop.set', 'kind': 'node', 'id': 'a', 'key': 'status', 'value': 'done'}], 1, 'update')
        self.expect_error('graph_revision_conflict', self.graph.query,
                          {'project': 'team', 'mode': 'filter', 'source_version': 1})

    def test_neighborhood_directions_and_boundary(self):
        self.change([{'op': 'node.add', 'id': n} for n in ['a', 'b', 'c']]
                    + [{'op': 'edge.add', 'id': 'out', 'source': 'a', 'target': 'b'},
                       {'op': 'edge.add', 'id': 'in', 'source': 'c', 'target': 'a'}])
        outgoing = self.graph.query({'project': 'team', 'mode': 'neighborhood', 'id': 'a', 'direction': 'out'})
        self.assertEqual(['out'], [e['id'] for e in outgoing['edges']])
        self.assertEqual({'a', 'b'}, {n['id'] for n in outgoing['nodes']})
        incoming = self.graph.query({'project': 'team', 'mode': 'neighborhood', 'id': 'a', 'direction': 'in'})
        self.assertEqual(['in'], [e['id'] for e in incoming['edges']])
        self.assertEqual({'a', 'c'}, {n['id'] for n in incoming['nodes']})
        self.assertEqual(1, incoming['scope']['depth'])

    def test_edge_property_operations_and_quoted_ids(self):
        self.change([{'op': 'node.add', 'id': 'node.with-dots'},
                     {'op': 'edge.add', 'id': 'edge.with-dots', 'source': 'node.with-dots', 'target': 'node.with-dots'},
                     {'op': 'prop.set', 'kind': 'edge', 'id': 'edge.with-dots', 'key': 'relation', 'value': 'arbitrary'}])
        self.assertEqual({'relation': 'arbitrary'}, self.inspect('edge.with-dots', 'edge')['edges'][0]['properties'])
        self.change([{'op': 'prop.unset', 'kind': 'edge', 'id': 'edge.with-dots', 'key': 'relation'}], 1, 'unset')
        self.assertEqual({}, self.inspect('edge.with-dots', 'edge')['edges'][0]['properties'])

    def test_persistent_graph_and_receipt_after_database_restart(self):
        first = self.change([{'op': 'node.add', 'id': 'durable', 'properties': {'nested': [None, '原件']}}])
        self.process.terminate()
        self.process.wait(timeout=10)
        type(self).start_database()
        self.binding['url'] = 'http://127.0.0.1:' + str(self.port)
        self.graph = GraphBackend(self.binding)
        self.assertEqual({'nested': [None, '原件']}, self.inspect('durable')['nodes'][0]['properties'])
        self.assertEqual(first['request_sha256'], self.graph.receipt('alice', 'first')['request_sha256'])
        self.assertEqual(1, self.graph.manifest()['source_version'])

    def test_real_authenticated_http_chain_uses_same_project_graph(self):
        from test_service import ServiceTest
        fixture = ServiceTest('runTest')
        fixture.setUp()
        try:
            # The public route prefix is stripped before the same authenticated
            # graph API; this uses the real database, not the auth substitute.
            fixture.url += '/t/graph-fixture'
            fixture.store.config['public_url'] = fixture.url
            config = {key: self.binding[key] for key in ('url', 'username', 'password', 'namespace')}
            config['projects'] = {'team': {key: self.binding[key] for key in ('database', 'graph_id')}}
            config_file = fixture.root / 'state' / 'graph.json'
            config_file.write_text(json.dumps(config))
            config_file.chmod(0o600)
            writer = fixture.member('graph-writer')[0]
            reader = fixture.member('graph-reader', 'reader')[0]
            manifest = fixture.call('GET', '/v1/graph/manifest?project=team', token=reader)
            self.assertEqual(0, manifest['source_version'])
            request = {'project': 'team', 'request_id': 'http-one', 'expected_revision': 0,
                       'operations': [{'op': 'node.add', 'id': 'requirement', 'properties': {'label': '共享依据'}}]}
            fixture.call('POST', '/v1/graph/mutations', request, reader, status=403)
            accepted = fixture.call('POST', '/v1/graph/mutations', request, writer)
            self.assertEqual(1, accepted['source_version'])
            observed = fixture.call('POST', '/v1/graph/query',
                                    {'project': 'team', 'mode': 'inspect', 'id': 'requirement'}, reader)
            self.assertEqual('共享依据', observed['nodes'][0]['properties']['label'])
            receipt = fixture.call('GET', '/v1/graph/requests/http-one?project=team', token=writer)
            self.assertEqual(accepted['request_sha256'], receipt['request_sha256'])
            fixture.call('GET', '/v1/graph/requests/http-one?project=team', token=reader, status=404)
        finally:
            fixture.tearDown()

    def test_result_byte_bound_returns_whole_objects_and_continuation(self):
        for batch in range(9):
            self.change([{'op': 'node.add', 'id': 'n%02d' % n, 'properties': {'text': 'x' * 31000}}
                         for n in range(batch * 2, batch * 2 + 2)], batch, 'batch-%d' % batch)
        first = self.graph.query({'project': 'team', 'mode': 'filter', 'limit': 100})
        self.assertTrue(first['truncated'])
        self.assertLessEqual(len(json.dumps(first, ensure_ascii=False).encode()), 512 * 1024)
        self.assertTrue(all(len(n['properties']['text']) == 31000 for n in first['nodes']))
        second = self.graph.query({'project': 'team', 'mode': 'filter', 'limit': 100,
                                   'after': first['next_after'], 'source_version': first['source_version']})
        self.assertTrue(second['complete'])
        ids = [n['id'] for n in first['nodes'] + second['nodes']]
        self.assertEqual(['n%02d' % n for n in range(18)], ids)

    def test_invalid_values_and_query_fields_are_not_accepted(self):
        for value in [float('nan'), float('inf'), 2**53]:
            with self.assertRaises(GraphError):
                self.change([{'op': 'node.add', 'id': 'bad', 'properties': {'value': value}}])
        self.expect_error('invalid_graph_request', self.graph.query,
                          {'project': 'team', 'mode': 'inspect', 'id': 'bad', 'where': {'ignored': True}})
        self.assertEqual(0, self.graph.manifest()['source_version'])

    def test_property_depth_does_not_depend_on_request_or_response_envelope(self):
        nested = 'leaf'
        for _ in range(29):
            nested = [nested]
        self.change([{'op': 'node.add', 'id': 'deep'}])
        self.change([{'op': 'prop.set', 'kind': 'node', 'id': 'deep', 'key': 'value', 'value': nested}], 1, 'deep-value')
        self.assertEqual(nested, self.inspect('deep')['nodes'][0]['properties']['value'])
        self.assertEqual(nested, self.graph.query({'project': 'team', 'mode': 'filter'})['nodes'][0]['properties']['value'])
        too_deep = 'leaf'
        for _ in range(33):
            too_deep = [too_deep]
        self.expect_error('graph_value_too_deep', self.change,
                          [{'op': 'prop.set', 'kind': 'node', 'id': 'deep', 'key': 'value', 'value': too_deep}], 2, 'too-deep')
        self.assertEqual(2, self.graph.manifest()['source_version'])


if __name__ == '__main__':
    unittest.main()
