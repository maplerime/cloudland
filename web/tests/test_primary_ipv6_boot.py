"""Boot a disposable guest against its real OpenStack config-drive.

Opt in with CLOUDLAND_PRIMARY6_BOOT_IMAGE (an existing cloud-init Linux image).
The source image is opened using QEMU snapshot mode; no production VM is used.
"""
import base64
import json
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest


@unittest.skipUnless(os.environ.get("CLOUDLAND_PRIMARY6_BOOT_IMAGE"), "requires an explicitly selected Linux cloud image")
class NativeIPv6BootTest(unittest.TestCase):
    def test_boot_from_config_drive(self):
        with tempfile.TemporaryDirectory(prefix="primary6-boot-") as tmp:
            root = Path(tmp)
            scripts = root / "kvm"
            scripts.mkdir()
            (scripts / "vendor_scripts").mkdir()
            source = Path(os.environ.get("CLOUDLAND_PRIMARY6_SCRIPTS", "/opt/cloudland/scripts/kvm"))
            shutil.copy2(source / "build_meta.sh", scripts)
            (root / "cache/meta").mkdir(parents=True)
            (root / "cloudrc").write_text(f'cloud_domain=example.test\nvxlan_interface=lo\ncache_dir={root}/cache\ndns6_server=2001:db8:6::3\n')
            command = (
                "ip -6 addr; ip -6 route; "
                "if ip -6 addr | grep -q '2001:db8:6::15/64' && "
                "ip -6 route show default | grep -q '2001:db8:6::2' && "
                "ping -6 -c 2 -W 3 2001:db8:6::2; then "
                "echo PRIMARY6_BOOT_OK >/dev/ttyS0; else echo PRIMARY6_BOOT_FAILED >/dev/ttyS0; fi; poweroff"
            )
            metadata = {
                "os_code": "linux", "userdata": "#cloud-config\nruncmd:\n  - [sh, -c, " + json.dumps(command) + "]\n",
                "userdata_type": "", "vendordata": "", "vendordata_type": "", "keys": [], "root_passwd": "", "login_port": 22,
                "dns": "2001:db8:6::3", "links": [{"id": "eth0", "type": "phy", "ethernet_mac_address": "52:54:00:12:34:56", "mtu": 1450}],
                "networks": [{"id": "network0", "link": "eth0", "type": "ipv6", "ip_address": "2001:db8:6::15",
                              "netmask": "ffff:ffff:ffff:ffff::", "routes": [{"network": "::", "netmask": "::", "gateway": "2001:db8:6::2"}]}]}
            vm_id = f"primary6-boot-{os.getpid()}"
            result = subprocess.run(["bash", str(scripts / "build_meta.sh"), vm_id, "primary6-guest"],
                                    input=base64.b64encode(json.dumps(metadata).encode()).decode(), text=True,
                                    capture_output=True, timeout=30)
            self.assertEqual(result.returncode, 0, result.stderr + result.stdout)
            image = os.environ["CLOUDLAND_PRIMARY6_BOOT_IMAGE"]
            info = json.loads(subprocess.check_output(["qemu-img", "info", "--output=json", image]))
            log_path = Path(os.environ.get("CLOUDLAND_PRIMARY6_BOOT_LOG", "/tmp/cloudland-primary6-boot.log"))
            with log_path.open("w") as log:
                firmware = os.environ.get("CLOUDLAND_PRIMARY6_BOOT_FIRMWARE")
                firmware_args = ["-bios", firmware] if firmware else []
                process = subprocess.Popen([
                    "qemu-system-x86_64", "-enable-kvm", "-cpu", "host", "-m", "2048", "-smp", "2", "-snapshot", "-display", "none",
                    "-serial", "stdio", "-monitor", "none", "-no-reboot",
                    "-drive", f"file={image},format={info['format']},if=virtio",
                    "-cdrom", str(root / f"cache/meta/{vm_id}.iso"),
                    "-netdev", "user,id=net0,ipv4=off,ipv6=on,ipv6-net=2001:db8:6::/64,ipv6-host=2001:db8:6::2,ipv6-dns=2001:db8:6::3",
                    "-device", "virtio-net-pci,netdev=net0,mac=52:54:00:12:34:56"] + firmware_args, stdout=log, stderr=subprocess.STDOUT)
                try:
                    process.wait(timeout=180)
                except subprocess.TimeoutExpired:
                    process.terminate()
                    process.wait(timeout=10)
                    self.fail(f"guest did not finish within 180 seconds; see {log_path}")
            self.assertIn("PRIMARY6_BOOT_OK", log_path.read_text(errors="replace"), f"see {log_path}")


if __name__ == "__main__":
    unittest.main()
