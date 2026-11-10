import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest


SCRIPT = Path(__file__).resolve().parents[2] / 'scripts/kvm/check_connection.sh'


class CheckConnectionScriptTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        script_dir = self.root / 'scripts/kvm'
        script_dir.mkdir(parents=True)
        self.script = script_dir / SCRIPT.name
        shutil.copy2(SCRIPT, self.script)
        (script_dir.parent / 'cloudrc').write_text('run_dir="' + str(self.root / 'run') + '"\n')
        mock = self.root / 'ipset'
        mock.write_text('''#!/bin/sh
if [ "$1" = list ] && [ "$2" = -name ]; then
    ls "$IPSET_FIXTURE"
elif [ "$1" = save ]; then
    cat "$IPSET_FIXTURE/$2"
else
    exit 9
fi
''')
        mock.chmod(0o755)
        self.fixture = self.root / 'sets'
        self.fixture.mkdir()
        (self.fixture / 'sgas-tap1').write_text('create sgas-tap1 hash:ip,mac family inet\nadd sgas-tap1 192.0.2.10,aa:bb:cc:dd:ee:ff\n')
        (self.fixture / 'sgas-tap2').write_text('create sgas-tap2 hash:ip,mac family inet6\nadd sgas-tap2 2001:db8::10,aa:bb:cc:dd:ee:ff\n')
        (self.fixture / 'blocked_ips').write_text('create blocked_ips hash:ip\nadd blocked_ips 192.0.2.1\n')
        self.env = dict(os.environ, PATH=str(self.root) + ':' + os.environ['PATH'],
                        IPSET_FIXTURE=str(self.fixture))
        self.target = self.root / 'run/check_connection.ip_list'

    def run_check(self, ip):
        return subprocess.run([str(self.script), ip], env=self.env,
                              capture_output=True, text=True)

    def test_exact_match_and_duplicate(self):
        result = self.run_check('192.0.2.10')
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(self.target.read_text(), '192.0.2.10\n')
        self.assertEqual(self.run_check('192.0.2.10').returncode, 0)
        self.assertEqual(self.target.read_text(), '192.0.2.10\n')
        (self.fixture / 'sgas-tap1').write_text('')
        self.assertEqual(self.run_check('192.0.2.10').returncode, 0)
        self.assertEqual(self.target.read_text(), '192.0.2.10\n')

    def test_ipv6(self):
        result = self.run_check('2001:db8::10')
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(self.target.read_text(), '2001:db8::10\n')
        self.assertEqual(self.run_check('192.0.2.10').returncode, 0)
        self.assertEqual(self.target.read_text(), '2001:db8::10\n192.0.2.10\n')

    def test_non_vm_and_substring_do_not_match(self):
        for ip in ('192.0.2.1', '192.0.2.2', '192.0.2.100', '2001:db8::1'):
            with self.subTest(ip=ip):
                self.assertEqual(self.run_check(ip).returncode, 0)
                self.assertFalse(self.target.exists())

    def test_concurrent_requests(self):
        processes = [subprocess.Popen([str(self.script), ip], env=self.env,
                                      stdout=subprocess.PIPE, stderr=subprocess.PIPE)
                     for ip in ('192.0.2.10', '2001:db8::10') * 5]
        for process in processes:
            _, error = process.communicate(timeout=10)
            self.assertEqual(process.returncode, 0, error)
        self.assertEqual(sorted(self.target.read_text().splitlines()),
                         ['192.0.2.10', '2001:db8::10'])

    def test_only_member_address_is_matched(self):
        with (self.fixture / 'sgas-tap1').open('a') as fixture:
            fixture.write('add sgas-tap1 192.0.2.20,aa:bb:cc:dd:ee:ff comment "192.0.2.30"\n')
            fixture.write('add other-set 192.0.2.40,aa:bb:cc:dd:ee:ff\n')
        for value in ('create sgas-tap1 hash:ip', '192.0.2.30', '192.0.2.40', 'aa:bb:cc:dd:ee:ff'):
            with self.subTest(value=value):
                self.assertEqual(self.run_check(value).returncode, 0)
                self.assertFalse(self.target.exists())
        self.assertEqual(self.run_check('192.0.2.20').returncode, 0)
        self.assertEqual(self.target.read_text(), '192.0.2.20\n')

    def test_missing_argument_and_ipset_failure(self):
        self.assertNotEqual(subprocess.run([str(self.script)], env=self.env, capture_output=True).returncode, 0)
        (self.root / 'ipset').write_text('#!/bin/sh\nexit 1\n')
        self.assertNotEqual(self.run_check('192.0.2.10').returncode, 0)
        self.assertFalse(self.target.exists())


if __name__ == '__main__':
    unittest.main()
