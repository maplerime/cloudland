import os
from pathlib import Path
import shlex
import shutil
import subprocess
import tempfile
import unittest


SCRIPT_DIR = Path(__file__).resolve().parents[2] / 'scripts/kvm'


class ObserveConnectionTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        script_dir = self.root / 'scripts/kvm'
        script_dir.mkdir(parents=True)
        self.script = script_dir / 'observe_connection.sh'
        shutil.copy2(SCRIPT_DIR / self.script.name, self.script)
        self.run_dir = self.root / 'run'
        self.run_dir.mkdir()
        self.cloudrc = script_dir.parent / 'cloudrc'
        self.cloudrc.write_text('run_dir="' + str(self.run_dir) + '"\n')
        self.watched = self.run_dir / 'check_connection.ip_list'
        self.watched.write_text('192.0.2.10\n192.0.2.20\n')
        self.fixture = self.root / 'connections'
        self.fixture.write_text('''tcp 6 100 ESTABLISHED src=192.0.2.10 dst=198.51.100.1 sport=1 dport=2 src=198.51.100.1 dst=192.0.2.10 sport=2 dport=1
udp 17 20 src=192.0.2.20 dst=198.51.100.2 sport=1 dport=2 src=198.51.100.2 dst=192.0.2.20 sport=2 dport=1
tcp 6 100 ESTABLISHED src=192.0.2.10 dst=198.51.100.1 sport=3 dport=4 src=198.51.100.1 dst=192.0.2.10 sport=4 dport=3
tcp 6 100 ESTABLISHED src=203.0.113.1 dst=192.0.2.10 sport=1 dport=2 src=192.0.2.10 dst=203.0.113.1 sport=2 dport=1
tcp 6 100 ESTABLISHED src=192.0.2.100 dst=198.51.100.3 sport=1 dport=2 src=198.51.100.3 dst=192.0.2.100 sport=2 dport=1
''')
        mock = self.root / 'conntrack'
        mock.write_text('#!/bin/sh\n[ "$1" = -L ] || exit 9\ncat "$CONNTRACK_FIXTURE"\n')
        mock.chmod(0o755)
        self.env = dict(os.environ, PATH=str(self.root) + ':' + os.environ['PATH'],
                        CONNTRACK_FIXTURE=str(self.fixture), SCI_CLIENT_ID='7')

    def observe(self):
        return subprocess.run([str(self.script)], env=self.env,
                              text=True, capture_output=True)

    def test_only_original_source_matches_and_destinations_are_unique(self):
        result = self.observe()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(result.stdout.strip(),
                         "|:-COMMAND-:| observe_connection.sh '7' '198.51.100.1' '198.51.100.2'")

    def test_no_list_empty_list_or_no_matches_is_silent(self):
        self.watched.unlink()
        self.assertEqual(self.observe().stdout, '')
        for content in ('', '\n', '192.0.2.30\n'):
            self.watched.write_text(content)
            result = self.observe()
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertEqual(result.stdout, '')

    def test_conntrack_failure_does_not_report(self):
        (self.root / 'conntrack').write_text('#!/bin/sh\nexit 1\n')
        result = self.observe()
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(result.stdout, '')

    def test_large_reports_are_batched(self):
        destinations = ['198.51.100.' + str(n) for n in range(1, 206)]
        self.fixture.write_text(''.join('udp 17 20 src=192.0.2.10 dst=' + ip + '\n'
                                        for ip in destinations))
        result = self.observe()
        self.assertEqual(result.returncode, 0, result.stderr)
        reports = [shlex.split(line) for line in result.stdout.splitlines()]
        self.assertEqual([len(report) - 3 for report in reports], [100, 100, 5])
        self.assertEqual(sorted(ip for report in reports for ip in report[3:]), sorted(destinations))
        self.assertTrue(all(report[:3] == ['|:-COMMAND-:|', 'observe_connection.sh', '7'] for report in reports))

    def test_dns_encrypted_dns_and_ntp_are_excluded(self):
        self.fixture.write_text(''.join(
            f'{proto} 6 100 src=192.0.2.10 dst=198.51.100.{index} sport=45000 dport={port}\n'
            for index, (proto, port) in enumerate(
                [('tcp', 53), ('udp', 53), ('tcp', 853), ('udp', 853), ('udp', 123)], 1)))
        self.assertEqual(self.observe().stdout, '')

    def test_public_dns_and_site_dns_are_excluded(self):
        with self.cloudrc.open('a') as config:
            config.write('dns_server=10.0.0.53\ndns6_server=2001:db8::53\n')
        self.fixture.write_text(''.join(
            f'tcp 6 100 src=192.0.2.10 dst={ip} sport=45000 dport=443\n'
            for ip in ['8.8.8.8', '1.1.1.1', '223.5.5.5', '10.0.0.53',
                       '2001:4860:4860::8888', '2001:db8::53']))
        result = self.observe()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(result.stdout, '')

    def test_additional_trusted_ips_and_exact_match(self):
        with self.cloudrc.open('a') as config:
            config.write('observe_connection_ignore_ips="198.51.100.1,198.51.100.2"\n')
        self.fixture.write_text(''.join(
            f'tcp 6 100 src=192.0.2.10 dst={ip} sport=45000 dport=443\n'
            for ip in ['198.51.100.1', '198.51.100.2', '198.51.100.10', '8.8.8.80']))
        result = self.observe()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(shlex.split(result.stdout)[3:], ['198.51.100.10', '8.8.8.80'])

    def test_only_original_destination_service_is_filtered(self):
        self.fixture.write_text('''tcp 6 100 ESTABLISHED src=192.0.2.10 dst=198.51.100.1 sport=53 dport=443 src=198.51.100.1 dst=192.0.2.10 sport=443 dport=53
tcp 6 100 ESTABLISHED src=192.0.2.10 dst=198.51.100.2 sport=45000 dport=123 src=198.51.100.2 dst=192.0.2.10 sport=123 dport=45000
''')
        result = self.observe()
        self.assertEqual(shlex.split(result.stdout)[3:], ['198.51.100.1', '198.51.100.2'])

    def test_service_exclusions_can_be_overridden_or_disabled(self):
        self.fixture.write_text('''tcp 6 100 src=192.0.2.10 dst=198.51.100.1 sport=45000 dport=53
tcp 6 100 src=192.0.2.10 dst=198.51.100.2 sport=45000 dport=8443
''')
        with self.cloudrc.open('a') as config:
            config.write('observe_connection_ignore_services="tcp:8443"\n')
        self.assertEqual(shlex.split(self.observe().stdout)[3:], ['198.51.100.1'])
        with self.cloudrc.open('a') as config:
            config.write('observe_connection_ignore_services=""\n')
        self.assertEqual(shlex.split(self.observe().stdout)[3:], ['198.51.100.1', '198.51.100.2'])


if __name__ == '__main__':
    unittest.main()
