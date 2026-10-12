"""Privileged, isolated NAT64 integration tests. No production namespaces touched.

Run with CLOUDLAND_FLOATING6_SCRIPTS and CLOUDLAND_JOOL_BIN set, as root.
"""
import json
import fcntl
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
        for name in ("floating6_common.sh", "create_floating.sh", "clear_floating.sh", "create_floating6.sh", "clear_floating6.sh", "create_veth.sh", "create_link.sh"):
            shutil.copy2(Path(os.environ["CLOUDLAND_FLOATING6_SCRIPTS"]) / name, self.scripts / name)
            (self.scripts / name).chmod(0o755)
        (self.fixture / "cloudrc").write_text("# Isolated fixture: no production cloudrc\nasync_exec() { :; }\n")
        # Exercise real IPv4 scripts with a numeric namespace table, avoiding
        # modifications of the host's /etc/iproute2/rt_tables during tests.
        for name in ("create_floating.sh", "clear_floating.sh"):
            script = self.scripts / name
            content = script.read_text().replace("table=fip-$ext_vlan", "table=110").replace("table=fip-${ext_dev##*-}", "table=110")
            content = content.replace("rt_file=/etc/iproute2/rt_tables", f"rt_file={self.fixture}/rt_tables")
            script.write_text(content)
        self.env = dict(os.environ)
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
        # br_netfilter can still consult the host FORWARD chain when its global
        # sysctl is enabled. Allow only the two fixture ports, then remove the
        # exact rules; never change the host's bridge sysctls or chain policy.
        for incoming, outgoing in (("f6test-ext", self.host_device), (self.host_device, "f6test-ext")):
            rule = ("-m", "physdev", "--physdev-is-bridged", "--physdev-in", incoming, "--physdev-out", outgoing, "-m", "comment", "--comment", f"floating6-test-{os.getpid()}", "-j", "ACCEPT")
            self.command("iptables", "-w", "-I", "FORWARD", "1", *rule)
            self.addCleanup(self.command, "iptables", "-w", "-D", "FORWARD", *rule)
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
        self.net(self.router, "sysctl", "-qw", "net.ipv4.ip_forward=1")
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
        self.command("bash", self.scripts / "clear_floating6.sh", "--router", self.router_id, env=self.env)
        self.command("ip", "link", "del", self.host_device, check=False)

    def create(self, fip=1, address="2001:db8:6::25/120", inbound=0, outbound=0, target="10.95.0.2/24", env=None, check=True, gateway="2001:db8:6::1"):
        return self.command("bash", self.scripts / "create_floating.sh", self.router_id, address, gateway, self.ext_vlan, target, self.int_vlan, fip, inbound, outbound, env=env or self.env, check=check)

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
        try:
            response = self.net(self.client, "python3", "-c", code).stdout
        except AssertionError:
            for args in (("ip", "-6", "route", "show", "table", "all"), ("ip6tables-save", "-c"), ("iptables-save", "-c"), ("ip", "-6", "neigh", "show")):
                print(self.net(self.router, *args).stdout)
            raise
        self.assertIn("IPv4 VM peer=198.1", response)
        self.assertIn("target=" + target, response)

    def create4(self):
        for address in ("192.0.2.1/24", "192.0.2.2/24"):
            self.net(self.client, "ip", "addr", "add", address, "dev", "f6test-client")
        self.command("bash", self.scripts / "create_floating.sh", self.router_id, "192.0.2.25/24", "192.0.2.1", self.ext_vlan, "10.95.0.2/24", self.int_vlan, 4, 0, 0, env=self.env, check=False)
        self.net(self.router, "iptables", "-I", "FORWARD", "-i", self.ext_device, "-o", f"ns-{self.int_vlan}", "-j", "ACCEPT")
        self.net(self.router, "iptables", "-I", "FORWARD", "-i", f"ns-{self.int_vlan}", "-o", self.ext_device, "-j", "ACCEPT")

    def http4(self):
        code = """import socket
s=socket.socket(); s.settimeout(5); s.bind(('192.0.2.2',0)); s.connect(('192.0.2.25',18080))
s.sendall(b'GET / HTTP/1.0\\r\\nHost: test\\r\\n\\r\\n'); data=b''
while True:
 chunk=s.recv(4096)
 if not chunk: break
 data+=chunk
assert b'IPv4 VM peer=192.0.2.2 target=10.95.0.2' in data, data
"""
        try:
            self.net(self.client, "python3", "-c", code)
        except AssertionError:
            for args in (("ip", "-4", "route", "show", "table", "all"), ("ip", "-4", "rule", "show"), ("iptables-save", "-c"), ("ip", "neigh", "show")):
                print(self.net(self.router, *args).stdout)
            raise

    def failure_environment(self, name, condition, status=42):
        wrappers = self.fixture / ("failure-" + str(time.time_ns()))
        wrappers.mkdir()
        executable = wrappers / name
        original = shutil.which(name)
        executable.write_text(f"#!/usr/bin/python3\nimport os,sys\na=sys.argv[1:]\nif {condition}:\n print('injected {name} failure',file=sys.stderr); sys.exit({status})\nos.execv({original!r},[{original!r}]+a)\n")
        executable.chmod(0o755)
        return dict(self.env, PATH=str(wrappers) + os.pathsep + os.environ["PATH"])

    def namespace(self, fip=1):
        return f"fip6-{self.router_id}-{fip}"

    def record(self, fip=1):
        data = json.loads(self.net(self.namespace(fip), "ip", "-j", "link", "show", "dev", "lo").stdout)
        return data[0]["ifalias"].split()

    def test_expanded_public_address_cleanup_preserves_other_fip(self):
        self.create(address="2001:0DB8:0006:0000:0000:0000:0000:0025/120")
        self.create(fip=2, address="2001:db8:6::26/120")
        self.http()
        self.clear()
        addresses = json.loads(self.net(self.router, "ip", "-6", "-j", "addr", "show").stdout)
        public = [a["local"] for dev in addresses for a in dev["addr_info"]]
        self.assertNotIn("2001:db8:6::25", public)
        self.http("2001:db8:6::26")

    def test_tcp_udp_icmp_multiple_fips_and_reassign(self):
        self.create(inbound=100, outbound=100)
        self.http()
        self.net(self.client, "ping", "-6", "-I", "2001:db8:99::2", "-c", "1", "-W", "3", "2001:db8:6::25")
        self.net(self.client, "python3", "-c", "import socket; s=socket.socket(socket.AF_INET6,socket.SOCK_DGRAM); s.settimeout(5); s.bind(('2001:db8:99::2',0)); s.sendto(b'nat64-udp',('2001:db8:6::25',18081)); assert s.recv(1024)==b'nat64-udp'")
        self.create(fip=2, address="2001:db8:6::26/120")
        self.http("2001:db8:6::26")
        self.create(target="10.95.0.3/24", inbound=50, outbound=50)
        self.http(target="10.95.0.3")
        self.clear()
        self.clear()
        self.http("2001:db8:6::26")
        self.clear(fip=2, address="2001:db8:6::26/120")
        self.assertNotIn(f"fip6-{self.router_id}-", self.command("ip", "netns", "list").stdout)
        self.assertNotIn("fip6-", self.net(self.router, "iptables-save").stdout)
        self.assertNotIn("fip6-", self.net(self.router, "ip6tables-save").stdout)

    def test_delete_ipv6_preserves_ipv4(self):
        self.create4()
        self.http4()
        self.create()
        self.http()
        self.http4()
        self.clear()
        self.http4()
        self.assertIn("192.0.2.25/24", self.net(self.router, "ip", "-4", "-o", "addr", "show", self.ext_device).stdout)
        self.assertIn("from 10.95.0.2 lookup 110", self.net(self.router, "ip", "-4", "rule", "show").stdout)

    def test_delete_ipv4_preserves_ipv6(self):
        self.create4()
        self.create()
        self.http4()
        self.http()
        self.command("bash", self.scripts / "clear_floating.sh", self.router_id, "192.0.2.25/24", "10.95.0.2/24", self.int_vlan, 4, env=self.env)
        self.http()

    def test_failed_cleanup_keeps_kernel_retry_handle(self):
        self.create()
        failed_env = self.failure_environment("ip", "a == ['link','del','6r1']")
        result = self.command("bash", self.scripts / "clear_floating.sh", self.router_id, "2001:db8:6::25/120", "10.95.0.2/24", self.int_vlan, 1, env=failed_env, check=False)
        self.assertNotEqual(result.returncode, 0)
        self.assertTrue(Path('/var/run/netns/' + self.namespace()).exists())
        self.assertEqual(self.record()[0], "cloudland-fip6")
        self.clear()
        self.assertFalse(Path('/var/run/netns/' + self.namespace()).exists())

    def test_failed_inspection_does_not_mean_absent(self):
        self.create()
        env = self.failure_environment("iptables-save", "True")
        result = self.command("bash", self.scripts / "clear_floating.sh", self.router_id, "2001:db8:6::25/120", "10.95.0.2/24", self.int_vlan, 1, env=env, check=False)
        self.assertNotEqual(result.returncode, 0)
        self.assertTrue(Path('/var/run/netns/' + self.namespace()).exists())
        self.assertIn("fip6-1", self.net(self.router, "iptables-save").stdout)
        self.clear()
        self.assertFalse(Path('/var/run/netns/' + self.namespace()).exists())

    def test_late_creation_failure_and_failed_rollback_can_retry(self):
        failed_env = self.failure_environment("ip", "a == ['link','del','6r1']")
        binary = os.environ['CLOUDLAND_JOOL_BIN']
        wrapper = self.fixture / 'failed-jool'
        wrapper.write_text(f"#!/usr/bin/python3\nimport os,sys\nif 'pool4' in sys.argv and '--udp' in sys.argv: sys.exit(43)\nos.execv({binary!r},[{binary!r}]+sys.argv[1:])\n")
        wrapper.chmod(0o755)
        result = self.create(env=dict(failed_env, CLOUDLAND_JOOL_BIN=str(wrapper)), check=False)
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('Rollback incomplete', result.stderr)
        self.assertTrue(Path('/var/run/netns/' + self.namespace()).exists())
        self.clear()
        self.assertFalse(Path('/var/run/netns/' + self.namespace()).exists())

    def test_half_created_host_veth_is_reclaimed(self):
        (self.scripts / 'create_veth.sh').write_text('#!/bin/bash\nip link add "$2" type veth peer name "$3"\nexit 44\n')
        result = self.create(check=False)
        self.assertNotEqual(result.returncode, 0)
        self.assertFalse(Path('/var/run/netns/' + self.namespace()).exists())
        self.assertNotEqual(self.command('ip','link','show',self.host_device,check=False).returncode,0)
        self.assertNotEqual(self.command('ip','link','show',self.ext_device,check=False).returncode,0)

    def test_partial_private_veth_is_reclaimed(self):
        env = self.failure_environment('ip', "len(a)==5 and a[:3]==['link','set','6r1'] and a[3]=='netns'")
        result = self.create(env=env,check=False)
        self.assertNotEqual(result.returncode,0)
        self.assertFalse(Path('/var/run/netns/' + self.namespace()).exists())
        self.assertNotEqual(self.command('ip','link','show','6r1',check=False).returncode,0)
        self.assertNotEqual(self.command('ip','link','show','6t1',check=False).returncode,0)

    def test_router_cleanup_reuses_lock(self):
        self.create()
        self.command('bash','-c','exec 9<"/var/run/netns/router-$1"; flock -x 9; export CLOUDLAND_FLOATING_NS_LOCK_FD=9; exec bash "$2" --router "$1"','fixture',self.router_id,self.scripts/'clear_floating6.sh',env=self.env)
        self.assertFalse(Path('/var/run/netns/'+self.namespace()).exists())

    def test_distinct_gateways_do_not_share_route_table(self):
        self.create()
        self.net(self.client,'ip','-6','addr','add','2001:db8:7::1/120','dev','f6test-client','nodad')
        self.create(fip=2,address='2001:db8:7::26/120',gateway='2001:db8:7::1')
        self.http()
        self.http('2001:db8:7::26')
        first, second = self.record(), self.record(2)
        self.assertNotEqual(first[-1], second[-1])
        self.clear(fip=2,address='2001:db8:7::26/120')
        self.http()

    def test_no_files_or_userspace_daemon(self):
        directory = self.fixture/'forbidden-state'
        directory.mkdir()
        directory.chmod(0o500)
        before=list(directory.iterdir())
        self.create(env=dict(self.env,CLOUDLAND_FLOATING6_STATE=str(directory)))
        self.http()
        self.assertEqual(list(directory.iterdir()),before)
        self.assertFalse(self.command('ip','netns','pids',self.namespace()).stdout.strip())
        self.clear()
        self.assertEqual(list(directory.iterdir()),before)

    def test_pending_cleanup_pair_cannot_be_reused(self):
        self.create()
        original=self.record()[-1]
        env=self.failure_environment('ip',f"a == ['netns','del',{self.namespace()!r}]")
        failed=self.command('bash',self.scripts/'clear_floating.sh',self.router_id,'2001:db8:6::25/120','10.95.0.2/24',self.int_vlan,1,env=env,check=False)
        self.assertNotEqual(failed.returncode,0)
        self.create(fip=65537,address='2001:db8:6::27/120')
        self.assertNotEqual(self.record(65537)[-1],original)
        self.clear()
        self.http('2001:db8:6::27')

    def test_target_rebuild_requires_only_server_arguments(self):
        self.create()
        self.http()
        self.clear()
        target_id=self.router_id-100
        target=f'router-{target_id}'
        if Path('/var/run/netns/'+target).exists(): self.skipTest('target test namespace already exists')
        self.command('ip','netns','add',target)
        self.addCleanup(self.command,'ip','netns','del',target,check=False)
        self.addCleanup(self.command,'bash',self.scripts/'clear_floating6.sh','--router',target_id,env=self.env)
        self.net(target,'ip','link','set','lo','up')
        self.net(target,'sysctl','-qw','net.ipv4.ip_forward=1')
        self.net(target,'ipset','create','nonat','hash:net')
        self.net(target,'ipset','add','nonat','10.95.0.0/24')
        # Model the VM's gateway moving to the destination node. No files or
        # translator data are copied between source and destination.
        self.net(self.router,'ip','link','set',f'ns-{self.int_vlan}','netns',target)
        self.net(target,'ip','link','set',f'ns-{self.int_vlan}','up')
        self.net(target,'ip','-4','addr','replace','10.95.0.1/24','dev',f'ns-{self.int_vlan}')
        self.command('bash',self.scripts/'create_floating.sh',target_id,'2001:db8:6::25/120','2001:db8:6::1',self.ext_vlan,'10.95.0.2/24',self.int_vlan,1,0,0,env=self.env)
        self.http()
        self.assertFalse(Path('/var/run/netns/'+self.namespace()).exists())


if __name__ == '__main__':
    unittest.main()
