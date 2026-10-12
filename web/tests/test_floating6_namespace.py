"""Privileged, isolated NAT64 integration tests. No production namespaces touched.

Run with CLOUDLAND_FLOATING6_SCRIPTS and CLOUDLAND_TAYGA_PATH set, as root.
"""
import json
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import time
import unittest


@unittest.skipUnless(os.environ.get("CLOUDLAND_FLOATING6_SCRIPTS"), "requires explicitly selected backend scripts and CAP_NET_ADMIN")
class Floating6NamespaceTest(unittest.TestCase):
    def command(self, *args, check=True, env=None):
        result = subprocess.run(list(map(str, args)), capture_output=True, text=True, env=env, timeout=30)
        if check and result.returncode:
            self.fail(f"{' '.join(map(str, args))}: {result.stderr or result.stdout}")
        return result

    def net(self, name, *args, check=True):
        return self.command("ip", "netns", "exec", name, *args, check=check)

    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory(prefix="cloudland-floating6-test-")
        self.addCleanup(self.temporary.cleanup)
        self.fixture = Path(self.temporary.name)
        self.scripts = self.fixture / "scripts"
        self.scripts.mkdir()
        for name in ("floating6.py", "create_floating.sh", "clear_floating.sh", "create_floating6.sh", "clear_floating6.sh", "create_veth.sh", "create_link.sh"):
            shutil.copy2(Path(os.environ["CLOUDLAND_FLOATING6_SCRIPTS"]) / name, self.scripts / name)
            (self.scripts / name).chmod(0o755)
        (self.fixture / "cloudrc").write_text("# Isolated fixture: no production cloudrc\n")
        self.env = dict(os.environ, CLOUDLAND_FLOATING6_STATE=str(self.fixture / "state"))
        self.router_id = 9900 + os.getpid() % 90
        self.router = f"router-{self.router_id}"
        self.client, self.guest = f"f6test-c-{os.getpid()}", f"f6test-g-{os.getpid()}"
        self.ext_vlan = 99000 + os.getpid() % 900
        self.int_vlan = self.ext_vlan + 1
        self.bridge = f"br{self.ext_vlan}"
        self.ext_device = f"te-{self.router_id}-{self.ext_vlan}"
        self.host_device = f"ext-{self.router_id}-{self.ext_vlan}"
        for name in (self.router, self.client, self.guest):
            if Path(f"/var/run/netns/{name}").exists():
                self.skipTest(f"test namespace {name} already exists")
        if self.command("ip", "link", "show", self.bridge, check=False).returncode == 0:
            self.skipTest("test bridge already exists")
        for name in ("f6test-ext", "f6test-client", "f6test-vm", self.host_device, f"ns-{self.int_vlan}"):
            if self.command("ip", "link", "show", name, check=False).returncode == 0:
                self.skipTest(f"test device {name} already exists")
        for name in (self.router, self.client, self.guest):
            self.command("ip", "netns", "add", name)
            self.addCleanup(self.command, "ip", "netns", "del", name, check=False)
            self.net(name, "ip", "link", "set", "lo", "up")
        self.addCleanup(self.clear_translators)
        self.command("ip", "link", "add", self.bridge, "type", "bridge")
        self.addCleanup(self.command, "ip", "link", "del", self.bridge, check=False)
        self.command("ip", "link", "set", self.bridge, "up")
        self.command("ip", "link", "set", self.bridge, "type", "bridge", "nf_call_iptables", "0", "nf_call_ip6tables", "0", "nf_call_arptables", "0")
        self.command("ip", "link", "add", "f6test-ext", "type", "veth", "peer", "name", "f6test-client")
        self.addCleanup(self.command, "ip", "link", "del", "f6test-ext", check=False)
        self.command("ip", "link", "set", "f6test-ext", "master", self.bridge)
        self.command("ip", "link", "set", "f6test-ext", "up")
        self.command("ip", "link", "set", "f6test-client", "netns", self.client)
        self.net(self.client, "ip", "link", "set", "f6test-client", "up")
        for cidr in ("2001:db8:6::1/120", "2001:db8:6::2/120", "2001:db8:99::2/64"):
            self.net(self.client, "ip", "addr", "add", cidr, "dev", "f6test-client", "nodad")
        self.command("ip", "link", "add", f"ns-{self.int_vlan}", "type", "veth", "peer", "name", "f6test-vm")
        self.command("ip", "link", "set", f"ns-{self.int_vlan}", "netns", self.router)
        self.command("ip", "link", "set", "f6test-vm", "netns", self.guest)
        self.net(self.router, "ip", "link", "set", f"ns-{self.int_vlan}", "up")
        self.net(self.router, "ip", "addr", "add", "10.95.0.1/24", "dev", f"ns-{self.int_vlan}")
        self.net(self.guest, "ip", "link", "set", "f6test-vm", "up")
        self.net(self.guest, "ip", "addr", "add", "10.95.0.2/24", "dev", "f6test-vm")
        self.net(self.guest, "ip", "addr", "add", "10.95.0.3/24", "dev", "f6test-vm")
        self.net(self.guest, "ip", "route", "add", "default", "via", "10.95.0.1")
        self.net(self.router, "ipset", "create", "nonat", "hash:net")
        self.net(self.router, "ipset", "add", "nonat", "10.95.0.0/24")
        # Exercise explicit ACCEPTs with restrictive namespace policies.
        self.net(self.router, "iptables", "-P", "FORWARD", "DROP")
        self.net(self.router, "ip6tables", "-P", "FORWARD", "DROP")
        self.net(self.router, "ip6tables", "-P", "INPUT", "DROP")
        server = """import socket, threading
from http.server import BaseHTTPRequestHandler, HTTPServer
class H(BaseHTTPRequestHandler):
 def do_GET(self):
  self.send_response(200); self.end_headers(); self.wfile.write(('IPv4 VM peer='+self.client_address[0]+' target='+self.connection.getsockname()[0]).encode())
 def log_message(self,*a): pass
def udp():
 s=socket.socket(socket.AF_INET,socket.SOCK_DGRAM); s.bind(('0.0.0.0',18081))
 while True:
  data,addr=s.recvfrom(4096); s.sendto(data,addr)
threading.Thread(target=udp,daemon=True).start()
HTTPServer(('0.0.0.0',18080),H).serve_forever()
"""
        self.server = subprocess.Popen(["ip", "netns", "exec", self.guest, "python3", "-u", "-c", server], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        self.addCleanup(self.stop, self.server)
        time.sleep(0.2)

    @staticmethod
    def stop(process):
        process.terminate()
        process.wait(timeout=5)

    def clear_translators(self):
        self.command("python3", self.scripts / "floating6.py", "clear-router", self.router_id, env=self.env)
        self.command("ip", "link", "del", self.host_device, check=False)

    def create(self, fip=1, address="2001:db8:6::25/120", inbound=0, outbound=0, target="10.95.0.2/24", env=None, check=True):
        return self.command("bash", self.scripts / "create_floating.sh", self.router_id, address, "2001:db8:6::1", self.ext_vlan, target, self.int_vlan, fip, inbound, outbound, env=env or self.env, check=check)

    def clear(self, fip=1, address="2001:db8:6::25/120"):
        self.command("bash", self.scripts / "clear_floating.sh", self.router_id, address, "10.95.0.2/24", self.int_vlan, fip, env=self.env)

    def http(self, address="2001:db8:6::25", target="10.95.0.2"):
        code = f"""import socket
s=socket.socket(socket.AF_INET6); s.settimeout(5)
s.bind(('2001:db8:99::2',0)); s.connect(('{address}',18080))
s.sendall(b'GET / HTTP/1.0\\r\\nHost: test\\r\\n\\r\\n')
data=b''
while True:
 chunk=s.recv(4096)
 if not chunk: break
 data+=chunk
print(data.decode())
"""
        self.assertIn("IPv4 VM peer=10.95.0.1 target=" + target, self.net(self.client, "python3", "-c", code).stdout)

    def test_traffic_multiple_fips_bandwidth_and_cleanup(self):
        self.create(inbound=100, outbound=100)
        try:
            self.http()
        except AssertionError:
            for args in (("ip", "-6", "route", "show", "table", "all"), ("ip", "-6", "rule", "show"), ("ip", "-4", "route", "show", "table", "all"), ("iptables-save", "-c"), ("ip6tables-save", "-c"), ("ip", "-6", "neigh", "show"), ("ip", "-s", "link", "show")):
                print(self.net(self.router, *args).stdout)
            print(self.net(self.client, "ip", "-6", "neigh", "show").stdout)
            raise
        self.net(self.client, "ping", "-6", "-I", "2001:db8:99::2", "-c", "1", "-W", "3", "2001:db8:6::25")
        self.net(self.client, "python3", "-c", "import socket; s=socket.socket(socket.AF_INET6,socket.SOCK_DGRAM); s.settimeout(5); s.bind(('2001:db8:99::2',0)); s.sendto(b'nat64-udp',('2001:db8:6::25',18081)); assert s.recv(1024)==b'nat64-udp'")
        self.create(fip=2, address="2001:db8:6::26/120")
        self.http("2001:db8:6::26")
        # A failed TUN creation rolls back only its own resources.
        failed = self.create(fip=3, address="2001:db8:6::27/120", env=dict(self.env, CLOUDLAND_TAYGA_PATH="/bin/false"), check=False)
        self.assertNotEqual(failed.returncode, 0)
        self.assertFalse((self.fixture / "state" / self.router / "3").exists())
        self.http()
        # Same IPv4 VM with an IPv4 floating IP: its policy and SNAT must not
        # redirect NAT64 replies onto the IPv4 public interface.
        self.net(self.router, "ip", "-4", "route", "add", "default", "dev", self.ext_device, "table", "110")
        self.net(self.router, "ip", "-4", "addr", "add", "192.0.2.25/24", "dev", self.ext_device)
        self.net(self.router, "ip", "-4", "rule", "add", "priority", "200", "from", "10.95.0.2", "lookup", "110")
        self.net(self.router, "iptables", "-t", "nat", "-A", "POSTROUTING", "-s", "10.95.0.2", "-m", "set", "!", "--match-set", "nonat", "dst", "-j", "SNAT", "--to-source", "192.0.2.25")
        self.http()
        self.http("2001:db8:6::26")
        self.command("bash", self.scripts / "clear_floating.sh", self.router_id, "192.0.2.25/24", "10.95.0.2/24", self.int_vlan, 4, env=self.env)
        self.http()
        self.http("2001:db8:6::26")
        # Reapplying the assignment neither duplicates rules nor leaks daemons.
        self.create(inbound=50, outbound=50)
        self.http()
        self.create(target="10.95.0.3/24")
        self.http(target="10.95.0.3")
        self.clear()
        self.clear()
        self.http("2001:db8:6::26")
        self.clear(fip=2, address="2001:db8:6::26/120")
        self.assertNotIn("f6-", self.net(self.router, "ip", "-o", "link", "show").stdout)
        self.assertNotIn("fip6-", self.net(self.router, "iptables-save").stdout)
        self.assertNotIn("fip6-", self.net(self.router, "ip6tables-save").stdout)
        self.assertNotIn("198.18.", self.net(self.router, "ip", "-4", "rule", "show").stdout)


if __name__ == "__main__":
    unittest.main()
