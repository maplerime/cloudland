import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest


SCRIPT = Path(__file__).resolve().parents[2] / 'scripts/kvm/operation/unblock_ip.sh'


class UnblockIPTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        script_dir = self.root / 'scripts/kvm/operation'
        script_dir.mkdir(parents=True)
        self.script = script_dir / SCRIPT.name
        shutil.copy2(SCRIPT, self.script)
        (script_dir.parents[1] / 'cloudrc').write_text('log_dir="' + str(self.root / 'log') + '"\n')
        self.sets = self.root / 'sets'
        self.sets.mkdir()
        self.calls = self.root / 'calls'
        mock = self.root / 'ipset'
        mock.write_text('''#!/bin/sh
case "$1" in
    list)
        [ "$FAIL_LIST" = 1 ] && exit 1
        ls "$IPSET_STATE"
        ;;
    test) grep -Fxq -- "$3" "$IPSET_STATE/$2" ;;
    del)
        echo "$2 $3" >> "$IPSET_CALLS"
        [ "$FAIL_DELETE" = 1 ] && exit 1
        awk -v ip="$3" '$0 != ip' "$IPSET_STATE/$2" > "$IPSET_STATE/$2.tmp"
        mv "$IPSET_STATE/$2.tmp" "$IPSET_STATE/$2"
        ;;
    *) exit 9 ;;
esac
''')
        mock.chmod(0o755)
        self.env = dict(os.environ, PATH=str(self.root) + ':' + os.environ['PATH'],
                        IPSET_STATE=str(self.sets), IPSET_CALLS=str(self.calls))

    def unblock(self, ip='192.0.2.1'):
        return subprocess.run([str(self.script), ip], env=self.env, text=True, capture_output=True)

    def test_removes_blacklist_and_info_entries_only(self):
        for name in ('blacklist', 'block_src', 'block_dst', 'sgas-tap1'):
            (self.sets / name).write_text('192.0.2.1\n192.0.2.2\n')
        result = self.unblock()
        self.assertEqual(result.returncode, 0, result.stderr)
        for name in ('blacklist', 'block_src', 'block_dst'):
            self.assertEqual((self.sets / name).read_text(), '192.0.2.2\n')
        self.assertEqual((self.sets / 'sgas-tap1').read_text(), '192.0.2.1\n192.0.2.2\n')
        self.assertEqual(len(self.calls.read_text().splitlines()), 3)
        self.assertEqual(self.unblock().returncode, 0)
        self.assertEqual(len(self.calls.read_text().splitlines()), 3)

    def test_missing_sets_and_entries_are_successful(self):
        self.assertEqual(self.unblock().returncode, 0)
        (self.sets / 'blacklist').write_text('192.0.2.2\n')
        self.assertEqual(self.unblock().returncode, 0)
        (self.sets / 'block_dst').write_text('192.0.2.1\n')
        self.assertEqual(self.unblock().returncode, 0)
        self.assertEqual((self.sets / 'block_dst').read_text(), '')

    def test_failures_and_invalid_input(self):
        self.assertNotEqual(self.unblock('invalid').returncode, 0)
        self.env['FAIL_LIST'] = '1'
        self.assertNotEqual(self.unblock().returncode, 0)
        self.env.pop('FAIL_LIST')
        (self.sets / 'blacklist').write_text('192.0.2.1\n')
        self.env['FAIL_DELETE'] = '1'
        self.assertNotEqual(self.unblock().returncode, 0)
        self.assertEqual((self.sets / 'blacklist').read_text(), '192.0.2.1\n')


if __name__ == '__main__':
    unittest.main()
