import os
from pathlib import Path
import shlex
import shutil
import subprocess
import tempfile
import unittest


SCRIPT = Path(__file__).resolve().parents[2] / 'scripts/kvm/operation/block_ip.sh'


class BlockIPTimeoutTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        script_dir = self.root / 'scripts/kvm/operation'
        script_dir.mkdir(parents=True)
        self.script = script_dir / SCRIPT.name
        shutil.copy2(SCRIPT, self.script)
        self.cloudrc = script_dir.parents[1] / 'cloudrc'
        self.cloudrc.write_text('log_dir="' + str(self.root / 'log') + '"\n')
        self.calls = self.root / 'calls'
        mock = self.root / 'ipset'
        mock.write_text('#!/bin/sh\necho "$*" >> "$IPSET_CALLS"\n')
        mock.chmod(0o755)
        mock = self.root / 'iptables'
        mock.write_text('#!/bin/sh\n[ "$1" = -L ] && echo "2 BLACKLIST all -- 0.0.0.0/0 0.0.0.0/0"\nexit 0\n')
        mock.chmod(0o755)
        self.env = dict(os.environ, PATH=str(self.root) + ':' + os.environ['PATH'], IPSET_CALLS=str(self.calls))

    def block(self, *args):
        return subprocess.run([str(self.script), '192.0.2.1', *args], env=self.env,
                              text=True, capture_output=True)

    def blacklist_adds(self):
        return [shlex.split(line) for line in self.calls.read_text().splitlines()
                if line.startswith('add blacklist ')]

    def test_default_and_existing_config(self):
        result = self.block('block_dst')
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(self.blacklist_adds()[-1], ['add', 'blacklist', '192.0.2.1', 'timeout', '3600', '-exist'])
        with self.cloudrc.open('a') as config:
            config.write('blocking_timeout=7200\n')
        self.assertEqual(self.block().returncode, 0)
        self.assertEqual(self.blacklist_adds()[-1][-3:], ['timeout', '7200', '-exist'])

    def test_explicit_timeout_overrides_and_refreshes_existing_ip(self):
        with self.cloudrc.open('a') as config:
            config.write('blocking_timeout=7200\n')
        for timeout in ('900', '0', '2147483'):
            result = self.block('block_dst', timeout)
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertEqual(self.blacklist_adds()[-1], ['add', 'blacklist', '192.0.2.1', 'timeout', timeout, '-exist'])
        self.assertIn('add block_dst 192.0.2.1 timeout 300 -exist', self.calls.read_text())

    def test_timeout_without_info_set(self):
        self.assertEqual(self.block('', '60').returncode, 0)
        self.assertEqual(self.blacklist_adds()[-1][-3:], ['timeout', '60', '-exist'])

    def test_invalid_timeout_is_rejected_before_ipset_operations(self):
        for timeout in ('-1', '1.5', 'invalid', '2147484', '9999999999999999999999'):
            with self.subTest(timeout=timeout):
                result = self.block('block_dst', timeout)
                self.assertNotEqual(result.returncode, 0)
                self.assertFalse(self.calls.exists())


if __name__ == '__main__':
    unittest.main()
