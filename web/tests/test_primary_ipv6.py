"""Config-drive and guest-agent regressions for native IPv6 VM interfaces.

Set CLOUDLAND_PRIMARY6_SCRIPTS to test staged backend scripts.
"""
import base64
import importlib.util
import json
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest
from unittest.mock import patch

SCRIPTS = Path(os.environ.get("CLOUDLAND_PRIMARY6_SCRIPTS", "/opt/cloudland/scripts/kvm"))


def load(name):
    spec = importlib.util.spec_from_file_location(name, SCRIPTS / (name + ".py"))
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def network(ip="2001:db8::10", family="ipv6"):
    return {"id": "network0", "link": "eth0", "type": family, "ip_address": ip,
            "netmask": "ffff:ffff:ffff:ffff:ffff:ffff:ffff:ff00" if family == "ipv6" else "255.255.255.0",
            "routes": [{"network": "::" if family == "ipv6" else "0.0.0.0",
                        "netmask": "::" if family == "ipv6" else "0.0.0.0",
                        "gateway": "2001:db8::1" if family == "ipv6" else "192.0.2.1"}]}


class PrimaryNetworkTest(unittest.TestCase):
    def test_updates_preserve_both_families_and_secondary_nics(self):
        update = load("update_network_data").update
        for address, gateway, family in [("2001:db8::20/120", "2001:db8::1", "ipv6"),
                                          ("192.0.2.20/24", "192.0.2.1", "ipv4")]:
            secondary = dict(network(), id="network1", link="eth1", routes=[])
            data = update({"networks": [network(), secondary]}, address, gateway,
                          ["2001:db8::30/120", "192.0.2.30/24"])
            self.assertEqual(data["networks"][0]["type"], family)
            self.assertEqual(data["networks"][0]["routes"][0]["gateway"], gateway)
            self.assertEqual([n["type"] for n in data["networks"][1:3]], ["ipv6", "ipv4"])
            self.assertEqual(data["networks"][-1], secondary)
            self.assertEqual(len(data["networks"]), 4)

    def test_windows_commands_use_the_right_family(self):
        module = load("windows_network")
        with patch.object(module, "guest_netsh") as run:
            module.configure("test-vm", "2001:db8::20/120", "2001:db8::1",
                             ["2001:db8::30/120", "192.0.2.30/24"], "2001:db8::53")
        requests = [["interface"] + call.args[1] for call in run.call_args_list]
        self.assertEqual(requests[0][1:4], ["ipv6", "delete", "address"])
        self.assertEqual(requests[1][1:4], ["ipv6", "add", "address"])
        self.assertIn("address=2001:db8::20/120", requests[1])
        self.assertIn("prefix=::/0", requests[3])
        self.assertIn("address=2001:db8::53", requests[4])
        self.assertEqual(requests[-1][1:4], ["ipv4", "add", "address"])

    def test_build_config_drive_and_cloud_init_render(self):
        with tempfile.TemporaryDirectory(prefix="primary6-config-") as tmp:
            root = Path(tmp)
            scripts = root / "kvm"
            scripts.mkdir()
            (scripts / "vendor_scripts").mkdir()
            shutil.copy2(SCRIPTS / "build_meta.sh", scripts)
            binaries = root / "bin"
            binaries.mkdir()
            # Capture the actual config-drive tree just before build_meta removes it.
            (binaries / "mkisofs").write_text('#!/bin/bash\ncp -r "${@: -1}/." "$CAPTURE/"\n')
            (binaries / "mkisofs").chmod(0o755)
            (root / "cloudrc").write_text('cloud_domain=example.test\nvxlan_interface=lo\ndns_server=192.0.2.53\ndns6_server=2001:db8::53\ndns_server6=2001:db8::bad\n')
            for index, (family, address, subnet_dns, dns) in enumerate([
                    ("ipv6", "2001:db8::10", "2001:db8::54", "2001:db8::54"),
                    ("ipv6", "2001:db8::10", "", "2001:db8::53"),
                    ("ipv6", "2001:db8::10", "192.0.2.54", "2001:db8::53"),
                    ("ipv4", "192.0.2.10", "192.0.2.54", "192.0.2.54"),
                    ("ipv4", "192.0.2.10", "", "192.0.2.53")]):
                output = root / str(index)
                output.mkdir()
                metadata = {"networks": [network(address, family)],
                            "links": [{"id": "eth0", "type": "phy", "ethernet_mac_address": "52:54:00:12:34:56", "mtu": 1450}],
                            "dns": subnet_dns, "os_code": "linux", "userdata": "", "userdata_type": "", "keys": [],
                            "root_passwd": "", "vendordata": "", "vendordata_type": "", "login_port": 22}
                env = dict(os.environ, PATH=f"{binaries}:{os.environ['PATH']}", CAPTURE=str(output))
                subprocess.run(["bash", str(scripts / "build_meta.sh"), f"primary6-{os.getpid()}-{family}", "guest"],
                               input=base64.b64encode(json.dumps(metadata).encode()).decode(), text=True,
                               env=env, capture_output=True, check=True, timeout=30)
                config = output / "openstack/latest/network_data.json"
                actual = json.loads(config.read_text())
                self.assertEqual(actual["networks"], metadata["networks"])
                self.assertEqual(actual["services"], [{"type": "dns", "address": dns}])
                if shutil.which("cloud-init"):
                    rendered = root / (str(index) + "-rendered")
                    result = subprocess.run(["cloud-init", "devel", "net-convert", "-p", str(config), "-k", "network_data.json",
                                    "-d", str(rendered), "-D", "ubuntu", "-O", "netplan", "-m", "eth0,52:54:00:12:34:56"],
                                   capture_output=True, text=True, timeout=30)
                    self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
                    netplan = next(rendered.rglob("*.yaml")).read_text()
                    self.assertIn(address + ("/120" if family == "ipv6" else "/24"), netplan)
                    self.assertIn(dns, netplan)
                    self.assertIn(metadata["networks"][0]["routes"][0]["gateway"], netplan)


if __name__ == "__main__":
    unittest.main()
