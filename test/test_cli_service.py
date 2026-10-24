"""Real native CLI/Python service interoperability in disposable local state."""
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import threading
import unittest

ROOT = Path(__file__).resolve().parents[1]
SERVICE = ROOT / 'services/collaboration'
sys.path.insert(0, str(SERVICE))
from otoha_server import Server, Store
from otoha import Client


class NativeServiceTest(unittest.TestCase):
    def test_native_and_python_clients_share_messages_and_keep_state_isolated(self):
        binary = ROOT / 'dist' / ('otoha.exe' if os.name == 'nt' else 'otoha')
        self.assertTrue(binary.is_file(), 'Build the native CLI before this integration test')
        with tempfile.TemporaryDirectory(prefix='otoha-integration-') as folder:
            temp = Path(folder)
            profile = temp / 'owner.json'
            subprocess.run([sys.executable, str(SERVICE / 'otoha_server.py'), 'init', '--state', str(temp / 'state'),
                            '--owner', 'fixture', '--url', 'http://127.0.0.1:1', '--credentials', str(profile),
                            '--min-free-bytes', '0'], check=True, capture_output=True)
            store = Store(temp / 'state')
            server = Server(('127.0.0.1', 0), store)
            url = 'http://127.0.0.1:' + str(server.server_port)
            data = json.loads(profile.read_text()); data['url'] = url
            profile.write_text(json.dumps(data)); profile.chmod(0o600)
            store.config['public_url'] = url
            thread = threading.Thread(target=server.serve_forever, daemon=True); thread.start()
            env = dict(os.environ, OTOHA_HOME=str(temp / 'client-state'))

            def native(*args):
                result = subprocess.run([str(binary), *args], env=env, capture_output=True, text=True, timeout=15)
                self.assertEqual(result.returncode, 0, result.stderr)
                return json.loads(result.stdout)

            def python(*args):
                result = subprocess.run([sys.executable, str(SERVICE / 'otoha.py'), '--profile', str(profile), *args],
                                        capture_output=True, text=True, timeout=15)
                self.assertEqual(result.returncode, 0, result.stderr)
                return json.loads(result.stdout)

            try:
                native('import', str(profile))
                Client(data).call('POST', '/v1/sessions', {'id': 'fixture-session', 'agent': 'integration-test'})
                sent = native('send', '--channel', 'general', '--session', 'fixture-session', '--text', 'Otoha 中文 ↔ native')
                self.assertGreater(sent['message_id'], 0)
                history = python('history')
                self.assertEqual(history['messages'][-1]['payload']['text'], 'Otoha 中文 ↔ native')
                python('send', '--channel', 'general', '--session', 'fixture-session', '--text', 'Python → Otoha')
                history = native('read', '--channel', 'general')
                self.assertEqual(history['messages'][-1]['payload']['text'], 'Python → Otoha')
                self.assertEqual(len(history['messages']), 2)
                self.assertTrue((temp / 'client-state/config.json').is_file())
                self.assertTrue((temp / 'client-state/outbox').is_dir())
                self.assertEqual(python('protocol')['protocol'], 'otoha-collaboration/1')
            finally:
                server.shutdown(); server.server_close(); thread.join(timeout=3); store.db.close()


if __name__ == '__main__':
    unittest.main()
