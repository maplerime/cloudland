"""Native IPv6 SG/NDP integration; all firewall rules live in test namespaces.

Opt in with CLOUDLAND_PRIMARY6_NAMESPACE=1 and CLOUDLAND_PRIMARY6_SCRIPTS.
"""
import json
import os
from pathlib import Path
import shutil
import select
import socket
import subprocess
import tempfile
import unittest


@unittest.skipUnless(os.environ.get("CLOUDLAND_PRIMARY6_NAMESPACE"), "requires isolated network namespace privileges")
class NativeIPv6NamespaceTest(unittest.TestCase):
    def command(self, *args, check=True, data=None):
        result = subprocess.run(list(map(str, args)), input=data, capture_output=True, text=True, timeout=30)
        if check and result.returncode:
            self.fail(f"{args}: {result.stderr or result.stdout}")
        return result

    def net(self, name, *args, **kwargs):
        return self.command("ip", "netns", "exec", name, *args, **kwargs)

    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix="primary6-ns-")
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.scripts = self.root / "kvm"
        self.scripts.mkdir()
        (self.scripts / "async_job").mkdir()
        source = Path(os.environ.get("CLOUDLAND_PRIMARY6_SCRIPTS", "/opt/cloudland/scripts/kvm"))
        for name in ("create_sg_chain.sh", "create_sg_chain6.sh", "clear_sg_chain6.sh", "apply_sg_rule.sh", "async_job/clear_sg_chain.sh"):
            shutil.copy2(source / name, self.scripts / name)
            (self.scripts / name).chmod(0o755)
        cloudrc = (Path(__file__).resolve().parents[2] / "scripts/cloudrc").read_text()
        firewall_function = cloudrc[cloudrc.index("function apply_fw()"):cloudrc.index("function apply_vnic()")]
        (self.root / "jobs").mkdir()
        (self.root / "cloudrc").write_text(f"# Test namespace: no production configuration\nrun_dir={self.root}\nasync_job_dir={self.root}/jobs\n" + firewall_function)
        self.hyper, self.guest, self.peer = [f"p6-{role}-{os.getpid()}" for role in ("h", "g", "p")]
        for ns in (self.hyper, self.guest, self.peer):
            self.command("ip", "netns", "add", ns)
            self.addCleanup(self.command, "ip", "netns", "del", ns, check=False)
            self.net(ns, "ip", "link", "set", "lo", "up")
        self.net(self.hyper, "ip", "link", "add", "br6", "type", "bridge")
        self.net(self.hyper, "ip", "link", "set", "br6", "type", "bridge", "nf_call_ip6tables", "1")
        self.net(self.hyper, "ip", "link", "set", "br6", "up")
        self.mac = "52:54:00:12:34:56"
        self.net(self.hyper, "iptables", "-N", "secgroup-chain")
        for chain in ("FORWARD", "INPUT"):
            self.net(self.hyper, "iptables", "-A", chain, "-m", "conntrack", "--ctstate", "RELATED,ESTABLISHED", "-j", "ACCEPT")
        self.net(self.hyper, "iptables", "-A", "FORWARD", "-m", "conntrack", "--ctstate", "INVALID", "-j", "DROP")
        for port, namespace, addr in (("tap6", self.guest, "2001:db8:6::10/120"),
                                      ("uplink6", self.peer, "2001:db8:6::1/120")):
            self.net(self.hyper, "ip", "link", "add", port, "type", "veth", "peer", "name", "eth0", "netns", namespace)
            self.net(self.hyper, "ip", "link", "set", port, "master", "br6")
            self.net(self.hyper, "ip", "link", "set", port, "up")
            if namespace == self.guest:
                self.net(namespace, "ip", "link", "set", "eth0", "address", self.mac)
            self.net(namespace, "ip", "link", "set", "eth0", "up")
            self.net(namespace, "ip", "-6", "addr", "add", addr, "dev", "eth0", "nodad")
        self.net(self.guest, "ip", "-6", "route", "add", "default", "via", "2001:db8:6::1")
        self.net(self.peer, "ip", "-6", "addr", "add", "2001:db8:99::1/128", "dev", "lo", "nodad")
        self.create()

    def create(self):
        self.net(self.hyper, "bash", self.scripts / "create_sg_chain.sh", "tap6", "2001:db8:6::10", self.mac, "false", data='["2001:db8:6::20/120"]')

    def test_chains_follow_allocated_address_families(self):
        for primary, extra, v4, v6 in [
                ("192.0.2.10", [], True, False),
                ("2001:db8:6::10", [], False, True),
                ("192.0.2.10", ["2001:db8:6::20/120"], True, True),
                ("2001:db8:6::10", ["192.0.2.20/24"], True, True),
                ("192.0.2.10", [], True, False)]:
            with self.subTest(primary=primary, extras=extra):
                self.net(self.hyper, "bash", self.scripts / "async_job/clear_sg_chain.sh", "tap6", "true")
                self.net(self.hyper, "bash", self.scripts / "create_sg_chain.sh", "tap6", primary, self.mac, "false", data=json.dumps(extra))
                self.allow()
                rules4 = self.net(self.hyper, "iptables-save").stdout
                rules6 = self.net(self.hyper, "ip6tables-save").stdout
                self.assertEqual("secgroup-in-tap6" in rules4, v4)
                self.assertEqual("secgroup6-in-tap6" in rules6, v6)
                sets = self.net(self.hyper, "ipset", "list", "-name").stdout.splitlines()
                self.assertEqual("sgas-tap6" in sets, v4)
                self.assertEqual("sgas6-tap6" in sets, v6)
                if v4:
                    self.assertIn("--dport 22", rules4)
                if v6:
                    self.assertIn("-p ipv6-icmp", rules6)

    def allow(self):
        rules = [{"direction": direction, "remote_ip": "::/0", "protocol": "icmp", "port_min": -1,
                  "port_max": -1, "ip_version": "ipv6"} for direction in ("ingress", "egress")]
        rules.append({"direction": "ingress", "remote_ip": "0.0.0.0/0", "protocol": "tcp", "port_min": 22, "port_max": 22, "ip_version": "ipv4"})
        # Match the production relative invocation from async_job.
        self.net(self.hyper, "bash", "-c", 'cd "$1"; ../apply_sg_rule.sh tap6', "test", self.scripts / "async_job", data=json.dumps(rules))

    def ping(self, namespace, destination, success=True):
        result = self.net(namespace, "ping", "-6", "-c", "1", "-W", "1", destination, check=False)
        self.assertEqual(result.returncode == 0, success, result.stdout + result.stderr)

    def test_native_ipv6_ndp_routes_security_and_cleanup(self):
        # Discovery is allowed before any application permissions are granted.
        self.ping(self.guest, "2001:db8:6::1", success=False)
        neighbours = self.net(self.guest, "ip", "-6", "neigh", "show").stdout
        self.assertIn("lladdr", neighbours)
        self.allow()
        self.ping(self.guest, "2001:db8:6::1")
        self.ping(self.guest, "2001:db8:99::1")
        self.ping(self.peer, "2001:db8:6::10")
        self.net(self.guest, "ip", "-6", "addr", "add", "2001:db8:6::20/120", "dev", "eth0", "nodad")
        self.ping(self.peer, "2001:db8:6::20")
        # An allow-all ICMP rule must not bypass source address validation.
        self.net(self.guest, "ip", "-6", "addr", "add", "2001:db8:6::30/120", "dev", "eth0", "nodad")
        result = self.net(self.guest, "ping", "-6", "-I", "2001:db8:6::30", "-c", "1", "-W", "1", "2001:db8:6::1", check=False)
        self.assertNotEqual(result.returncode, 0)
        self.net(self.guest, "ip", "-6", "addr", "del", "2001:db8:6::30/120", "dev", "eth0")
        # Rebuild and cleanup must both be repeatable.
        self.create()
        self.allow()
        self.ping(self.guest, "2001:db8:6::1")
        for _ in range(2):
            self.net(self.hyper, "bash", self.scripts / "clear_sg_chain6.sh", "tap6")
        self.assertNotIn("secgroup6-", self.net(self.hyper, "ip6tables-save").stdout)
        self.assertNotIn("sgas6-tap6", self.net(self.hyper, "ipset", "list", "-name").stdout)

    def test_ipv6_tcp_and_dns_permissions(self):
        # Real sockets exercise TCP/UDP SG rules in both directions.
        server = '''import socket,sys
s=socket.socket(socket.AF_INET6, socket.SOCK_DGRAM if sys.argv[1]=='udp' else socket.SOCK_STREAM)
s.bind(('::',int(sys.argv[2])))
if sys.argv[1]=='tcp': s.listen(5)
print('READY',flush=True)
while True:
 if sys.argv[1]=='udp':
  query,peer=s.recvfrom(4096)
  # Answer the test's single AAAA question with 2001:db8:99::53.
  response=query[:2]+bytes.fromhex('81800001000100000000')+query[12:]+bytes.fromhex('c00c001c00010000003c0010')+socket.inet_pton(socket.AF_INET6,'2001:db8:99::53')
  s.sendto(response,peer)
 else:
  connection,_=s.accept()
  connection.sendall(b'primary6-tcp-ok')
  connection.close()
'''
        for ns, protocol, port in ((self.peer, "tcp", 18080), (self.guest, "tcp", 18081), (self.peer, "udp", 53)):
            process = subprocess.Popen(["ip", "netns", "exec", ns, "python3", "-u", "-c", server, protocol, str(port)],
                                       stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
            def stop(p=process):
                p.terminate()
                p.communicate(timeout=5)
            self.addCleanup(stop)
            self.assertTrue(select.select([process.stdout], [], [], 5)[0], "server did not start")
            self.assertEqual(process.stdout.readline().strip(), "READY")
        client = "import socket,sys; s=socket.socket(socket.AF_INET6); s.settimeout(1); s.connect((sys.argv[1],int(sys.argv[2]))); assert s.recv(100)==b'primary6-tcp-ok'"
        result = self.net(self.guest, "python3", "-c", client, "2001:db8:6::1", "18080", check=False)
        self.assertNotEqual(result.returncode, 0)
        rules = [{"direction": "egress", "remote_ip": "::/0", "protocol": "tcp", "port_min": 18080, "port_max": 18080},
                 {"direction": "ingress", "remote_ip": "::/0", "protocol": "tcp", "port_min": 18081, "port_max": 18081},
                 {"direction": "egress", "remote_ip": "::/0", "protocol": "udp", "port_min": 53, "port_max": 53}]
        self.net(self.hyper, "bash", self.scripts / "apply_sg_rule.sh", "tap6", "ipv6", data=json.dumps(rules))
        self.net(self.guest, "python3", "-c", client, "2001:db8:6::1", "18080")
        self.net(self.peer, "python3", "-c", client, "2001:db8:6::10", "18081")
        dns_client = "import socket; s=socket.socket(socket.AF_INET6,socket.SOCK_DGRAM); s.settimeout(2); s.sendto(bytes.fromhex('123401000001000000000000')+b'\\x06ipv6vm\\x04test\\x00'+bytes.fromhex('001c0001'),('2001:db8:6::1',53)); answer=s.recv(4096); assert answer[-16:]==socket.inet_pton(socket.AF_INET6,'2001:db8:99::53')"
        self.net(self.guest, "python3", "-c", dns_client)


if __name__ == "__main__":
    unittest.main()
