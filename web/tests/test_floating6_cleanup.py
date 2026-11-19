"""Unprivileged regression tests for shell router cleanup dispatch."""
import json
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest

SCRIPTS = Path(os.environ.get("CLOUDLAND_FLOATING6_SCRIPTS", str(Path(__file__).resolve().parents[2] / "scripts/kvm")))


class RouterCleanupTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix="floating6-router-cleanup-")
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        scripts = self.root / "scripts"
        scripts.mkdir()
        shutil.copy2(SCRIPTS / "clear_local_router.sh", scripts / "clear_local_router.sh")
        (scripts / "clear_link.sh").write_text('#!/bin/bash\necho clear_link >> "$TEST_LOG"\n')
        (scripts / "clear_link.sh").chmod(0o755)
        (scripts / "clear_floating6.sh").write_text('#!/bin/bash\necho f6_clear >> "$TEST_LOG"\nexit "${HELPER_STATUS:-1}"\n')
        (self.root / "cloudrc").write_text(f'cache_dir={self.root}/cache\nSCI_CLIENT_ID=1\nvxlan_interface=test\napply_vnic() {{ echo apply_vnic >> "$TEST_LOG"; }}\n')
        self.log = self.root / "commands.log"
        self.log.touch()
        wrappers = self.root / "bin"
        wrappers.mkdir()
        for name in ("ip", "ls", "ps", "ifconfig", "iptables", "python3"):
            wrapper = wrappers / name
            wrapper.write_text(f"""#!/usr/bin/python3
import json,os,sys
a=sys.argv[1:]
with open(os.environ['TEST_LOG'],'a') as out: out.write({name!r}+' '+json.dumps(a)+'\\n')
if {name!r}=='python3': sys.exit(int(os.environ.get('PYTHON_STATUS','127')))
if {name!r}=='ls': print('v-998\\nln-998')
if {name!r}=='ifconfig': print(' inet 192.0.2.1')
""")
            if name == "ip":
                wrapper.write_text(wrapper.read_text() + "if a[-3:]==['ip','link','show']: print('42: ns-998@if43: <UP>')\nif a==['netns','list']: print(os.environ.get('MOCK_NAMESPACES',''))\n")
            wrapper.chmod(0o755)
        self.env = dict(os.environ, TEST_LOG=str(self.log), PATH=str(wrappers) + os.pathsep + os.environ["PATH"], CLOUDLAND_FLOATING6_STATE=str(self.root / "state"))
        self.router_id = 2147000033
        self.script = scripts / "clear_local_router.sh"

    def execute(self):
        result = subprocess.run(["bash", str(self.script), str(self.router_id)], text=True, capture_output=True, env=self.env, timeout=10)
        return result, self.log.read_text()

    def test_ipv4_router_needs_no_python_or_ipv6_helper(self):
        result, commands = self.execute()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertNotIn("python3", commands)
        self.assertNotIn("f6_clear", commands)
        self.assertIn('["netns", "del", "router-2147000033"]', commands)

    def test_stale_local_files_do_not_control_cleanup(self):
        directory = self.root / "state" / f"router-{self.router_id}"
        directory.mkdir(parents=True)
        (directory / ".lock").touch()
        (directory / "state.json").write_text("invalid JSON")
        result, commands = self.execute()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertNotIn("python3", commands)

    def test_ipv6_failure_precedes_router_teardown(self):
        self.env["MOCK_NAMESPACES"] = f"fip6-{self.router_id}-1"
        result, commands = self.execute()
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("f6_clear", commands)
        self.assertNotIn("python3", commands)
        self.assertNotIn("clear_link", commands)
        self.assertNotIn("apply_vnic", commands)
        self.assertNotIn('["netns", "del",', commands)

    def test_other_router_translators_are_not_touched(self):
        self.env["MOCK_NAMESPACES"] = "fip6-1234-1"
        result, commands = self.execute()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertNotIn("f6_clear", commands)


if __name__ == "__main__":
    unittest.main()
