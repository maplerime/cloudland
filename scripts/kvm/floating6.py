#!/usr/bin/env python3
"""Per-floating-IP TAYGA translators in the existing router namespace."""
import argparse
import fcntl
import ipaddress
import json
import os
from pathlib import Path
import shutil
import signal
import subprocess
import sys
import time

ROOT = Path(os.environ.get("CLOUDLAND_FLOATING6_STATE", "/run/cloudland/floating6"))
TAYGA = os.environ.get("CLOUDLAND_TAYGA_PATH", "tayga")


def run(*args, check=True):
    result = subprocess.run([str(a) for a in args], text=True, capture_output=True)
    if check and result.returncode:
        raise RuntimeError(f"{' '.join(map(str, args))}: {result.stderr.strip() or result.stdout.strip()}")
    return result


def ns(router, *args, check=True):
    return run("ip", "netns", "exec", router, *args, check=check)


def ip(router, *args, check=True):
    return ns(router, "ip", *args, check=check)


def addresses(router, device):
    result = ip(router, "-j", "addr", "show", "dev", device, check=False)
    return json.loads(result.stdout or "[]") if result.returncode == 0 else []


def ensure_rule(router, binary, table, chain, rule):
    if ns(router, binary, "-w", "-t", table, "-C", chain, *rule, check=False).returncode:
        ns(router, binary, "-w", "-t", table, "-I", chain, "1", *rule)


def ensure_policy(router, family, priority, selector, address, table):
    rules = json.loads(ip(router, family, "-j", "rule", "show").stdout)
    key = "src" if selector == "from" else "dst"
    wanted = ipaddress.ip_network(address, strict=False)
    for rule in rules:
        try:
            existing = ipaddress.ip_network(rule.get(key, "all"), strict=False)
        except ValueError:
            continue
        if rule.get("priority") == priority and existing == wanted and str(rule.get("table")) == str(table):
            return
    ip(router, family, "rule", "add", "priority", priority, selector, address, "lookup", table)


def pool_for(router, directory):
    used = []
    for route in json.loads(ip(router, "-4", "-j", "route", "show", "table", "all").stdout):
        destination = route.get("dst", "default")
        if destination != "default":
            used.append(ipaddress.ip_network(destination, strict=False))
    for device in json.loads(ip(router, "-4", "-j", "addr", "show").stdout):
        for address in device.get("addr_info", []):
            used.append(ipaddress.ip_network(f"{address['local']}/{address['prefixlen']}", strict=False))
    for state_file in directory.glob("*/state.json"):
        used.append(ipaddress.ip_network(json.loads(state_file.read_text())["pool"]))
    devices = {d["ifname"] for d in json.loads(ip(router, "-j", "link", "show").stdout)}
    # Benchmarking space is never advertised to the guest or the public network.
    # Skip any range already used by this VPC, including its connected networks.
    for slot, pool in enumerate(ipaddress.ip_network("198.18.0.0/15").subnets(new_prefix=24)):
        if f"f6-{slot}" not in devices and not any(pool.overlaps(network) for network in used):
            return slot, str(pool)
    raise RuntimeError("No free NAT64 dynamic pool in 198.18.0.0/15 for this router")


def stop_daemon(router, directory):
    pid_file = directory / "tayga.pid"
    if not pid_file.exists():
        return
    pid = int(pid_file.read_text().strip())
    process = Path(f"/proc/{pid}")
    try:
        cmdline = (process / "cmdline").read_bytes().split(b"\0")
        if str(directory / "tayga.conf").encode() not in cmdline:
            raise RuntimeError("Refusing to signal a process unrelated to this floating IP")
        if os.stat(process / "ns/net").st_ino != os.stat(f"/var/run/netns/{router}").st_ino:
            raise RuntimeError("TAYGA process belongs to another namespace")
        os.kill(pid, signal.SIGTERM)
        for _ in range(50):
            if not process.exists() or ") Z " in (process / "stat").read_text():
                break
            time.sleep(0.1)
        else:
            raise RuntimeError("TAYGA did not terminate")
    except FileNotFoundError:
        pass
    pid_file.unlink(missing_ok=True)


def clear(router, directory):
    state_file = directory / "state.json"
    if not state_file.exists():
        return
    state = json.loads(state_file.read_text())
    stop_daemon(router, directory)
    for binary, table, chain, rule in state["rules"]:
        while ns(router, binary, "-w", "-t", table, "-C", chain, *rule, check=False).returncode == 0:
            ns(router, binary, "-w", "-t", table, "-D", chain, *rule)
    ip(router, "-4", "rule", "del", "priority", 100, "to", state["pool"], "lookup", "main", check=False)
    ip(router, "-6", "rule", "del", "priority", 1000, "from", state["ext_ip"] + "/128", "lookup", state["table"], check=False)
    ip(router, "-6", "rule", "del", "priority", 1000, "from", state["translator6"] + "/128", "lookup", state["table"], check=False)
    ip(router, "-6", "route", "del", state["ext_ip"] + "/128", "dev", state["tun"], check=False)
    ip(router, "-4", "route", "del", state["pool"], "dev", state["tun"], check=False)
    ns(router, "ipset", "del", "nonat", state["pool"], check=False)
    ip(router, "link", "del", state["tun"], check=False)
    ip(router, "-6", "neigh", "del", "proxy", state["ext_ip"], "dev", state["ext_dev"], check=False)
    other_translators = [p for p in directory.parent.glob("*/state.json") if p != state_file and json.loads(p.read_text())["ext_dev"] == state["ext_dev"]]
    remaining = [a for d in addresses(router, state["ext_dev"]) for a in d.get("addr_info", []) if a.get("scope") == "global"]
    if not other_translators:
        ip(router, "-6", "route", "flush", "table", state["table"], check=False)
    if not remaining and not other_translators:
        ip(router, "link", "del", state["ext_dev"], check=False)
    shutil.rmtree(directory)


def create(args, router, directory):
    if not shutil.which(TAYGA):
        raise RuntimeError("TAYGA is required on the hypervisor for IPv6 floating IPs")
    if not Path("/dev/net/tun").exists():
        raise RuntimeError("The TUN device /dev/net/tun is required")
    ip(router, "link", "show", "dev", f"ns-{args.int_vlan}")
    target = ipaddress.IPv4Interface(args.int_cidr)
    ext = ipaddress.IPv6Interface(args.ext_cidr)
    gateway6 = ipaddress.IPv6Address(args.ext_gw.split("/")[0])
    if gateway6 not in ext.network:
        raise RuntimeError("IPv6 gateway must belong to the public subnet")
    internal_device = f"ns-{args.int_vlan}"
    gateway4 = None
    for device in addresses(router, internal_device):
        for address in device.get("addr_info", []):
            if address["family"] == "inet" and target.ip in ipaddress.ip_network(f"{address['local']}/{address['prefixlen']}", strict=False):
                gateway4 = address["local"]
                break
    if not gateway4:
        raise RuntimeError("No IPv4 gateway for the instance in the router namespace")
    ext_device = f"te-{args.router}-{args.ext_vlan}"
    host_device = f"ext-{args.router}-{args.ext_vlan}"
    if len(host_device) > 15:
        raise RuntimeError("External veth name exceeds Linux's 15-character limit")
    clear(router, directory)
    directory.mkdir(parents=True, mode=0o700)
    slot, pool = pool_for(router, directory.parent)
    tun = f"f6-{slot}"
    translator4 = str(ipaddress.ip_network(pool).network_address + 1)
    translator6 = f"fd64::{slot + 1:x}:1"
    table = 20000000 + args.ext_vlan
    comment = ["-m", "comment", "--comment", f"fip6-{args.fip_id}"]
    rules = [
        ["iptables", "nat", "POSTROUTING", ["-s", pool, "-d", str(target.ip) + "/32", "-o", internal_device, *comment, "-j", "SNAT", "--to-source", gateway4]],
        ["iptables", "filter", "FORWARD", ["-i", tun, "-o", internal_device, "-d", str(target.ip) + "/32", *comment, "-j", "ACCEPT"]],
        ["iptables", "filter", "FORWARD", ["-i", internal_device, "-o", tun, "-s", str(target.ip) + "/32", "-d", pool, *comment, "-j", "ACCEPT"]],
        ["ip6tables", "filter", "FORWARD", ["-i", ext_device, "-o", tun, "-d", str(ext.ip) + "/128", *comment, "-j", "ACCEPT"]],
        ["ip6tables", "filter", "FORWARD", ["-i", tun, "-o", ext_device, "-s", str(ext.ip) + "/128", *comment, "-j", "ACCEPT"]],
    ]
    for icmp_type in ("neighbor-solicitation", "neighbor-advertisement"):
        rules.append(["ip6tables", "filter", "INPUT", ["-i", ext_device, "-p", "ipv6-icmp", "--icmpv6-type", icmp_type, *comment, "-j", "ACCEPT"]])
    state = dict(pool=pool, tun=tun, ext_dev=ext_device, ext_cidr=str(ext), ext_ip=str(ext.ip), translator6=translator6, table=table, rules=rules)
    (directory / "state.json").write_text(json.dumps(state))
    config = directory / "tayga.conf"
    config.write_text(f"tun-device {tun}\nipv4-addr {translator4}\nipv6-addr {translator6}\ndynamic-pool {pool}\ndata-dir {directory}\nmap {target.ip} {ext.ip}\n")
    try:
        run("bash", str(Path(__file__).with_name("create_veth.sh")), router, host_device, ext_device)
        ns(router, "sysctl", "-qw", "net.ipv4.ip_forward=1", "net.ipv6.conf.all.forwarding=1", f"net.ipv6.conf.{ext_device}.disable_ipv6=0", f"net.ipv6.conf.{ext_device}.proxy_ndp=1")
        ns(router, TAYGA, "--config", config, "--mktun")
        ns(router, "sysctl", "-qw", f"net.ipv6.conf.{tun}.disable_ipv6=0", f"net.ipv4.conf.{tun}.rp_filter=0")
        ip(router, "link", "set", "dev", tun, "mtu", "1450", "up")
        # Proxy NDP keeps public traffic routable to TAYGA and lets gateway
        # neighbour discovery use the external device's local link-local IP.
        ip(router, "-6", "neigh", "add", "proxy", str(ext.ip), "dev", ext_device)
        ip(router, "-6", "route", "replace", str(ext.ip) + "/128", "dev", tun)
        ip(router, "-4", "route", "replace", pool, "dev", tun)
        ip(router, "-6", "route", "replace", str(ext.network), "dev", ext_device, "table", table)
        ip(router, "-6", "route", "replace", "default", "via", gateway6, "dev", ext_device, "onlink", "table", table)
        ensure_policy(router, "-6", 1000, "from", str(ext.ip) + "/128", table)
        ensure_policy(router, "-6", 1000, "from", translator6 + "/128", table)
        # Replies must reach TAYGA even when this VM also has an IPv4 floating IP.
        ensure_policy(router, "-4", 100, "to", pool, "main")
        ns(router, "ipset", "add", "nonat", pool, "-exist")
        for binary, rule_table, chain, rule in rules:
            ensure_rule(router, binary, rule_table, chain, rule)
        if args.inbound or args.outbound:
            ns(router, "tc", "qdisc", "replace", "dev", tun, "root", "handle", "1:", "htb", "default", "30")
            ns(router, "tc", "class", "replace", "dev", tun, "parent", "1:", "classid", "1:30", "htb", "rate", "100000mbit")
            for class_id, rate, protocol, field, address in (("10", args.inbound, "ipv6", "dst_ip", str(ext.ip) + "/128"), ("20", args.outbound, "ip", "src_ip", str(target.ip) + "/32")):
                if rate:
                    ns(router, "tc", "class", "replace", "dev", tun, "parent", "1:", "classid", f"1:{class_id}", "htb", "rate", f"{rate}mbit")
                    ns(router, "tc", "filter", "replace", "dev", tun, "parent", "1:", "protocol", protocol, "prio", class_id, "flower", field, address, "flowid", f"1:{class_id}")
        ns(router, TAYGA, "--config", config, "--pidfile", directory / "tayga.pid")
    except Exception:
        clear(router, directory)
        raise


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("action", choices=("create", "clear", "clear-router"))
    parser.add_argument("router", type=int)
    parser.add_argument("params", nargs="*")
    args = parser.parse_args()
    if args.router <= 0:
        parser.error("router must be positive")
    router = f"router-{args.router}"
    parent = ROOT / router
    parent.mkdir(parents=True, exist_ok=True, mode=0o700)
    with (parent / ".lock").open("w") as lock:
        fcntl.flock(lock, fcntl.LOCK_EX)
        if args.action == "clear-router":
            for directory in parent.iterdir():
                if directory.is_dir():
                    clear(router, directory)
            return
        if args.action == "create":
            if len(args.params) != 8:
                parser.error("create requires ext_cidr ext_gw ext_vlan int_cidr int_vlan fip_id inbound outbound")
            args.ext_cidr, args.ext_gw, ext_vlan, args.int_cidr, int_vlan, fip_id, inbound, outbound = args.params
            args.ext_vlan, args.int_vlan, args.fip_id, args.inbound, args.outbound = map(int, (ext_vlan, int_vlan, fip_id, inbound, outbound))
            if min(args.ext_vlan, args.int_vlan, args.fip_id) <= 0 or not (0 <= args.inbound <= 20000 and 0 <= args.outbound <= 20000):
                parser.error("invalid VLAN, floating IP ID or bandwidth")
            create(args, router, parent / str(args.fip_id))
        else:
            if len(args.params) != 4:
                parser.error("clear requires ext_cidr int_cidr int_vlan fip_id")
            fip_id = int(args.params[3])
            if fip_id <= 0:
                parser.error("floating IP ID must be positive")
            clear(router, parent / str(fip_id))


if __name__ == "__main__":
    try:
        main()
    except (RuntimeError, ValueError, OSError) as error:
        print(f"IPv6 floating IP: {error}", file=sys.stderr)
        sys.exit(1)
